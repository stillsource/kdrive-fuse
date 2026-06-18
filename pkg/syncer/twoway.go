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
// nothing.
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
		Bootstrap(m, idx, local)
	}
	plan := PlanTwoWay(local, idx, m)
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

func printTwoWayPlan(out io.Writer, p TwoWayPlan) {
	_, _ = fmt.Fprintf(out, "dry-run: %d to push, %d to pull, %d conflicts\n", len(p.Push), len(p.Pull), len(p.Conflicts))
	for _, it := range p.Push {
		_, _ = fmt.Fprintf(out, "  push %-9s %s\n", opName(it.Op), it.Rel)
	}
	for _, it := range p.Pull {
		_, _ = fmt.Fprintf(out, "  pull %-13s %s\n", pullOpName(it.Op), it.Rel)
	}
}
