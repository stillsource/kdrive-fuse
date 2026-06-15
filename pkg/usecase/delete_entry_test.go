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

var _ = Describe("DeleteEntry", func() {
	var (
		files *servicefakes.FilesFake
		cache *listingcache.DirCache
		uc    *usecase.DeleteEntry
	)

	const (
		fileID   int64 = 99
		parentID int64 = 5
	)

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			DeleteResults: map[int64]error{},
		}
		cache = listingcache.NewDirCache(time.Minute)
		uc = usecase.NewDeleteEntry(files, cache)

		// Seed the parent listing so we can observe invalidation.
		cache.Set(parentID, []domain.FileInfo{{ID: fileID, Name: "doomed"}})
	})

	It("deletes the file then invalidates the parent listing on success", func() {
		err := uc.Execute(context.Background(), fileID, parentID)
		Expect(err).NotTo(HaveOccurred())

		Expect(files.GetDeleteCalls()).To(Equal([]int64{fileID}))

		_, ok := cache.Get(parentID)
		Expect(ok).To(BeFalse())
	})

	It("returns the error and does NOT invalidate the parent on a delete failure", func() {
		boom := errors.New("delete boom")
		files.DeleteResults[fileID] = boom

		err := uc.Execute(context.Background(), fileID, parentID)
		Expect(err).To(MatchError(boom))

		Expect(files.GetDeleteCalls()).To(Equal([]int64{fileID}))

		// The pre-seeded parent listing survives — no invalidation on failure.
		entry, ok := cache.Get(parentID)
		Expect(ok).To(BeTrue())
		Expect(entry).To(Equal([]domain.FileInfo{{ID: fileID, Name: "doomed"}}))
	})
})
