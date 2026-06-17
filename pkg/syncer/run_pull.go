package syncer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

// Downloader streams the full content of a remote file.
type Downloader interface {
	DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error)
}

// PullActor performs the side-effecting half of a pull against the local tree.
// It is called concurrently and must be safe for concurrent use.
type PullActor interface {
	// Download writes remote file remoteID to the local path rel and returns the
	// written size and the resulting local mtime (Unix seconds).
	Download(ctx context.Context, rel string, remoteID int64) (size, localMtime int64, err error)
	// DeleteLocal removes the local file at rel.
	DeleteLocal(rel string) error
}

// PullResult summarizes a pull run.
type PullResult struct {
	Downloaded int
	Deleted    int
	Failed     int
	Errs       []error
}

type pullOutcome struct {
	item       PullItem
	size       int64
	localMtime int64
	err        error
}

// RunPull executes items with up to jobs concurrent PullActor calls, updating
// the manifest as each action succeeds. A failed action leaves its manifest
// entry untouched so a re-run retries it. Manifest mutation is serialized on the
// calling goroutine; the in-memory manifest is updated but not persisted.
func RunPull(ctx context.Context, items []PullItem, actor PullActor, m *manifest.Manifest, jobs int) PullResult {
	if jobs < 1 {
		jobs = 1
	}
	in := make(chan PullItem)
	out := make(chan pullOutcome)

	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range in {
				o := pullOutcome{item: it}
				switch it.Op {
				case PullDownload:
					o.size, o.localMtime, o.err = actor.Download(ctx, it.Rel, it.RemoteID)
				case PullDeleteLocal:
					o.err = actor.DeleteLocal(it.Rel)
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

	var res PullResult
	for o := range out {
		if o.err != nil {
			res.Failed++
			res.Errs = append(res.Errs, o.err)
			continue
		}
		switch o.item.Op {
		case PullDownload:
			m.Set(o.item.Rel, manifest.Entry{
				Size:        o.size,
				LocalMtime:  o.localMtime,
				RemoteID:    o.item.RemoteID,
				RemoteMtime: o.item.RemoteMtime,
			})
			res.Downloaded++
		case PullDeleteLocal:
			m.Delete(o.item.Rel)
			res.Deleted++
		}
	}
	return res
}

// PullExecutor is the concrete PullActor: it streams remote content into the
// local tree (atomically) and removes local files.
type PullExecutor struct {
	localRoot string
	dl        Downloader
}

// NewPullExecutor builds a PullExecutor writing under localRoot, downloading via dl.
func NewPullExecutor(localRoot string, dl Downloader) *PullExecutor {
	return &PullExecutor{localRoot: localRoot, dl: dl}
}

// Download streams remoteID to localRoot/rel via a temp file + rename, and
// returns the written size and the local mtime.
func (e *PullExecutor) Download(ctx context.Context, rel string, remoteID int64) (int64, int64, error) {
	rc, err := e.dl.DownloadStream(ctx, remoteID, 0, 0)
	if err != nil {
		return 0, 0, err
	}
	defer rc.Close() //nolint:errcheck // read-only

	dst := e.local(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, 0, err
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, 0, err
	}
	n, err := io.Copy(f, rc)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return 0, 0, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return 0, 0, err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return 0, 0, err
	}
	return n, info.ModTime().Unix(), nil
}

// DeleteLocal removes the local file at rel.
func (e *PullExecutor) DeleteLocal(rel string) error {
	return os.Remove(e.local(rel))
}

func (e *PullExecutor) local(rel string) string {
	return filepath.Join(e.localRoot, filepath.FromSlash(rel))
}
