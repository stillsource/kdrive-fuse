package remoteindex_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// fakeMkdirer records created directories and hands out fresh ids.
type fakeMkdirer struct {
	mu      sync.Mutex
	nextID  int64
	created []string // "parentID/name"
	err     error    // when non-nil, Mkdir fails with it
}

func (f *fakeMkdirer) Mkdir(_ context.Context, parentID int64, name string) (domain.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.FileInfo{}, f.err
	}
	f.nextID++
	f.created = append(f.created, fmt.Sprintf("%d/%s", parentID, name))
	return domain.FileInfo{ID: 1000 + f.nextID, Name: name, Type: domain.FileTypeDir}, nil
}

var _ = Describe("Resolver", func() {
	It("resolves the root for empty, dot and slash", func() {
		r := remoteindex.NewResolver(&fakeLister{calls: map[int64]int{}}, &fakeMkdirer{}, 1)
		for _, p := range []string{"", ".", "/"} {
			id, err := r.Resolve(context.Background(), p)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal(int64(1)))
		}
	})

	It("returns an existing directory without creating it", func() {
		fl := &fakeLister{
			calls: map[int64]int{},
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 2, Name: "2025", Type: domain.FileTypeDir}},
			},
		}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		id, err := r.Resolve(context.Background(), "2025")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(2)))
		Expect(mk.created).To(BeEmpty())
	})

	It("creates missing directories along a nested path", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		id, err := r.Resolve(context.Background(), "2025/11/05")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(BeZero())
		Expect(mk.created).To(HaveLen(3)) // 2025, then 11, then 05
	})

	It("caches resolved paths so repeated resolves do no extra work", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		_, _ = r.Resolve(context.Background(), "a/b")
		_, _ = r.Resolve(context.Background(), "a/b")
		Expect(mk.created).To(HaveLen(2)) // a and b created once total
	})

	It("creates each directory exactly once under concurrent resolves", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = r.Resolve(context.Background(), "x/y/z")
			}()
		}
		wg.Wait()
		Expect(mk.created).To(HaveLen(3)) // x, y, z exactly once each
	})

	It("propagates a listing error (and the parent-resolve error above it)", func() {
		fl := &fakeLister{
			calls:   map[int64]int{},
			errs:    map[int64]error{1: errors.New("list boom")},
			folders: map[int64][]domain.FileInfo{},
		}
		r := remoteindex.NewResolver(fl, &fakeMkdirer{}, 1)
		_, err := r.Resolve(context.Background(), "a/b")
		Expect(err).To(HaveOccurred())
	})

	It("propagates a mkdir error", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{err: errors.New("mkdir boom")}
		r := remoteindex.NewResolver(fl, mk, 1)
		_, err := r.Resolve(context.Background(), "a")
		Expect(err).To(HaveOccurred())
	})
})
