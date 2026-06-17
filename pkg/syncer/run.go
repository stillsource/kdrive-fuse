package syncer

import (
	"context"
	"sync"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

// Executor performs the side-effecting half of a push: it carries out one
// planned action against the remote (resolving the parent folder, opening the
// local file, calling the upload/delete use cases — see PR #5). It is called
// concurrently and must be safe for concurrent use.
type Executor interface {
	// Upload creates a new remote file from the local file at rel and returns
	// its new remote id and remote mtime.
	Upload(ctx context.Context, rel string, size int64) (remoteID, remoteMtime int64, err error)
	// Overwrite replaces the content of remote file remoteID from the local file
	// at rel and returns the new remote mtime.
	Overwrite(ctx context.Context, rel string, remoteID, size int64) (remoteMtime int64, err error)
	// Delete removes the remote file remoteID.
	Delete(ctx context.Context, rel string, remoteID int64) error
}

// Result summarizes a push run.
type Result struct {
	Uploaded    int
	Overwritten int
	Deleted     int
	Failed      int
	Errs        []error
}

type outcome struct {
	item        Item
	remoteID    int64
	remoteMtime int64
	err         error
}

// RunPush executes items with up to jobs concurrent Executor calls, updating the
// manifest as each action succeeds. A failed action leaves its manifest entry
// untouched so a re-run retries it. Manifest mutation is serialized on the
// calling goroutine (the manifest is not safe for concurrent writes); the
// in-memory manifest is updated but not persisted (the caller saves it).
func RunPush(ctx context.Context, items []Item, ex Executor, m *manifest.Manifest, jobs int) Result {
	if jobs < 1 {
		jobs = 1
	}
	in := make(chan Item)
	out := make(chan outcome)

	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range in {
				o := outcome{item: it, remoteID: it.RemoteID}
				switch it.Op {
				case OpUpload:
					o.remoteID, o.remoteMtime, o.err = ex.Upload(ctx, it.Rel, it.Size)
				case OpOverwrite:
					o.remoteMtime, o.err = ex.Overwrite(ctx, it.Rel, it.RemoteID, it.Size)
				case OpDelete:
					o.err = ex.Delete(ctx, it.Rel, it.RemoteID)
				}
				out <- o
			}
		}()
	}

	go func() {
		for _, it := range items {
			in <- it
		}
		close(in)
	}()
	go func() {
		wg.Wait()
		close(out)
	}()

	var res Result
	for o := range out {
		if o.err != nil {
			res.Failed++
			res.Errs = append(res.Errs, o.err)
			continue
		}
		switch o.item.Op {
		case OpUpload, OpOverwrite:
			m.Set(o.item.Rel, manifest.Entry{
				Size:        o.item.Size,
				LocalMtime:  o.item.Mtime,
				RemoteID:    o.remoteID,
				RemoteMtime: o.remoteMtime,
			})
			if o.item.Op == OpUpload {
				res.Uploaded++
			} else {
				res.Overwritten++
			}
		case OpDelete:
			m.Delete(o.item.Rel)
			res.Deleted++
		}
	}
	return res
}
