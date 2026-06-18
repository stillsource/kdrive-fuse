package metrics_test

import (
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/metrics"
)

var _ = Describe("Registry", func() {
	var reg *metrics.Registry

	BeforeEach(func() {
		reg = metrics.New()
	})

	Describe("ObserveRequest", func() {
		It("counts requests by method and status", func() {
			reg.ObserveRequest("GET", "200")
			reg.ObserveRequest("GET", "200")
			reg.ObserveRequest("POST", "201")

			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			body := w.Body.String()
			Expect(body).To(ContainSubstring(`kdrive_api_requests_total{method="GET",status="200"} 2`))
			Expect(body).To(ContainSubstring(`kdrive_api_requests_total{method="POST",status="201"} 1`))
		})
	})

	Describe("AddBytesUploaded / AddBytesDownloaded", func() {
		It("accumulates byte counts", func() {
			reg.AddBytesUploaded(100)
			reg.AddBytesUploaded(50)
			reg.AddBytesDownloaded(200)

			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			body := w.Body.String()
			Expect(body).To(ContainSubstring("kdrive_bytes_uploaded_total 150"))
			Expect(body).To(ContainSubstring("kdrive_bytes_downloaded_total 200"))
		})
	})

	Describe("CacheHit / CacheMiss / SetCacheBytes", func() {
		It("tracks cache events", func() {
			reg.CacheHit()
			reg.CacheHit()
			reg.CacheMiss()
			reg.SetCacheBytes(4096)

			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			body := w.Body.String()
			Expect(body).To(ContainSubstring("kdrive_cache_hits_total 2"))
			Expect(body).To(ContainSubstring("kdrive_cache_misses_total 1"))
			Expect(body).To(ContainSubstring("kdrive_cache_bytes_on_disk 4096"))
		})
	})

	Describe("Handler", func() {
		It("sets the correct Content-Type", func() {
			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			Expect(w.Header().Get("Content-Type")).To(ContainSubstring("text/plain"))
		})

		It("includes all TYPE declarations", func() {
			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			body := w.Body.String()
			for _, line := range []string{
				"# TYPE kdrive_api_requests_total counter",
				"# TYPE kdrive_bytes_uploaded_total counter",
				"# TYPE kdrive_bytes_downloaded_total counter",
				"# TYPE kdrive_cache_hits_total counter",
				"# TYPE kdrive_cache_misses_total counter",
				"# TYPE kdrive_cache_bytes_on_disk gauge",
			} {
				Expect(body).To(ContainSubstring(line), "missing: "+line)
			}
		})

		It("sorts request labels deterministically", func() {
			reg.ObserveRequest("POST", "200")
			reg.ObserveRequest("GET", "404")
			reg.ObserveRequest("GET", "200")

			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			body := w.Body.String()
			getIdx := strings.Index(body, `method="GET"`)
			postIdx := strings.Index(body, `method="POST"`)
			Expect(getIdx).To(BeNumerically("<", postIdx))
		})
	})

	Describe("concurrency", func() {
		It("is safe for concurrent use", func() {
			const goroutines = 50
			var wg sync.WaitGroup
			wg.Add(goroutines)
			for i := 0; i < goroutines; i++ {
				go func() {
					defer wg.Done()
					reg.ObserveRequest("GET", "200")
					reg.AddBytesUploaded(1)
					reg.AddBytesDownloaded(1)
					reg.CacheHit()
					reg.CacheMiss()
					reg.SetCacheBytes(1024)
				}()
			}
			wg.Wait()
			w := httptest.NewRecorder()
			reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			Expect(w.Code).To(Equal(200))
		})
	})
})
