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

var _ = Describe("Build hardening", func() {
	It("propagates an error from a non-root folder", func() {
		fl := &fakeLister{
			calls: map[int64]int{},
			errs:  map[int64]error{3: errors.New("deep boom")},
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 2, Name: "d", Type: domain.FileTypeDir}},
				2: {{ID: 3, Name: "e", Type: domain.FileTypeDir}},
				// folder 3 returns an error
			},
		}
		_, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Resolver hardening", func() {
	It("ignores a non-directory of the same name and creates the directory", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{
			1: {{ID: 5, Name: "2025", Type: domain.FileTypeFile}}, // a FILE named 2025
		}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		id, err := r.Resolve(context.Background(), "2025")
		Expect(err).NotTo(HaveOccurred())
		Expect(mk.created).To(HaveLen(1)) // the file is ignored; a dir is created
		Expect(id).NotTo(BeZero())
	})

	It("hands the same id to all concurrent resolvers of a new path", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		ids := make([]int64, 10)
		var wg sync.WaitGroup
		for i := range 10 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id, _ := r.Resolve(context.Background(), "x")
				ids[i] = id
			}(i)
		}
		wg.Wait()
		for _, id := range ids {
			Expect(id).To(Equal(ids[0])) // all callers see the one created folder
		}
		Expect(mk.created).To(HaveLen(1))
	})
})
