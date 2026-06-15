package usecase_test

import (
	"context"
	"errors"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

var _ = Describe("MakeDir", func() {
	var (
		files *servicefakes.FilesFake
		cache *listingcache.DirCache
		uc    *usecase.MakeDir
	)

	const parentID int64 = 5

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			MkdirResults: map[string]servicefakes.MkdirResult{},
		}
		cache = listingcache.NewDirCache(time.Minute)
		uc = usecase.NewMakeDir(files, cache)

		// Seed the parent listing so we can observe invalidation.
		cache.Set(parentID, []domain.FileInfo{{ID: 1, Name: "existing"}})
	})

	It("creates the directory and invalidates the parent listing on success", func() {
		want := domain.FileInfo{ID: 42, Name: "sub"}
		files.MkdirResults[strconv.FormatInt(parentID, 10)+"/sub"] = servicefakes.MkdirResult{Info: want}

		got, err := uc.Execute(context.Background(), parentID, "sub")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))

		Expect(files.GetMkdirCalls()).To(Equal([]servicefakes.MkdirCall{{ParentID: parentID, Name: "sub"}}))

		_, ok := cache.Get(parentID)
		Expect(ok).To(BeFalse())
	})

	It("returns the error and does NOT invalidate the parent on a Mkdir failure", func() {
		boom := errors.New("mkdir boom")
		files.MkdirResults[strconv.FormatInt(parentID, 10)+"/bad"] = servicefakes.MkdirResult{Err: boom}

		got, err := uc.Execute(context.Background(), parentID, "bad")
		Expect(err).To(MatchError(boom))
		Expect(got).To(Equal(domain.FileInfo{}))

		// The pre-seeded parent listing survives — no invalidation on failure.
		entry, ok := cache.Get(parentID)
		Expect(ok).To(BeTrue())
		Expect(entry).To(Equal([]domain.FileInfo{{ID: 1, Name: "existing"}}))
	})
})
