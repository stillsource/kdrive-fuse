package kdriveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

func (c *Client) observe(method, status string) {
	if c.metrics != nil {
		c.metrics.ObserveRequest(method, status)
	}
}

// do executes an authenticated request with retry on transient failures.
// Body must be nil or a rewindable []byte (repeatable for retries).
// Caller must close the returned response's Body.
func (c *Client) do(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error) {
	return c.doRaw(ctx, method, c.baseURL+"/"+c.driveID+endpoint, "application/json", body, nil)
}

// doRaw is the transport core. contentType is set only when body != nil.
// extraHeaders takes precedence over defaults.
func (c *Client) doRaw(ctx context.Context, method, url, contentType string, body []byte, extraHeaders map[string]string) (*http.Response, error) {
	var lastErr error
	backoff := c.initialBackoff

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
		}

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, scerr.Wrap(domain.ErrValidation, scerr.WithDetailf("build request: %v", err))
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		if body != nil && contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryableError(err) {
				c.observe(method, "error")
				return nil, scerr.Wrap(domain.ErrServer,
					scerr.WithDetail("transport error"),
					scerr.CausedBy(err),
				)
			}
			c.log.Warn("kdrive request failed",
				slog.String("method", method),
				slog.Int("attempt", attempt+1),
				slog.String("err", err.Error()),
			)
			continue
		}

		if shouldRetry(resp.StatusCode) && attempt < c.maxRetries {
			drainAndClose(resp.Body)
			lastErr = fmt.Errorf("transient %d", resp.StatusCode)
			c.log.Warn("kdrive transient status",
				slog.Int("status", resp.StatusCode),
				slog.Int("attempt", attempt+1),
			)
			continue
		}

		if resp.StatusCode >= 400 {
			c.observe(method, strconv.Itoa(resp.StatusCode))
			apiErr := fromResponse(resp, method+" "+endpointOf(url))
			drainAndClose(resp.Body)
			return nil, apiErr
		}
		c.observe(method, strconv.Itoa(resp.StatusCode))
		return resp, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("kdrive: retries exhausted")
	}
	c.observe(method, "error")
	return nil, scerr.Wrap(domain.ErrServer,
		scerr.WithDetail("retries exhausted"),
		scerr.CausedBy(lastErr),
	)
}

// decodeJSON runs do() and unmarshals a JSON response.
func (c *Client) decodeJSON(ctx context.Context, method, endpoint string, body []byte, out any) error {
	resp, err := c.do(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // cleanup only
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return scerr.Wrap(domain.ErrServer,
			scerr.WithDetail("decode response"),
			scerr.CausedBy(err),
		)
	}
	return nil
}

// endpointOf extracts the path component from a full URL (best effort, for logging).
// Token-bearing URLs (none in this client) would be stripped by this caller anyway.
func endpointOf(rawURL string) string {
	// crude: find "://host" and return after, else return as-is
	const sep = "://"
	i := indexOf(rawURL, sep)
	if i < 0 {
		return rawURL
	}
	rest := rawURL[i+len(sep):]
	j := indexOf(rest, "/")
	if j < 0 {
		return "/"
	}
	return rest[j:]
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
