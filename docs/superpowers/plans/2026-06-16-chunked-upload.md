# Chunked Upload (> 100 MB) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upload files larger than 100 MB through the kDrive upload-session (chunked) flow, transparently behind the existing `FilesService.Upload`, with per-chunk transient retry — without changing any other layer or the single-shot path.

**Architecture:** Infrastructure-adapter-only feature. A new `upload_session.go` in `package kdriveapi` implements `start → chunk×N → finish` (with `cancel` on failure) against the upload host. `FilesService.Upload` gains a one-line size dispatch. The trickiest detail — kDrive's `total_chunk_hash` is a *hash of the per-chunk hex hashes* — is isolated in a `ChunkHasher` helper in the existing `internal/hash` package and unit-tested first.

**Tech Stack:** Go, `github.com/zeebo/xxh3`, `scality/go-errors`, Ginkgo v2 + Gomega, `httptest`.

**Branch:** `feat/chunked-upload` (already exists, with the design spec committed).

**Spec:** `docs/superpowers/specs/2026-06-15-chunked-upload-design.md`.

---

## Conventions (read first)

- Commit messages: English, Conventional Commits. **NO `Co-Authored-By` trailer, no AI attribution.**
- TDD: write the failing test, watch it fail, implement, watch it pass, commit.
- After each task: `go build ./... && go test ./... -count=1` green; `gofmt -l .` clean; `go vet ./...` clean.
- The single-shot path in `files.go` `Upload` (lines 113–247) must stay byte-for-byte unchanged except the one inserted dispatch block — do NOT refactor its retry loop.
- Editor/LSP diagnostics can show stale mid-edit errors; trust `go build`/`go test`.

## File structure

- **Modify** `pkg/infrastructure/kdriveapi/internal/hash/xxh3.go` — add `ChunkHasher` (hash-of-hashes accumulator).
- **Create** `pkg/infrastructure/kdriveapi/internal/hash/chunkhasher_test.go` — unit test for `ChunkHasher`.
- **Create** `pkg/infrastructure/kdriveapi/upload_session.go` — `uploadSession` + `sessionStart`/`sessionChunk`/`sessionFinish`/`sessionCancel`/`doSessionAttempt`/`sessionURL` + the `uploadSessionThreshold`/`chunkSize` vars.
- **Create** `pkg/infrastructure/kdriveapi/upload_session_test.go` — httptest-backed flow tests.
- **Modify** `pkg/infrastructure/kdriveapi/files.go` — insert the size dispatch in `Upload`.
- **Modify** `pkg/service/upload_input.go` — widen the `UploadInput` doc comment.
- **Modify** `CLAUDE.md`, `ROADMAP.md` — reflect chunked upload as shipped.

---

## Task 1: `ChunkHasher` (the hash-of-hashes)

kDrive's `total_chunk_hash` is xxh3-64 over the concatenation of the per-chunk **16-hex strings** (the `xxh3:` prefix stripped), in chunk order. Isolate and test it first.

**Files:**
- Modify: `pkg/infrastructure/kdriveapi/internal/hash/xxh3.go`
- Create: `pkg/infrastructure/kdriveapi/internal/hash/chunkhasher_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/infrastructure/kdriveapi/internal/hash/chunkhasher_test.go`:

```go
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
```

- [ ] **Step 2: Run the test — expect failure (undefined)**

Run: `go test ./pkg/infrastructure/kdriveapi/internal/hash/ -run TestChunkHasher -count=1`
Expected: compile error / FAIL — `undefined: NewChunkHasher`.

- [ ] **Step 3: Implement `ChunkHasher`**

Edit `pkg/infrastructure/kdriveapi/internal/hash/xxh3.go`. Add `"strings"` to the imports and append:

```go
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
```

The import block becomes:

```go
import (
	"fmt"
	"io"
	"strings"

	"github.com/zeebo/xxh3"
)
```

- [ ] **Step 4: Run the test — expect pass**

Run: `go test ./pkg/infrastructure/kdriveapi/internal/hash/ -count=1`
Expected: PASS (both new tests + any existing ones).

