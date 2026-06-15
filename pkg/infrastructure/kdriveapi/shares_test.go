package kdriveapi

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("SharesService.Publish", func() {
	var fx *testFixture
	var ctx context.Context

	BeforeEach(func() {
		fx = newTestFixture()
		ctx = context.Background()
		DeferCleanup(fx.Server.Close)
	})

	It("returns existing share when one already exists", func() {
		var postCalled atomic.Bool
		fx.Mux.HandleFunc("/2/drive/1234/files/5/shares", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				writeJSON(w, 200, `{"data":[{"id":1,"share_url":"https://kdrive.io/s/abc"}]}`)
			case "POST":
				postCalled.Store(true)
				writeJSON(w, 200, `{"data":{"share_url":"https://kdrive.io/s/new"}}`)
			}
		})
		info, err := fx.Client.Shares.Publish(ctx, 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ShareURL).To(Equal("https://kdrive.io/s/abc"))
		Expect(postCalled.Load()).To(BeFalse())
	})

	It("creates a new share when none exists", func() {
		var postCalled atomic.Bool
		fx.Mux.HandleFunc("/2/drive/1234/files/5/shares", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				writeJSON(w, 200, `{"data":[]}`)
			case "POST":
				postCalled.Store(true)
				writeJSON(w, 200, `{"data":{"share_url":"https://kdrive.io/s/fresh"}}`)
			}
		})
		info, err := fx.Client.Shares.Publish(ctx, 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ShareURL).To(Equal("https://kdrive.io/s/fresh"))
		Expect(postCalled.Load()).To(BeTrue())
	})

	It("creates when GET fails (falls back to POST)", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/5/shares", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				writeJSON(w, 500, `{"error":"transient"}`)
				return
			}
			writeJSON(w, 200, `{"data":{"share_url":"https://kdrive.io/s/after-err"}}`)
		})
		info, err := fx.Client.Shares.Publish(ctx, 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ShareURL).To(Equal("https://kdrive.io/s/after-err"))
	})

	It("fails if POST returns empty url", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/5/shares", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				writeJSON(w, 200, `{"data":[]}`)
				return
			}
			writeJSON(w, 200, `{"data":{}}`)
		})
		_, err := fx.Client.Shares.Publish(ctx, 5)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, domain.ErrServer)).To(BeTrue())
	})

	It("rejects invalid file id", func() {
		_, err := fx.Client.Shares.Publish(ctx, 0)
		Expect(errors.Is(err, domain.ErrValidation)).To(BeTrue())
	})
})
