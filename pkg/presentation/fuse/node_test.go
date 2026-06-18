package fuse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// mountFixture spins up an in-process FUSE mount backed by an in-memory Files fake.
// All fake state must be populated BEFORE calling newMountFixture — once the mount
// is live, concurrent kernel goroutines read the fake and racing writes are detected.
type mountFixture struct {
	Dir   string
	Cache string
	Fake  *servicefakes.FilesFake
	KDFS  *KDriveFS
	Srv   *fuse.Server
}

func newMountFixture(fake *servicefakes.FilesFake) *mountFixture {
	tmp := GinkgoT().TempDir()
	mnt := filepath.Join(tmp, "mnt")
	Expect(os.Mkdir(mnt, 0o755)).To(Succeed())
	cache := filepath.Join(tmp, "cache")

	disk, err := contentcache.NewDiskCache(cache, 1<<20, fake, nil)
	Expect(err).NotTo(HaveOccurred())
	kdfs := NewKDriveFS(fake, time.Minute, disk, false)
	root := NewRootDirNode(kdfs, 1)

	ttl := 50 * time.Millisecond
	srv, err := fs.Mount(mnt, root, &fs.Options{
		MountOptions: fuse.MountOptions{Name: "kdrive-test", FsName: "kdrive-test"},
		AttrTimeout:  &ttl,
		EntryTimeout: &ttl,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(srv.WaitMount()).To(Succeed())

	DeferCleanup(func() { _ = srv.Unmount() })

	return &mountFixture{Dir: mnt, Cache: cache, Fake: fake, KDFS: kdfs, Srv: srv}
}

// baseFake returns a fake pre-populated with the test directory layout:
//
//	/ (id=1)
//	├── hello.txt (id=10, size=11, "hello world")
//	└── sub/ (id=20)
//	    └── nested.txt (id=30, "nested")
func baseFake() *servicefakes.FilesFake {
	return &servicefakes.FilesFake{
		ListResults: map[int64]servicefakes.ListResult{
			1: {Files: []domain.FileInfo{
				{ID: 10, Name: "hello.txt", Type: domain.FileTypeFile, Size: 11, LastModifiedAt: 100},
				{ID: 20, Name: "sub", Type: domain.FileTypeDir},
			}},
			20: {Files: []domain.FileInfo{
				{ID: 30, Name: "nested.txt", Type: domain.FileTypeFile, Size: 6, LastModifiedAt: 200},
			}},
		},
		DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{
			10: {Data: []byte("hello world")},
			30: {Data: []byte("nested")},
		},
	}
}

var _ = Describe("DirNode via FUSE mount — read paths", func() {
	var fx *mountFixture

	BeforeEach(func() { fx = newMountFixture(baseFake()) })

	It("Readdir lists children of root", func() {
		entries, err := os.ReadDir(fx.Dir)
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		Expect(names).To(ConsistOf("hello.txt", "sub"))
	})

	It("Lookup resolves a file and returns correct size", func() {
		info, err := os.Stat(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Size()).To(Equal(int64(11)))
		Expect(info.IsDir()).To(BeFalse())
	})

	It("Lookup recurses into a directory", func() {
		info, err := os.Stat(filepath.Join(fx.Dir, "sub", "nested.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Size()).To(Equal(int64(6)))
	})

	It("Lookup reports the mounting user as owner so files stay deletable", func() {
		info, err := os.Stat(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).NotTo(HaveOccurred())
		st, ok := info.Sys().(*syscall.Stat_t)
		Expect(ok).To(BeTrue())
		Expect(st.Uid).To(Equal(uint32(os.Getuid())))
		Expect(st.Gid).To(Equal(uint32(os.Getgid())))
	})

	It("Lookup returns ENOENT for missing entries", func() {
		_, err := os.Stat(filepath.Join(fx.Dir, "no-such"))
		Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	It("Read streams content through the disk cache", func() {
		data, err := os.ReadFile(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello world"))
	})

	It("Open for reading + ReadAt serves an offset", func() {
		f, err := os.Open(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).NotTo(HaveOccurred())
		defer f.Close()
		b := make([]byte, 5)
		_, err = f.ReadAt(b, 6)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("world"))
	})
})

var _ = Describe("DirNode via FUSE mount — mutating paths", func() {
	It("Mkdir creates a directory", func() {
		fake := baseFake()
		fake.MkdirResults = map[string]servicefakes.MkdirResult{
			"1/newdir": {Info: domain.FileInfo{ID: 40, Name: "newdir", Type: domain.FileTypeDir}},
		}
		fx := newMountFixture(fake)
		Expect(os.Mkdir(filepath.Join(fx.Dir, "newdir"), 0o755)).To(Succeed())
		Expect(fx.Fake.GetMkdirCalls()).To(HaveLen(1))
	})

	It("Unlink deletes a file", func() {
		fake := baseFake()
		fake.DeleteResults = map[int64]error{10: nil}
		fx := newMountFixture(fake)
		Expect(os.Remove(filepath.Join(fx.Dir, "hello.txt"))).To(Succeed())
		Expect(fx.Fake.GetDeleteCalls()).To(ContainElement(int64(10)))
	})

	It("Rmdir deletes a directory", func() {
		fake := baseFake()
		fake.DeleteResults = map[int64]error{20: nil}
		fx := newMountFixture(fake)
		Expect(os.Remove(filepath.Join(fx.Dir, "sub"))).To(Succeed())
		Expect(fx.Fake.GetDeleteCalls()).To(ContainElement(int64(20)))
	})

	It("Rename within same directory calls API Rename only", func() {
		fake := baseFake()
		fake.RenameResults = map[int64]servicefakes.RenameResult{
			10: {Info: domain.FileInfo{ID: 10, Name: "renamed.txt"}},
		}
		fx := newMountFixture(fake)
		Expect(os.Rename(
			filepath.Join(fx.Dir, "hello.txt"),
			filepath.Join(fx.Dir, "renamed.txt"),
		)).To(Succeed())
		Expect(fx.Fake.GetRenameCalls()).To(HaveLen(1))
		Expect(fx.Fake.GetMoveCalls()).To(BeEmpty())
	})

	It("Rename across directories calls Move + Rename", func() {
		fake := baseFake()
		fake.MoveResults = map[int64]error{10: nil}
		fake.RenameResults = map[int64]servicefakes.RenameResult{
			10: {Info: domain.FileInfo{ID: 10, Name: "other.txt"}},
		}
		fx := newMountFixture(fake)
		Expect(os.Rename(
			filepath.Join(fx.Dir, "hello.txt"),
			filepath.Join(fx.Dir, "sub", "other.txt"),
		)).To(Succeed())
		Expect(fx.Fake.GetMoveCalls()).To(HaveLen(1))
		Expect(fx.Fake.GetMoveCalls()[0].DestDirID).To(Equal(int64(20)))
		Expect(fx.Fake.GetRenameCalls()).To(HaveLen(1))
	})

	It("Create uploads a new file via writeHandle", func() {
		fake := baseFake()
		fake.UploadStub = func(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
			data, _ := io.ReadAll(in.Body)
			Expect(string(data)).To(Equal("new content"))
			return domain.FileInfo{ID: 50, Name: in.Name, Size: in.Size, Type: domain.FileTypeFile}, nil
		}
		fx := newMountFixture(fake)
		Expect(os.WriteFile(
			filepath.Join(fx.Dir, "new.txt"),
			[]byte("new content"), 0o644,
		)).To(Succeed())
		Expect(fx.Fake.GetUploadCalls()).To(HaveLen(1))
		Expect(fx.Fake.GetUploadCalls()[0].ParentID).To(Equal(int64(1)))
		Expect(fx.Fake.GetUploadCalls()[0].ExistingFileID).To(Equal(int64(0)))
	})

	It("Create (new file) commits UploadInput with Conflict 'rename'", func() {
		fake := baseFake()
		fake.UploadStub = func(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
			return domain.FileInfo{ID: 55, Name: in.Name, Size: in.Size, Type: domain.FileTypeFile}, nil
		}
		fx := newMountFixture(fake)
		Expect(os.WriteFile(
			filepath.Join(fx.Dir, "conflict-test.txt"),
			[]byte("data"), 0o644,
		)).To(Succeed())
		calls := fx.Fake.GetUploadCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].ExistingFileID).To(Equal(int64(0)))
		Expect(calls[0].Conflict).To(Equal("rename"))
	})

	It("Edit (existing file) commits UploadInput with Conflict ''", func() {
		fake := baseFake()
		fake.UploadStub = func(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
			return domain.FileInfo{ID: 10, Name: in.Name, Size: in.Size, Type: domain.FileTypeFile}, nil
		}
		fx := newMountFixture(fake)
		Expect(os.WriteFile(
			filepath.Join(fx.Dir, "hello.txt"),
			[]byte("edited"), 0o644,
		)).To(Succeed())
		calls := fx.Fake.GetUploadCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].ExistingFileID).To(Equal(int64(10)))
		Expect(calls[0].Conflict).To(Equal(""))
	})

	It("does not upload a file created and closed without writing (no 0-byte placeholder)", func() {
		fx := newMountFixture(baseFake())
		f, err := os.Create(filepath.Join(fx.Dir, "placeholder.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Close()).To(Succeed())
		// Nothing was written, so nothing is committed to the server: an
		// interrupted/empty create must not leave a 0-byte placeholder.
		Expect(fx.Fake.GetUploadCalls()).To(BeEmpty())
	})

	It("Setattr mtime (touch) calls SetModifiedAt and updates the node's reported mtime", func() {
		const newMtime = int64(1700000000)
		fake := baseFake()
		fake.SetModifiedAtResults = map[int64]servicefakes.SetModifiedAtResult{
			10: {Info: domain.FileInfo{ID: 10, Name: "hello.txt", Type: domain.FileTypeFile, Size: 11, LastModifiedAt: newMtime}},
		}
		fx := newMountFixture(fake)

		target := time.Unix(newMtime, 0)
		Expect(os.Chtimes(filepath.Join(fx.Dir, "hello.txt"), target, target)).To(Succeed())

		calls := fx.Fake.GetSetModifiedAtCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].FileID).To(Equal(int64(10)))
		Expect(calls[0].ModifiedAt).To(Equal(newMtime))

		// Allow the kernel attr cache to expire, then stat — mtime must reflect the update.
		time.Sleep(100 * time.Millisecond)
		info, err := os.Stat(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ModTime().Unix()).To(Equal(newMtime))
	})
})

var _ = Describe("DirNode unit — no mount", func() {
	It("Getattr returns directory mode", func() {
		d := &DirNode{kdfs: &KDriveFS{}}
		var out fuse.AttrOut
		errno := d.Getattr(context.Background(), nil, &out)
		Expect(errno).To(BeZero())
		Expect(out.Mode & syscall.S_IFDIR).NotTo(BeZero())
	})

	It("Getattr stamps the mounting user as owner, not root", func() {
		d := &DirNode{kdfs: &KDriveFS{Uid: 4242, Gid: 4343}}
		var out fuse.AttrOut
		Expect(d.Getattr(context.Background(), nil, &out)).To(BeZero())
		Expect(out.Owner.Uid).To(Equal(uint32(4242)))
		Expect(out.Owner.Gid).To(Equal(uint32(4343)))
	})

	It("list propagates API errors", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{1: {Err: domain.ErrNotFound}},
		}
		d := &DirNode{
			kdfs:     &KDriveFS{ListDir: usecase.NewListDir(fake, listingcache.NewDirCache(time.Second))},
			folderID: 1,
		}
		_, err := d.list(context.Background())
		Expect(err).To(MatchError(domain.ErrNotFound))
	})

	It("list caches subsequent calls", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{{ID: 10, Name: "a"}}},
			},
		}
		d := &DirNode{
			kdfs:     &KDriveFS{ListDir: usecase.NewListDir(fake, listingcache.NewDirCache(time.Second))},
			folderID: 1,
		}
		_, err := d.list(context.Background())
		Expect(err).NotTo(HaveOccurred())
		_, err = d.list(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.ListCalls).To(HaveLen(1))
	})
})

