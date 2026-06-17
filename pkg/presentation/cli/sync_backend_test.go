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

// fakeSyncFiles is an in-memory syncer.FilesPort for runSync tests.
type fakeSyncFiles struct {
	mu         sync.Mutex
	uploads    int
	failUpload bool
}

func (f *fakeSyncFiles) List(context.Context, int64) ([]domain.FileInfo, error) { return nil, nil }

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

var _ = Describe("runSync with a fake backend", func() {
	var (
		orig  func(context.Context, string, string, io.Writer) (syncer.FilesPort, int64, string, error)
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

	It("pushes and prints a summary", func() {
		ff := &fakeSyncFiles{}
		syncBackend = func(context.Context, string, string, io.Writer) (syncer.FilesPort, int64, string, error) {
			return ff, 1, mpath, nil
		}
		code := runSync([]string{root, "Remote"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("synced: 1 uploaded"))
		Expect(ff.uploads).To(Equal(1))
	})

	It("dry-run prints a plan and uploads nothing", func() {
		ff := &fakeSyncFiles{}
		syncBackend = func(context.Context, string, string, io.Writer) (syncer.FilesPort, int64, string, error) {
			return ff, 1, mpath, nil
		}
		code := runSync([]string{"--dry-run", root, "Remote"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("dry-run"))
		Expect(ff.uploads).To(Equal(0))
	})

	It("returns 1 when the backend fails", func() {
		syncBackend = func(context.Context, string, string, io.Writer) (syncer.FilesPort, int64, string, error) {
			return nil, 0, "", errors.New("no config")
		}
		code := runSync([]string{root, "Remote"}, out, errb)
		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("no config"))
	})

	It("returns 1 when a transfer fails", func() {
		ff := &fakeSyncFiles{failUpload: true}
		syncBackend = func(context.Context, string, string, io.Writer) (syncer.FilesPort, int64, string, error) {
			return ff, 1, mpath, nil
		}
		code := runSync([]string{root, "Remote"}, out, errb)
		Expect(code).To(Equal(1))
		Expect(out.String()).To(ContainSubstring("1 failed"))
	})
})
