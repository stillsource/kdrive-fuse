package syncer

import (
	"context"
	"fmt"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// FilesPort is the remote surface Push needs. It is satisfied by the kDrive API
// client's *FilesService (which implements all four embedded interfaces).
type FilesPort interface {
	remoteindex.Lister
	remoteindex.Mkdirer
	service.FileWriter
	fileDeleter
}

// PushOptions configures a push run.
type PushOptions struct {
	LocalRoot string
	Jobs      int
	Force     bool
	DryRun    bool
	NoDelete  bool
	AssumeNew bool
}

// Push mirrors opts.LocalRoot onto the remote folder rootID, using the manifest
// at manifestPath as the baseline. On an empty manifest (and unless AssumeNew)
// it bootstraps from a remote index so an already-uploaded tree is not re-pushed
// wholesale. In DryRun it prints the plan to out and changes nothing; otherwise
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
	if m.Len() == 0 && !opts.AssumeNew {
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
	if err := GuardDeletes(items, m.Len(), opts.Force); err != nil {
		return Result{}, err
	}
	if opts.DryRun {
		printPlan(out, items)
		return Result{}, nil
	}
	resolver := remoteindex.NewResolver(files, files, rootID)
	exec := NewPushExecutor(opts.LocalRoot, resolver, files, files)
	res := RunPush(ctx, items, exec, m, opts.Jobs)
	if err := m.Save(manifestPath); err != nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	return res, nil
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
