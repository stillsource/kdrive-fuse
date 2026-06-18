package cli

import (
	"bytes"
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// fakeSearchBackend is an in-memory service.Searcher for runSearch tests.
type fakeSearchBackend struct {
	results map[string]fakeSearchBackendResult
	calls   []string
}

type fakeSearchBackendResult struct {
	files []domain.FileInfo
	err   error
}

func (f *fakeSearchBackend) Search(_ context.Context, query string) ([]domain.FileInfo, error) {
	f.calls = append(f.calls, query)
	if res, ok := f.results[query]; ok {
		return res.files, res.err
	}
	return nil, nil
}

var _ service.Searcher = (*fakeSearchBackend)(nil)

// stubSearchBackend replaces searchBackend with one that returns the given fake.
func stubSearchBackend(s service.Searcher) func() {
	orig := searchBackend
	searchBackend = func(context.Context, io.Writer) (service.Searcher, error) {
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

	It("prints matching files with id, name and size", func() {
		fake.results["report"] = fakeSearchBackendResult{
			files: []domain.FileInfo{
				{ID: 42, Name: "annual-report.pdf", Size: 204800},
				{ID: 43, Name: "report-2025.docx", Size: 51200},
			},
		}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"report"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(errb.String()).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("42"))
		Expect(out.String()).To(ContainSubstring("annual-report.pdf"))
		Expect(out.String()).To(ContainSubstring("204800"))
		Expect(out.String()).To(ContainSubstring("43"))
		Expect(out.String()).To(ContainSubstring("report-2025.docx"))
	})

	It("joins multi-word args into a single query", func() {
		fake.results["hello world"] = fakeSearchBackendResult{
			files: []domain.FileInfo{{ID: 1, Name: "hello world.txt", Size: 10}},
		}
		restore := stubSearchBackend(fake)
		defer restore()

		code := runSearch([]string{"hello", "world"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(fake.calls).To(ConsistOf("hello world"))
		Expect(out.String()).To(ContainSubstring("hello world.txt"))
	})

	It("prints 'no matches' when zero results are returned", func() {
		fake.results["ghost"] = fakeSearchBackendResult{files: []domain.FileInfo{}}
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

	It("returns 1 when the backend fails", func() {
		orig := searchBackend
		searchBackend = func(context.Context, io.Writer) (service.Searcher, error) {
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
