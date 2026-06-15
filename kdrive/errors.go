package kdrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

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
	httpErr := &domain.HTTPError{StatusCode: resp.StatusCode, Body: string(snippet)}
	opts = append(opts, scerr.CausedBy(httpErr))
	return scerr.Wrap(domain.ErrServer, opts...)
}

func sentinelForStatus(status int) error {
	switch {
	case status == http.StatusNotFound:
		return domain.ErrNotFound
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return domain.ErrAuth
	case status == http.StatusConflict:
		return domain.ErrConflict
	case status == http.StatusUnprocessableEntity, status == http.StatusBadRequest:
		return domain.ErrValidation
	case status == http.StatusTooManyRequests:
		return domain.ErrRateLimit
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
