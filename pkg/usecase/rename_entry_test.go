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

var _ = Describe("RenameEntry", func() {
	var (
		files *servicefakes.FilesFake
		cache *listingcache.DirCache
		uc    *usecase.RenameEntry
	)

	const (
		fileID    int64 = 99
		srcDirID  int64 = 5
		destDirID int64 = 6
	)

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			MoveResults:   map[int64]error{},
			RenameResults: map[int64]servicefakes.RenameResult{},
		}
		cache = listingcache.NewDirCache(time.Minute)
		uc = usecase.NewRenameEntry(files, cache)

		// Seed both listings so we can observe (non-)invalidation.
		cache.Set(srcDirID, []domain.FileInfo{{ID: fileID, Name: "old"}})
		cache.Set(destDirID, []domain.FileInfo{{ID: 1, Name: "other"}})
	})

	It("moves then renames and invalidates both listings when dir and name both change", func() {
		err := uc.Execute(context.Background(), fileID, srcDirID, destDirID, "old", "new")
		Expect(err).NotTo(HaveOccurred())

		Expect(files.GetMoveCalls()).To(Equal([]servicefakes.MoveCall{{FileID: fileID, DestDirID: destDirID}}))
		Expect(files.GetRenameCalls()).To(Equal([]servicefakes.RenameCall{{FileID: fileID, NewName: "new"}}))

		_, ok := cache.Get(srcDirID)
		Expect(ok).To(BeFalse())
		_, ok = cache.Get(destDirID)
		Expect(ok).To(BeFalse())
	})

	It("moves but does not rename when only the dir changes", func() {
		err := uc.Execute(context.Background(), fileID, srcDirID, destDirID, "same", "same")
		Expect(err).NotTo(HaveOccurred())

		Expect(files.GetMoveCalls()).To(Equal([]servicefakes.MoveCall{{FileID: fileID, DestDirID: destDirID}}))
		Expect(files.GetRenameCalls()).To(BeEmpty())

		_, ok := cache.Get(srcDirID)
		Expect(ok).To(BeFalse())
		_, ok = cache.Get(destDirID)
		Expect(ok).To(BeFalse())
	})

	It("renames but does not move when only the name changes (same dir)", func() {
		err := uc.Execute(context.Background(), fileID, srcDirID, srcDirID, "old", "new")
		Expect(err).NotTo(HaveOccurred())

		Expect(files.GetMoveCalls()).To(BeEmpty())
		Expect(files.GetRenameCalls()).To(Equal([]servicefakes.RenameCall{{FileID: fileID, NewName: "new"}}))

		_, ok := cache.Get(srcDirID)
		Expect(ok).To(BeFalse())
	})

	It("performs no remote mutation but still invalidates when neither dir nor name changes", func() {
		err := uc.Execute(context.Background(), fileID, srcDirID, srcDirID, "same", "same")
		Expect(err).NotTo(HaveOccurred())

		Expect(files.GetMoveCalls()).To(BeEmpty())
		Expect(files.GetRenameCalls()).To(BeEmpty())

		// srcDirID == destDirID here; invalidating it twice is a harmless no-op.
		_, ok := cache.Get(srcDirID)
		Expect(ok).To(BeFalse())
	})

	It("returns the error and invalidates neither listing when Move fails", func() {
		boom := errors.New("move boom")
		files.MoveResults[fileID] = boom

		err := uc.Execute(context.Background(), fileID, srcDirID, destDirID, "old", "new")
		Expect(err).To(MatchError(boom))

		// Rename is never attempted after a Move failure.
		Expect(files.GetRenameCalls()).To(BeEmpty())

		// Both pre-seeded listings survive — no invalidation on failure.
		src, ok := cache.Get(srcDirID)
		Expect(ok).To(BeTrue())
		Expect(src).To(Equal([]domain.FileInfo{{ID: fileID, Name: "old"}}))
		dest, ok := cache.Get(destDirID)
		Expect(ok).To(BeTrue())
		Expect(dest).To(Equal([]domain.FileInfo{{ID: 1, Name: "other"}}))
	})

	It("leaves the move applied but skips cache invalidation when Rename fails after a successful Move", func() {
		boom := errors.New("rename boom")
		files.RenameResults[fileID] = servicefakes.RenameResult{Err: boom}

		err := uc.Execute(context.Background(), fileID, srcDirID, destDirID, "old", "new")
		Expect(err).To(MatchError(boom))

		// The Move did happen (partial mutation), then Rename was attempted and failed.
		Expect(files.GetMoveCalls()).To(Equal([]servicefakes.MoveCall{{FileID: fileID, DestDirID: destDirID}}))
		Expect(files.GetRenameCalls()).To(Equal([]servicefakes.RenameCall{{FileID: fileID, NewName: "new"}}))

		// Neither cache is invalidated when the operation errors mid-flight.
		src, ok := cache.Get(srcDirID)
		Expect(ok).To(BeTrue())
		Expect(src).To(Equal([]domain.FileInfo{{ID: fileID, Name: "old"}}))
		dest, ok := cache.Get(destDirID)
		Expect(ok).To(BeTrue())
		Expect(dest).To(Equal([]domain.FileInfo{{ID: 1, Name: "other"}}))
	})
})
