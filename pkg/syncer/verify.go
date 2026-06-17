package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// VerifyResult summarizes a presence + size comparison of a local tree against
// its remote copy.
type VerifyResult struct {
	OK            int // present on both sides at the same size
	MissingRemote int // present locally, absent remotely
	MissingLocal  int // present remotely, absent locally
	SizeDiff      int // present on both sides at different sizes
}

// Issues returns the total number of discrepancies.
func (r VerifyResult) Issues() int { return r.MissingRemote + r.MissingLocal + r.SizeDiff }

// Verify compares localRoot against the remote folder rootID by presence and
// size only (metadata, no content download), writing each discrepancy to out.
// kDrive verifies each upload's xxh3 hash, so a file present on both sides at
// the same size is content-correct.
func Verify(ctx context.Context, localRoot string, lister remoteindex.Lister, rootID int64, out io.Writer) (VerifyResult, error) {
	idx, err := remoteindex.Build(ctx, lister, rootID)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("remote index: %w", err)
	}
	local, err := WalkLocal(localRoot)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return VerifyResult{}, fmt.Errorf("walk %s: %w", localRoot, err)
		}
		local = nil
	}

	var res VerifyResult
	localIdx := indexLocal(local)
	for _, f := range local {
		switch r, ok := idx[f.Rel]; {
		case !ok:
			_, _ = fmt.Fprintf(out, "MISSING remote   %s\n", f.Rel)
			res.MissingRemote++
		case r.Size != f.Size:
			_, _ = fmt.Fprintf(out, "SIZE %d->%d   %s\n", f.Size, r.Size, f.Rel)
			res.SizeDiff++
		default:
			res.OK++
		}
	}
	for rel := range idx {
		if _, ok := localIdx[rel]; !ok {
			_, _ = fmt.Fprintf(out, "MISSING local    %s\n", rel)
			res.MissingLocal++
		}
	}
	return res, nil
}
