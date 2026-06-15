package di

import "github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"

// ContentCache returns the memoized disk-backed content cache. The cache is
// memoized only on success, so a transient failure can be retried.
func (c *Container) ContentCache() (*contentcache.DiskCache, error) {
	if c.content == nil {
		content, err := contentcache.NewDiskCache(c.cfg.DiskCacheDir, c.cfg.DiskCacheBytes, c.Client().Files)
		if err != nil {
			return nil, err
		}
		c.content = content
	}
	return c.content, nil
}