- [ ] **Step 5: Commit**

```bash
git add pkg/infrastructure/kdriveapi/internal/hash/
git commit -m "feat(kdriveapi): add ChunkHasher for upload-session total_chunk_hash"
```

---

## Task 2: `uploadSession` core + size dispatch

**Files:**
- Create: `pkg/infrastructure/kdriveapi/upload_session.go`
- Create: `pkg/infrastructure/kdriveapi/upload_session_test.go`
- Modify: `pkg/infrastructure/kdriveapi/files.go` (one dispatch block in `Upload`)

- [ ] **Step 1: Write the failing happy-path test (create mode)**

Create `pkg/infrastructure/kdriveapi/upload_session_test.go`:

```go
package kdriveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi/internal/hash"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// withSmallChunks shrinks the package-level session vars for a spec and restores
// them afterwards, so tests exercise multi-chunk flows without huge allocations.
func withSmallChunks(threshold, chunk int64) {
	origT, origC := uploadSessionThreshold, chunkSize
	uploadSessionThreshold = threshold
	chunkSize = chunk
	DeferCleanup(func() {
		uploadSessionThreshold = origT
		chunkSize = origC
	})
}

var _ = Describe("FilesService.Upload — chunked session", func() {
	It("uploads a large file via start -> chunks -> finish (create mode)", func() {
		withSmallChunks(4, 4) // threshold 4 bytes, 4-byte chunks

		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		content := []byte("0123456789") // 10 bytes -> chunks of 4,4,2 = 3 chunks

		var mu sync.Mutex
		var startBody map[string]any
		var finishBody map[string]any
		chunks := map[int64][]byte{}
		chunkParams := map[int64]map[string]string{}

		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.Unmarshal(readBody(r), &startBody)
			writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			n, _ := strconv.ParseInt(r.URL.Query().Get("chunk_number"), 10, 64)
			chunks[n] = readBody(r)
			chunkParams[n] = map[string]string{
				"chunk_size": r.URL.Query().Get("chunk_size"),
				"chunk_hash": r.URL.Query().Get("chunk_hash"),
			}
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.Unmarshal(readBody(r), &finishBody)
			writeJSON(w, http.StatusOK, `{"data":{"id":99,"name":"big.bin","size":10,"type":"file"}}`)
		})

		info, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7,
			Name:     "big.bin",
			Body:     bytes.NewReader(content),
			Size:     int64(len(content)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ID).To(Equal(int64(99)))

		// start body (create mode)
		Expect(startBody["file_name"]).To(Equal("big.bin"))
		Expect(startBody["directory_id"]).To(Equal("7"))
		Expect(startBody["conflict"]).To(Equal("error"))
		Expect(startBody["total_size"]).To(Equal("10"))
		Expect(startBody["total_chunks"]).To(Equal("3"))

		// reassembled chunk bodies == original, in order
		Expect(chunks).To(HaveLen(3))
		got := append(append(append([]byte{}, chunks[1]...), chunks[2]...), chunks[3]...)
		Expect(got).To(Equal(content))
		Expect(chunkParams[1]["chunk_size"]).To(Equal("4"))
		Expect(chunkParams[3]["chunk_size"]).To(Equal("2"))

		// per-chunk hash matches xxh3 of that chunk
		expH1, _ := hash.XXH3Stream(bytes.NewReader(chunks[1]))
		Expect(chunkParams[1]["chunk_hash"]).To(Equal(expH1))

		// finish total_chunk_hash == ChunkHasher over the 3 chunks, computed independently
		h := hash.NewChunkHasher()
		for _, n := range []int64{1, 2, 3} {
			hs, _ := hash.XXH3Stream(bytes.NewReader(chunks[n]))
			h.Add(hs)
		}
		Expect(finishBody["total_chunk_hash"]).To(Equal(h.Sum()))
		Expect(finishBody["total_chunks"]).To(BeEquivalentTo(3)) // JSON number
	})
})
```

Add `"strconv"` to the import block (used by the handlers).

- [ ] **Step 2: Run the test — expect failure**

