package syncer

import "fmt"

// deleteDivisor sets the deletion guard threshold: a run refuses to delete more
// than 1/deleteDivisor of the baseline manifest (i.e. 20%) without --force. This
// guards against a lost manifest or a wrong root silently wiping the remote.
const deleteDivisor = 5

// GuardDeletes returns an error when a push plan would delete more than 20% of
// the baseline manifest entries, unless force is set. An empty baseline always
// passes (a first run has nothing to over-delete).
func GuardDeletes(items []Item, baseline int, force bool) error {
	dels := 0
	for _, it := range items {
		if it.Op == OpDelete {
			dels++
		}
	}
	return checkDeleteRatio(dels, baseline, force)
}

// GuardPullDeletes is GuardDeletes for a pull plan (local deletions).
func GuardPullDeletes(items []PullItem, baseline int, force bool) error {
	dels := 0
	for _, it := range items {
		if it.Op == PullDeleteLocal {
			dels++
		}
	}
	return checkDeleteRatio(dels, baseline, force)
}

// checkDeleteRatio fails when dels exceeds 20% of baseline, unless force is set
// or the baseline is empty.
func checkDeleteRatio(dels, baseline int, force bool) error {
	if force || baseline == 0 {
		return nil
	}
	if dels*deleteDivisor > baseline {
		return fmt.Errorf("refusing to delete %d of %d tracked files (>%d%%); re-run with --force to override", dels, baseline, 100/deleteDivisor)
	}
	return nil
}
