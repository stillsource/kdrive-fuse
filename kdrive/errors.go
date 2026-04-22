package kdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	scerr "github.com/scality/go-errors"
)

// Sentinel errors returned for common kDrive failure modes.
// Use errors.Is to check:
//
//	if errors.Is(err, kdrive.ErrNotFound) { ... }
var (
	ErrNotFound   = scerr.New("kdrive: not found")
	ErrAuth       = scerr.New("kdrive: authentication failed")
	ErrConflict   = scerr.New("kdrive: conflict")
	ErrValidation = scerr.New("kdrive: validation failed")
	ErrRateLimit  = scerr.New("kdrive: rate limited")
	ErrServer     = scerr.New("kdrive: server error")
)

// HTTPError wraps an HTTP response that didn't match a specific sentinel.
// Body is truncated to 512 bytes and never contains the bearer token.
type HTTPError struct {
	StatusCode int
	Body       string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("kdrive: http %d: %s", e.StatusCode, e.Body)
}

// fromResponse reads a failed response body, maps the status to a sentinel
// when possible, and wraps context using scality/go-errors.
func fromResponse(resp *http.Response, op string) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet = bytes.TrimSpace(snippet)

	sentinel := sentinelForStatus(resp.StatusCode)
	opts := []scerr.Option{
		scerr.WithIdentifier(uint32(resp.StatusCode)),
		scerr.WithDetailf("%s returned %d", op, resp.StatusCode),
		scerr.WithProperty("status_code", resp.StatusCode),
	}
	if len(snippet) > 0 {
		opts = append(opts, scerr.WithProperty("response_body", string(snippet)))
	}

	if sentinel != nil {
		return scerr.Wrap(sentinel, opts...)
	}
	httpErr := &HTTPError{StatusCode: resp.StatusCode, Body: string(snippet)}
	opts = append(opts, scerr.CausedBy(httpErr))
	return scerr.Wrap(ErrServer, opts...)
}

func sentinelForStatus(status int) error {
	switch {
	case status == http.StatusNotFound:
		return ErrNotFound
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ErrAuth
	case status == http.StatusConflict:
		return ErrConflict
	case status == http.StatusUnprocessableEntity, status == http.StatusBadRequest:
		return ErrValidation
	case status == http.StatusTooManyRequests:
		return ErrRateLimit
	case status >= 500:
		return nil // let caller see the raw HTTPError via ErrServer wrap
	}
	return nil
}

// shouldRetry reports whether a response status warrants a retry.
func shouldRetry(status int) bool {
	return status >= 500 || status == http.StatusTooManyRequests
}

// isRetryableError says whether a transport-level error (dial, TLS, etc.) is retryable.
// Context cancellation is not retryable.
func isRetryableError(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
