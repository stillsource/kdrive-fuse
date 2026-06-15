package kdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/kdrive/internal/hash"
)

var _ = Describe("FilesService", func() {
	var fx *testFixture
	var ctx context.Context

	BeforeEach(func() {
		fx = newTestFixture()
		ctx = context.Background()
		DeferCleanup(fx.Server.Close)
	})

	Describe("List", func() {
		It("returns children of a folder", func() {
			fx.Mux.HandleFunc("/2/drive/1234/files/1/files", func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal("GET"))
				Expect(r.URL.Query().Get("per_page")).To(Equal("500"))
				writeJSON(w, 200, `{"data":[
					{"id":10,"name":"a.txt","type":"file","size":3,"created_at":100,"last_modified_at":200},
					{"id":11,"name":"sub","type":"dir"}
				]}`)
			})
			files, err := fx.Client.Files.List(ctx, 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(2))
			Expect(files[0].Name).To(Equal("a.txt"))
			Expect(files[0].IsDir()).To(BeFalse())
			Expect(files[1].IsDir()).To(BeTrue())
		})

		It("pages until fewer than per_page results come back", func() {
			page := 0
			fx.Mux.HandleFunc("/2/drive/1234/files/1/files", func(w http.ResponseWriter, r *http.Request) {
				page++
				if page == 1 {
					// 500 items → another page fetched
					var items []string
					for i := 0; i < 500; i++ {
						items = append(items, fmt.Sprintf(`{"id":%d,"name":"f%d","type":"file"}`, i, i))
					}
					writeJSON(w, 200, `{"data":[`+strings.Join(items, ",")+`]}`)
					return
				}
				// second page: only 2 entries → stop
				writeJSON(w, 200, `{"data":[{"id":600,"name":"last1","type":"file"},{"id":601,"name":"last2","type":"file"}]}`)
			})
			files, err := fx.Client.Files.List(ctx, 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(502))
			Expect(files[501].Name).To(Equal("last2"))
		})

		It("rejects invalid folder id", func() {
			_, err := fx.Client.Files.List(ctx, 0)
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})

		It("maps 404 to ErrNotFound", func() {
			fx.Mux.HandleFunc("/2/drive/1234/files/99/files", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 404, `{"error":"nope"}`)
			})
			_, err := fx.Client.Files.List(ctx, 99)
			Expect(errors.Is(err, ErrNotFound)).To(BeTrue())
		})
	})

	Describe("Stat", func() {
		It("fetches file info", func() {
			fx.Mux.HandleFunc("/2/drive/1234/files/42", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"data":{"id":42,"name":"doc.pdf","type":"file","size":1024,"last_modified_at":1700000000}}`)
			})
			info, err := fx.Client.Files.Stat(ctx, 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.ID).To(Equal(int64(42)))
			Expect(info.Size).To(Equal(int64(1024)))
		})

		It("rejects invalid id", func() {
			_, err := fx.Client.Files.Stat(ctx, 0)
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})
	})

	Describe("Download", func() {
		It("reads full body", func() {
			fx.Mux.HandleFunc("/2/drive/1234/files/7/download", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("hello world"))
			})
			data, err := fx.Client.Files.Download(ctx, 7)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("hello world"))
		})

		It("rejects invalid id", func() {
			_, err := fx.Client.Files.Download(ctx, 0)
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})
	})

	Describe("DownloadStream", func() {
		It("sets Range header when length > 0", func() {
			var gotRange string
			fx.Mux.HandleFunc("/2/drive/1234/files/7/download", func(w http.ResponseWriter, r *http.Request) {
				gotRange = r.Header.Get("Range")
				_, _ = w.Write([]byte("part"))
			})
			rc, err := fx.Client.Files.DownloadStream(ctx, 7, 10, 100)
			Expect(err).NotTo(HaveOccurred())
			defer rc.Close()
			Expect(gotRange).To(Equal("bytes=10-109"))
		})

		It("sets open-ended Range when length == 0 and off > 0", func() {
			var gotRange string
			fx.Mux.HandleFunc("/2/drive/1234/files/7/download", func(w http.ResponseWriter, r *http.Request) {
				gotRange = r.Header.Get("Range")
				_, _ = w.Write([]byte("rest"))
			})
			rc, err := fx.Client.Files.DownloadStream(ctx, 7, 50, 0)
			Expect(err).NotTo(HaveOccurred())
			defer rc.Close()
			Expect(gotRange).To(Equal("bytes=50-"))
		})

		It("sets no Range when off and length are 0", func() {
			var gotRange string
			fx.Mux.HandleFunc("/2/drive/1234/files/7/download", func(w http.ResponseWriter, r *http.Request) {
				gotRange = r.Header.Get("Range")
				_, _ = w.Write([]byte("all"))
			})
			rc, err := fx.Client.Files.DownloadStream(ctx, 7, 0, 0)
			Expect(err).NotTo(HaveOccurred())
			defer rc.Close()
			Expect(gotRange).To(Equal(""))
		})
	})

	Describe("Upload", func() {
		It("creates a new file with correct query params and body", func() {
			var gotBody []byte
			var gotURL string
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				gotBody = readBody(r)
				gotURL = r.URL.String()
				Expect(r.Header.Get("Content-Type")).To(Equal("application/octet-stream"))
				Expect(r.ContentLength).To(Equal(int64(len(gotBody))))
				writeJSON(w, 200, `{"data":{"id":999,"name":"new.txt","type":"file","size":5}}`)
			})
			body := []byte("hello")
			info, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1,
				Name:     "new.txt",
				Body:     bytes.NewReader(body),
				Size:     int64(len(body)),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(info.ID).To(Equal(int64(999)))
			Expect(gotBody).To(Equal(body))
			Expect(gotURL).To(ContainSubstring("file_name=new.txt"))
			Expect(gotURL).To(ContainSubstring("directory_id=1"))
			Expect(gotURL).To(ContainSubstring("conflict=error"))
			Expect(gotURL).To(ContainSubstring("total_size=5"))
			Expect(gotURL).To(ContainSubstring("total_chunk_hash=xxh3%3A"))
		})

		It("retries a 429 then succeeds, rewinding the body each attempt", func() {
			var calls int
			var lastBody []byte
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				calls++
				lastBody = readBody(r)
				if calls == 1 {
					writeJSON(w, http.StatusTooManyRequests, `{"error":"slow down"}`)
					return
				}
				writeJSON(w, 200, `{"data":{"id":7,"name":"r.txt","type":"file","size":5}}`)
			})
			info, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "r.txt",
				Body: bytes.NewReader([]byte("hello")), Size: 5,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(info.ID).To(Equal(int64(7)))
			Expect(calls).To(Equal(2))
			Expect(lastBody).To(Equal([]byte("hello"))) // body fully re-sent after rewind
		})

		It("retries a 5xx then succeeds", func() {
			var calls int
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = readBody(r)
				if calls < 2 {
					writeJSON(w, http.StatusBadGateway, `{"error":"upstream"}`)
					return
				}
				writeJSON(w, 200, `{"data":{"id":8,"name":"r.txt","type":"file"}}`)
			})
			_, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "r.txt",
				Body: bytes.NewReader([]byte("data")), Size: 4,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(2))
		})

		It("does not retry a 4xx hash mismatch and returns ErrValidation", func() {
			var calls int
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = readBody(r)
				writeJSON(w, http.StatusBadRequest, `{"error":{"description":"upload hash mismatch"}}`)
			})
			_, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "r.txt",
				Body: bytes.NewReader([]byte("data")), Size: 4,
			})
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
			Expect(calls).To(Equal(1))
		})

		It("returns ErrServer after exhausting retries on 5xx", func() {
			var calls int
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = readBody(r)
				writeJSON(w, http.StatusServiceUnavailable, `{"error":"down"}`)
			})
			_, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "r.txt",
				Body: bytes.NewReader([]byte("data")), Size: 4,
			})
			Expect(errors.Is(err, ErrServer)).To(BeTrue())
			Expect(calls).To(Equal(3)) // maxRetries(2) + 1
		})

		It("retries a transport error then succeeds", func() {
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				_ = readBody(r)
				writeJSON(w, 200, `{"data":{"id":9,"name":"r.txt","type":"file"}}`)
			})
			var calls int
			rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return nil, errors.New("transport boom")
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			client := New(testToken, testDriveID,
				WithUploadBaseURL(fx.Server.URL+"/2/drive"),
				WithHTTPClient(&http.Client{Transport: rt}),
				WithRetries(2, 5*time.Millisecond),
			)
			_, err := client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "r.txt",
				Body: bytes.NewReader([]byte("data")), Size: 4,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(2))
		})

		It("computes xxh3 hash correctly", func() {
			var gotHash string
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				gotHash = r.URL.Query().Get("total_chunk_hash")
				_ = readBody(r)
				writeJSON(w, 200, `{"data":{"id":1,"name":"h.txt","type":"file"}}`)
			})
			body := []byte("deterministic content")
			expected, err := hash.XXH3Stream(bytes.NewReader(body))
			Expect(err).NotTo(HaveOccurred())
			_, err = fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "h.txt",
				Body: bytes.NewReader(body),
				Size: int64(len(body)),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(gotHash).To(Equal(expected))
		})

		It("edits existing file when ExistingFileID > 0", func() {
			var gotURL string
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				gotURL = r.URL.String()
				_ = readBody(r)
				writeJSON(w, 200, `{"data":{"id":42,"name":"kept.txt","type":"file","size":3}}`)
			})
			body := []byte("new")
			info, err := fx.Client.Files.Upload(ctx, UploadInput{
				ExistingFileID: 42,
				Body:           bytes.NewReader(body),
				Size:           3,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(info.ID).To(Equal(int64(42)))
			Expect(gotURL).To(ContainSubstring("file_id=42"))
			Expect(gotURL).NotTo(ContainSubstring("file_name="))
			Expect(gotURL).NotTo(ContainSubstring("directory_id="))
			Expect(gotURL).NotTo(ContainSubstring("conflict="))
		})

		It("rejects nil body", func() {
			_, err := fx.Client.Files.Upload(ctx, UploadInput{ParentID: 1, Name: "x.txt"})
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})

		It("rejects invalid parent in create mode", func() {
			_, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 0, Name: "x.txt",
				Body: bytes.NewReader([]byte("a")), Size: 1,
			})
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})

		It("rejects invalid name", func() {
			_, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "bad/name",
				Body: bytes.NewReader([]byte("a")), Size: 1,
			})
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})

		It("surfaces server errors", func() {
			fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
				_ = readBody(r)
				writeJSON(w, 500, `{"error":"boom"}`)
			})
			_, err := fx.Client.Files.Upload(ctx, UploadInput{
				ParentID: 1, Name: "x.txt",
				Body: bytes.NewReader([]byte("a")), Size: 1,
			})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Mkdir", func() {
		It("creates and returns FileInfo", func() {
			var gotBody []byte
			fx.Mux.HandleFunc("/2/drive/1234/files/1/directory", func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal("POST"))
				gotBody = readBody(r)
				writeJSON(w, 200, `{"data":{"id":50,"name":"newdir","type":"dir"}}`)
			})
			info, err := fx.Client.Files.Mkdir(ctx, 1, "newdir")
			Expect(err).NotTo(HaveOccurred())
			Expect(info.ID).To(Equal(int64(50)))
			Expect(info.IsDir()).To(BeTrue())
			var body map[string]string
			Expect(json.Unmarshal(gotBody, &body)).To(Succeed())
			Expect(body["name"]).To(Equal("newdir"))
		})

		It("rejects invalid parent", func() {
			_, err := fx.Client.Files.Mkdir(ctx, 0, "x")
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})

		It("rejects invalid name", func() {
			_, err := fx.Client.Files.Mkdir(ctx, 1, "bad/name")
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})
	})

	Describe("Delete", func() {
		It("sends DELETE and succeeds", func() {
			called := false
			fx.Mux.HandleFunc("/2/drive/1234/files/88", func(w http.ResponseWriter, r *http.Request) {
				called = true
				Expect(r.Method).To(Equal("DELETE"))
				writeJSON(w, 200, `{"data":{"cancel_id":"abc"}}`)
			})
			Expect(fx.Client.Files.Delete(ctx, 88)).To(Succeed())
			Expect(called).To(BeTrue())
		})

		It("rejects invalid id", func() {
			Expect(errors.Is(fx.Client.Files.Delete(ctx, 0), ErrValidation)).To(BeTrue())
		})
	})

	Describe("Rename", func() {
		It("posts new name and returns info", func() {
			var gotBody []byte
			fx.Mux.HandleFunc("/2/drive/1234/files/88/rename", func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal("POST"))
				gotBody = readBody(r)
				writeJSON(w, 200, `{"data":{"id":88,"name":"new.txt","type":"file"}}`)
			})
			info, err := fx.Client.Files.Rename(ctx, 88, "new.txt")
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Name).To(Equal("new.txt"))
			Expect(string(gotBody)).To(ContainSubstring(`"new.txt"`))
		})

		It("rejects invalid name", func() {
			_, err := fx.Client.Files.Rename(ctx, 1, "")
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		})
	})

	Describe("Move", func() {
		It("posts to /move/{destID}", func() {
			called := false
			fx.Mux.HandleFunc("/2/drive/1234/files/10/move/20", func(w http.ResponseWriter, r *http.Request) {
				called = true
				Expect(r.Method).To(Equal("POST"))
				writeJSON(w, 200, `{"data":{"cancel_id":"abc"}}`)
			})
			Expect(fx.Client.Files.Move(ctx, 10, 20)).To(Succeed())
			Expect(called).To(BeTrue())
		})

		It("rejects invalid ids", func() {
			Expect(errors.Is(fx.Client.Files.Move(ctx, 0, 1), ErrValidation)).To(BeTrue())
			Expect(errors.Is(fx.Client.Files.Move(ctx, 1, 0), ErrValidation)).To(BeTrue())
		})
	})
})

// drain helpers for stream tests
var _ = drainReader

func drainReader(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}

var _ = Describe("FilesService edge cases", func() {
	var fx *testFixture
	var ctx context.Context
	BeforeEach(func() {
		fx = newTestFixture()
		ctx = context.Background()
		DeferCleanup(fx.Server.Close)
	})

	It("Upload returns error on malformed JSON response", func() {
		fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
			_ = readBody(r)
			writeJSON(w, 200, `not json at all`)
		})
		_, err := fx.Client.Files.Upload(ctx, UploadInput{
			ParentID: 1, Name: "x.txt",
			Body: bytes.NewReader([]byte("x")), Size: 1,
		})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrServer)).To(BeTrue())
	})

	It("Upload maps 4xx to sentinel", func() {
		fx.Mux.HandleFunc("/2/drive/1234/upload", func(w http.ResponseWriter, r *http.Request) {
			_ = readBody(r)
			writeJSON(w, 409, `{"error":"conflict"}`)
		})
		_, err := fx.Client.Files.Upload(ctx, UploadInput{
			ParentID: 1, Name: "x.txt",
			Body: bytes.NewReader([]byte("x")), Size: 1,
		})
		Expect(errors.Is(err, ErrConflict)).To(BeTrue())
	})

	It("Upload rejects negative size", func() {
		_, err := fx.Client.Files.Upload(ctx, UploadInput{
			ParentID: 1, Name: "x.txt",
			Body: bytes.NewReader(nil), Size: -1,
		})
		Expect(errors.Is(err, ErrValidation)).To(BeTrue())
	})

	It("Upload rejects invalid existing file id", func() {
		_, err := fx.Client.Files.Upload(ctx, UploadInput{
			ExistingFileID: -1,
			Body:           bytes.NewReader([]byte("x")),
			Size:           1,
		})
		Expect(errors.Is(err, ErrValidation)).To(BeTrue())
	})

	It("Rename rejects invalid file id", func() {
		_, err := fx.Client.Files.Rename(ctx, 0, "good.txt")
		Expect(errors.Is(err, ErrValidation)).To(BeTrue())
	})

	It("Mkdir maps 4xx to sentinel", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/1/directory", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 409, `{"error":"exists"}`)
		})
		_, err := fx.Client.Files.Mkdir(ctx, 1, "dup")
		Expect(errors.Is(err, ErrConflict)).To(BeTrue())
	})

	It("decodeJSON surfaces invalid JSON as ErrServer", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, `garbage`)
		})
		_, err := fx.Client.Files.Stat(ctx, 1)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrServer)).To(BeTrue())
	})

	It("DownloadStream maps 404 to ErrNotFound", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/123/download", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 404, `{"error":"gone"}`)
		})
		_, err := fx.Client.Files.DownloadStream(ctx, 123, 0, 0)
		Expect(errors.Is(err, ErrNotFound)).To(BeTrue())
	})
})

var _ = Describe("transport surface", func() {
	It("returns error when server is unreachable", func() {
		fx := newTestFixture()
		fx.Server.Close() // now URL points to nothing
		_, err := fx.Client.Files.Stat(context.Background(), 1)
		Expect(err).To(HaveOccurred())
	})
})
