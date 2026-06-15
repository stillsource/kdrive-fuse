package usecase_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

var _ = Describe("ShareFile", func() {
	var (
		sharer *servicefakes.SharesFake
		uc     *usecase.ShareFile
	)

	const fileID int64 = 99

	BeforeEach(func() {
		sharer = &servicefakes.SharesFake{
			PublishResults: map[int64]servicefakes.PublishResult{},
		}
		uc = usecase.NewShareFile(sharer)
	})

	It("returns the share info from Publish on success", func() {
		want := domain.ShareInfo{ID: 7, ShareURL: "https://kdrive.example/s/abc"}
		sharer.PublishResults[fileID] = servicefakes.PublishResult{Info: want}

		got, err := uc.Execute(context.Background(), fileID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))

		Expect(sharer.PublishCalls).To(Equal([]int64{fileID}))
	})

	It("returns the error when Publish fails", func() {
		boom := errors.New("publish boom")
		sharer.PublishResults[fileID] = servicefakes.PublishResult{Err: boom}

		got, err := uc.Execute(context.Background(), fileID)
		Expect(err).To(MatchError(boom))
		Expect(got).To(Equal(domain.ShareInfo{}))
	})
})
