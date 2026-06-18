package remoteindex_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// fakeLister is a concurrency-safe canned directory tree. Shared with resolver_test.go.
type fakeLister struct {
	mu      sync.Mutex
	folders map[int64][]domain.FileInfo
	errs    map[int64]error
	calls   map[int64]int
}

func (f *fakeLister) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[folderID]++
	if f.errs != nil {
		if err := f.errs[folderID]; err != nil {
			return nil, err
		}
	}
	return f.folders[folderID], nil
}

var _ = Describe("Build", func() {
	dir := func(id int64, name string) domain.FileInfo {
		return domain.FileInfo{ID: id, Name: name, Type: domain.FileTypeDir}
	}
	file := func(id int64, name string, size, mtime int64) domain.FileInfo {
		return domain.FileInfo{ID: id, Name: name, Type: domain.FileTypeFile, Size: size, LastModifiedAt: mtime}
	}

	It("indexes files across a nested tree by relative path", func() {
		fl := &fakeLister{
			calls: map[int64]int{},
			folders: map[int64][]domain.FileInfo{
				1: {file(10, "root.txt", 3, 100), dir(2, "2025")},
				2: {dir(3, "11"), file(11, "top.jpg", 5, 101)},
				3: {file(20, "a.jpg", 7, 200), file(21, "b.jpg", 9, 201)},
			},
		}
		idx, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(HaveLen(4))
		Expect(idx["root.txt"]).To(Equal(remoteindex.Entry{ID: 10, Size: 3, Mtime: 100}))
		Expect(idx["2025/top.jpg"]).To(Equal(remoteindex.Entry{ID: 11, Size: 5, Mtime: 101}))
		Expect(idx["2025/11/a.jpg"]).To(Equal(remoteindex.Entry{ID: 20, Size: 7, Mtime: 200}))
		Expect(idx["2025/11/b.jpg"]).To(Equal(remoteindex.Entry{ID: 21, Size: 9, Mtime: 201}))
	})

	It("returns an empty index for an empty root", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{1: nil}}
		idx, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(BeEmpty())
	})

	It("propagates a listing error", func() {
		fl := &fakeLister{
			calls:   map[int64]int{},
			errs:    map[int64]error{1: errors.New("boom")},
			folders: map[int64][]domain.FileInfo{},
		}
		_, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).To(HaveOccurred())
	})

	It("honors WithParallelism without changing the result", func() {
		fl := &fakeLister{
			calls: map[int64]int{},
			folders: map[int64][]domain.FileInfo{
				1: {file(10, "root.txt", 3, 100), dir(2, "2025")},
				2: {file(11, "top.jpg", 5, 101)},
			},
		}
		idx, err := remoteindex.Build(context.Background(), fl, 1, remoteindex.WithParallelism(2))
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(HaveLen(2))
		Expect(idx["2025/top.jpg"]).To(Equal(remoteindex.Entry{ID: 11, Size: 5, Mtime: 101}))
	})

	It("ignores a non-positive WithParallelism (keeps the default)", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{1: {file(10, "x", 1, 1)}}}
		idx, err := remoteindex.Build(context.Background(), fl, 1, remoteindex.WithParallelism(0))
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(HaveLen(1))
	})
})

var _ = Describe("ResolveDir", func() {
	dir := func(id int64, name string) domain.FileInfo {
		return domain.FileInfo{ID: id, Name: name, Type: domain.FileTypeDir}
	}
	file := func(id int64, name string) domain.FileInfo {
		return domain.FileInfo{ID: id, Name: name, Type: domain.FileTypeFile}
	}
	tree := func() *fakeLister {
		return &fakeLister{
			calls: map[int64]int{},
			folders: map[int64][]domain.FileInfo{
				1: {dir(2, "Photos"), file(99, "root.txt")},
				2: {dir(3, "2025"), file(20, "a.jpg")},
				3: {file(30, "b.jpg")},
			},
		}
	}
	ctx := context.Background()

	It("resolves a nested directory path to its folder id", func() {
		id, err := remoteindex.ResolveDir(ctx, tree(), 1, "Photos/2025")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(3)))
	})

	DescribeTable("resolves the root for empty-ish paths",
		func(p string) {
			id, err := remoteindex.ResolveDir(ctx, tree(), 1, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal(int64(1)))
		},
		Entry("empty", ""), Entry("dot", "."), Entry("slash", "/"),
	)

	It("returns ErrNotFound when a segment is missing", func() {
		_, err := remoteindex.ResolveDir(ctx, tree(), 1, "Photos/nope")
		Expect(errors.Is(err, domain.ErrNotFound)).To(BeTrue())
	})

	It("returns ErrNotFound when a segment names a non-directory", func() {
		_, err := remoteindex.ResolveDir(ctx, tree(), 1, "root.txt")
		Expect(errors.Is(err, domain.ErrNotFound)).To(BeTrue())
	})

	It("propagates a listing error", func() {
		fl := tree()
		fl.errs = map[int64]error{2: errors.New("boom")}
		_, err := remoteindex.ResolveDir(ctx, fl, 1, "Photos/2025")
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})
})
