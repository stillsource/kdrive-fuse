// Package metrics is a tiny, dependency-free Prometheus text-exposition collector
// for the kdrive-fuse daemon. It is intentionally minimal (a handful of counters
// + one gauge) so it needs no external library. If the metric set grows, migrate
// to github.com/prometheus/client_golang (a Registry → prometheus.Registerer, the
// observe methods → prometheus.Counter/Gauge, Handler → promhttp.Handler).
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
)

type reqKey struct{ method, status string }

// Registry collects kdrive metrics and renders them in Prometheus text format.
// All methods are safe for concurrent use. The zero value is not usable — use New.
type Registry struct {
	mu              sync.Mutex
	requests        map[reqKey]int64
	bytesUploaded   int64
	bytesDownloaded int64
	cacheHits       int64
	cacheMisses     int64
	cacheBytes      int64 // gauge
}

func New() *Registry { return &Registry{requests: map[reqKey]int64{}} }

// ObserveRequest records one API request by HTTP method and status ("200",
// "404", or "error" for a transport failure).
func (r *Registry) ObserveRequest(method, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[reqKey{method, status}]++
}

func (r *Registry) AddBytesUploaded(n int64)   { r.mu.Lock(); r.bytesUploaded += n; r.mu.Unlock() }
func (r *Registry) AddBytesDownloaded(n int64) { r.mu.Lock(); r.bytesDownloaded += n; r.mu.Unlock() }
func (r *Registry) CacheHit()                  { r.mu.Lock(); r.cacheHits++; r.mu.Unlock() }
func (r *Registry) CacheMiss()                 { r.mu.Lock(); r.cacheMisses++; r.mu.Unlock() }
func (r *Registry) SetCacheBytes(n int64)      { r.mu.Lock(); r.cacheBytes = n; r.mu.Unlock() }

// Handler renders the current metrics in Prometheus text format at any path.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintln(w, "# HELP kdrive_api_requests_total kDrive API requests by method and status.")
		_, _ = fmt.Fprintln(w, "# TYPE kdrive_api_requests_total counter")
		keys := make([]reqKey, 0, len(r.requests))
		for k := range r.requests {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].method != keys[j].method {
				return keys[i].method < keys[j].method
			}
			return keys[i].status < keys[j].status
		})
		for _, k := range keys {
			_, _ = fmt.Fprintf(w, "kdrive_api_requests_total{method=%q,status=%q} %d\n", k.method, k.status, r.requests[k])
		}
		_, _ = fmt.Fprintln(w, "# TYPE kdrive_bytes_uploaded_total counter")
		_, _ = fmt.Fprintf(w, "kdrive_bytes_uploaded_total %d\n", r.bytesUploaded)
		_, _ = fmt.Fprintln(w, "# TYPE kdrive_bytes_downloaded_total counter")
		_, _ = fmt.Fprintf(w, "kdrive_bytes_downloaded_total %d\n", r.bytesDownloaded)
		_, _ = fmt.Fprintln(w, "# TYPE kdrive_cache_hits_total counter")
		_, _ = fmt.Fprintf(w, "kdrive_cache_hits_total %d\n", r.cacheHits)
		_, _ = fmt.Fprintln(w, "# TYPE kdrive_cache_misses_total counter")
		_, _ = fmt.Fprintf(w, "kdrive_cache_misses_total %d\n", r.cacheMisses)
		_, _ = fmt.Fprintln(w, "# TYPE kdrive_cache_bytes_on_disk gauge")
		_, _ = fmt.Fprintf(w, "kdrive_cache_bytes_on_disk %d\n", r.cacheBytes)
	})
}