Run: `go test ./pkg/infrastructure/kdriveapi/ -run TestKdrive -count=1`
Expected: FAIL — `uploadSessionThreshold`/`chunkSize`/`uploadSession` undefined (and `Upload` doesn't dispatch).

- [ ] **Step 3: Create `upload_session.go`**

Create `pkg/infrastructure/kdriveapi/upload_session.go`:

```go
package kdriveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi/internal/hash"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// uploadSessionThreshold is the size ABOVE which Upload switches to the chunked
// upload-session flow (strictly greater: a file of exactly this size stays
// single-shot, matching the kDrive client's bigFileThreshold dispatch).
// chunkSize is the fixed per-chunk size, within the server-accepted 10–100 MB.
// Both are vars (not const) so tests can shrink them without huge allocations.
var (
	uploadSessionThreshold int64 = 100 * 1024 * 1024
	chunkSize              int64 = 50 * 1024 * 1024
)

// uploadSession uploads in.Body via the kDrive upload-session flow:
// POST /upload/session/start -> POST /upload/session/{token}/chunk * N ->
// POST /upload/session/{token}/finish. On any unrecoverable failure it
// best-effort cancels the session (DELETE /upload/session/{token}). The body is
// read sequentially one chunk at a time into memory (<= chunkSize), so per-chunk
// retries replay from memory and only one chunk is resident at a time.
func (s *FilesService) uploadSession(ctx context.Context, in service.UploadInput) (domain.FileInfo, error) {
	if _, err := in.Body.Seek(0, io.SeekStart); err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
			scerr.WithDetail("upload session: seek to start"), scerr.CausedBy(err))
	}
	totalChunks := (in.Size + chunkSize - 1) / chunkSize
	if totalChunks == 0 {
		totalChunks = 1
	}

	token, err := s.sessionStart(ctx, in, totalChunks)
	if err != nil {
		return domain.FileInfo{}, err
	}

	hasher := hash.NewChunkHasher()
	remaining := in.Size
	for n := int64(1); n <= totalChunks; n++ {
		clen := chunkSize
		if remaining < clen {
			clen = remaining
		}
		buf := make([]byte, clen)
		if _, err := io.ReadFull(in.Body, buf); err != nil {
			s.sessionCancel(ctx, token)
			return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
				scerr.WithDetailf("upload session: read chunk %d", n), scerr.CausedBy(err))
		}
		chunkHash, err := hash.XXH3Stream(bytes.NewReader(buf))
		if err != nil {
			s.sessionCancel(ctx, token)
			return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
				scerr.WithDetailf("upload session: hash chunk %d", n), scerr.CausedBy(err))
		}
		hasher.Add(chunkHash)
		if err := s.sessionChunk(ctx, token, n, clen, chunkHash, buf); err != nil {
			s.sessionCancel(ctx, token)
			return domain.FileInfo{}, err
		}
		remaining -= clen
	}

	info, err := s.sessionFinish(ctx, token, totalChunks, hasher.Sum())
	if err != nil {
		s.sessionCancel(ctx, token)
		return domain.FileInfo{}, err
	}
	return info, nil
}

func (s *FilesService) sessionURL(path string) string {
	return s.client.uploadBaseURL + "/" + s.client.driveID + path
}

func (s *FilesService) sessionStart(ctx context.Context, in service.UploadInput, totalChunks int64) (string, error) {
	body := map[string]string{
		"total_size":   strconv.FormatInt(in.Size, 10),
		"total_chunks": strconv.FormatInt(totalChunks, 10),
	}
	if in.ExistingFileID > 0 {
		body["file_id"] = strconv.FormatInt(in.ExistingFileID, 10)
	} else {
		body["file_name"] = in.Name
		body["directory_id"] = strconv.FormatInt(in.ParentID, 10)
		body["conflict"] = "error"
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", scerr.Wrap(domain.ErrServer, scerr.WithDetailf("marshal session start: %v", err))
	}
	resp, err := s.doSessionAttempt(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL("/upload/session/start"), bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(jsonBody))
		return req, nil
	}, "session start")
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	drainAndClose(resp.Body)
	if err != nil {
		return "", scerr.Wrap(domain.ErrServer, scerr.WithDetail("decode session start"), scerr.CausedBy(err))
	}
	if out.Data.Token == "" {
		return "", scerr.Wrap(domain.ErrServer, scerr.WithDetail("session start: empty token"))
	}
	return out.Data.Token, nil
}

func (s *FilesService) sessionChunk(ctx context.Context, token string, number, size int64, chunkHash string, buf []byte) error {
	q := url.Values{}
	q.Set("chunk_number", strconv.FormatInt(number, 10))
	q.Set("chunk_size", strconv.FormatInt(size, 10))
	q.Set("chunk_hash", chunkHash)
	reqURL := s.sessionURL("/upload/session/"+token+"/chunk") + "?" + q.Encode()
	resp, err := s.doSessionAttempt(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = size
		return req, nil
	}, fmt.Sprintf("session chunk %d", number))
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}

func (s *FilesService) sessionFinish(ctx context.Context, token string, totalChunks int64, totalHash string) (domain.FileInfo, error) {
	now := time.Now().Unix()
	body := map[string]any{
		"total_chunk_hash": totalHash,
		"total_chunks":     totalChunks,
		"created_at":       now,
		"last_modified_at": now,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("marshal session finish: %v", err))
	}
	resp, err := s.doSessionAttempt(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL("/upload/session/"+token+"/finish"), bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(jsonBody))
		return req, nil
	}, "session finish")
	if err != nil {
		return domain.FileInfo{}, err
	}
	var out struct {
		Data domain.FileInfo `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	drainAndClose(resp.Body)
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetail("decode session finish"), scerr.CausedBy(err))
	}
	return out.Data, nil
}

