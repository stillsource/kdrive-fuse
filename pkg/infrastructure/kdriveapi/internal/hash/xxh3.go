// Package hash wraps xxh3 with the kDrive-specific formatting expected
// by the upload endpoint: "xxh3:" + lowercase 16-hex of the 64-bit sum.
package hash

import (
	"fmt"
	"io"
	"strings"

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

// ChunkHasher accumulates per-chunk hashes into a kDrive upload-session
// total_chunk_hash. That value is NOT a content hash of the file: it is
// xxh3-64 over the concatenation of the per-chunk 16-hex hash strings (without
// the "xxh3:" prefix), fed in chunk order.
type ChunkHasher struct {
	h *xxh3.Hasher
}

// NewChunkHasher returns an empty accumulator.
func NewChunkHasher() *ChunkHasher {
	return &ChunkHasher{h: xxh3.New()}
}

// Add feeds one chunk's hash. It accepts the "xxh3:<hex>" wire form (the prefix
// is stripped) or a bare 16-hex string.
func (c *ChunkHasher) Add(chunkHash string) {
	_, _ = c.h.Write([]byte(strings.TrimPrefix(chunkHash, "xxh3:")))
}

// Sum returns the kDrive-formatted total_chunk_hash over all added chunks.
func (c *ChunkHasher) Sum() string {
	return fmt.Sprintf("xxh3:%016x", c.h.Sum64())
}
