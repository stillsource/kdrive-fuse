package hash

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/zeebo/xxh3"
)

func TestChunkHasher(t *testing.T) {
	chunks := [][]byte{[]byte("alpha"), []byte("bravo"), []byte("charlie")}

	// Independent expectation: xxh3-64 over the concatenation of each chunk's
	// 16-hex hash string (no "xxh3:" prefix), in order.
	var concat strings.Builder
	h := NewChunkHasher()
	for _, c := range chunks {
		hs, err := XXH3Stream(bytes.NewReader(c))
		if err != nil {
			t.Fatalf("XXH3Stream: %v", err)
		}
		h.Add(hs)
		concat.WriteString(strings.TrimPrefix(hs, "xxh3:"))
	}
	exp := xxh3.New()
	_, _ = exp.Write([]byte(concat.String()))
	want := fmt.Sprintf("xxh3:%016x", exp.Sum64())

	if got := h.Sum(); got != want {
		t.Fatalf("ChunkHasher.Sum() = %q, want %q", got, want)
	}
}

func TestChunkHasherEmpty(t *testing.T) {
	// No chunks added: still a valid xxh3 of the empty input, prefixed.
	exp := xxh3.New()
	want := fmt.Sprintf("xxh3:%016x", exp.Sum64())
	if got := NewChunkHasher().Sum(); got != want {
		t.Fatalf("empty ChunkHasher.Sum() = %q, want %q", got, want)
	}
}