// sessionCancel best-effort deletes a session after an unrecoverable failure.
// Errors are logged, not returned (the caller already holds the real error).
func (s *FilesService) sessionCancel(ctx context.Context, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.sessionURL("/upload/session/"+token), nil)
	if err != nil {
		s.client.log.Warn("kdrive session cancel build", slog.String("err", err.Error()))
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.client.token)
	resp, err := s.client.http.Do(req)
	if err != nil {
		s.client.log.Warn("kdrive session cancel", slog.String("err", err.Error()))
		return
	}
	drainAndClose(resp.Body)
}

// doSessionAttempt runs build() with transient retry (5xx / 429 / transport),
// mirroring the single-shot loop. build must produce a FRESH request (fresh body)
// each call so the body replays on retry. On success the caller owns resp.Body.
func (s *FilesService) doSessionAttempt(ctx context.Context, build func() (*http.Request, error), op string) (*http.Response, error) {
	var lastErr error
	backoff := s.client.initialBackoff
	for attempt := 0; attempt <= s.client.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
		}
		req, err := build()
		if err != nil {
			return nil, scerr.Wrap(domain.ErrValidation, scerr.WithDetailf("build %s req: %v", op, err))
		}
		resp, err := s.client.http.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryableError(err) {
				return nil, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("%s transport", op), scerr.CausedBy(err))
			}
			s.client.log.Warn("kdrive session request failed",
				slog.String("op", op), slog.Int("attempt", attempt+1), slog.String("err", err.Error()))
			continue
		}
		if shouldRetry(resp.StatusCode) && attempt < s.client.maxRetries {
			drainAndClose(resp.Body)
			lastErr = fmt.Errorf("transient %d", resp.StatusCode)
			s.client.log.Warn("kdrive session transient status",
				slog.String("op", op), slog.Int("status", resp.StatusCode), slog.Int("attempt", attempt+1))
			continue
		}
		if resp.StatusCode >= 400 {
			apiErr := fromResponse(resp, op)
			drainAndClose(resp.Body)
			return nil, apiErr
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("kdrive: %s retries exhausted", op)
	}
	return nil, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("%s retries exhausted", op), scerr.CausedBy(lastErr))
}
```

- [ ] **Step 4: Insert the size dispatch in `Upload`**

Edit `pkg/infrastructure/kdriveapi/files.go`. In `Upload`, immediately after the validation block (after the closing `}` of the `else { ... }` at line 131, before the first `in.Body.Seek` at line 133), insert:

```go
	if in.Size > uploadSessionThreshold {
		return s.uploadSession(ctx, in)
	}

