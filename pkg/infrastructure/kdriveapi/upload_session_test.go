package kdriveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
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

	It("sends conflict=rename in start body when Conflict is 'rename'", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		var startBody map[string]any
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.Unmarshal(readBody(r), &startBody)
			writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"id":60,"name":"r.bin","type":"file"}}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7,
			Name:     "r.bin",
			Body:     bytes.NewReader([]byte("0123456789")),
			Size:     10,
			Conflict: "rename",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(startBody["conflict"]).To(Equal("rename"))
	})

	It("sends conflict=version in start body when Conflict is 'version'", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		var startBody map[string]any
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.Unmarshal(readBody(r), &startBody)
			writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"id":61,"name":"v.bin","type":"file"}}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7,
			Name:     "v.bin",
			Body:     bytes.NewReader([]byte("0123456789")),
			Size:     10,
			Conflict: "version",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(startBody["conflict"]).To(Equal("version"))
	})

	It("falls back to conflict=error in start body for unrecognized Conflict value", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		var startBody map[string]any
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.Unmarshal(readBody(r), &startBody)
			writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"id":62,"name":"b.bin","type":"file"}}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7,
			Name:     "b.bin",
			Body:     bytes.NewReader([]byte("0123456789")),
			Size:     10,
			Conflict: "bogus",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(startBody["conflict"]).To(Equal("error"))
	})

	It("Conflict is ignored in chunked edit mode (no conflict key in start body)", func() {
		withSmallChunks(4, 8)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		var startBody map[string]any
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
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
			Conflict:       "rename", // must be ignored
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(startBody).NotTo(HaveKey("conflict"))
	})

	It("start carries file_id in edit mode (no file_name/directory_id/conflict)", func() {
		withSmallChunks(4, 8)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		var startBody map[string]any
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
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
			mu.Lock()
			defer mu.Unlock()
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
		Expect(attemptsByNum["1"]).To(Equal(2))            // retried once
		Expect(string(bodiesByNum["1"])).To(Equal("0123")) // correct bytes re-sent
	})

	It("fails fast on a non-transient chunk 4xx and cancels the session", func() {
		// Use parallelism=1 to guarantee only 1 chunk attempt before cancel.
		withSmallChunks(4, 4)
		fx := newTestFixture(WithUploadParallelism(1))
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		chunkAttempts := 0
		cancelled := false
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			chunkAttempts++
			writeJSON(w, http.StatusBadRequest, `{"error":"bad chunk"}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodDelete {
				cancelled = true
			}
			writeJSON(w, http.StatusOK, `{}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrValidation)) // 400 -> ErrValidation, no retry
		Expect(chunkAttempts).To(Equal(1))               // fail-fast: only 1 chunk attempted at parallelism=1
		Expect(cancelled).To(BeTrue())                   // DELETE observed
	})

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
			mu.Lock()
			defer mu.Unlock()
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

	It("fails fast when start returns a non-transient 4xx (no chunks, no session)", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		startAttempts := 0
		chunkHit := false
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			startAttempts++
			writeJSON(w, http.StatusBadRequest, `{"error":"bad start"}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			chunkHit = true
			writeJSON(w, http.StatusOK, `{}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrValidation)) // 400 -> ErrValidation, no retry
		Expect(startAttempts).To(Equal(1))               // fail-fast on start
		Expect(chunkHit).To(BeFalse())                   // never reached the chunk loop
	})

	It("errors when start returns an empty token", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		chunkHit := false
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":""}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session//chunk", func(w http.ResponseWriter, r *http.Request) {
			chunkHit = true
			writeJSON(w, http.StatusOK, `{}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrServer))
		Expect(err.Error()).To(ContainSubstring("empty token"))
		Expect(chunkHit).To(BeFalse()) // never reached the chunk loop
	})

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

	It("retries then returns ErrServer when start keeps returning 5xx", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)

		var mu sync.Mutex
		startAttempts := 0
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			startAttempts++
			writeJSON(w, http.StatusInternalServerError, `{"error":"boom"}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrServer))
		Expect(startAttempts).To(BeNumerically(">", 1)) // transient 5xx was retried
	})

	It("returns ErrServer when the transport keeps failing (retries exhausted)", func() {
		withSmallChunks(4, 4)
		calls := 0
		rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, io.ErrUnexpectedEOF // retryable transport error, always fails
		})
		c := New(testToken, testDriveID,
			WithUploadBaseURL("http://example.invalid/2/drive"),
			WithHTTPClient(&http.Client{Transport: rt}),
			WithRetries(2, time.Millisecond),
		)
		_, err := c.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrServer))
		Expect(calls).To(Equal(3)) // 1 attempt + 2 retries, then exhausted
	})

	It("errors when start returns malformed JSON", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{not json`)
		})
		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrServer))
	})

	It("errors when finish returns malformed JSON", func() {
		withSmallChunks(4, 8)
		fx := newTestFixture()
		DeferCleanup(fx.Server.Close)
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":"SESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SESS/finish", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{not json`)
		})
		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x", Body: bytes.NewReader([]byte("0123456789")), Size: 10,
		})
		Expect(err).To(MatchError(domain.ErrServer))
	})
})

