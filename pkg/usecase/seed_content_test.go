package usecase_test

import (
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

var _ = Describe("SeedContent", func() {
	var (
		files *servicefakes.FilesFake
		uc    *usecase.SeedContent
	)

	const fileID int64 = 99

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			DownloadStreamResults: map[int64]servicefakes.DownloadStreamResult{},
		}
		uc = usecase.NewSeedContent(files)
	})

	It("streams the full remote content via DownloadStream(fileID, 0, 0)", func() {
		files.DownloadStreamResults[fileID] = servicefakes.DownloadStreamResult{Data: []byte("hello world")}

		rc, err := uc.Execute(context.Background(), fileID)
		Expect(err).NotTo(HaveOccurred())
		defer rc.Close()

		body, err := io.ReadAll(rc)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal([]byte("hello world")))

		Expect(files.DownloadStreamCalls).To(Equal([]servicefakes.DownloadStreamCall{
			{FileID: fileID, Off: 0, Length: 0},
		}))
	})

	It("returns the error when DownloadStream fails", func() {
		boom := errors.New("download boom")
		files.DownloadStreamResults[fileID] = servicefakes.DownloadStreamResult{Err: boom}

		rc, err := uc.Execute(context.Background(), fileID)
		Expect(err).To(MatchError(boom))
		Expect(rc).To(BeNil())
	})
})
