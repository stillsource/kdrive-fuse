package hash

import (
	"bytes"
	"strings"
	"testing"
)

func TestXXH3Stream_format(t *testing.T) {
	got, err := XXH3Stream(bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "xxh3:") {
		t.Fatalf("missing prefix: %q", got)
	}
	if len(got) != len("xxh3:")+16 {
		t.Fatalf("unexpected hash length: %d in %q", len(got), got)
	}
}

func TestXXH3Stream_deterministic(t *testing.T) {
	a, _ := XXH3Stream(bytes.NewReader([]byte("deterministic")))
	b, _ := XXH3Stream(bytes.NewReader([]byte("deterministic")))
	if a != b {
		t.Fatalf("hash not deterministic: %s vs %s", a, b)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errStub }

var errStub = &readErr{}

type readErr struct{}

func (*readErr) Error() string { return "boom" }

func TestXXH3Stream_readError(t *testing.T) {
	_, err := XXH3Stream(errReader{})
	if err == nil {
		t.Fatal("expected error")
	}
}
