package usecase_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

var _ = Describe("CommitWrite", func() {
	var (
		files *servicefakes.FilesFake
		cache *listingcache.DirCache
		uc    *usecase.CommitWrite
	)

	const parentID int64 = 5

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			UploadResults: map[string]servicefakes.UploadResult{},
		}
		cache = listingcache.NewDirCache(time.Minute)
		uc = usecase.NewCommitWrite(files, cache)

		// Seed the parent listing so we can observe invalidation.
		cache.Set(parentID, []domain.FileInfo{{ID: 1, Name: "old"}})
	})

	It("returns the uploaded info and invalidates the parent listing on success", func() {
		want := domain.FileInfo{ID: 42, Name: "new.txt"}
		files.UploadResults["new.txt"] = servicefakes.UploadResult{Info: want}

		got, err := uc.Execute(context.Background(), service.UploadInput{Name: "new.txt", ParentID: parentID}, parentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))

		// The parent listing was invalidated.
		_, ok := cache.Get(parentID)
		Expect(ok).To(BeFalse())
	})

	It("returns the error and does NOT invalidate the parent on an upload failure", func() {
		boom := errors.New("upload boom")
		files.UploadResults["bad.txt"] = servicefakes.UploadResult{Err: boom}

		got, err := uc.Execute(context.Background(), service.UploadInput{Name: "bad.txt", ParentID: parentID}, parentID)
		Expect(err).To(MatchError(boom))
		Expect(got).To(Equal(domain.FileInfo{}))

		// The pre-seeded parent listing survives — no invalidation on failure.
		entry, ok := cache.Get(parentID)
		Expect(ok).To(BeTrue())
		Expect(entry).To(Equal([]domain.FileInfo{{ID: 1, Name: "old"}}))
	})
})
