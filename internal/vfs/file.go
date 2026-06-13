package vfs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/kdrive"
)

// FileNode represents a kDrive file.
type FileNode struct {
	fs.Inode
	kdfs *KDriveFS
	info kdrive.FileInfo

	mu sync.Mutex
	wh *writeHandle // active write handle, so a late Setattr(size) can truncate it
}

var _ fs.NodeGetattrer = (*FileNode)(nil)
var _ fs.NodeOpener = (*FileNode)(nil)
var _ fs.NodeSetattrer = (*FileNode)(nil)

// Getattr returns file attributes.
func (f *FileNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o644 | syscall.S_IFREG
	out.Size = uint64(f.info.Size)
	out.Mtime = uint64(f.info.LastModifiedAt)
	out.Ctime = uint64(f.info.CreatedAt)
	out.SetTimeout(30 * time.Second)
	return 0
}

// Setattr accepts size/time updates as no-ops so truncate-on-open succeeds.
func (f *FileNode) Setattr(_ context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if size, ok := in.GetSize(); ok {
		f.info.Size = int64(size)
		// The kernel delivers O_TRUNC as a path-based Setattr (no file handle)
		// that can arrive *after* Open. Propagate the truncate to the active
		// write handle so a short rewrite doesn't keep a stale tail.
		f.mu.Lock()
		wh := f.wh
		f.mu.Unlock()
		if wh != nil {
			wh.truncateTo(int64(size))
		}
	}
	out.Mode = 0o644 | syscall.S_IFREG
	out.Size = uint64(f.info.Size)
	out.Mtime = uint64(f.info.LastModifiedAt)
	out.Ctime = uint64(f.info.CreatedAt)
	out.SetTimeout(30 * time.Second)
	return 0
}

// Open returns a read or write handle depending on flags.
// For O_WRONLY|O_RDWR without O_TRUNC, seeds the tempfile with remote content.
func (f *FileNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	writable := flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0
	truncate := flags&syscall.O_TRUNC != 0

	if !writable {
		return &readHandle{kdfs: f.kdfs, info: f.info}, fuse.FOPEN_KEEP_CACHE, 0
	}

	_, parent := f.EmbeddedInode().Parent()
	if parent == nil {
		slog.Error("open writable: no parent", "name", f.info.Name)
		return nil, 0, syscall.EIO
	}
	pDir, ok := parent.Operations().(*DirNode)
	if !ok {
		slog.Error("open writable: parent not DirNode", "name", f.info.Name)
		return nil, 0, syscall.EIO
	}

	wh, err := newWriteHandle(f.kdfs.Files, pDir.folderID, f.info.ID, f.info.Name, func(info kdrive.FileInfo) {
		f.info = info
		f.kdfs.Cache.Invalidate(pDir.folderID)
	})
	if err != nil {
		return nil, 0, syscall.EIO
	}
	wh.node = f
	// Seed the tempfile with current remote content only for a genuine partial
	// write. When O_TRUNC is requested the kernel either drops f.info.Size to 0
	// via a Setattr before Open (caught by the size guard here) or sends that
	// Setattr after Open (caught by truncateTo) — either way we must not restore
	// a stale tail under the freshly written bytes.
	if !truncate && f.info.ID > 0 && f.info.Size > 0 {
		rc, err := f.kdfs.Files.DownloadStream(ctx, f.info.ID, 0, 0)
		if err == nil {
			_, _ = io.Copy(wh.tmp, rc)
			_ = rc.Close()
		}
	}
	// Publish the handle only after seeding, so a late truncate can't race the copy.
	f.mu.Lock()
	f.wh = wh
	f.mu.Unlock()
	return wh, 0, 0
}

// readHandle serves reads from a disk-cached copy, downloading on first Read.
type readHandle struct {
	kdfs    *KDriveFS
	info    kdrive.FileInfo
	mu      sync.Mutex
	file    *os.File
	opened  bool
	openErr error
}

var _ fs.FileReader = (*readHandle)(nil)
var _ fs.FileReleaser = (*readHandle)(nil)

