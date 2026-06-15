package fuse

import (
	"context"
	"log/slog"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// DirNode represents a kDrive directory.
type DirNode struct {
	fs.Inode
	kdfs     *KDriveFS
	folderID int64
}

var _ fs.NodeLookuper = (*DirNode)(nil)
var _ fs.NodeReaddirer = (*DirNode)(nil)
var _ fs.NodeGetattrer = (*DirNode)(nil)
var _ fs.NodeCreater = (*DirNode)(nil)
var _ fs.NodeMkdirer = (*DirNode)(nil)
var _ fs.NodeUnlinker = (*DirNode)(nil)
var _ fs.NodeRmdirer = (*DirNode)(nil)
var _ fs.NodeRenamer = (*DirNode)(nil)

// Getattr returns directory attributes.
func (d *DirNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o755 | syscall.S_IFDIR
	d.kdfs.applyOwner(&out.Attr)
	out.SetTimeout(30 * time.Second)
	return 0
}

func (d *DirNode) list(ctx context.Context) ([]domain.FileInfo, error) {
	return d.kdfs.ListDir.Execute(ctx, d.folderID)
}

// Readdir streams directory entries from the cached (or freshly fetched) listing.
func (d *DirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	files, err := d.list(ctx)
	if err != nil {
		slog.Warn("readdir failed", "folder", d.folderID, "err", err)
		return nil, syscall.EIO
	}
	entries := make([]fuse.DirEntry, 0, len(files))
	for _, f := range files {
		mode := uint32(syscall.S_IFREG)
		if f.IsDir() {
			mode = syscall.S_IFDIR
		}
		entries = append(entries, fuse.DirEntry{
			Mode: mode,
			Name: f.Name,
			Ino:  uint64(f.ID),
		})
	}
	return fs.NewListDirStream(entries), 0
}

// Lookup resolves a named child to a DirNode or FileNode.
func (d *DirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	files, err := d.list(ctx)
	if err != nil {
		return nil, syscall.EIO
	}
	for _, f := range files {
		if f.Name != name {
			continue
		}
		out.SetAttrTimeout(30 * time.Second)
		out.SetEntryTimeout(30 * time.Second)
		d.kdfs.applyOwner(&out.Attr)
		if f.IsDir() {
			out.Mode = 0o755 | syscall.S_IFDIR
			node := &DirNode{kdfs: d.kdfs, folderID: f.ID}
			return d.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFDIR, Ino: uint64(f.ID)}), 0
		}
		out.Mode = 0o644 | syscall.S_IFREG
		out.Size = uint64(f.Size)
		out.Mtime = uint64(f.LastModifiedAt)
		out.Ctime = uint64(f.CreatedAt)
		node := &FileNode{kdfs: d.kdfs, info: f}
		return d.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFREG, Ino: uint64(f.ID)}), 0
	}
	return nil, syscall.ENOENT
}

// Mkdir creates a new directory.
func (d *DirNode) Mkdir(ctx context.Context, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	info, err := d.kdfs.MakeDir.Execute(ctx, d.folderID, name)
	if err != nil {
		slog.Warn("mkdir failed", "parent", d.folderID, "name", name, "err", err)
		return nil, syscall.EIO
	}

	out.Mode = 0o755 | syscall.S_IFDIR
	out.SetAttrTimeout(30 * time.Second)
	out.SetEntryTimeout(30 * time.Second)
	d.kdfs.applyOwner(&out.Attr)
	node := &DirNode{kdfs: d.kdfs, folderID: info.ID}
	return d.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFDIR, Ino: uint64(info.ID)}), 0
}

// Create makes a new file placeholder and returns a writable handle.
// The real ID is assigned after the upload completes (patched via onUploaded).
func (d *DirNode) Create(ctx context.Context, name string, _ uint32, _ uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	// Temporary inode number — stable during the handle's lifetime, replaced on next Lookup.
	tmpIno := uint64(d.folderID)<<32 ^ uint64(len(name))
	node := &FileNode{kdfs: d.kdfs, info: domain.FileInfo{Name: name}}
	child := d.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFREG, Ino: tmpIno})

	wh, err := newWriteHandle(d.kdfs.SeedContent, d.kdfs.CommitWrite, d.folderID, 0, name, func(info domain.FileInfo) {
		// CommitWrite already invalidates the parent listing; only patch the node.
		node.info = info
	})
	if err != nil {
		slog.Error("create tempfile", "err", err)
		return nil, nil, 0, syscall.EIO
	}

	out.Mode = 0o644 | syscall.S_IFREG
	out.SetAttrTimeout(30 * time.Second)
	out.SetEntryTimeout(30 * time.Second)
	d.kdfs.applyOwner(&out.Attr)
	return child, wh, 0, 0
}

// Unlink soft-deletes a file.
func (d *DirNode) Unlink(ctx context.Context, name string) syscall.Errno {
	return d.removeChild(ctx, name, false)
}

// Rmdir soft-deletes a directory.
func (d *DirNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	return d.removeChild(ctx, name, true)
}

func (d *DirNode) removeChild(ctx context.Context, name string, wantDir bool) syscall.Errno {
	files, err := d.list(ctx)
	if err != nil {
		return syscall.EIO
	}
	for _, f := range files {
		if f.Name != name {
			continue
		}
		if f.IsDir() != wantDir {
			if wantDir {
				return syscall.ENOTDIR
			}
			return syscall.EISDIR
		}
		if err := d.kdfs.DeleteEntry.Execute(ctx, f.ID, d.folderID); err != nil {
			slog.Warn("delete failed", "file", f.ID, "err", err)
			return syscall.EIO
		}
		return 0
	}
	return syscall.ENOENT
}

// Rename relocates and/or renames a child.
func (d *DirNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	files, err := d.list(ctx)
	if err != nil {
		return syscall.EIO
	}
	var src domain.FileInfo
	for _, f := range files {
		if f.Name == name {
			src = f
			break
		}
	}
	if src.ID == 0 {
		return syscall.ENOENT
	}

	target, ok := newParent.(*DirNode)
	if !ok {
		return syscall.EXDEV
	}

	if err := d.kdfs.RenameEntry.Execute(ctx, src.ID, d.folderID, target.folderID, name, newName); err != nil {
		slog.Warn("rename failed", "file", src.ID, "dest", target.folderID, "newName", newName, "err", err)
		return syscall.EIO
	}
	return 0
}
