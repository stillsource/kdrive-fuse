package vfs

import (
	"sync"
	"time"

	"github.com/stillsource/kdrive-fuse/kdrive"
)

type dirEntry struct {
	files   []kdrive.FileInfo
	expires time.Time
}

// DirCache is a TTL cache for directory listings keyed by folder ID.
type DirCache struct {
	mu      sync.Mutex
	entries map[int64]dirEntry
	ttl     time.Duration
}

func NewDirCache(ttl time.Duration) *DirCache {
	return &DirCache{
		entries: make(map[int64]dirEntry),
		ttl:     ttl,
	}
}

func (c *DirCache) Get(folderID int64) ([]kdrive.FileInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[folderID]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.files, true
}

func (c *DirCache) Set(folderID int64, files []kdrive.FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[folderID] = dirEntry{files: files, expires: time.Now().Add(c.ttl)}
}

func (c *DirCache) Invalidate(folderID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, folderID)
}
