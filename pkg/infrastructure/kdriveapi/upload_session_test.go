package kdriveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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
