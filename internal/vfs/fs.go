package vfs

import (
	"os"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// fileClient is the union of file ports the VFS needs. Interim: Task 6 replaces
// KDriveFS's direct client dependency with use cases.
type fileClient interface {
	service.FileReader
	service.FileWriter
	service.FileManager
}

// KDriveFS holds the shared state used by all VFS nodes.
type KDriveFS struct {
	Files     fileClient
	Cache     service.ListingCache
	DiskCache service.ContentCache
	// Uid/Gid are stamped onto every node's attributes. kDrive has no POSIX
	// ownership, so without this nodes default to root (uid 0) and the mounting
	// user can't delete or edit them through file managers (no write on the
	// parent dir). Default to the process owner — the user who ran the mount.
	Uid uint32
	Gid uint32
}

// NewKDriveFS wires shared state for VFS nodes.
func NewKDriveFS(files fileClient, cacheTTL time.Duration, disk service.ContentCache) *KDriveFS {
	return &KDriveFS{
		Files:     files,
		Cache:     listingcache.NewDirCache(cacheTTL),
		DiskCache: disk,
		Uid:       uint32(os.Getuid()),
		Gid:       uint32(os.Getgid()),
	}
}

// applyOwner stamps the mounting user's uid/gid onto a FUSE attribute block so
// files and directories aren't reported as root-owned.
func (k *KDriveFS) applyOwner(attr *fuse.Attr) {
	attr.Uid = k.Uid
	attr.Gid = k.Gid
}

// NewRootDirNode returns the root directory node for mounting.
func NewRootDirNode(kdfs *KDriveFS, rootFolderID int64) *DirNode {
	return &DirNode{kdfs: kdfs, folderID: rootFolderID}
}
