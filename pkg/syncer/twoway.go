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

// TwoWayOptions configures a two-way sync run.
type TwoWayOptions struct {
	LocalRoot       string
	Jobs            int
	Force           bool
	DryRun          bool
	NoDelete        bool // when true, suppress all deletions on both sides
	DeleteThreshold float64
}

// TwoWayResult summarizes a two-way run.
type TwoWayResult struct {
	Pushed    int
	Pulled    int
	Conflicts int
	Failed    int
}

// TwoWay reconciles opts.LocalRoot and the remote folder rootID in one pass:
// non-conflicting changes are applied in their direction; a file changed on both
// sides is reported as a conflict and left untouched. Deletions on each direction
// are subject to the deletion guard. In DryRun it prints the plan and changes
// nothing. On a first run (empty manifest), paths that exist on both sides with
// the same size are treated as already-synced; paths present on both sides with
// a DIFFERENT size are reported as a conflict rather than letting one side
// silently win.
//
// Note: --assume-new, --refresh, and --detect-moves do not apply to two-way sync.
func TwoWay(ctx context.Context, opts TwoWayOptions, files Remote, rootID int64, manifestPath string, out io.Writer) (TwoWayResult, error) {
	idx, err := remoteindex.Build(ctx, files, rootID)
	if err != nil {
		return TwoWayResult{}, fmt.Errorf("remote index: %w", err)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return TwoWayResult{}, err
	}
	local, err := WalkLocal(opts.LocalRoot)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return TwoWayResult{}, fmt.Errorf("walk %s: %w", opts.LocalRoot, err)
		}
		local = nil
	}
	if m.Len() == 0 {
		seedSynced(m, idx, local)
	}
	plan := PlanTwoWay(local, idx, m)
	if opts.NoDelete {
		plan.Push = dropDeletes(plan.Push)
		plan.Pull = dropPullDeletes(plan.Pull)
	}
	for _, rel := range plan.Conflicts {
		_, _ = fmt.Fprintf(out, "conflict (changed on both sides, skipped): %s\n", rel)
	}
	if err := GuardDeletes(plan.Push, m.Len(), opts.DeleteThreshold, opts.Force); err != nil {
		return TwoWayResult{}, err
	}
	if err := GuardPullDeletes(plan.Pull, m.Len(), opts.DeleteThreshold, opts.Force); err != nil {
		return TwoWayResult{}, err
	}
	if opts.DryRun {
		printTwoWayPlan(out, plan)
		return TwoWayResult{Conflicts: len(plan.Conflicts)}, nil
	}
	checkpoint := func() { _ = m.Save(manifestPath) }
	resolver := remoteindex.NewResolver(files, files, rootID)
	pushExec := NewPushExecutor(opts.LocalRoot, resolver, files, files, files, files, files)
	pullExec := NewPullExecutor(opts.LocalRoot, files)
	pullRes := RunPull(ctx, plan.Pull, pullExec, m, opts.Jobs, checkpoint)
	pushRes := RunPush(ctx, plan.Push, pushExec, m, opts.Jobs, checkpoint)
	if err := m.Save(manifestPath); err != nil {
		return TwoWayResult{}, fmt.Errorf("save manifest: %w", err)
	}
	return TwoWayResult{
		Pushed:    pushRes.Uploaded + pushRes.Overwritten + pushRes.Deleted,
		Pulled:    pullRes.Downloaded + pullRes.Deleted,
		Conflicts: len(plan.Conflicts),
		Failed:    pushRes.Failed + pullRes.Failed,
	}, nil
}

// seedSynced seeds the baseline only for files present on both sides with the
// SAME size, so a first two-way run does not re-classify an already-synced tree.
// A path that exists on both sides with a DIFFERING size is deliberately left
// unseeded, so PlanTwoWay surfaces it as a (both-new) conflict instead of letting
// one side silently win. (Same-size-but-different-content is indistinguishable
// without a content hash, which the kDrive API does not expose — the size match
// is the best available signal, consistent with the rest of the syncer.)
func seedSynced(m *manifest.Manifest, idx map[string]remoteindex.Entry, local []LocalFile) {
	for _, f := range local {
		r, ok := idx[f.Rel]
		if !ok || r.Size != f.Size {
			continue
		}
		m.Set(f.Rel, manifest.Entry{Size: r.Size, LocalMtime: f.Mtime, RemoteID: r.ID, RemoteMtime: r.Mtime})
	}
}

func printTwoWayPlan(out io.Writer, p TwoWayPlan) {
	_, _ = fmt.Fprintf(out, "dry-run: %d to push, %d to pull, %d conflicts\n", len(p.Push), len(p.Pull), len(p.Conflicts))
	for _, it := range p.Push {
		_, _ = fmt.Fprintf(out, "  push %-9s %s\n", opName(it.Op), it.Rel)
	}
	for _, it := range p.Pull {
		_, _ = fmt.Fprintf(out, "  pull %-13s %s\n", pullOpName(it.Op), it.Rel)
	}
}
