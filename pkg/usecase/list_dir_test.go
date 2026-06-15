package usecase_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

var _ = Describe("ListDir", func() {
	var (
		files *servicefakes.FilesFake
		cache *listingcache.DirCache
		uc    *usecase.ListDir
	)

	sample := []domain.FileInfo{{ID: 10, Name: "a"}, {ID: 11, Name: "b"}}

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				7: {Files: sample},
			},
		}
		cache = listingcache.NewDirCache(time.Minute)
		uc = usecase.NewListDir(files, cache)
	})

	It("calls List once on a cache miss, then serves subsequent calls from cache", func() {
		ctx := context.Background()

		got, err := uc.Execute(ctx, 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(sample))
		Expect(files.GetListCalls()).To(HaveLen(1))

		// The result is now cached.
		cached, ok := cache.Get(7)
		Expect(ok).To(BeTrue())
		Expect(cached).To(Equal(sample))

		// Second call is served from cache: List is not called again.
		got2, err := uc.Execute(ctx, 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(got2).To(Equal(sample))
		Expect(files.GetListCalls()).To(HaveLen(1))
	})

	It("returns the error and does not populate the cache when List fails", func() {
		boom := errors.New("list boom")
		files.ListResults[9] = servicefakes.ListResult{Err: boom}

		got, err := uc.Execute(context.Background(), 9)
		Expect(err).To(MatchError(boom))
		Expect(got).To(BeNil())

		_, ok := cache.Get(9)
		Expect(ok).To(BeFalse())
	})
})
