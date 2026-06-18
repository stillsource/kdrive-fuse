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

var _ = Describe("SetMtime", func() {
	var (
		files *servicefakes.FilesFake
		cache *listingcache.DirCache
		uc    *usecase.SetMtime
	)

	const (
		fileID   int64 = 42
		parentID int64 = 5
		mtime    int64 = 1700000000
	)

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			SetModifiedAtResults: map[int64]servicefakes.SetModifiedAtResult{},
		}
		cache = listingcache.NewDirCache(time.Minute)
		uc = usecase.NewSetMtime(files, cache)

		// Seed the parent listing so we can observe invalidation.
		cache.Set(parentID, []domain.FileInfo{{ID: fileID, Name: "doc.txt", LastModifiedAt: 100}})
	})

	It("calls SetModifiedAt with the correct arguments", func() {
		files.SetModifiedAtResults[fileID] = servicefakes.SetModifiedAtResult{
			Info: domain.FileInfo{ID: fileID, Name: "doc.txt", LastModifiedAt: mtime},
		}
		info, err := uc.Execute(context.Background(), fileID, mtime, parentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.LastModifiedAt).To(Equal(mtime))

		calls := files.GetSetModifiedAtCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].FileID).To(Equal(fileID))
		Expect(calls[0].ModifiedAt).To(Equal(mtime))
	})

	It("invalidates the parent listing on success", func() {
		files.SetModifiedAtResults[fileID] = servicefakes.SetModifiedAtResult{
			Info: domain.FileInfo{ID: fileID, Name: "doc.txt", LastModifiedAt: mtime},
		}
		_, err := uc.Execute(context.Background(), fileID, mtime, parentID)
		Expect(err).NotTo(HaveOccurred())

		_, ok := cache.Get(parentID)
		Expect(ok).To(BeFalse(), "parent listing should be invalidated")
	})

	It("returns the error and does not invalidate the listing when SetModifiedAt fails", func() {
		boom := errors.New("set-mtime boom")
		files.SetModifiedAtResults[fileID] = servicefakes.SetModifiedAtResult{Err: boom}

		_, err := uc.Execute(context.Background(), fileID, mtime, parentID)
		Expect(err).To(MatchError(boom))

		// The pre-seeded listing must survive — no invalidation on failure.
		listing, ok := cache.Get(parentID)
		Expect(ok).To(BeTrue())
		Expect(listing).To(HaveLen(1))
	})
})
