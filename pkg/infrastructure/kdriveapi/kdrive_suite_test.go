package kdriveapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testToken   = "test-token-abc123"
	testDriveID = "1234"
)

func TestKdrive(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "kdrive Suite")
}

// recordingHandler captures slog records so tests can assert on them (e.g. no token leak).
type recordingHandler struct {
	mu      sync.Mutex
	records []loggedRecord
}

type loggedRecord struct {
	msg   string
	attrs []slog.Attr
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec := loggedRecord{msg: r.Message}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs = append(rec.attrs, a)
		return true
	})
	h.records = append(h.records, rec)
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) all() []loggedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]loggedRecord(nil), h.records...)
}

// containsSubstring reports whether any record text or attribute value contains s.
func containsSubstring(records []loggedRecord, s string) bool {
	for _, r := range records {
		var buf bytes.Buffer
		buf.WriteString(r.msg)
		for _, a := range r.attrs {
			buf.WriteString(" ")
			buf.WriteString(a.Key)
			buf.WriteString("=")
			buf.WriteString(a.Value.String())
		}
		if bytes.Contains(buf.Bytes(), []byte(s)) {
			return true
		}
	}
	return false
}

// testFixture bundles the httptest-backed Client, mux and log recorder.
type testFixture struct {
	Client *Client
	Server *httptest.Server
	Mux    *http.ServeMux
	Logs   *recordingHandler
}

// newTestFixture builds a fresh fixture. DeferCleanup(fx.Server.Close) from the caller.
func newTestFixture(opts ...Option) *testFixture {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	logs := &recordingHandler{}
	allOpts := append([]Option{
		WithBaseURL(srv.URL + "/2/drive"),
		WithUploadBaseURL(srv.URL + "/2/drive"),
		WithLogger(slog.New(logs)),
		WithRetries(2, 5*time.Millisecond),
	}, opts...)
	return &testFixture{
		Client: New(testToken, testDriveID, allOpts...),
		Server: srv,
		Mux:    mux,
		Logs:   logs,
	}
}

// roundTripFunc adapts a function to http.RoundTripper for transport-level test doubles.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// writeJSON helper for handlers.
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// readBody drains r.Body.
func readBody(r *http.Request) []byte {
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return b
}

// fmtJSON is used in test expectations.
var _ = fmt.Sprintf