func (h *readHandle) ensureOpen(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.opened {
		if h.openErr != nil {
			return syscall.EIO
		}
		return 0
	}
	h.opened = true
	if h.kdfs.DiskCache == nil {
		h.openErr = errNoCache
		return syscall.EIO
	}
	f, err := h.kdfs.DiskCache.Open(ctx, h.info.ID, h.info.LastModifiedAt, h.info.Size)
	if err != nil {
		slog.Warn("disk cache open", "id", h.info.ID, "err", err)
		h.openErr = err
		return syscall.EIO
	}
	h.file = f
	return 0
}

// Read returns dest[:n] from the cached file.
func (h *readHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if errno := h.ensureOpen(ctx); errno != 0 {
		return nil, errno
	}
	n, err := h.file.ReadAt(dest, off)
	if err != nil && err != io.EOF {
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(dest[:n]), 0
}

// Release closes the cached file handle.
func (h *readHandle) Release(_ context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file != nil {
		_ = h.file.Close()
		h.file = nil
	}
	return 0
}

var errNoCache = errors.New("disk cache not configured")

// writeHandle buffers writes to a tempfile and uploads on Flush.
// existingFileID > 0 enters edit mode (replace remote content); 0 creates a new file.
type writeHandle struct {
	files          kdrive.Files
	parentID       int64
	existingFileID int64
	name           string
	tmp            *os.File
	onUploaded     func(kdrive.FileInfo)
	node           *FileNode // set by Open; lets a late Setattr truncate this handle
	mu             sync.Mutex
	uploaded       bool
}

// truncateTo resizes the buffered tempfile. The kernel may send a truncating
// Setattr after Open, so this keeps the buffer in sync with the requested size.
func (h *writeHandle) truncateTo(n int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tmp != nil {
		_ = h.tmp.Truncate(n)
	}
}

var _ fs.FileWriter = (*writeHandle)(nil)
var _ fs.FileFlusher = (*writeHandle)(nil)
var _ fs.FileReleaser = (*writeHandle)(nil)

func newWriteHandle(files kdrive.Files, parentID, existingFileID int64, name string, onUploaded func(kdrive.FileInfo)) (*writeHandle, error) {
	tmp, err := os.CreateTemp("", "kdrive-upload-*")
	if err != nil {
		return nil, err
	}
	return &writeHandle{
		files:          files,
		parentID:       parentID,
		existingFileID: existingFileID,
		name:           name,
		tmp:            tmp,
		onUploaded:     onUploaded,
	}, nil
}

// Write appends data at offset.
func (h *writeHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n, err := h.tmp.WriteAt(data, off)
	if err != nil {
		return 0, syscall.EIO
	}
	return uint32(n), 0
}

// Flush uploads the accumulated content.
func (h *writeHandle) Flush(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.uploaded {
		return 0
	}
	stat, err := h.tmp.Stat()
	if err != nil {
		slog.Error("flush stat", "err", err)
		return syscall.EIO
	}
	if _, err := h.tmp.Seek(0, io.SeekStart); err != nil {
		slog.Error("flush seek", "err", err)
		return syscall.EIO
	}
	info, err := h.files.Upload(ctx, kdrive.UploadInput{
		ParentID:       h.parentID,
		ExistingFileID: h.existingFileID,
		Name:           h.name,
		Body:           h.tmp,
		Size:           stat.Size(),
	})
	if err != nil {
		slog.Error("flush upload",
			"name", h.name,
			"parent", h.parentID,
			"existing", h.existingFileID,
			"size", stat.Size(),
			"err", err,
		)
		return syscall.EIO
	}
	h.uploaded = true
	if h.onUploaded != nil {
		h.onUploaded(info)
	}
	return 0
}

// Release removes the tempfile.
func (h *writeHandle) Release(_ context.Context) syscall.Errno {
	h.mu.Lock()
	tmp := h.tmp
	h.tmp = nil
	node := h.node
	h.mu.Unlock()
	if tmp == nil {
		return 0
	}
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
	if node != nil {
		node.mu.Lock()
		if node.wh == h {
			node.wh = nil
		}
		node.mu.Unlock()
	}
	return 0
}
