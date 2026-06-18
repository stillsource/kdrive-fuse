package filesearch_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/filesearch"
)

// fakeLister serves a fixed folder tree, keyed by folder id. A folder id present
// in errs returns that error instead of children.
type fakeLister struct {
	folders map[int64][]domain.FileInfo
	errs    map[int64]error
}

func (f *fakeLister) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	if err, ok := f.errs[folderID]; ok {
		return nil, err
	}
	return f.folders[folderID], nil
}

// tree rooted at id 1:
//
//	report.pdf                       (10)
//	Photos/DSCF001.JPG               (20)
//	Photos/vacation 2025.jpg         (21)
//	Docs/report-2025.txt             (30)
//	Docs/Archive/old report.pdf      (40)
func newTree() *fakeLister {
	return &fakeLister{folders: map[int64][]domain.FileInfo{
		1: {
			{ID: 10, Name: "report.pdf", Type: domain.FileTypeFile, Size: 100},
			{ID: 2, Name: "Photos", Type: domain.FileTypeDir},
			{ID: 3, Name: "Docs", Type: domain.FileTypeDir},
		},
		2: {
			{ID: 20, Name: "DSCF001.JPG", Type: domain.FileTypeFile, Size: 200},
			{ID: 21, Name: "vacation 2025.jpg", Type: domain.FileTypeFile, Size: 210},
		},
		3: {
			{ID: 30, Name: "report-2025.txt", Type: domain.FileTypeFile, Size: 300},
			{ID: 4, Name: "Archive", Type: domain.FileTypeDir},
		},
		4: {
			{ID: 40, Name: "old report.pdf", Type: domain.FileTypeFile, Size: 400},
		},
	}}
}

var _ = Describe("filesearch.Searcher", func() {
	var s *filesearch.Searcher
	ctx := context.Background()

	BeforeEach(func() {
		s = filesearch.New(newTree(), 1)
	})

	It("matches a term against names and ancestor directories, sorted by path", func() {
		hits, err := s.Search(ctx, "report")
		Expect(err).NotTo(HaveOccurred())
		var got []string
		for _, h := range hits {
			got = append(got, h.Path)
		}
		// All three "report" paths, in lexical order.
		Expect(got).To(Equal([]string{
			"Docs/Archive/old report.pdf",
			"Docs/report-2025.txt",
			"report.pdf",
		}))
		// id + size carried through for scripting (hits[2] is report.pdf).
		Expect(hits[2].ID).To(Equal(int64(10)))
		Expect(hits[2].Size).To(Equal(int64(100)))
	})

	It("requires ALL terms (AND) across the path", func() {
		hits, err := s.Search(ctx, "report 2025")
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Path).To(Equal("Docs/report-2025.txt"))
		Expect(hits[0].ID).To(Equal(int64(30)))
		Expect(hits[0].Size).To(Equal(int64(300)))
	})

	It("is case-insensitive", func() {
		hits, err := s.Search(ctx, "REPORT")
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(3))
	})

	It("matches on a directory term", func() {
		hits, err := s.Search(ctx, "photos")
		Expect(err).NotTo(HaveOccurred())
		var got []string
		for _, h := range hits {
			got = append(got, h.Path)
		}
		Expect(got).To(ConsistOf("Photos/DSCF001.JPG", "Photos/vacation 2025.jpg"))
	})

	It("returns no hits when nothing matches", func() {
		hits, err := s.Search(ctx, "zzqxnonexistent")
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(BeEmpty())
	})

	It("rejects an empty or whitespace-only query as a validation error", func() {
		_, err := s.Search(ctx, "   ")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, domain.ErrValidation)).To(BeTrue())
	})

	It("propagates a listing error from the tree walk", func() {
		boom := errors.New("list failed")
		lister := newTree()
		lister.errs = map[int64]error{2: boom} // Photos folder errors
		s = filesearch.New(lister, 1)
		_, err := s.Search(ctx, "report")
		Expect(err).To(MatchError(boom))
	})
})
