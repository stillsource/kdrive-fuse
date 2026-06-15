package servicefakes_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
)

func TestKdrivefakes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "servicefakes Suite")
}

var _ = Describe("FilesFake", func() {
	var f *servicefakes.FilesFake
	ctx := context.Background()

	BeforeEach(func() {
		f = &servicefakes.FilesFake{}
	})

	It("List uses stub when set", func() {
		f.ListStub = func(_ context.Context, id int64) ([]domain.FileInfo, error) {
			return []domain.FileInfo{{ID: id, Name: "s"}}, nil
		}
		got, err := f.List(ctx, 42)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal(int64(42)))
		Expect(f.ListCalls).To(HaveLen(1))
	})

	It("List uses Results map when stub absent", func() {
		f.ListResults = map[int64]servicefakes.ListResult{
			42: {Files: []domain.FileInfo{{ID: 99}}, Err: nil},
		}
		got, err := f.List(ctx, 42)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal(int64(99)))
	})

	It("List returns zero value as default", func() {
		got, err := f.List(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("Stat results map propagates error", func() {
		boom := errors.New("boom")
		f.StatResults = map[int64]servicefakes.StatResult{1: {Err: boom}}
		_, err := f.Stat(ctx, 1)
		Expect(err).To(Equal(boom))
	})

	It("Download uses Results", func() {
		f.DownloadResults = map[int64]servicefakes.DownloadResult{1: {Data: []byte("abc")}}
		got, err := f.Download(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("abc"))
	})

	It("DownloadStream wraps bytes in a ReadCloser", func() {
		f.DownloadStreamResults = map[int64]servicefakes.DownloadStreamResult{7: {Data: []byte("xyz")}}
		rc, err := f.DownloadStream(ctx, 7, 0, 0)
		Expect(err).NotTo(HaveOccurred())
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		Expect(string(b)).To(Equal("xyz"))
		Expect(f.DownloadStreamCalls).To(HaveLen(1))
	})

	It("Upload records input and returns result by name key", func() {
		f.UploadResults = map[string]servicefakes.UploadResult{
			"new.txt": {Info: domain.FileInfo{ID: 5, Name: "new.txt"}},
		}
		info, err := f.Upload(ctx, service.UploadInput{
			ParentID: 1, Name: "new.txt",
			Body: bytes.NewReader(nil), Size: 0,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ID).To(Equal(int64(5)))
		Expect(f.UploadCalls).To(HaveLen(1))
	})

	It("Upload keys by id:N in edit mode", func() {
		f.UploadResults = map[string]servicefakes.UploadResult{
			"id:42": {Info: domain.FileInfo{ID: 42}},
		}
		info, err := f.Upload(ctx, service.UploadInput{
			ExistingFileID: 42, Body: bytes.NewReader(nil), Size: 0,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ID).To(Equal(int64(42)))
	})

	It("Mkdir uses parentID/name key", func() {
		f.MkdirResults = map[string]servicefakes.MkdirResult{
			"1/new": {Info: domain.FileInfo{ID: 9, Name: "new", Type: domain.FileTypeDir}},
		}
		info, err := f.Mkdir(ctx, 1, "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())
	})

	It("Delete / Rename / Move use per-id maps", func() {
		boom := errors.New("boom")
		f.DeleteResults = map[int64]error{3: boom}
		f.RenameResults = map[int64]servicefakes.RenameResult{
			5: {Info: domain.FileInfo{ID: 5, Name: "r"}, Err: nil},
		}
		f.MoveResults = map[int64]error{7: nil}

		Expect(f.Delete(ctx, 3)).To(Equal(boom))
		info, err := f.Rename(ctx, 5, "r")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Name).To(Equal("r"))
		Expect(f.Move(ctx, 7, 8)).To(Succeed())

		Expect(f.DeleteCalls).To(Equal([]int64{3}))
		Expect(f.RenameCalls).To(HaveLen(1))
		Expect(f.MoveCalls).To(HaveLen(1))
	})

	It("Stubs take precedence over Results maps for every method", func() {
		f.DownloadStub = func(_ context.Context, id int64) ([]byte, error) { return []byte("stubbed"), nil }
		f.DownloadResults = map[int64]servicefakes.DownloadResult{1: {Data: []byte("results")}}
		got, err := f.Download(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("stubbed"))
	})
})

var _ = Describe("FilesFake snapshot getters", func() {
	It("return defensive copies of every Calls slice", func() {
		f := &servicefakes.FilesFake{}
		ctx := context.Background()
		_, _ = f.List(ctx, 1)
		_, _ = f.Mkdir(ctx, 1, "d")
		_ = f.Delete(ctx, 3)
		_, _ = f.Rename(ctx, 4, "r")
		_ = f.Move(ctx, 5, 6)
		_, _ = f.Upload(ctx, service.UploadInput{ParentID: 1, Name: "x", Body: bytes.NewReader(nil)})

		Expect(f.GetListCalls()).To(HaveLen(1))
		Expect(f.GetMkdirCalls()).To(HaveLen(1))
		Expect(f.GetDeleteCalls()).To(Equal([]int64{3}))
		Expect(f.GetRenameCalls()).To(HaveLen(1))
		Expect(f.GetMoveCalls()).To(HaveLen(1))
		Expect(f.GetUploadCalls()).To(HaveLen(1))

		// Mutating the returned copy must not affect the fake.
		del := f.GetDeleteCalls()
		del[0] = 99
		Expect(f.GetDeleteCalls()[0]).To(Equal(int64(3)))
	})
})

var _ = Describe("SharesFake", func() {
	It("supports stub / results / default", func() {
		f := &servicefakes.SharesFake{}
		info, err := f.Publish(context.Background(), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ShareURL).To(BeEmpty())

		f.PublishResults = map[int64]servicefakes.PublishResult{
			2: {Info: domain.ShareInfo{ShareURL: "https://x"}},
		}
		info, err = f.Publish(context.Background(), 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ShareURL).To(Equal("https://x"))

		f.PublishStub = func(_ context.Context, _ int64) (domain.ShareInfo, error) {
			return domain.ShareInfo{ShareURL: "https://stub"}, nil
		}
		info, _ = f.Publish(context.Background(), 2)
		Expect(info.ShareURL).To(Equal("https://stub"))
		Expect(f.PublishCalls).To(HaveLen(3))
	})
})
