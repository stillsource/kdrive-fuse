package domain

import (
	"fmt"

	scerr "github.com/scality/go-errors"
)

// Sentinel errors returned for common kDrive failure modes.
// Use errors.Is to check:
//
//	if errors.Is(err, domain.ErrNotFound) { ... }
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
