package vfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// DiskCache stores file content on disk keyed by (fileID, mtime).
// New mtime → new path → old entries are orphaned and reclaimed by the LRU cleanup.
type DiskCache struct {
	dir     string
	maxB    int64
	files   service.FileReader
	locks   sync.Map // fileID → *sync.Mutex
	evictMu sync.Mutex
}

// NewDiskCache creates/uses dir to store cached file bodies, capped at maxBytes.
func NewDiskCache(dir string, maxBytes int64, files service.FileReader) (*DiskCache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("disk cache mkdir: %w", err)
	}
	return &DiskCache{dir: dir, maxB: maxBytes, files: files}, nil
}

func (c *DiskCache) path(fileID, mtime int64) string {
	return filepath.Join(c.dir, fmt.Sprintf("%d_%d", fileID, mtime))
}

// Open returns an *os.File for reading. Downloads and caches it if missing.
// Caller must Close.
func (c *DiskCache) Open(ctx context.Context, fileID, mtime, size int64) (*os.File, error) {
	p := c.path(fileID, mtime)

	lkAny, _ := c.locks.LoadOrStore(fileID, &sync.Mutex{})
	lk := lkAny.(*sync.Mutex)
	lk.Lock()
	defer lk.Unlock()

	if f, err := os.Open(p); err == nil {
		now := time.Now()
		_ = os.Chtimes(p, now, now) // bump atime for LRU
		return f, nil
	}

	if err := c.evictIfNeeded(size); err != nil {
		return nil, err
	}

	tmp := p + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return nil, fmt.Errorf("cache create: %w", err)
	}
	rc, err := c.files.DownloadStream(ctx, fileID, 0, 0)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = rc.Close()
		_ = out.Close()
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("cache download: %w", err)
	}
	_ = rc.Close()
	_ = out.Close()
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("cache rename: %w", err)
	}
	return os.Open(p)
}

// evictIfNeeded drops the oldest-atime entries until there's room for need bytes.
func (c *DiskCache) evictIfNeeded(need int64) error {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	type fi struct {
		path  string
		size  int64
		atime int64
	}
	var items []fi
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		items = append(items, fi{
			path:  filepath.Join(c.dir, e.Name()),
			size:  info.Size(),
			atime: info.ModTime().Unix(),
		})
	}
	if total+need <= c.maxB {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].atime < items[j].atime })
	for _, it := range items {
		if total+need <= c.maxB {
			break
		}
		if err := os.Remove(it.path); err == nil {
			total -= it.size
		}
	}
	return nil
}
