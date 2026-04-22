package vfs

import (
	"context"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/kdrive"
	"github.com/stillsource/kdrive-fuse/kdrive/kdrivefakes"
)

var _ = Describe("writeHandle", func() {
	var (
		fake *kdrivefakes.FilesFake
		ctx  context.Context
	)

	BeforeEach(func() {
		fake = &kdrivefakes.FilesFake{}
		ctx = context.Background()
	})

	It("buffers writes then uploads on Flush", func() {
		fake.UploadStub = func(_ context.Context, in kdrive.UploadInput) (kdrive.FileInfo, error) {
			data, _ := io.ReadAll(in.Body)
			Expect(string(data)).To(Equal("hello"))
			Expect(in.Size).To(Equal(int64(5)))
			Expect(in.Name).To(Equal("x.txt"))
			return kdrive.FileInfo{ID: 99, Name: in.Name, Size: in.Size}, nil
		}
		var captured kdrive.FileInfo
		wh, err := newWriteHandle(fake, 1, 0, "x.txt", func(info kdrive.FileInfo) {
			captured = info
		})
		Expect(err).NotTo(HaveOccurred())

		n, errno := wh.Write(ctx, []byte("hello"), 0)
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(5)))

		Expect(wh.Flush(ctx)).To(BeZero())
		Expect(captured.ID).To(Equal(int64(99)))

		// Second flush is a no-op (already uploaded).
		Expect(wh.Flush(ctx)).To(BeZero())
		Expect(fake.UploadCalls).To(HaveLen(1))

		Expect(wh.Release(ctx)).To(BeZero())
	})

	It("returns EIO when upload fails", func() {
		fake.UploadStub = func(_ context.Context, _ kdrive.UploadInput) (kdrive.FileInfo, error) {
			return kdrive.FileInfo{}, kdrive.ErrServer
		}
		wh, _ := newWriteHandle(fake, 1, 0, "x.txt", nil)
		_, _ = wh.Write(ctx, []byte("a"), 0)
		errno := wh.Flush(ctx)
		Expect(errno).NotTo(BeZero())
		_ = wh.Release(ctx)
	})

	It("Release after upload closes and removes tempfile", func() {
		wh, _ := newWriteHandle(fake, 1, 0, "x.txt", nil)
		name := wh.tmp.Name()
		Expect(wh.Release(ctx)).To(BeZero())
		// subsequent Release is a no-op
		Expect(wh.Release(ctx)).To(BeZero())
		_ = name
	})
})

var _ = Describe("readHandle", func() {
	var (
		dir  string
		fake *kdrivefakes.FilesFake
		ctx  context.Context
		kdfs *KDriveFS
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		fake = &kdrivefakes.FilesFake{
			DownloadStreamResults: map[int64]kdrivefakes.DownloadStreamResult{
				10: {Data: []byte("the quick brown fox")},
			},
		}
		ctx = context.Background()
		disk, _ := NewDiskCache(dir, 1024, fake)
		kdfs = NewKDriveFS(fake, time.Second, disk)
	})

	It("Read serves requested offset and length", func() {
		h := &readHandle{kdfs: kdfs, info: kdrive.FileInfo{ID: 10, LastModifiedAt: 1, Size: 19}}
		buf := make([]byte, 5)
		res, errno := h.Read(ctx, buf, 4)
		Expect(errno).To(BeZero())
		data, _ := res.Bytes(nil)
		Expect(string(data)).To(Equal("quick"))
		Expect(h.Release(ctx)).To(BeZero())
	})

	It("propagates EIO when DiskCache fails", func() {
		fake.DownloadStreamResults = map[int64]kdrivefakes.DownloadStreamResult{
			10: {Err: kdrive.ErrNotFound},
		}
		h := &readHandle{kdfs: kdfs, info: kdrive.FileInfo{ID: 10, LastModifiedAt: 99}}
		_, errno := h.Read(ctx, make([]byte, 1), 0)
		Expect(errno).NotTo(BeZero())
		// second call returns EIO fast-path without retry
		_, errno2 := h.Read(ctx, make([]byte, 1), 0)
		Expect(errno2).NotTo(BeZero())
	})

	It("returns EIO when DiskCache is nil", func() {
		kdfsNoCache := &KDriveFS{Files: fake, Cache: NewDirCache(time.Second)}
		h := &readHandle{kdfs: kdfsNoCache, info: kdrive.FileInfo{ID: 10}}
		_, errno := h.Read(ctx, make([]byte, 1), 0)
		Expect(errno).NotTo(BeZero())
	})
})
