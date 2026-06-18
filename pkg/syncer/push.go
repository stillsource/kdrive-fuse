package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// fileStater stats a remote file by id (used to detect remote drift before a
// push overwrites or deletes it).
type fileStater interface {
	Stat(ctx context.Context, fileID int64) (domain.FileInfo, error)
}

// FilesPort is the remote surface Push needs. It is satisfied by the kDrive API
// client's *FilesService (which implements all embedded interfaces).
type FilesPort interface {
	remoteindex.Lister
	remoteindex.Mkdirer
	service.FileWriter
	fileDeleter
	fileStater
}

// PushOptions configures a push run.
type PushOptions struct {
	LocalRoot       string
	Jobs            int
	Force           bool
	DryRun          bool
	NoDelete        bool
	AssumeNew       bool
	Refresh         bool    // re-bootstrap the manifest from a fresh remote index
	DeleteThreshold float64 // fraction of baseline; default 0.20 when zero
}

// Push mirrors opts.LocalRoot onto the remote folder rootID, using the manifest
// at manifestPath as the baseline. On an empty manifest (and unless AssumeNew),
// or whenever Refresh is set, it bootstraps from a fresh remote index so an
// already-uploaded tree is not re-pushed wholesale and stale remote ids are
// reconciled. In DryRun it prints the plan to out and changes nothing; otherwise
// it executes the plan and saves the updated manifest.
func Push(ctx context.Context, opts PushOptions, files FilesPort, rootID int64, manifestPath string, out io.Writer) (Result, error) {
	local, err := WalkLocal(opts.LocalRoot)
	if err != nil {
		return Result{}, fmt.Errorf("walk %s: %w", opts.LocalRoot, err)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return Result{}, err
	}
	if opts.Refresh || (m.Len() == 0 && !opts.AssumeNew) {
		idx, err := remoteindex.Build(ctx, files, rootID)
		if err != nil {
			return Result{}, fmt.Errorf("remote index: %w", err)
		}
		Bootstrap(m, idx, local)
	}
	items := PlanPush(local, m)
	if opts.NoDelete {
		items = dropDeletes(items)
	}
	if !opts.Force {
		items, err = filterPushDrift(ctx, items, m, files, out)
		if err != nil {
			return Result{}, err
		}
	}
	if err := GuardDeletes(items, m.Len(), opts.DeleteThreshold, opts.Force); err != nil {
		return Result{}, err
	}
	if opts.DryRun {
		printPlan(out, items)
		return Result{}, nil
	}
	resolver := remoteindex.NewResolver(files, files, rootID)
	exec := NewPushExecutor(opts.LocalRoot, resolver, files, files, files)
	res := RunPush(ctx, items, exec, m, opts.Jobs, func() { _ = m.Save(manifestPath) })
	if err := m.Save(manifestPath); err != nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	return res, nil
}

// filterPushDrift drops overwrite/delete items whose remote file diverged from
// the manifest baseline since the last sync (an out-of-band edit), warning on
// each, so a push never silently clobbers a remote change. New uploads are never
// dropped. A missing remote is treated as drift for an overwrite (the file
// vanished) and as already-done for a delete. It returns an error only on an
// unexpected stat failure.
func filterPushDrift(ctx context.Context, items []Item, m *manifest.Manifest, st fileStater, out io.Writer) ([]Item, error) {
	kept := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Op != OpOverwrite && it.Op != OpDelete {
			kept = append(kept, it)
			continue
		}
		info, err := st.Stat(ctx, it.RemoteID)
		if errors.Is(err, domain.ErrNotFound) {
			if it.Op == OpDelete {
				kept = append(kept, it) // already gone; the idempotent delete no-ops
			} else {
				_, _ = fmt.Fprintf(out, "skip (remote gone, --force to re-upload): %s\n", it.Rel)
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("check remote drift for %s: %w", it.Rel, err)
		}
		e, ok := m.Get(it.Rel)
		if !ok {
			kept = append(kept, it) // untracked rel: no baseline to compare, let it through
			continue
		}
		if info.LastModifiedAt != e.RemoteMtime || info.Size != e.Size {
			_, _ = fmt.Fprintf(out, "skip (remote changed): %s\n", it.Rel)
			continue
		}
		kept = append(kept, it)
	}
	return kept, nil
}

func dropDeletes(items []Item) []Item {
	kept := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Op != OpDelete {
			kept = append(kept, it)
		}
	}
	return kept
}

func printPlan(out io.Writer, items []Item) {
	var up, ov, del int
	for _, it := range items {
		switch it.Op {
		case OpUpload:
			up++
		case OpOverwrite:
			ov++
		case OpDelete:
			del++
		}
	}
	_, _ = fmt.Fprintf(out, "dry-run: %d to upload, %d to overwrite, %d to delete\n", up, ov, del)
	for _, it := range items {
		_, _ = fmt.Fprintf(out, "  %-9s %s\n", opName(it.Op), it.Rel)
	}
}

func opName(op Op) string {
	switch op {
	case OpUpload:
		return "upload"
	case OpOverwrite:
		return "overwrite"
	case OpDelete:
		return "delete"
	default:
		return "?"
	}
}
