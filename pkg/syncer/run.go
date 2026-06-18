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
	// Move relocates remote file remoteID from fromRel to toRel (a different
	// folder and/or a new name). The content is unchanged. Returns the
	// authoritative remote mtime after the relocation.
	Move(ctx context.Context, fromRel, toRel string, remoteID int64) (remoteMtime int64, err error)
}

// Result summarizes a push run.
type Result struct {
	Uploaded    int
	Overwritten int
	Deleted     int
	Moved       int
	Failed      int
	Errs        []error
}

type outcome struct {
	item        Item
	remoteID    int64
	remoteMtime int64
	err         error
}

// checkpointInterval is how many successful operations RunPush/RunPull apply
// before flushing the manifest to disk, bounding re-work after a crash.
const checkpointInterval = 64

// RunPush executes items with up to jobs concurrent Executor calls, updating the
// manifest as each action succeeds. A failed action leaves its manifest entry
// untouched so a re-run retries it. Manifest mutation is serialized on the
// calling goroutine (the manifest is not safe for concurrent writes); the
// in-memory manifest is updated but not persisted (the caller saves it).
func RunPush(ctx context.Context, items []Item, ex Executor, m *manifest.Manifest, jobs int, checkpoint func()) Result {
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
				if err := ctx.Err(); err != nil {
					// Cancelled (e.g. Ctrl-C): skip remaining work fast.
					o.err = err
					out <- o
					continue
				}
				switch it.Op {
				case OpUpload:
					o.remoteID, o.remoteMtime, o.err = ex.Upload(ctx, it.Rel, it.Size)
				case OpOverwrite:
					o.remoteMtime, o.err = ex.Overwrite(ctx, it.Rel, it.RemoteID, it.Size)
				case OpDelete:
					o.err = ex.Delete(ctx, it.Rel, it.RemoteID)
				case OpMove:
					o.remoteMtime, o.err = ex.Move(ctx, it.SrcRel, it.Rel, it.RemoteID)
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
	since := 0
	maybeCheckpoint := func() {
		if checkpoint == nil {
			return
		}
		since++
		if since >= checkpointInterval {
			checkpoint()
			since = 0
		}
	}
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
			maybeCheckpoint()
		case OpDelete:
			m.Delete(o.item.Rel)
			res.Deleted++
			maybeCheckpoint()
		case OpMove:
			m.Delete(o.item.SrcRel)
			m.Set(o.item.Rel, manifest.Entry{
				Size:        o.item.Size,
				LocalMtime:  o.item.Mtime,
				RemoteID:    o.item.RemoteID,
				RemoteMtime: o.remoteMtime, // authoritative post-move value from Stat
			})
			res.Moved++
			maybeCheckpoint()
		}
	}
	return res
}