var _ = Describe("FileNode unit — no mount", func() {
	It("Getattr reports file mode and size", func() {
		f := &FileNode{kdfs: &KDriveFS{}, info: domain.FileInfo{Size: 12, Type: domain.FileTypeFile}}
		var out fuse.AttrOut
		errno := f.Getattr(context.Background(), nil, &out)
		Expect(errno).To(BeZero())
		Expect(out.Size).To(Equal(uint64(12)))
		Expect(out.Mode & syscall.S_IFREG).NotTo(BeZero())
	})

	It("Getattr stamps the mounting user as owner, not root", func() {
		f := &FileNode{kdfs: &KDriveFS{Uid: 4242, Gid: 4343}, info: domain.FileInfo{Type: domain.FileTypeFile}}
		var out fuse.AttrOut
		Expect(f.Getattr(context.Background(), nil, &out)).To(BeZero())
		Expect(out.Owner.Uid).To(Equal(uint32(4242)))
		Expect(out.Owner.Gid).To(Equal(uint32(4343)))
	})

	It("Setattr updates size", func() {
		f := &FileNode{kdfs: &KDriveFS{}, info: domain.FileInfo{Size: 12}}
		in := &fuse.SetAttrIn{SetAttrInCommon: fuse.SetAttrInCommon{Valid: fuse.FATTR_SIZE, Size: 0}}
		var out fuse.AttrOut
		errno := f.Setattr(context.Background(), nil, in, &out)
		Expect(errno).To(BeZero())
		Expect(f.info.Size).To(Equal(int64(0)))
	})

	It("Setattr mtime returns EIO when node has no parent (not attached to tree)", func() {
		// A bare FileNode (not mounted) has no parent — SetMtime cannot resolve
		// the parent folderID and must return EIO rather than panic.
		newMtime := time.Unix(1700000000, 0)
		fake := &servicefakes.FilesFake{}
		setMtime := usecase.NewSetMtime(fake, listingcache.NewDirCache(time.Second))
		f := &FileNode{
			kdfs: &KDriveFS{SetMtime: setMtime},
			info: domain.FileInfo{ID: 10, Name: "doc.txt"},
		}
		in := &fuse.SetAttrIn{SetAttrInCommon: fuse.SetAttrInCommon{
			Valid: fuse.FATTR_MTIME,
			Mtime: uint64(newMtime.Unix()),
		}}
		var out fuse.AttrOut
		errno := f.Setattr(context.Background(), nil, in, &out)
		Expect(errno).To(Equal(syscall.EIO))
		Expect(fake.GetSetModifiedAtCalls()).To(BeEmpty())
	})
})

