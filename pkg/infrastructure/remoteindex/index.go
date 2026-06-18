// Package remoteindex builds a recursive snapshot of a kDrive folder tree and
// resolves (creating as needed) a relative path to a folder id. It is the
// remote-side input to kdrive sync: Build maps every remote file to its id,
// size and mtime; Resolver turns a local file's directory into the remote
// folder id to upload into.
package remoteindex

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Entry is the remote metadata of one file, keyed by its path relative to the
// index root.
type Entry struct {
	ID    int64
	Size  int64
	Mtime int64 // remote last_modified_at (Unix seconds)
}

// Lister lists the direct children of a folder.
type Lister interface {
	List(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
}

// defaultParallelism bounds the number of concurrent List calls during Build
// when no WithParallelism option overrides it.
const defaultParallelism = 8

// buildConfig holds Build's tunables.
type buildConfig struct{ parallelism int }

// Option configures Build.
type Option func(*buildConfig)

// WithParallelism bounds the number of concurrent List calls during Build. A
// value <= 0 keeps the default (defaultParallelism), so callers can thread an
// unset config field straight through without a special case.
func WithParallelism(n int) Option {
	return func(c *buildConfig) {
		if n > 0 {
			c.parallelism = n
		}
	}
}

// Build walks the folder tree rooted at rootID and returns a map from each
// file's path (relative to the root, slash-separated) to its Entry. Directories
// are traversed but not themselves recorded. Listings run concurrently, bounded
// to the configured parallelism (defaultParallelism unless WithParallelism is
// given).
func Build(ctx context.Context, l Lister, rootID int64, opts ...Option) (map[string]Entry, error) {
	cfg := buildConfig{parallelism: defaultParallelism}
	for _, o := range opts {
		o(&cfg)
	}
	idx := make(map[string]Entry)
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.parallelism)

	g, ctx := errgroup.WithContext(ctx)
	var walk func(folderID int64, prefix string)
	walk = func(folderID int64, prefix string) {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			children, err := l.List(ctx, folderID)
			<-sem
			if err != nil {
				return err
			}
			for _, c := range children {
				rel := c.Name
				if prefix != "" {
					rel = prefix + "/" + c.Name
				}
				if c.IsDir() {
					walk(c.ID, rel)
				} else {
					mu.Lock()
					idx[rel] = Entry{ID: c.ID, Size: c.Size, Mtime: c.LastModifiedAt}
					mu.Unlock()
				}
			}
			return nil
		})
	}
	walk(rootID, "")
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return idx, nil
}
