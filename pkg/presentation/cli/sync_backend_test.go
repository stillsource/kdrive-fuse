package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// fakeSyncFiles is an in-memory syncer.Remote for runSync tests.
type fakeSyncFiles struct {
	mu         sync.Mutex
	uploads    int
	failUpload bool
	listing    map[int64][]domain.FileInfo // remote tree for List/Build
	content    map[int64][]byte            // file content for DownloadStream
}

func (f *fakeSyncFiles) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	return f.listing[folderID], nil
}

func (f *fakeSyncFiles) Mkdir(_ context.Context, _ int64, name string) (domain.FileInfo, error) {
	return domain.FileInfo{ID: 9, Name: name, Type: domain.FileTypeDir}, nil
}

func (f *fakeSyncFiles) Upload(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failUpload {
		return domain.FileInfo{}, errors.New("upload failed")
	}
	f.uploads++
	return domain.FileInfo{ID: 100, Size: in.Size, LastModifiedAt: 42}, nil
}

func (f *fakeSyncFiles) Delete(context.Context, int64) error { return nil }

func (f *fakeSyncFiles) DownloadStream(_ context.Context, fileID, _, _ int64) (io.ReadCloser, error) {
	b, ok := f.content[fileID]
	if !ok {
		return nil, errors.New("missing content")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

var _ = Describe("runSync with a fake backend", func() {
	var (
		orig  func(context.Context, string, string, io.Writer) (syncer.Remote, int64, string, error)
		root  string
		mpath string
		out   *bytes.Buffer
		errb  *bytes.Buffer
	)
	BeforeEach(func() {
		orig = syncBackend
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
		Expect(os.WriteFile(filepath.Join(root, "a.jpg"), []byte("x"), 0o644)).To(Succeed())
	})
	AfterEach(func() { syncBackend = orig })

	stub := func(r syncer.Remote, mp string) {
		syncBackend = func(context.Context, string, string, io.Writer) (syncer.Remote, int64, string, error) {
			return r, 1, mp, nil
		}
	}

	It("pushes and prints a summary", func() {
		ff := &fakeSyncFiles{}
		stub(ff, mpath)
		code := runSync([]string{root, "Remote"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("synced: 1 uploaded"))
		Expect(ff.uploads).To(Equal(1))
	})

	It("dry-run prints a plan and uploads nothing", func() {
		ff := &fakeSyncFiles{}
		stub(ff, mpath)
		code := runSync([]string{"--dry-run", root, "Remote"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("dry-run"))
		Expect(ff.uploads).To(Equal(0))
	})

	It("pulls and prints a summary", func() {
		freshRoot := GinkgoT().TempDir() // empty: no local-drift, download proceeds
		ff := &fakeSyncFiles{
			listing: map[int64][]domain.FileInfo{
				1: {{ID: 7, Name: "a.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 100}},
			},
			content: map[int64][]byte{7: []byte("hello")},
		}
		stub(ff, mpath)
		code := runSync([]string{"--pull", freshRoot, "Remote"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("pulled: 1 downloaded"))
		data, err := os.ReadFile(filepath.Join(freshRoot, "a.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello"))
	})

	It("runs --verify after a push and reports the comparison", func() {
		// root already contains a.jpg ("x", size 1); a matching remote means the
		// push is a no-op and verify reports one OK file.
		ff := &fakeSyncFiles{
			listing: map[int64][]domain.FileInfo{
				1: {{ID: 7, Name: "a.jpg", Type: domain.FileTypeFile, Size: 1, LastModifiedAt: 100}},
			},
		}
		stub(ff, mpath)
		code := runSync([]string{"--verify", root, "Remote"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("verify: ok=1"))
	})

	It("returns 1 when the backend fails", func() {
		syncBackend = func(context.Context, string, string, io.Writer) (syncer.Remote, int64, string, error) {
			return nil, 0, "", errors.New("no config")
		}
		code := runSync([]string{root, "Remote"}, out, errb)
		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("no config"))
	})

	It("returns 1 when a transfer fails", func() {
		ff := &fakeSyncFiles{failUpload: true}
		stub(ff, mpath)
		code := runSync([]string{root, "Remote"}, out, errb)
		Expect(code).To(Equal(1))
		Expect(out.String()).To(ContainSubstring("1 failed"))
	})

	It("returns 1 when syncer.Push returns an error (guard deletes)", func() {
		// A manifest with 5 tracked files + an empty local dir means deleting all of
		// them, which exceeds the 20% deletion guard, so Push returns an error.
		emptyRoot := GinkgoT().TempDir()
		manifestData := "1\t1\t10\t1\ta.jpg\n1\t1\t11\t1\tb.jpg\n1\t1\t12\t1\tc.jpg\n1\t1\t13\t1\td.jpg\n1\t1\t14\t1\te.jpg\n"
		Expect(os.WriteFile(mpath, []byte(manifestData), 0o644)).To(Succeed())
		stub(&fakeSyncFiles{}, mpath)
		code := runSync([]string{emptyRoot, "Remote"}, out, errb)
		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("refusing to delete"))
	})
})

var _ = Describe("expandHome", func() {
	It("returns a path unchanged when it has no leading tilde", func() {
		p, err := expandHome("/abs/path")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/abs/path"))
	})

	It("expands bare ~ to the home directory", func() {
		home, err := os.UserHomeDir()
		Expect(err).NotTo(HaveOccurred())
		p, err := expandHome("~")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(home))
	})

	It("expands ~/sub to home/sub", func() {
		home, err := os.UserHomeDir()
		Expect(err).NotTo(HaveOccurred())
		p, err := expandHome("~/pictures")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(filepath.Join(home, "pictures")))
	})
})
