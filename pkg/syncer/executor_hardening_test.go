package syncer_test

import (
	"context"
	"errors"
	"io"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// errReader fails every read; erroringDownloader hands one out as the body.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stream boom") }

type erroringDownloader struct{}

func (erroringDownloader) DownloadStream(context.Context, int64, int64, int64) (io.ReadCloser, error) {
	return io.NopCloser(errReader{}), nil
}

var _ = Describe("executor error paths", func() {
	It("PushExecutor.Overwrite errors when the local file is missing", func() {
		root := GinkgoT().TempDir()
		files := &recordingFiles{folders: map[int64][]domain.FileInfo{}, failUpload: map[string]bool{}}
		resolver := remoteindex.NewResolver(files, files, 1)
		ex := syncer.NewPushExecutor(root, resolver, files, files, files, files)
		_, err := ex.Overwrite(context.Background(), "missing.jpg", 7, 3)
		Expect(err).To(HaveOccurred())
	})

	It("PullExecutor.Download errors and leaves no temp file when the stream fails", func() {
		root := GinkgoT().TempDir()
		ex := syncer.NewPullExecutor(root, erroringDownloader{})
		_, _, err := ex.Download(context.Background(), "a.jpg", 7)
		Expect(err).To(HaveOccurred())
		entries, readErr := os.ReadDir(root)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty()) // temp file cleaned up on failure
	})
})
