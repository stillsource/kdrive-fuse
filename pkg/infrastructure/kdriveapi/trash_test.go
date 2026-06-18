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

var _ = Describe("FilesService trash operations", func() {
	var fx *testFixture
	var ctx context.Context

	BeforeEach(func() {
		fx = newTestFixture()
		ctx = context.Background()
		DeferCleanup(fx.Server.Close)
	})

	Describe("ListTrash", func() {
		It("returns all trashed items", func() {
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal("GET"))
				Expect(r.URL.Query().Get("per_page")).To(Equal("500"))
				writeJSON(w, 200, `{"data":[
					{"id":10,"name":"deleted.txt","type":"file","size":42},
					{"id":11,"name":"old-dir","type":"dir","size":0}
				]}`)
			})
			items, err := fx.Client.Files.ListTrash(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(2))
			Expect(items[0].ID).To(Equal(int64(10)))
			Expect(items[0].Name).To(Equal("deleted.txt"))
			Expect(items[1].IsDir()).To(BeTrue())
		})

		It("pages until fewer than per_page results come back", func() {
			page := 0
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				page++
				if page == 1 {
					var items []string
					for i := 0; i < 500; i++ {
						items = append(items, fmt.Sprintf(`{"id":%d,"name":"f%d","type":"file"}`, i+1, i+1))
					}
					writeJSON(w, 200, `{"data":[`+strings.Join(items, ",")+`]}`)
					return
				}
				writeJSON(w, 200, `{"data":[{"id":600,"name":"last","type":"file"}]}`)
			})
			items, err := fx.Client.Files.ListTrash(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(501))
			Expect(items[500].Name).To(Equal("last"))
		})

		It("returns an empty slice when trash is empty", func() {
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"data":[]}`)
			})
			items, err := fx.Client.Files.ListTrash(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(BeEmpty())
		})

		It("maps 4xx to an appropriate sentinel", func() {
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 401, `{"error":"unauthorized"}`)
			})
			_, err := fx.Client.Files.ListTrash(ctx)
			Expect(errors.Is(err, domain.ErrAuth)).To(BeTrue())
		})

		It("sends the page query parameter on each page", func() {
			var pages []string
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				pages = append(pages, r.URL.Query().Get("page"))
				writeJSON(w, 200, `{"data":[]}`)
			})
			_, err := fx.Client.Files.ListTrash(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(pages).To(Equal([]string{"1"}))
		})
	})

	Describe("RestoreTrash", func() {
		It("posts to /trash/{id}/restore and succeeds on 2xx", func() {
			called := false
			fx.Mux.HandleFunc("/2/drive/1234/trash/42/restore", func(w http.ResponseWriter, r *http.Request) {
				called = true
				Expect(r.Method).To(Equal("POST"))
				writeJSON(w, 200, `{"data":{}}`)
			})
			Expect(fx.Client.Files.RestoreTrash(ctx, 42)).To(Succeed())
			Expect(called).To(BeTrue())
		})

		It("rejects invalid (zero) file id", func() {
			Expect(errors.Is(fx.Client.Files.RestoreTrash(ctx, 0), domain.ErrValidation)).To(BeTrue())
		})

		It("rejects negative file id", func() {
			Expect(errors.Is(fx.Client.Files.RestoreTrash(ctx, -1), domain.ErrValidation)).To(BeTrue())
		})

		It("maps 404 to ErrNotFound", func() {
			fx.Mux.HandleFunc("/2/drive/1234/trash/99/restore", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 404, `{"error":"not found"}`)
			})
			Expect(errors.Is(fx.Client.Files.RestoreTrash(ctx, 99), domain.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("PurgeTrash", func() {
		It("sends DELETE to /trash/{id} and succeeds on 2xx", func() {
			called := false
			fx.Mux.HandleFunc("/2/drive/1234/trash/55", func(w http.ResponseWriter, r *http.Request) {
				called = true
				Expect(r.Method).To(Equal("DELETE"))
				writeJSON(w, 200, `{"data":{}}`)
			})
			Expect(fx.Client.Files.PurgeTrash(ctx, 55)).To(Succeed())
			Expect(called).To(BeTrue())
		})

		It("rejects invalid (zero) file id", func() {
			Expect(errors.Is(fx.Client.Files.PurgeTrash(ctx, 0), domain.ErrValidation)).To(BeTrue())
		})

		It("maps 404 to ErrNotFound", func() {
			fx.Mux.HandleFunc("/2/drive/1234/trash/77", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 404, `{"error":"not found"}`)
			})
			Expect(errors.Is(fx.Client.Files.PurgeTrash(ctx, 77), domain.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("EmptyTrash", func() {
		It("sends DELETE to /trash and succeeds on 2xx", func() {
			called := false
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				called = true
				Expect(r.Method).To(Equal("DELETE"))
				writeJSON(w, 200, `{"data":{}}`)
			})
			Expect(fx.Client.Files.EmptyTrash(ctx)).To(Succeed())
			Expect(called).To(BeTrue())
		})

		It("maps 5xx to ErrServer", func() {
			fx.Mux.HandleFunc("/2/drive/1234/trash", func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					writeJSON(w, 500, `{"error":"server error"}`)
				} else {
					writeJSON(w, 200, `{"data":[]}`)
				}
			})
			Expect(errors.Is(fx.Client.Files.EmptyTrash(ctx), domain.ErrServer)).To(BeTrue())
		})
	})
})
