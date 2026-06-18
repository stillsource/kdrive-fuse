package fuse

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

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// FileNode represents a kDrive file.
type FileNode struct {
	fs.Inode
	kdfs *KDriveFS
	info domain.FileInfo

	mu sync.Mutex
	wh *writeHandle // active write handle, so a late Setattr(size) can truncate it
}

var _ fs.NodeGetattrer = (*FileNode)(nil)
var _ fs.NodeOpener = (*FileNode)(nil)
var _ fs.NodeSetattrer = (*FileNode)(nil)
var _ fs.NodeGetxattrer = (*FileNode)(nil)
var _ fs.NodeListxattrer = (*FileNode)(nil)

// Getattr returns file attributes.
func (f *FileNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o644 | syscall.S_IFREG
	out.Size = uint64(f.info.Size)
	out.Mtime = uint64(f.info.LastModifiedAt)
	out.Ctime = uint64(f.info.CreatedAt)
	f.kdfs.applyOwner(&out.Attr)
	out.SetTimeout(30 * time.Second)
	return 0
}

// Getxattr returns the value of one extended attribute. It follows the FUSE
// size-probe protocol: a zero-length dest returns the size; a too-small dest
// returns ERANGE; an unknown attribute returns ENODATA.
func (f *FileNode) Getxattr(_ context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	return getXattrValue(kdriveXattrs(f.info), attr, dest)
}

// Listxattr returns the NUL-separated list of attribute names, following the
// same size-probe protocol as Getxattr.
func (f *FileNode) Listxattr(_ context.Context, dest []byte) (uint32, syscall.Errno) {
	return listXattrNames(kdriveXattrs(f.info), dest)
}

// Setattr accepts size/time updates. Truncate updates the working file if one
// is open. Mtime changes are persisted to the remote via SetModifiedAt so that
// touch(1) works as expected.
func (f *FileNode) Setattr(ctx context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if f.kdfs.ReadOnly {
		if _, ok := in.GetSize(); ok {
			return syscall.EROFS
		}
		if _, ok := in.GetMTime(); ok {
			return syscall.EROFS
		}
	}
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
	if mt, ok := in.GetMTime(); ok {
		_, parent := f.EmbeddedInode().Parent()
		if parent == nil {
			slog.Error("setattr mtime: no parent", "name", f.info.Name)
			return syscall.EIO
		}
		pDir, ok := parent.Operations().(*DirNode)
		if !ok {
			slog.Error("setattr mtime: parent not DirNode", "name", f.info.Name)
			return syscall.EIO
		}
		info, err := f.kdfs.SetMtime.Execute(ctx, f.info.ID, mt.Unix(), pDir.folderID)
		if err != nil {
			slog.Error("setattr mtime: SetModifiedAt failed",
				"name", f.info.Name,
				"id", f.info.ID,
				"mtime", mt.Unix(),
				"err", err,
			)
			return syscall.EIO
		}
		f.info.LastModifiedAt = info.LastModifiedAt
	}
	out.Mode = 0o644 | syscall.S_IFREG
	out.Size = uint64(f.info.Size)
	out.Mtime = uint64(f.info.LastModifiedAt)
	out.Ctime = uint64(f.info.CreatedAt)
	f.kdfs.applyOwner(&out.Attr)
	out.SetTimeout(30 * time.Second)
	return 0
}

// Open returns a read or write handle depending on flags. Write handles start
// empty; an edit pulls remote content lazily on the first Write (see seedLocked).
func (f *FileNode) Open(_ context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	writable := flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0
	if f.kdfs.ReadOnly && writable {
		return nil, 0, syscall.EROFS
	}
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

	wh, err := newWriteHandle(f.kdfs.SeedContent, f.kdfs.CommitWrite, pDir.folderID, f.info.ID, f.info.Name, func(info domain.FileInfo) {
		// CommitWrite already invalidates the parent listing; only patch the node.
		f.info = info
	})
	if err != nil {
		return nil, 0, syscall.EIO
	}
	wh.node = f
	// Don't seed here. O_TRUNC is often delivered as a separate Setattr the
	// kernel sends around Open (so it may be absent from these flags); a zero
	// size means the file is empty or already truncated. In both cases the
	// working file must start empty — seeding is deferred to the first Write.
	wh.truncated = truncate || f.info.Size == 0
	f.mu.Lock()
	f.wh = wh
	f.mu.Unlock()
	return wh, 0, 0
}

