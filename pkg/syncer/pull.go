package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// PullPort is the remote surface Pull needs: list the tree (to build the index)
// and stream file content. Satisfied by the kDrive API client's *FilesService.
type PullPort interface {
	remoteindex.Lister
	Downloader
}

// Remote is the full remote surface the CLI sync command needs for either
// direction (push or pull). The *FilesService satisfies it.
type Remote interface {
	FilesPort
	Downloader
}

// PullOptions configures a pull run.
type PullOptions struct {
	LocalRoot string
	Jobs      int
	Force     bool
	DryRun    bool
	NoDelete  bool
}

// Pull mirrors the remote folder rootID onto opts.LocalRoot, using the manifest
// at manifestPath as the baseline. It always builds a remote index. A download
// that would overwrite a locally-modified file (one that differs from the
// baseline) is skipped with a warning unless Force. In DryRun it prints the plan
// to out and changes nothing; otherwise it executes and saves the manifest.
func Pull(ctx context.Context, opts PullOptions, files PullPort, rootID int64, manifestPath string, out io.Writer) (PullResult, error) {
	idx, err := remoteindex.Build(ctx, files, rootID)
	if err != nil {
		return PullResult{}, fmt.Errorf("remote index: %w", err)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return PullResult{}, err
	}
	local, err := WalkLocal(opts.LocalRoot)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return PullResult{}, fmt.Errorf("walk %s: %w", opts.LocalRoot, err)
		}
		local = nil // fresh pull into a not-yet-existing directory
	}

	items := PlanPull(idx, m)
	if !opts.Force {
		items = filterDrift(items, indexLocal(local), m, out)
	}
	if opts.NoDelete {
		items = dropPullDeletes(items)
	}
	if err := GuardPullDeletes(items, m.Len(), opts.Force); err != nil {
		return PullResult{}, err
	}
	if opts.DryRun {
		printPullPlan(out, items)
		return PullResult{}, nil
	}
	exec := NewPullExecutor(opts.LocalRoot, files)
	res := RunPull(ctx, items, exec, m, opts.Jobs)
	if err := m.Save(manifestPath); err != nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	return res, nil
}

// indexLocal keys local files by their relative path.
func indexLocal(local []LocalFile) map[string]LocalFile {
	m := make(map[string]LocalFile, len(local))
	for _, f := range local {
		m[f.Rel] = f
	}
	return m
}

// filterDrift drops download items that would overwrite a local file diverging
// from the manifest baseline (an unpushed local edit or an untracked file),
// warning on each. Deletes and downloads with no local file are kept.
func filterDrift(items []PullItem, localIdx map[string]LocalFile, m *manifest.Manifest, out io.Writer) []PullItem {
	kept := make([]PullItem, 0, len(items))
	for _, it := range items {
		if it.Op == PullDownload {
			if lf, exists := localIdx[it.Rel]; exists {
				e, tracked := m.Get(it.Rel)
				if !tracked || lf.Size != e.Size || lf.Mtime != e.LocalMtime {
					_, _ = fmt.Fprintf(out, "skip (local changed): %s\n", it.Rel)
					continue
				}
			}
		}
		kept = append(kept, it)
	}
	return kept
}

func dropPullDeletes(items []PullItem) []PullItem {
	kept := make([]PullItem, 0, len(items))
	for _, it := range items {
		if it.Op != PullDeleteLocal {
			kept = append(kept, it)
		}
	}
	return kept
}

func printPullPlan(out io.Writer, items []PullItem) {
	var down, del int
	for _, it := range items {
		switch it.Op {
		case PullDownload:
			down++
		case PullDeleteLocal:
			del++
		}
	}
	_, _ = fmt.Fprintf(out, "dry-run: %d to download, %d to delete locally\n", down, del)
	for _, it := range items {
		_, _ = fmt.Fprintf(out, "  %-13s %s\n", pullOpName(it.Op), it.Rel)
	}
}

func pullOpName(op PullOp) string {
	switch op {
	case PullDownload:
		return "download"
	case PullDeleteLocal:
		return "delete-local"
	default:
		return "?"
	}
}
