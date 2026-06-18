package usecase_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// fakeSearcher is a minimal in-memory service.Searcher for SearchFiles tests.
type fakeSearcher struct {
	results map[string]fakeSearchResult
	calls   []string
}

type fakeSearchResult struct {
	hits []service.SearchHit
	err  error
}

func (f *fakeSearcher) Search(_ context.Context, query string) ([]service.SearchHit, error) {
	f.calls = append(f.calls, query)
	if res, ok := f.results[query]; ok {
		return res.hits, res.err
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

	It("returns hits from the searcher on success", func() {
		want := []service.SearchHit{
			{ID: 1, Path: "docs/report.pdf", Size: 2048},
			{ID: 2, Path: "notes.txt", Size: 512},
		}
		searcher.results["report"] = fakeSearchResult{hits: want}

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
		searcher.results["nothing"] = fakeSearchResult{hits: []service.SearchHit{}}

		got, err := uc.Execute(context.Background(), "nothing")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
})