// readHandle serves reads from a disk-cached copy, downloading on first Read.
type readHandle struct {
	kdfs    *KDriveFS
	info    domain.FileInfo
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
	if h.kdfs.ReadFile == nil {
		h.openErr = errNoCache
		return syscall.EIO
	}
	f, err := h.kdfs.ReadFile.Execute(ctx, h.info.ID, h.info.LastModifiedAt, h.info.Size)
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

// writeHandle buffers writes to a working tempfile and commits them to the
// server once (on Flush after the first write, with Release as a safety net).
// existingFileID > 0 enters edit mode (replace remote content); 0 creates a new file.
type writeHandle struct {
	seed           *usecase.SeedContent
	commit         *usecase.CommitWrite
	parentID       int64
	existingFileID int64
	name           string
	tmp            *os.File
	onUploaded     func(domain.FileInfo)
	node           *FileNode // set by Open; lets a late Setattr truncate this handle
	mu             sync.Mutex
	uploaded       bool // content already committed to the server
	wrote          bool // at least one Write happened
	truncated      bool // a truncate was requested (suppresses the remote seed)
	seeded         bool // remote content has been pulled into the working file
}

// truncateTo resizes the buffered tempfile. The kernel may send a truncating
// Setattr after Open, so this keeps the buffer in sync with the requested size.
func (h *writeHandle) truncateTo(n int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.truncated = true
	h.seeded = true // a truncate means we won't pull remote content under the new bytes
	if h.tmp != nil {
		_ = h.tmp.Truncate(n)
	}
}

var _ fs.FileWriter = (*writeHandle)(nil)
var _ fs.FileFlusher = (*writeHandle)(nil)
var _ fs.FileReleaser = (*writeHandle)(nil)

func newWriteHandle(seed *usecase.SeedContent, commit *usecase.CommitWrite, parentID, existingFileID int64, name string, onUploaded func(domain.FileInfo)) (*writeHandle, error) {
	tmp, err := os.CreateTemp("", "kdrive-upload-*")
	if err != nil {
		return nil, err
	}
	return &writeHandle{
		seed:           seed,
		commit:         commit,
		parentID:       parentID,
		existingFileID: existingFileID,
		name:           name,
		tmp:            tmp,
		onUploaded:     onUploaded,
	}, nil
}

// Write seeds the working file with remote content on first use (for a partial
// edit) and applies the write at offset.
func (h *writeHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if errno := h.seedLocked(ctx); errno != 0 {
		return 0, errno
	}
	n, err := h.tmp.WriteAt(data, off)
	if err != nil {
		return 0, syscall.EIO
	}
	h.wrote = true
	return uint32(n), 0
}

// seedLocked pulls the current remote content into the working file the first
// time it's needed: an edit that isn't a truncate needs the existing bytes so a
// partial write doesn't blank the rest of the file. Caller holds h.mu.
func (h *writeHandle) seedLocked(ctx context.Context) syscall.Errno {
	if h.seeded || h.truncated || h.existingFileID == 0 {
		return 0
	}
	rc, err := h.seed.Execute(ctx, h.existingFileID)
	if err != nil {
		slog.Error("seed download", "id", h.existingFileID, "err", err)
		return syscall.EIO
	}
	defer rc.Close() //nolint:errcheck // cleanup only
	if _, err := io.Copy(h.tmp, rc); err != nil {
		slog.Error("seed copy", "id", h.existingFileID, "err", err)
		return syscall.EIO
	}
	h.seeded = true
	return 0
}

// Flush commits the buffered content once at least one Write has happened, so a
// FLUSH the kernel delivers *before* the WRITEs (which it does on a truncating
// rewrite) is a harmless no-op. The commit also runs on Release as a safety net.
func (h *writeHandle) Flush(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.uploaded || !h.wrote {
		return 0
	}
	return h.commitLocked(ctx)
}

// commitLocked uploads the working file and records the result. Caller holds h.mu.
func (h *writeHandle) commitLocked(ctx context.Context) syscall.Errno {
	stat, err := h.tmp.Stat()
	if err != nil {
		slog.Error("commit stat", "err", err)
		return syscall.EIO
	}
	if _, err := h.tmp.Seek(0, io.SeekStart); err != nil {
		slog.Error("commit seek", "err", err)
		return syscall.EIO
	}
	conflict := ""
	if h.existingFileID == 0 {
		conflict = "rename"
	}
	in := service.UploadInput{
		ParentID:       h.parentID,
		ExistingFileID: h.existingFileID,
		Name:           h.name,
		Body:           h.tmp,
		Size:           stat.Size(),
		Conflict:       conflict,
	}
	info, err := h.commit.Execute(ctx, in, h.parentID)
	if err != nil {
		slog.Error("commit upload",
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

// Release commits content written but not yet flushed (writes after the last
// Flush) and drops the working file. It commits ONLY when content was actually
// written: a handle that was created/opened but never written is NOT uploaded.
// This avoids two hazards: a brand-new file left as a 0-byte placeholder when a
// copy is interrupted before writing, and an aborted truncating overwrite
// (O_TRUNC then no write) silently emptying an existing remote file. The
// trade-off — `touch newfile` and `: > existing` (truncate-to-empty with no
// write) don't persist — is acceptable for this workload. Release errors can't
// reach close(), so a commit error here is only logged; the common write path
// still surfaces errors via Flush.
func (h *writeHandle) Release(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	if h.tmp == nil {
		h.mu.Unlock()
		return 0
	}
	if !h.uploaded && h.wrote {
		_ = h.commitLocked(ctx)
	}
	tmp := h.tmp
	h.tmp = nil
	node := h.node
	h.mu.Unlock()

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
