package cli

import (
	"bytes"
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// fakeSearchBackend is an in-memory service.Searcher for runSearch tests.
type fakeSearchBackend struct {
	results map[string]fakeSearchBackendResult
	calls   []string
}

type fakeSearchBackendResult struct {
	hits []service.SearchHit
	err  error
}

func (f *fakeSearchBackend) Search(_ context.Context, query string) ([]service.SearchHit, error) {
	f.calls = append(f.calls, query)
	if res, ok := f.results[query]; ok {
		return res.hits, res.err
	}
	return nil, nil
}

var _ service.Searcher = (*fakeSearchBackend)(nil)

// lastSearchPath records the pathArg the stubbed backend was called with.
var lastSearchPath string

// stubSearchBackend replaces searchBackend with one that returns the given fake
// and records the pathArg it was called with.
func stubSearchBackend(s service.Searcher) func() {
	orig := searchBackend
	lastSearchPath = ""
	searchBackend = func(_ context.Context, pathArg string, _ io.Writer) (service.Searcher, error) {
		lastSearchPath = pathArg
		return s, nil
	}
	return func() { searchBackend = orig }
}

var _ = Describe("runSearch with a fake backend", func() {
	var (
		fake *fakeSearchBackend
		out  *bytes.Buffer
		errb *bytes.Buffer
	)

	BeforeEach(func() {
		fake = &fakeSearchBackend{results: map[string]fakeSearchBackendResult{}}
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints matching files with id, path and size", func() {
		fake.results["report"] = fakeSearchBackendResult{
			hits: []service.SearchHit{
				{ID: 42, Path: "2025/annual-report.pdf", Size: 204800},
				{ID: 43, Path: "docs/report-2025.docx", Size: 51200},
			},
		}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"report"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(errb.String()).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("42"))
		Expect(out.String()).To(ContainSubstring("2025/annual-report.pdf"))
		Expect(out.String()).To(ContainSubstring("204800"))
		Expect(out.String()).To(ContainSubstring("43"))
		Expect(out.String()).To(ContainSubstring("docs/report-2025.docx"))
	})

	It("joins multi-word args into a single query", func() {
		fake.results["hello world"] = fakeSearchBackendResult{
			hits: []service.SearchHit{{ID: 1, Path: "notes/hello world.txt", Size: 10}},
		}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"hello", "world"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(fake.calls).To(ConsistOf("hello world"))
		Expect(out.String()).To(ContainSubstring("notes/hello world.txt"))
	})

	It("prints 'no matches' when zero results are returned", func() {
		fake.results["ghost"] = fakeSearchBackendResult{hits: []service.SearchHit{}}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"ghost"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("no matches"))
		Expect(errb.String()).To(BeEmpty())
	})

	It("returns 1 and prints error when the searcher fails", func() {
		fake.results["bad"] = fakeSearchBackendResult{err: errors.New("api down")}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"bad"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("api down"))
	})

	It("strips --path from the query and passes it to the backend", func() {
		fake.results["report"] = fakeSearchBackendResult{
			hits: []service.SearchHit{{ID: 7, Path: "Docs/q3/report.pdf", Size: 1}},
		}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"--path", "Docs/q3", "report"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(lastSearchPath).To(Equal("Docs/q3")) // flag consumed, passed to backend
		Expect(fake.calls).To(ConsistOf("report"))  // query excludes the flag
	})

	It("returns 1 when the backend fails", func() {
		orig := searchBackend
		searchBackend = func(context.Context, string, io.Writer) (service.Searcher, error) {
			return nil, errors.New("no credentials")
		}
		defer func() { searchBackend = orig }()

		code := runSearch([]string{"anything"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("no credentials"))
	})
})

var _ = Describe("runSearch flag/usage handling", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints search usage and exits 0 on --help", func() {
		code := runSearch([]string{"--help"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive search"))
	})

	It("prints search usage and exits 0 on -h", func() {
		code := runSearch([]string{"-h"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive search"))
	})

	It("exits 2 with usage when no query is given", func() {
		code := runSearch([]string{}, out, errb)
		Expect(code).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("kdrive search"))
	})
})

var _ = Describe("Run dispatches to search", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("routes 'search --help' to runSearch and exits 0", func() {
		code := Run([]string{"search", "--help"}, "dev", out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive search"))
	})

	It("routes 'search' with no args to runSearch and exits 2", func() {
		code := Run([]string{"search"}, "dev", out, errb)
		Expect(code).To(Equal(2))
	})
})
