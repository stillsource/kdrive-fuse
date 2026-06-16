package kdriveapi

import (
	"bytes"
	"context"
	"encoding/json"
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
		Expect(chunkAttempts).To(Equal(1))               // fail-fast
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
})