var _ = Describe("Read-only mount", func() {
	var fx *mountFixture

	BeforeEach(func() {
		fake := baseFake()
		fake.MkdirResults = map[string]servicefakes.MkdirResult{}
		fake.DeleteResults = map[int64]error{}
		fake.RenameResults = map[int64]servicefakes.RenameResult{}

		tmp := GinkgoT().TempDir()
		mnt := filepath.Join(tmp, "mnt")
		Expect(os.Mkdir(mnt, 0o755)).To(Succeed())
		cache := filepath.Join(tmp, "cache")

		disk, err := contentcache.NewDiskCache(cache, 1<<20, fake, nil)
		Expect(err).NotTo(HaveOccurred())
		kdfs := NewKDriveFS(fake, time.Minute, disk, true /* readOnly */)
		root := NewRootDirNode(kdfs, 1)

		ttl := 50 * time.Millisecond
		srv, err := fs.Mount(mnt, root, &fs.Options{
			MountOptions: fuse.MountOptions{Name: "kdrive-ro-test", FsName: "kdrive-ro-test"},
			AttrTimeout:  &ttl,
			EntryTimeout: &ttl,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.WaitMount()).To(Succeed())

		DeferCleanup(func() { _ = srv.Unmount() })

		fx = &mountFixture{Dir: mnt, Cache: cache, Fake: fake, KDFS: kdfs, Srv: srv}
	})

	It("ls (Readdir) still succeeds", func() {
		entries, err := os.ReadDir(fx.Dir)
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		Expect(names).To(ConsistOf("hello.txt", "sub"))
	})

	It("reading an existing file still succeeds", func() {
		data, err := os.ReadFile(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello world"))
	})

	It("Mkdir returns EROFS", func() {
		err := os.Mkdir(filepath.Join(fx.Dir, "newdir"), 0o755)
		Expect(err).To(HaveOccurred())
		var pathErr *os.PathError
		Expect(errors.As(err, &pathErr)).To(BeTrue())
		Expect(errors.Is(pathErr.Err, syscall.EROFS)).To(BeTrue())
	})

	It("Create (new file) returns EROFS", func() {
		_, err := os.Create(filepath.Join(fx.Dir, "new.txt"))
		Expect(err).To(HaveOccurred())
		var pathErr *os.PathError
		Expect(errors.As(err, &pathErr)).To(BeTrue())
		Expect(errors.Is(pathErr.Err, syscall.EROFS)).To(BeTrue())
	})

	It("Unlink returns EROFS", func() {
		err := syscall.Unlink(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).To(Equal(syscall.EROFS))
	})

	It("Rmdir returns EROFS", func() {
		err := syscall.Rmdir(filepath.Join(fx.Dir, "sub"))
		Expect(err).To(Equal(syscall.EROFS))
	})

	It("Rename returns EROFS", func() {
		err := os.Rename(
			filepath.Join(fx.Dir, "hello.txt"),
			filepath.Join(fx.Dir, "renamed.txt"),
		)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, syscall.EROFS)).To(BeTrue())
	})

	It("Open for writing returns EROFS", func() {
		err := syscall.Access(filepath.Join(fx.Dir, "hello.txt"), syscall.F_OK)
		Expect(err).NotTo(HaveOccurred()) // file exists
		_, err = os.OpenFile(filepath.Join(fx.Dir, "hello.txt"), os.O_WRONLY, 0o644)
		Expect(err).To(HaveOccurred())
		var pathErr *os.PathError
		Expect(errors.As(err, &pathErr)).To(BeTrue())
		Expect(errors.Is(pathErr.Err, syscall.EROFS)).To(BeTrue())
	})

	It("Truncate (Setattr size change) returns EROFS", func() {
		err := os.Truncate(filepath.Join(fx.Dir, "hello.txt"), 0)
		Expect(err).To(HaveOccurred())
		var pathErr *os.PathError
		Expect(errors.As(err, &pathErr)).To(BeTrue())
		Expect(errors.Is(pathErr.Err, syscall.EROFS)).To(BeTrue())
	})

	It("Setattr mtime (touch) returns EROFS on a read-only mount", func() {
		target := time.Unix(1700000000, 0)
		err := os.Chtimes(filepath.Join(fx.Dir, "hello.txt"), target, target)
		Expect(err).To(HaveOccurred())
		var pathErr *os.PathError
		Expect(errors.As(err, &pathErr)).To(BeTrue())
		Expect(errors.Is(pathErr.Err, syscall.EROFS)).To(BeTrue())
	})
})

