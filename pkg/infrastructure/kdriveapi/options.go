package kdriveapi

import (
	"log/slog"
	"net/http"
	"time"
)

// Option configures a Client at construction time.
type Option func(*Client)

// metricsSink is the (optional) metrics surface the client reports to.
// *metrics.Registry satisfies it; nil disables reporting.
type metricsSink interface {
	ObserveRequest(method, status string)
	AddBytesUploaded(n int64)
	AddBytesDownloaded(n int64)
}

// WithMetrics reports request counts and transfer bytes to m. nil disables it.
func WithMetrics(m metricsSink) Option {
	return func(k *Client) {
		if m != nil {
			k.metrics = m
		}
	}
}

// WithHTTPClient injects a custom HTTP client (for custom transport, timeouts, etc.).
// It is used for BOTH reads and uploads, so a test double or custom transport
// applies everywhere. Default read client: &http.Client{Timeout: 60s};
// default upload client: &http.Client{Timeout: 2m}. Use WithUploadTimeout to tune
// only the upload timeout.
func WithHTTPClient(c *http.Client) Option {
	return func(k *Client) {
		if c != nil {
			k.http = c
			k.uploadHTTP = c
		}
	}
}

// WithUploadTimeout sets the timeout of the dedicated upload HTTP client while
// keeping its tuned transport (HTTP/1.1 multi-conn). Uploads of large files (or
// slow/degraded kDrive responses) need more headroom than the 60s read timeout.
// Default: 2 minutes.
func WithUploadTimeout(d time.Duration) Option {
	return func(k *Client) {
		if d > 0 {
			k.uploadHTTP = &http.Client{
				Timeout:   d,
				Transport: k.uploadHTTP.Transport,
			}
		}
	}
}

// WithBaseURL overrides the base URL for list/stat/download/rename/mkdir/delete operations.
// Default: "https://api.infomaniak.com/2/drive".
func WithBaseURL(u string) Option {
	return func(k *Client) {
		if u != "" {
			k.baseURL = u
		}
	}
}

// WithUploadBaseURL overrides the base URL for binary uploads.
// Default: "https://api.kdrive.infomaniak.com/2/drive".
// Must differ from WithBaseURL in production; kDrive routes uploads to a different host.
func WithUploadBaseURL(u string) Option {
	return func(k *Client) {
		if u != "" {
			k.uploadBaseURL = u
		}
	}
}

// WithLogger sets the slog logger used for request diagnostics.
// Default: slog.Default(). Set a discard logger to silence.
func WithLogger(l *slog.Logger) Option {
	return func(k *Client) {
		if l != nil {
			k.log = l
		}
	}
}

// WithRetries configures retry behavior for transient failures (5xx, 429).
// max is the number of retry attempts after the initial request.
// initial is the first backoff duration; each retry doubles it.
// Default: 3 retries starting at 1 second.
func WithRetries(max int, initial time.Duration) Option {
	return func(k *Client) {
		if max >= 0 {
			k.maxRetries = max
		}
		if initial > 0 {
			k.initialBackoff = initial
		}
	}
}

// WithUploadParallelism sets the number of concurrent chunk uploads when the
// upload body implements io.ReaderAt. A value <= 0 keeps the default (4).
// More goroutines open more TCP connections and aggregate more bandwidth at the
// cost of proportionally more memory (parallelism × chunkSize per upload).
func WithUploadParallelism(n int) Option {
	return func(k *Client) {
		if n > 0 {
			k.uploadParallelism = n
		}
	}
}