// nonReaderAt wraps a []byte so it satisfies io.ReadSeeker but NOT io.ReaderAt,
// allowing tests to exercise the sequential fallback path.
type nonReaderAt struct{ r *bytes.Reader }

func (n *nonReaderAt) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n *nonReaderAt) Seek(offset int64, whence int) (int64, error) {
	return n.r.Seek(offset, whence)
}

var _ = Describe("FilesService.Upload — parallel chunked session", func() {
	It("all N chunk POSTs arrive, content reassembles correctly, hash matches sequential", func() {
		withSmallChunks(4, 4) // 4-byte chunks
		fx := newTestFixture(WithUploadParallelism(4))
		DeferCleanup(fx.Server.Close)

		content := []byte("0123456789ab") // 12 bytes -> 3 chunks of 4

		var mu sync.Mutex
		chunks := map[int64][]byte{}
		chunkHashes := map[int64]string{}
		var finishBody map[string]any

		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":"PSESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/PSESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			n, _ := strconv.ParseInt(r.URL.Query().Get("chunk_number"), 10, 64)
			body := readBody(r)
			mu.Lock()
			chunks[n] = body
			chunkHashes[n] = r.URL.Query().Get("chunk_hash")
			mu.Unlock()
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/PSESS/finish", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			_ = json.Unmarshal(readBody(r), &finishBody)
			mu.Unlock()
			writeJSON(w, http.StatusOK, `{"data":{"id":200,"name":"par.bin","size":12,"type":"file"}}`)
		})

		info, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "par.bin",
			Body: bytes.NewReader(content),
			Size: int64(len(content)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ID).To(Equal(int64(200)))

		// All 3 chunks arrived (order-independent).
		Expect(chunks).To(HaveLen(3))
		// Content reassembles correctly in order.
		got := append(append(append([]byte{}, chunks[1]...), chunks[2]...), chunks[3]...)
		Expect(got).To(Equal(content))

		// Per-chunk hashes are correct.
		for n := int64(1); n <= 3; n++ {
			expH, _ := hash.XXH3Stream(bytes.NewReader(chunks[n]))
			Expect(chunkHashes[n]).To(Equal(expH))
		}

		// total_chunk_hash equals what the sequential ChunkHasher would produce.
		h := hash.NewChunkHasher()
		for _, n := range []int64{1, 2, 3} {
			hs, _ := hash.XXH3Stream(bytes.NewReader(chunks[n]))
			h.Add(hs)
		}
		Expect(finishBody["total_chunk_hash"]).To(Equal(h.Sum()))
	})

	It("total_chunk_hash is deterministic across parallelism=1 and parallelism=4", func() {
		withSmallChunks(4, 4)

		content := []byte("abcdefghijkl") // 12 bytes, 3 chunks

		runUpload := func(parallelism int) string {
			fx := newTestFixture(WithUploadParallelism(parallelism))
			DeferCleanup(fx.Server.Close)
			var mu sync.Mutex
			var got string
			fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"data":{"token":"HSESS"}}`)
			})
			fx.Mux.HandleFunc("/2/drive/1234/upload/session/HSESS/chunk", func(w http.ResponseWriter, r *http.Request) {
				readBody(r)
				writeJSON(w, http.StatusOK, `{}`)
			})
			fx.Mux.HandleFunc("/2/drive/1234/upload/session/HSESS/finish", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.Unmarshal(readBody(r), &body)
				mu.Lock()
				got, _ = body["total_chunk_hash"].(string)
				mu.Unlock()
				writeJSON(w, http.StatusOK, `{"data":{"id":1,"name":"x","type":"file"}}`)
			})
			_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
				ParentID: 7, Name: "x",
				Body: bytes.NewReader(content),
				Size: int64(len(content)),
			})
			Expect(err).NotTo(HaveOccurred())
			return got
		}

		h1 := runUpload(1)
		h4 := runUpload(4)
		Expect(h1).NotTo(BeEmpty())
		Expect(h4).To(Equal(h1))
	})

	It("one chunk failing cancels the session and returns an error without calling finish", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture(WithUploadParallelism(4))
		DeferCleanup(fx.Server.Close)

		content := []byte("0123456789ab") // 12 bytes -> 3 chunks

		var mu sync.Mutex
		cancelled := false
		finishCalled := false

		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":"FSESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/FSESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			n := r.URL.Query().Get("chunk_number")
			readBody(r)
			if n == "2" {
				writeJSON(w, http.StatusBadRequest, `{"error":"bad chunk"}`)
				return
			}
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/FSESS/finish", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			finishCalled = true
			mu.Unlock()
			writeJSON(w, http.StatusOK, `{"data":{"id":99,"name":"x","type":"file"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/FSESS", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				mu.Lock()
				cancelled = true
				mu.Unlock()
			}
			writeJSON(w, http.StatusOK, `{}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "x",
			Body: bytes.NewReader(content),
			Size: int64(len(content)),
		})
		Expect(err).To(HaveOccurred())
		Expect(cancelled).To(BeTrue())     // session cancelled
		Expect(finishCalled).To(BeFalse()) // finish NOT called
	})

	It("falls back to sequential when body is not io.ReaderAt", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture(WithUploadParallelism(4))
		DeferCleanup(fx.Server.Close)

		content := []byte("0123456789ab")
		seqBody := &nonReaderAt{r: bytes.NewReader(content)}

		var mu sync.Mutex
		chunks := map[int64][]byte{}

		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":"SSESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SSESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			n, _ := strconv.ParseInt(r.URL.Query().Get("chunk_number"), 10, 64)
			body := readBody(r)
			mu.Lock()
			chunks[n] = body
			mu.Unlock()
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/SSESS/finish", func(w http.ResponseWriter, r *http.Request) {
			readBody(r)
			writeJSON(w, http.StatusOK, `{"data":{"id":300,"name":"seq.bin","type":"file"}}`)
		})

		info, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "seq.bin",
			Body: seqBody,
			Size: int64(len(content)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ID).To(Equal(int64(300)))

		// All chunks must still arrive correctly.
		Expect(chunks).To(HaveLen(3))
		got := append(append(append([]byte{}, chunks[1]...), chunks[2]...), chunks[3]...)
		Expect(got).To(Equal(content))
	})

	It("per-chunk retry still works in parallel mode", func() {
		withSmallChunks(4, 4)
		fx := newTestFixture(WithUploadParallelism(4))
		DeferCleanup(fx.Server.Close)

		content := []byte("0123456789ab")

		var mu sync.Mutex
		attemptsByChunk := map[string]int{}

		fx.Mux.HandleFunc("/2/drive/1234/upload/session/start", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"data":{"token":"RSESS"}}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/RSESS/chunk", func(w http.ResponseWriter, r *http.Request) {
			n := r.URL.Query().Get("chunk_number")
			readBody(r)
			mu.Lock()
			attemptsByChunk[n]++
			cnt := attemptsByChunk[n]
			mu.Unlock()
			if n == "1" && cnt == 1 {
				writeJSON(w, http.StatusTooManyRequests, `{}`)
				return
			}
			writeJSON(w, http.StatusOK, `{}`)
		})
		fx.Mux.HandleFunc("/2/drive/1234/upload/session/RSESS/finish", func(w http.ResponseWriter, r *http.Request) {
			readBody(r)
			writeJSON(w, http.StatusOK, `{"data":{"id":400,"name":"retry.bin","type":"file"}}`)
		})

		_, err := fx.Client.Files.Upload(context.Background(), service.UploadInput{
			ParentID: 7, Name: "retry.bin",
			Body: bytes.NewReader(content),
			Size: int64(len(content)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(attemptsByChunk["1"]).To(Equal(2)) // chunk 1 retried once
	})
})
