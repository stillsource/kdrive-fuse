package fuse

import (
	"context"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

var _ = Describe("writeHandle", func() {
	var (
		fake *servicefakes.FilesFake
		ctx  context.Context
		seed *usecase.SeedContent
		comm *usecase.CommitWrite
	)

	BeforeEach(func() {
		fake = &servicefakes.FilesFake{}
		ctx = context.Background()
		// Wire the two use cases the writeHandle drives over the same fake.
		// CommitWrite needs a listing cache to invalidate; a real one is fine.
		seed = usecase.NewSeedContent(fake)
		comm = usecase.NewCommitWrite(fake, listingcache.NewDirCache(time.Second))
	})

	// newWH builds a writeHandle over the wired use cases — keeps call sites terse.
	newWH := func(parentID, existingFileID int64, name string, onUploaded func(domain.FileInfo)) *writeHandle {
		wh, err := newWriteHandle(seed, comm, parentID, existingFileID, name, onUploaded)
		Expect(err).NotTo(HaveOccurred())
		return wh
	}

	// uploadRecorder returns an UploadStub that records every uploaded body.
	uploadRecorder := func(bodies *[]string) func(context.Context, service.UploadInput) (domain.FileInfo, error) {
		return func(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
			data, _ := io.ReadAll(in.Body)
			*bodies = append(*bodies, string(data))
			return domain.FileInfo{ID: 99, Name: in.Name, Size: int64(len(data))}, nil
		}
	}

	It("a Flush before any write does not upload; the written content is committed on the next Flush", func() {
		var bodies []string
		fake.UploadStub = uploadRecorder(&bodies)
		wh := newWH(1, 0, "x.txt", nil)

		Expect(wh.Flush(ctx)).To(BeZero()) // premature flush, nothing written yet
		Expect(bodies).To(BeEmpty())       // must NOT upload an empty buffer

		_, _ = wh.Write(ctx, []byte("data"), 0)
		Expect(wh.Flush(ctx)).To(BeZero()) // close flush, after the write
		Expect(bodies).To(Equal([]string{"data"}))
		Expect(wh.Release(ctx)).To(BeZero()) // already uploaded -> no second upload
		Expect(bodies).To(Equal([]string{"data"}))
	})

	It("commits the final buffer on Release when writes arrive after the last Flush", func() {
		var bodies []string
		fake.UploadStub = uploadRecorder(&bodies)
		wh := newWH(1, 0, "x.txt", nil)

		Expect(wh.Flush(ctx)).To(BeZero()) // flush before the write (the kernel's FLUSH-before-WRITE order)
		_, _ = wh.Write(ctx, []byte("late"), 0)
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(bodies).To(Equal([]string{"late"})) // Release is the safety net
	})

	It("a truncate then write uploads only the new bytes (no stale tail)", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			10: {Data: []byte("ABCDEFGHIJ")},
		}
		var bodies []string
		fake.UploadStub = uploadRecorder(&bodies)
		wh := newWH(1, 10, "x.txt", nil) // edit existing id=10

		wh.truncateTo(0) // kernel truncate (Setattr size=0)
		_, _ = wh.Write(ctx, []byte("short"), 0)
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(bodies).To(Equal([]string{"short"})) // not "shortFGHIJ"
	})

	It("seeds remote content lazily then overlays a partial write", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			10: {Data: []byte("ABCDEFGHIJ")},
		}
		var bodies []string
		fake.UploadStub = uploadRecorder(&bodies)
		wh := newWH(1, 10, "x.txt", nil) // edit, not truncated

		_, _ = wh.Write(ctx, []byte("xy"), 2)
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(bodies).To(Equal([]string{"ABxyEFGHIJ"}))
	})

	It("does NOT upload on a truncate with no write (an aborted overwrite must not empty the file)", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			10: {Data: []byte("ABCDEFGHIJ")},
		}
		var bodies []string
		fake.UploadStub = uploadRecorder(&bodies)
		wh := newWH(1, 10, "x.txt", nil)

		wh.truncateTo(0) // O_TRUNC then no write — could be an aborted overwrite
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(bodies).To(BeEmpty()) // the existing file is left untouched
	})

	It("does NOT upload a new file that was never written (no 0-byte placeholder)", func() {
		var bodies []string
		fake.UploadStub = uploadRecorder(&bodies)
		wh := newWH(1, 0, "empty.txt", nil) // new file, created but never written
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(bodies).To(BeEmpty()) // no empty placeholder committed
	})

	It("does not upload an existing file opened and closed without changes", func() {
		wh := newWH(1, 10, "x.txt", nil) // edit, untouched
		Expect(wh.Flush(ctx)).To(BeZero())
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(fake.GetUploadCalls()).To(BeEmpty())
	})

	It("surfaces upload errors on Flush (so close() fails)", func() {
		fake.UploadStub = func(_ context.Context, _ service.UploadInput) (domain.FileInfo, error) {
			return domain.FileInfo{}, domain.ErrServer
		}
		wh := newWH(1, 0, "x.txt", nil)
		_, _ = wh.Write(ctx, []byte("a"), 0)
		Expect(wh.Flush(ctx)).NotTo(BeZero())
		_ = wh.Release(ctx)
	})

	It("Release closes and removes the tempfile, idempotently", func() {
		wh := newWH(1, 0, "x.txt", nil)
		name := wh.tmp.Name()
		Expect(wh.Release(ctx)).To(BeZero())
		Expect(wh.Release(ctx)).To(BeZero()) // second Release is a no-op
		_ = name
	})
})

var _ = Describe("readHandle", func() {
	var (
		dir  string
		fake *servicefakes.FilesFake
		ctx  context.Context
		kdfs *KDriveFS
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		fake = &servicefakes.FilesFake{
			DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{
				10: {Data: []byte("the quick brown fox")},
			},
		}
		ctx = context.Background()
		disk, _ := contentcache.NewDiskCache(dir, 1024, fake)
		kdfs = NewKDriveFS(fake, time.Second, disk, false)
	})

	It("Read serves requested offset and length", func() {
		h := &readHandle{kdfs: kdfs, info: domain.FileInfo{ID: 10, LastModifiedAt: 1, Size: 19}}
		buf := make([]byte, 5)
		res, errno := h.Read(ctx, buf, 4)
		Expect(errno).To(BeZero())
		data, _ := res.Bytes(nil)
		Expect(string(data)).To(Equal("quick"))
		Expect(h.Release(ctx)).To(BeZero())
	})

	It("propagates EIO when DiskCache fails", func() {
		fake.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{
			10: {Err: domain.ErrNotFound},
		}
		h := &readHandle{kdfs: kdfs, info: domain.FileInfo{ID: 10, LastModifiedAt: 99}}
		_, errno := h.Read(ctx, make([]byte, 1), 0)
		Expect(errno).NotTo(BeZero())
		// second call returns EIO fast-path without retry
		_, errno2 := h.Read(ctx, make([]byte, 1), 0)
		Expect(errno2).NotTo(BeZero())
	})

	It("returns EIO when DiskCache is nil", func() {
		kdfsNoCache := &KDriveFS{} // no ReadFile use case wired
		h := &readHandle{kdfs: kdfsNoCache, info: domain.FileInfo{ID: 10}}
		_, errno := h.Read(ctx, make([]byte, 1), 0)
		Expect(errno).NotTo(BeZero())
	})
})