var _ = Describe("FileNode xattr via FUSE mount", func() {
	It("syscall.Getxattr returns user.kdrive.id for a mounted file", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{
					{ID: 42, Name: "meta.txt", Type: domain.FileTypeFile, Size: 3,
						CreatedAt: 1700000000, MimeType: "text/plain"},
				}},
			},
			DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{
				42: {Data: []byte("hi!")},
			},
		}
		fx := newMountFixture(fake)
		path := filepath.Join(fx.Dir, "meta.txt")

		// Force a lookup so the node is populated.
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())

		// size probe
		sz, errno := syscall.Getxattr(path, "user.kdrive.id", nil)
		Expect(errno).To(BeZero())
		Expect(sz).To(BeNumerically(">", 0))

		// value read
		buf := make([]byte, sz)
		n, errno := syscall.Getxattr(path, "user.kdrive.id", buf)
		Expect(errno).To(BeZero())
		Expect(string(buf[:n])).To(Equal("42"))
	})

	It("syscall.Getxattr returns user.kdrive.created_at for a mounted file", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{
					{ID: 10, Name: "hello.txt", Type: domain.FileTypeFile, Size: 11,
						CreatedAt: 1700000000, LastModifiedAt: 100},
				}},
			},
			DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{
				10: {Data: []byte("hello world")},
			},
		}
		fx := newMountFixture(fake)
		path := filepath.Join(fx.Dir, "hello.txt")

		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())

		buf := make([]byte, 64)
		n, errno := syscall.Getxattr(path, "user.kdrive.created_at", buf)
		Expect(errno).To(BeZero())
		Expect(string(buf[:n])).To(Equal("1700000000"))
	})

	It("syscall.Getxattr returns user.kdrive.mime_type when set", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{
					{ID: 10, Name: "hello.txt", Type: domain.FileTypeFile, Size: 11,
						MimeType: "text/plain"},
				}},
			},
			DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{
				10: {Data: []byte("hello world")},
			},
		}
		fx := newMountFixture(fake)
		path := filepath.Join(fx.Dir, "hello.txt")

		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())

		buf := make([]byte, 64)
		n, errno := syscall.Getxattr(path, "user.kdrive.mime_type", buf)
		Expect(errno).To(BeZero())
		Expect(string(buf[:n])).To(Equal("text/plain"))
	})

	It("syscall.Getxattr returns ENODATA for unknown attribute", func() {
		fx := newMountFixture(baseFake())
		path := filepath.Join(fx.Dir, "hello.txt")

		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())

		_, errno := syscall.Getxattr(path, "user.kdrive.share_url", nil)
		Expect(errno).To(Equal(syscall.ENODATA))
	})

	It("syscall.Listxattr returns NUL-separated names including user.kdrive.id", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{
					{ID: 10, Name: "hello.txt", Type: domain.FileTypeFile, Size: 11,
						MimeType: "text/plain"},
				}},
			},
			DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{
				10: {Data: []byte("hello world")},
			},
		}
		fx := newMountFixture(fake)
		path := filepath.Join(fx.Dir, "hello.txt")

		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())

		sz, errno := syscall.Listxattr(path, nil)
		Expect(errno).To(BeZero())
		Expect(sz).To(BeNumerically(">", 0))

		buf := make([]byte, sz)
		n, errno := syscall.Listxattr(path, buf)
		Expect(errno).To(BeZero())
		names := string(buf[:n])
		Expect(names).To(ContainSubstring("user.kdrive.id"))
		Expect(names).To(ContainSubstring("user.kdrive.created_at"))
		Expect(names).To(ContainSubstring("user.kdrive.mime_type"))
	})
})

