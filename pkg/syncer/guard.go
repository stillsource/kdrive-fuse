package syncer

import "fmt"

// GuardDeletes returns an error when a push plan would delete more than
// threshold (a fraction, e.g. 0.20) of the baseline manifest entries, unless
// force is set. An empty baseline always passes (a first run, with no entries,
// can plan no deletions).
func GuardDeletes(items []Item, baseline int, threshold float64, force bool) error {
	dels := 0
	for _, it := range items {
		if it.Op == OpDelete {
			dels++
		}
	}
	return checkDeleteRatio(dels, baseline, threshold, force)
}

// GuardPullDeletes is GuardDeletes for a pull plan (local deletions).
func GuardPullDeletes(items []PullItem, baseline int, threshold float64, force bool) error {
	dels := 0
	for _, it := range items {
		if it.Op == PullDeleteLocal {
			dels++
		}
	}
	return checkDeleteRatio(dels, baseline, threshold, force)
}

// checkDeleteRatio fails when dels exceeds threshold×baseline, unless force is
// set or the baseline is empty. A zero threshold defaults to 0.20.
func checkDeleteRatio(dels, baseline int, threshold float64, force bool) error {
	if force || baseline == 0 {
		return nil
	}
	if threshold == 0 {
		threshold = 0.20
	}
	if float64(dels) > threshold*float64(baseline) {
		return fmt.Errorf("refusing to delete %d of %d tracked files (>%.0f%%); re-run with --force to override", dels, baseline, threshold*100)
	}
	return nil
}