```

Nothing else in `Upload` changes.

- [ ] **Step 5: Run the test — expect pass**

Run: `go test ./pkg/infrastructure/kdriveapi/ -count=1`
Expected: PASS (the new chunked spec + all existing single-shot specs).
Also run `gofmt -l .` (no output) and `go vet ./...`.

- [ ] **Step 6: Commit**

```bash
git add pkg/infrastructure/kdriveapi/upload_session.go pkg/infrastructure/kdriveapi/upload_session_test.go pkg/infrastructure/kdriveapi/files.go
git commit -m "feat(kdriveapi): chunked upload-session flow for files >100MB"
```

---

## Task 3: edge cases (edit mode, threshold, retry, cancel)

Each step adds a spec to `upload_session_test.go`. They lock behaviour the Task 2 code already provides; if a spec fails, fix the code in this task.

- [ ] **Step 1: Edit-mode start body**

Add this spec inside the `Describe`:

```go
It("start carries file_id in edit mode (no file_name/directory_id/conflict)", func() {
	withSmallChunks(4, 8)
	fx := newTestFixture()
	DeferCleanup(fx.Server.Close)

	var mu sync.Mutex
	var startBody map[string]any
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); defer mu.Unlock()
		_ = json.Unmarshal(readBody(r), &startBody)
		writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"data":{"id":50,"name":"x"}}`)
	})

	_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
		ExistingFileID: 321,
		Body:           bytes.NewReader([]byte("0123456789")),
		Size:           10,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(startBody["file_id"]).To(Equal("321"))
	Expect(startBody).NotTo(HaveKey("file_name"))
	Expect(startBody).NotTo(HaveKey("directory_id"))
	Expect(startBody).NotTo(HaveKey("conflict"))
})
```

- [ ] **Step 2: Threshold boundary (== stays single-shot, +1 uses session)**

```go
It("dispatches by size: exactly threshold = single-shot, threshold+1 = session", func() {
	withSmallChunks(8, 8)
	fx := newTestFixture()
	DeferCleanup(fx.Server.Close)

	var singleShot, sessionStarted bool
	fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
		singleShot = true
		writeJSON(w, http.StatusOK, `{"data":{"id":1,"name":"x"}}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
		sessionStarted = true
		writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"data":{"id":2,"name":"x"}}`)
	})

	// exactly threshold (8 bytes) -> single-shot
	_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 7, Name: "x", Body: bytes.NewReader(bytes.Repeat([]byte("a"), 8)), Size: 8,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(singleShot).To(BeTrue())
	Expect(sessionStarted).To(BeFalse())

	// threshold+1 (9 bytes) -> session
	_, err = fx.Client.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 7, Name: "x", Body: bytes.NewReader(bytes.Repeat([]byte("a"), 9)), Size: 9,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(sessionStarted).To(BeTrue())
})
```

- [ ] **Step 3: Per-chunk transient retry (429 then 200)**

```go
It("retries a chunk on 429 and re-sends the same bytes", func() {
	withSmallChunks(4, 4)
	fx := newTestFixture()
	DeferCleanup(fx.Server.Close)

	var mu sync.Mutex
	attemptsByNum := map[string]int{}
	bodiesByNum := map[string][]byte{}
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); defer mu.Unlock()
		n := r.URL.Query().Get("chunk_number")
		attemptsByNum[n]++
		body := readBody(r)
		if n == "1" && attemptsByNum[n] == 1 {
			writeJSON(w, http.StatusTooManyRequests, `{}`) // first attempt for chunk 1 -> 429
			return
		}
		bodiesByNum[n] = body
		writeJSON(w, http.StatusOK, `{}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"data":{"id":7,"name":"x"}}`)
	})

	_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(attemptsByNum["1"]).To(Equal(2))         // retried once
	Expect(string(bodiesByNum["1"])).To(Equal("0123")) // correct bytes re-sent
})
```

