package kdriveapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// fakeSink is a minimal metricsSink for tests.
type fakeSink struct {
	requests   []struct{ method, status string }
	uploaded   int64
	downloaded int64
}

func (f *fakeSink) ObserveRequest(method, status string) {
	f.requests = append(f.requests, struct{ method, status string }{method, status})
}

func (f *fakeSink) AddBytesUploaded(n int64)   { atomic.AddInt64(&f.uploaded, n) }
func (f *fakeSink) AddBytesDownloaded(n int64) { atomic.AddInt64(&f.downloaded, n) }

var _ = Describe("Client metrics", func() {
	var fx *testFixture
	var sink *fakeSink
	var ctx context.Context

	BeforeEach(func() {
		sink = &fakeSink{}
		fx = newTestFixture(WithMetrics(sink))
		ctx = context.Background()
		DeferCleanup(fx.Server.Close)
	})

	It("records a successful GET request", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/1/files", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, `{"data":[]}`)
		})
		_, err := fx.Client.Files.List(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(sink.requests).To(HaveLen(1))
		Expect(sink.requests[0].method).To(Equal("GET"))
		Expect(sink.requests[0].status).To(Equal("200"))
	})

	It("records a 404 response", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/99/files", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 404, `{"error":"not found"}`)
		})
		_, _ = fx.Client.Files.List(ctx, 99)
		Expect(sink.requests).To(HaveLen(1))
		Expect(sink.requests[0].status).To(Equal("404"))
	})

	It("records bytes uploaded on successful upload", func() {
		fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
			_ = readBody(r)
			writeJSON(w, 200, `{"data":{"id":1,"name":"x.txt","type":"file","size":5}}`)
		})
		body := []byte("hello")
		_, err := fx.Client.Files.Upload(ctx, service.UploadInput{
			ParentID: 1, Name: "x.txt",
			Body: bytes.NewReader(body), Size: int64(len(body)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(atomic.LoadInt64(&sink.uploaded)).To(Equal(int64(5)))
	})

	It("counts bytes as the download stream is read", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/7/download", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello world"))
		})
		rc, err := fx.Client.Files.DownloadStream(ctx, 7, 0, 0)
		Expect(err).NotTo(HaveOccurred())
		defer rc.Close() //nolint:errcheck
		Expect(atomic.LoadInt64(&sink.downloaded)).To(Equal(int64(0)))
		data, err := io.ReadAll(rc)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello world"))
		Expect(atomic.LoadInt64(&sink.downloaded)).To(Equal(int64(11)))
	})

	It("does not panic when metrics is nil (default)", func() {
		fxNoMetrics := newTestFixture()
		DeferCleanup(fxNoMetrics.Server.Close)
		fxNoMetrics.Mux.HandleFunc("/2/drive/1234/files/7/download", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("data"))
		})
		rc, err := fxNoMetrics.Client.Files.DownloadStream(ctx, 7, 0, 0)
		Expect(err).NotTo(HaveOccurred())
		defer rc.Close() //nolint:errcheck
		_, _ = io.ReadAll(rc)
	})
})
