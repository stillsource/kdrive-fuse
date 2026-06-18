package kdriveapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("FilesService.Search", func() {
	var fx *testFixture
	var ctx context.Context

	BeforeEach(func() {
		fx = newTestFixture()
		ctx = context.Background()
		DeferCleanup(fx.Server.Close)
	})

	It("returns files matching the query", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/search", func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal("GET"))
			Expect(r.URL.Query().Get("q")).To(Equal("hello"))
			Expect(r.URL.Query().Get("per_page")).To(Equal("500"))
			writeJSON(w, 200, `{"data":[
				{"id":10,"name":"hello.txt","type":"file","size":5},
				{"id":11,"name":"hello world.pdf","type":"file","size":100}
			]}`)
		})
		files, err := fx.Client.Files.Search(ctx, "hello")
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(HaveLen(2))
		Expect(files[0].Name).To(Equal("hello.txt"))
		Expect(files[1].Name).To(Equal("hello world.pdf"))
	})

	It("URL-encodes the query parameter", func() {
		var gotQ string
		fx.Mux.HandleFunc("/2/drive/1234/files/search", func(w http.ResponseWriter, r *http.Request) {
			gotQ = r.URL.Query().Get("q")
			writeJSON(w, 200, `{"data":[]}`)
		})
		_, err := fx.Client.Files.Search(ctx, "hello world & stuff")
		Expect(err).NotTo(HaveOccurred())
		Expect(gotQ).To(Equal("hello world & stuff"))
	})

	It("pages until fewer than per_page results come back", func() {
		page := 0
		fx.Mux.HandleFunc("/2/drive/1234/files/search", func(w http.ResponseWriter, r *http.Request) {
			page++
			if page == 1 {
				var items []string
				for i := 0; i < 500; i++ {
					items = append(items, fmt.Sprintf(`{"id":%d,"name":"f%d","type":"file"}`, i, i))
				}
				writeJSON(w, 200, `{"data":[`+strings.Join(items, ",")+`]}`)
				return
			}
			writeJSON(w, 200, `{"data":[{"id":600,"name":"last","type":"file"}]}`)
		})
		files, err := fx.Client.Files.Search(ctx, "f")
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(HaveLen(501))
		Expect(files[500].Name).To(Equal("last"))
	})

	It("rejects an empty query", func() {
		_, err := fx.Client.Files.Search(ctx, "")
		Expect(errors.Is(err, domain.ErrValidation)).To(BeTrue())
	})

	It("maps 404 to ErrNotFound", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/search", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 404, `{"error":"not found"}`)
		})
		_, err := fx.Client.Files.Search(ctx, "anything")
		Expect(errors.Is(err, domain.ErrNotFound)).To(BeTrue())
	})

	It("returns an empty slice when no results match", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/search", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, `{"data":[]}`)
		})
		files, err := fx.Client.Files.Search(ctx, "noresult")
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
})
