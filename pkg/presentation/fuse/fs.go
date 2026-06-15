package fuse

import (
	"os"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// fileClient is the union of file ports the FUSE composition root needs to wire
// the use cases. It mirrors the remote-client surface the use cases consume.
type fileClient interface {
	service.FileReader
	service.FileWriter
	service.FileManager
}

// KDriveFS is the FUSE composition root: it holds the use cases the nodes and
// handles invoke, plus the uid/gid stamped onto every node's attributes.
type KDriveFS struct {
	ListDir     *usecase.ListDir
	ReadFile    *usecase.ReadFile
	SeedContent *usecase.SeedContent
	CommitWrite *usecase.CommitWrite
	DeleteEntry *usecase.DeleteEntry
	RenameEntry *usecase.RenameEntry
	MakeDir     *usecase.MakeDir

	// Uid/Gid are stamped onto every node's attributes. kDrive has no POSIX
	// ownership, so without this nodes default to root (uid 0) and the mounting
	// user can't delete or edit them through file managers (no write on the
	// parent dir). Default to the process owner — the user who ran the mount.
	Uid uint32
	Gid uint32
}

// NewKDriveFS is the FUSE composition root: it builds the listing cache and
// wires every use case over the given remote client and content cache.
func NewKDriveFS(files fileClient, cacheTTL time.Duration, disk service.ContentCache) *KDriveFS {
	listing := listingcache.NewDirCache(cacheTTL)
	return &KDriveFS{
		ListDir:     usecase.NewListDir(files, listing),
		ReadFile:    usecase.NewReadFile(disk),
		SeedContent: usecase.NewSeedContent(files),
		CommitWrite: usecase.NewCommitWrite(files, listing),
		DeleteEntry: usecase.NewDeleteEntry(files, listing),
		RenameEntry: usecase.NewRenameEntry(files, listing),
		MakeDir:     usecase.NewMakeDir(files, listing),
		Uid:         uint32(os.Getuid()),
		Gid:         uint32(os.Getgid()),
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
