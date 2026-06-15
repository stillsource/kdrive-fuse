package vfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
)

var _ = Describe("DiskCache", func() {
	var (
		dir  string
		fake *servicefakes.FilesFake
		ctx  context.Context
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		fake = &servicefakes.FilesFake{}
		ctx = context.Background()
	})

	It("downloads on miss and caches the content", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			10: {Data: []byte("hello cached")},
		}
		dc, err := NewDiskCache(dir, 1024, fake)
		Expect(err).NotTo(HaveOccurred())

		f, err := dc.Open(ctx, 10, 1700000000, 12)
		Expect(err).NotTo(HaveOccurred())
		defer f.Close()
		got, _ := io.ReadAll(f)
		Expect(string(got)).To(Equal("hello cached"))
		Expect(fake.DownloadStreamCalls).To(HaveLen(1))
	})

	It("serves subsequent opens from disk without re-downloading", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			10: {Data: []byte("x")},
		}
		dc, _ := NewDiskCache(dir, 1024, fake)

		f1, _ := dc.Open(ctx, 10, 42, 1)
		_ = f1.Close()
		f2, err := dc.Open(ctx, 10, 42, 1)
		Expect(err).NotTo(HaveOccurred())
		defer f2.Close()
		Expect(fake.DownloadStreamCalls).To(HaveLen(1))
	})

	It("re-downloads when mtime key changes (invalidation)", func() {
		fake.DownloadStreamStub = func(_ context.Context, id, _, _ int64) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("v"))), nil
		}
		dc, _ := NewDiskCache(dir, 1024, fake)

		f1, _ := dc.Open(ctx, 10, 1, 1)
		_ = f1.Close()
		f2, _ := dc.Open(ctx, 10, 2, 1) // different mtime
		_ = f2.Close()
		Expect(fake.DownloadStreamCalls).To(HaveLen(2))
	})

	It("evicts oldest entries when cache exceeds budget", func() {
		// Each download returns 5 bytes; budget is 8 bytes total (below two entries).
		fake.DownloadStreamStub = func(_ context.Context, _, _, _ int64) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("abcde"))), nil
		}
		dc, _ := NewDiskCache(dir, 8, fake)

		_, _ = dc.Open(ctx, 1, 1, 5)
		time.Sleep(10 * time.Millisecond) // separate atime
		_, _ = dc.Open(ctx, 2, 1, 5)

		entries, _ := os.ReadDir(dir)
		Expect(len(entries)).To(BeNumerically("<=", 1))
	})

	It("propagates download error", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			1: {Err: domain.ErrNotFound},
		}
		dc, _ := NewDiskCache(dir, 1024, fake)
		_, err := dc.Open(ctx, 1, 1, 1)
		Expect(err).To(MatchError(domain.ErrNotFound))
	})

	It("rejects a non-writable dir on construction", func() {
		bad := filepath.Join(dir, "nope", "\x00")
		_, err := NewDiskCache(bad, 1024, fake)
		Expect(err).To(HaveOccurred())
	})
})