- [ ] **Step 4: Fail-fast on 4xx + session cancelled**

```go
It("fails fast on a non-transient chunk 4xx and cancels the session", func() {
	withSmallChunks(4, 4)
	fx := newTestFixture()
	DeferCleanup(fx.Server.Close)

	var mu sync.Mutex
	chunkAttempts := 0
	cancelled := false
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); defer mu.Unlock()
		chunkAttempts++
		writeJSON(w, http.StatusBadRequest, `{"error":"bad chunk"}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); defer mu.Unlock()
		if r.Method == http.MethodDelete {
			cancelled = true
		}
		writeJSON(w, http.StatusOK, `{}`)
	})

	_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
	})
	Expect(err).To(MatchError(domain.ErrValidation)) // 400 -> ErrValidation, no retry
	Expect(chunkAttempts).To(Equal(1))               // fail-fast
	Expect(cancelled).To(BeTrue())                   // DELETE observed
})
```

Add `"github.com/stillsource/kdrive-fuse/pkg/domain"` to the test imports.

- [ ] **Step 5: Finish failure cancels the session**

```go
It("cancels the session when finish fails", func() {
	withSmallChunks(4, 8)
	fx := newTestFixture()
	DeferCleanup(fx.Server.Close)

	var mu sync.Mutex
	cancelled := false
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"error":"hash mismatch"}`)
	})
	fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); defer mu.Unlock()
		if r.Method == http.MethodDelete {
			cancelled = true
		}
		writeJSON(w, http.StatusOK, `{}`)
	})

	_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
	})
	Expect(err).To(HaveOccurred())
	Expect(cancelled).To(BeTrue())
})
```

- [ ] **Step 6: Transport error on start surfaces as ErrServer (covers the transport branch)**

```go
It("surfaces a non-retryable transport error from start", func() {
	withSmallChunks(4, 4)
	// A client whose transport always fails with a context-cancelled (non-retryable) error.
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})
	c := New(testToken, testDriveID,
		WithUploadBaseURL("http://example.invalid/2/drive"),
		WithHTTPClient(&http.Client{Transport: rt}),
		WithRetries(2, time.Millisecond),
	)
	_, err := c.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
	})
	Expect(err).To(HaveOccurred())
})
```

Add `"time"` to the test imports. (If `WithHTTPClient` is the exact option name, use it; confirm against `options.go` — adjust the name if it differs.)

- [ ] **Step 7: Run all kdriveapi tests — expect pass**

Run: `go test ./pkg/infrastructure/kdriveapi/ -count=1`
Expected: PASS. If any edge spec fails, fix the cause in `upload_session.go` (not the test).
Run `gofmt -l .` and `go vet ./...` clean.

- [ ] **Step 8: Commit**

```bash
git add pkg/infrastructure/kdriveapi/upload_session_test.go pkg/infrastructure/kdriveapi/upload_session.go
git commit -m "test(kdriveapi): cover edit-mode, threshold, retry and cancel for upload sessions"
```

---

## Task 4: docs + full verification

**Files:** `pkg/service/upload_input.go`, `CLAUDE.md`, `ROADMAP.md`.

- [ ] **Step 1: Widen the `UploadInput` doc comment**

In `pkg/service/upload_input.go`, replace the "single-shot file upload" wording so it covers both paths, e.g.:

```go
// UploadInput describes a file upload. Small files go single-shot; files larger
// than the upload-session threshold are uploaded in chunks (the adapter decides
// by Size). Set ExistingFileID > 0 to replace an existing file's content (edit
// mode); otherwise ParentID must be > 0 and Name the desired filename.
//
// Body must be seekable — it is rewound to zero and read in one or more passes.
// Size is mandatory (kDrive rejects chunked transfer-encoding on each request).
```

Keep the struct fields unchanged.

- [ ] **Step 2: Update CLAUDE.md**

In the "kDrive API quirks" section, replace the "Chunked upload for > 100 MB … Not implemented yet — single-shot for every size" bullet with a note that it is implemented: upload-session flow (`/upload/session/{start,{token}/chunk,{token}/finish}`, `DELETE` to cancel) on `uploadBaseURL`, triggered when `Size > 100 MB`; `total_chunk_hash` is the hash-of-hashes (see `ChunkHasher`). Remove chunked upload from the "Known gaps" list.

- [ ] **Step 3: Update ROADMAP.md**

Move the "Chunked upload (> 100 MB)" item out of "Blocking remaining work" into the Shipped section with a one-line description (session flow, 50 MB chunks, per-chunk retry, cancel-on-failure, `ChunkHasher` total). Leave the "Blocking remaining work" section empty or remove it.

- [ ] **Step 4: Full verification (mirror CI)**

Run:
```bash
go build ./...
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
golangci-lint run ./...
gofmt -l .
go test ./pkg/... -coverprofile=/tmp/cov.out -coverpkg=./pkg/... -count=1 && go tool cover -func=/tmp/cov.out | tail -1
```
Expected: build OK; all `ok`; race clean; vet silent; lint `0 issues`; gofmt clean; coverage ≥ 90%. If `upload_session.go` coverage drags the total below 90%, add a targeted spec for the uncovered branch (e.g. a start 4xx, or the empty-token path).

- [ ] **Step 5: Commit**

```bash
git add pkg/service/upload_input.go CLAUDE.md ROADMAP.md
git commit -m "docs: chunked upload is implemented (upload-session flow)"
```

---

## Task 5: PR + deploy

- [ ] **Step 1: Push**

```bash
git push -u origin feat/chunked-upload
```

- [ ] **Step 2: Open the PR** (github-perso MCP `create_pull_request`, owner `stillsource`, repo `kdrive-fuse`, base `main`, head `feat/chunked-upload`). English body, **no Claude attribution**: summary, the verified API contract (host/endpoints/hash-of-hashes/threshold), the "single-shot unchanged" guarantee, and the test list. Title: `feat: chunked upload (>100MB) via the kDrive upload-session flow`.

- [ ] **Step 3: Wait for CI, merge**

Poll `pull_request_read get_check_runs` until `test` + `lint` are `success`; merge via MCP `merge_pull_request` (squash). Then `git checkout main && git pull && git branch -d feat/chunked-upload && git push origin --delete feat/chunked-upload`.

- [ ] **Step 4: Live smoke test (optional but recommended)**

```bash
make build && make install
systemctl --user restart kdrive-vfs.service && sleep 2 && systemctl --user is-active kdrive-vfs.service
```
On a self-made throwaway path under the mount, write a file larger than 100 MB (e.g. `head -c 120M /dev/urandom > ~/kDrive-vfs/_kfuse_big/big.bin`), verify it round-trips (size + a `cmp` after re-reading), then delete it. Do NOT touch real photo files.

---

## Self-review notes

- **Spec coverage:** endpoints (Task 2 `sessionStart/Chunk/Finish` + `sessionCancel`), hash-of-hashes (Task 1 `ChunkHasher` + asserted in Task 2/3), threshold strictly-greater (Task 3 step 2), per-chunk retry / fail-fast / cancel (Task 3 steps 3–5), JSON-body start/finish vs query chunk (Task 2 code + asserts), `uploadBaseURL` host (Task 2 `sessionURL`), `UploadInput` doc (Task 4), single-shot untouched (only a dispatch block inserted; its loop unchanged). Open questions stay as live-call confirmations (Task 5 smoke test).
- **No placeholders:** every code/test step has complete code and an exact command.
- **Type consistency:** `ChunkHasher.Add/Sum`, `uploadSession`, `sessionStart/Chunk/Finish/Cancel`, `doSessionAttempt`, `sessionURL`, vars `uploadSessionThreshold`/`chunkSize` are used identically across tasks; `service.UploadInput`, `domain.FileInfo`, helpers `sleepCtx/shouldRetry/isRetryableError/fromResponse/drainAndClose` match the current code.
- **Verify before claiming:** `WithHTTPClient` (Task 3 step 6) and the exact "Chunked upload" wording in CLAUDE.md/ROADMAP should be confirmed against the files when editing.
