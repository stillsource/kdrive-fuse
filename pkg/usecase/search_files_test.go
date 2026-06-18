package usecase_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// fakeSearcher is a minimal in-memory service.Searcher for SearchFiles tests.
type fakeSearcher struct {
	results map[string]fakeSearchResult
	calls   []string
}

type fakeSearchResult struct {
	files []domain.FileInfo
	err   error
}

func (f *fakeSearcher) Search(_ context.Context, query string) ([]domain.FileInfo, error) {
	f.calls = append(f.calls, query)
	if res, ok := f.results[query]; ok {
		return res.files, res.err
	}
	return nil, nil
}

var _ = Describe("SearchFiles", func() {
	var (
		searcher *fakeSearcher
		uc       *usecase.SearchFiles
	)

	BeforeEach(func() {
		searcher = &fakeSearcher{results: map[string]fakeSearchResult{}}
		uc = usecase.NewSearchFiles(searcher)
	})

	It("returns files from the searcher on success", func() {
		want := []domain.FileInfo{
			{ID: 1, Name: "report.pdf", Size: 2048},
			{ID: 2, Name: "notes.txt", Size: 512},
		}
		searcher.results["report"] = fakeSearchResult{files: want}

		got, err := uc.Execute(context.Background(), "report")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))
		Expect(searcher.calls).To(Equal([]string{"report"}))
	})

	It("returns the error when Search fails", func() {
		boom := errors.New("api down")
		searcher.results["fail"] = fakeSearchResult{err: boom}

		got, err := uc.Execute(context.Background(), "fail")
		Expect(err).To(MatchError(boom))
		Expect(got).To(BeNil())
	})

	It("returns an empty slice when no results match", func() {
		searcher.results["nothing"] = fakeSearchResult{files: []domain.FileInfo{}}

		got, err := uc.Execute(context.Background(), "nothing")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
})
