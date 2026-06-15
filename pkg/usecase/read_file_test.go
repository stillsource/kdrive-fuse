package usecase_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// contentCacheFake is a test-local stub for service.ContentCache. The package
// ships no shared fake for this single-method port, so the spec defines one
// here and records the forwarded arguments.
type contentCacheFake struct {
	openFn    func(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error)
	openCalls []contentCacheOpenCall
}

type contentCacheOpenCall struct {
	FileID, LastModifiedAt, Size int64
}

var _ service.ContentCache = (*contentCacheFake)(nil)

func (f *contentCacheFake) Open(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error) {
	f.openCalls = append(f.openCalls, contentCacheOpenCall{FileID: fileID, LastModifiedAt: lastModifiedAt, Size: size})
	return f.openFn(ctx, fileID, lastModifiedAt, size)
}

var _ = Describe("ReadFile", func() {
	var (
		cache *contentCacheFake
		uc    *usecase.ReadFile
	)

	const (
		fileID         int64 = 99
		lastModifiedAt int64 = 1700000000
		size           int64 = 11
	)

	BeforeEach(func() {
		cache = &contentCacheFake{}
		uc = usecase.NewReadFile(cache)
	})

	It("forwards its arguments to ContentCache.Open and returns the handle", func() {
		path := filepath.Join(GinkgoT().TempDir(), "cached")
		Expect(os.WriteFile(path, []byte("hello world"), 0o600)).To(Succeed())
		want, err := os.Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(want.Close)

		cache.openFn = func(_ context.Context, _, _, _ int64) (*os.File, error) {
			return want, nil
		}

		got, err := uc.Execute(context.Background(), fileID, lastModifiedAt, size)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeIdenticalTo(want))

		Expect(cache.openCalls).To(Equal([]contentCacheOpenCall{
			{FileID: fileID, LastModifiedAt: lastModifiedAt, Size: size},
		}))
	})

	It("returns the error when ContentCache.Open fails", func() {
		boom := errors.New("open boom")
		cache.openFn = func(_ context.Context, _, _, _ int64) (*os.File, error) {
			return nil, boom
		}

		got, err := uc.Execute(context.Background(), fileID, lastModifiedAt, size)
		Expect(err).To(MatchError(boom))
		Expect(got).To(BeNil())
	})
})