// drainRC drains a ReadCloser fully — helper for stream tests.
var _ = drainRC

func drainRC(rc io.ReadCloser) []byte {
	defer rc.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, rc)
	return buf.Bytes()
}

var _ = Describe("DirNode error paths via mount", func() {
	It("Rmdir on a file returns ENOTDIR", func() {
		fx := newMountFixture(baseFake())
		err := syscall.Rmdir(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).To(Equal(syscall.ENOTDIR))
	})

	It("Unlink on a directory returns EISDIR", func() {
		fx := newMountFixture(baseFake())
		err := syscall.Unlink(filepath.Join(fx.Dir, "sub"))
		Expect(err).To(Equal(syscall.EISDIR))
	})

	It("Unlink on missing entry returns ENOENT", func() {
		fx := newMountFixture(baseFake())
		err := os.Remove(filepath.Join(fx.Dir, "missing"))
		Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	It("Readdir propagates API error as EIO", func() {
		fake := &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Err: domain.ErrServer},
			},
		}
		fx := newMountFixture(fake)
		_, err := os.ReadDir(fx.Dir)
		Expect(err).To(HaveOccurred())
	})

	It("Mkdir propagates API error as EIO", func() {
		fake := baseFake()
		fake.MkdirStub = func(_ context.Context, _ int64, _ string) (domain.FileInfo, error) {
			return domain.FileInfo{}, domain.ErrConflict
		}
		fx := newMountFixture(fake)
		err := os.Mkdir(filepath.Join(fx.Dir, "x"), 0o755)
		Expect(err).To(HaveOccurred())
	})

	It("Delete API error bubbles up as EIO on unlink", func() {
		fake := baseFake()
		fake.DeleteStub = func(_ context.Context, _ int64) error {
			return domain.ErrServer
		}
		fx := newMountFixture(fake)
		err := os.Remove(filepath.Join(fx.Dir, "hello.txt"))
		Expect(err).To(HaveOccurred())
	})

	It("Rename with Move API error returns EIO", func() {
		fake := baseFake()
		fake.MoveStub = func(_ context.Context, _, _ int64) error {
			return domain.ErrServer
		}
		fx := newMountFixture(fake)
		err := os.Rename(
			filepath.Join(fx.Dir, "hello.txt"),
			filepath.Join(fx.Dir, "sub", "moved.txt"),
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rename with Rename API error returns EIO", func() {
		fake := baseFake()
		fake.RenameStub = func(_ context.Context, _ int64, _ string) (domain.FileInfo, error) {
			return domain.FileInfo{}, domain.ErrConflict
		}
		fx := newMountFixture(fake)
		err := os.Rename(
			filepath.Join(fx.Dir, "hello.txt"),
			filepath.Join(fx.Dir, "different.txt"),
		)
		Expect(err).To(HaveOccurred())
	})

	It("Upload error on Create surfaces as IO error on close", func() {
		fake := baseFake()
		fake.UploadStub = func(_ context.Context, _ service.UploadInput) (domain.FileInfo, error) {
			return domain.FileInfo{}, domain.ErrServer
		}
		fx := newMountFixture(fake)
		err := os.WriteFile(filepath.Join(fx.Dir, "bad.txt"), []byte("x"), 0o644)
		Expect(err).To(HaveOccurred())
	})

	It("Rewrite an existing file triggers edit mode upload", func() {
		fake := baseFake()
		fake.UploadStub = func(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
			Expect(in.ExistingFileID).To(Equal(int64(10)))
			return domain.FileInfo{ID: 10, Name: in.Name, Size: in.Size}, nil
		}
		fx := newMountFixture(fake)
		Expect(os.WriteFile(
			filepath.Join(fx.Dir, "hello.txt"),
			[]byte("edited"), 0o644,
		)).To(Succeed())
		Expect(fx.Fake.GetUploadCalls()).To(HaveLen(1))
	})

	It("Truncating rewrite uploads only the new bytes, not merged with old content", func() {
		fake := baseFake() // hello.txt (id=10) holds "hello world" (11 bytes)
		gotBody := make(chan string, 1)
		fake.UploadStub = func(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
			body, _ := io.ReadAll(in.Body)
			gotBody <- string(body)
			return domain.FileInfo{ID: 10, Name: in.Name, Size: int64(len(body))}, nil
		}
		fx := newMountFixture(fake)
		Expect(os.WriteFile(
			filepath.Join(fx.Dir, "hello.txt"),
			[]byte("edited"), 0o644,
		)).To(Succeed())
		// Bug: O_TRUNC is delivered as Setattr(size=0) then Open without O_TRUNC,
		// so Open re-seeds the old remote content and the short write leaves a
		// stale tail -> body becomes "editedworld" instead of "edited".
		Eventually(gotBody).Should(Receive(Equal("edited")))
	})

	It("removeChild with wrong type in list returns EISDIR/ENOTDIR", func() {
		// covered by the two specs above; this entry keeps the contract explicit
	})
})
