// Package hash wraps xxh3 with the kDrive-specific formatting expected
// by the upload endpoint: "xxh3:" + lowercase 16-hex of the 64-bit sum.
package hash

import (
	"fmt"
	"io"

	"github.com/zeebo/xxh3"
)

// XXH3Stream computes the xxh3-64 hash of r and returns the kDrive-formatted string.
func XXH3Stream(r io.Reader) (string, error) {
	h := xxh3.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("xxh3 hash: %w", err)
	}
	return fmt.Sprintf("xxh3:%016x", h.Sum64()), nil
}
