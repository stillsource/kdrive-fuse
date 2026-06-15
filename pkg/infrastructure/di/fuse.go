package di

import "github.com/stillsource/kdrive-fuse/pkg/presentation/fuse"

// KDriveFS returns the memoized FUSE composition root, delegating use-case
// wiring to fuse.NewKDriveFS over the API client and the content cache.
func (c *Container) KDriveFS() (*fuse.KDriveFS, error) {
	if c.kdfs == nil {
		content, err := c.ContentCache()
		if err != nil {
			return nil, err
		}
		c.kdfs = fuse.NewKDriveFS(c.Client().Files, c.cfg.CacheTTL, content)
	}
	return c.kdfs, nil
}

// RootNode returns the root directory node for mounting.
func (c *Container) RootNode() (*fuse.DirNode, error) {
	kdfs, err := c.KDriveFS()
	if err != nil {
		return nil, err
	}
	return fuse.NewRootDirNode(kdfs, c.cfg.RootFolderID), nil
}
