package vfs

import (
	"time"

	"github.com/stillsource/kdrive-fuse/kdrive"
)

// KDriveFS holds the shared state used by all VFS nodes.
type KDriveFS struct {
	Files     kdrive.Files
	Cache     *DirCache
	DiskCache *DiskCache
}

// NewKDriveFS wires shared state for VFS nodes.
func NewKDriveFS(files kdrive.Files, cacheTTL time.Duration, disk *DiskCache) *KDriveFS {
	return &KDriveFS{
		Files:     files,
		Cache:     NewDirCache(cacheTTL),
		DiskCache: disk,
	}
}

// NewRootDirNode returns the root directory node for mounting.
func NewRootDirNode(kdfs *KDriveFS, rootFolderID int64) *DirNode {
	return &DirNode{kdfs: kdfs, folderID: rootFolderID}
}
