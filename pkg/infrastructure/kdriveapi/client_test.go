package kdriveapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("Client construction", func() {
	It("uses defaults when no options passed", func() {
		c := New("tok", "123")
		Expect(c.baseURL).To(Equal(DefaultBaseURL))
		Expect(c.uploadBaseURL).To(Equal(DefaultUploadBaseURL))
		Expect(c.token).To(Equal("tok"))
		Expect(c.driveID).To(Equal("123"))
		Expect(c.maxRetries).To(Equal(defaultMaxRetries))
		Expect(c.initialBackoff).To(Equal(defaultInitialBackoff))
		Expect(c.Files).NotTo(BeNil())
		Expect(c.Shares).NotTo(BeNil())
		Expect(c.log).NotTo(BeNil())
		Expect(c.http).NotTo(BeNil())
	})

	It("applies WithHTTPClient", func() {
		custom := &http.Client{Timeout: 1 * time.Second}
		c := New("tok", "1", WithHTTPClient(custom))
		Expect(c.http).To(BeIdenticalTo(custom))
	})

	It("WithHTTPClient ignores nil", func() {
		c := New("tok", "1", WithHTTPClient(nil))
		Expect(c.http).NotTo(BeNil())
	})

	It("applies WithBaseURL / WithUploadBaseURL", func() {
		c := New("tok", "1",
			WithBaseURL("https://custom.example/api"),
			WithUploadBaseURL("https://up.example/api"),
		)
		Expect(c.baseURL).To(Equal("https://custom.example/api"))
		Expect(c.uploadBaseURL).To(Equal("https://up.example/api"))
	})

	It("WithBaseURL / WithUploadBaseURL ignore empty", func() {
		c := New("tok", "1", WithBaseURL(""), WithUploadBaseURL(""))
		Expect(c.baseURL).To(Equal(DefaultBaseURL))
		Expect(c.uploadBaseURL).To(Equal(DefaultUploadBaseURL))
	})

	It("applies WithLogger", func() {
		l := slog.Default()
		c := New("tok", "1", WithLogger(l))
		Expect(c.log).To(BeIdenticalTo(l))
	})

	It("WithLogger ignores nil", func() {
		c := New("tok", "1", WithLogger(nil))
		Expect(c.log).NotTo(BeNil())
	})

	It("applies WithRetries", func() {
		c := New("tok", "1", WithRetries(7, 2*time.Second))
		Expect(c.maxRetries).To(Equal(7))
		Expect(c.initialBackoff).To(Equal(2 * time.Second))
	})

	It("WithRetries ignores negative max", func() {
		c := New("tok", "1", WithRetries(-1, 100*time.Millisecond))
		Expect(c.maxRetries).To(Equal(defaultMaxRetries))
	})

	It("WithRetries ignores zero backoff", func() {
		c := New("tok", "1", WithRetries(5, 0))
		Expect(c.initialBackoff).To(Equal(defaultInitialBackoff))
	})
})

var _ = Describe("Transport behavior", func() {
	var fx *testFixture

	BeforeEach(func() {
		fx = newTestFixture()
		DeferCleanup(fx.Server.Close)
	})

	It("sends Authorization: Bearer <token>", func() {
		var gotAuth string
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			writeJSON(w, 200, `{"data":{"id":1,"name":"","type":"dir"}}`)
		})
		_, err := fx.Client.Files.Stat(context.Background(), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotAuth).To(Equal("Bearer " + testToken))
	})

	It("retries on 5xx then succeeds", func() {
		var attempts atomic.Int32
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if n < 3 {
				writeJSON(w, 503, `{"error":"try again"}`)
				return
			}
			writeJSON(w, 200, `{"data":{"id":1,"name":"","type":"dir"}}`)
		})
		_, err := fx.Client.Files.Stat(context.Background(), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempts.Load()).To(Equal(int32(3)))
	})

	It("retries on 429", func() {
		var attempts atomic.Int32
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			if attempts.Add(1) == 1 {
				writeJSON(w, 429, `{"error":"slow down"}`)
				return
			}
			writeJSON(w, 200, `{"data":{"id":1,"name":"","type":"dir"}}`)
		})
		_, err := fx.Client.Files.Stat(context.Background(), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempts.Load()).To(Equal(int32(2)))
	})

	It("gives up after exhausting retries on persistent 5xx", func() {
		var attempts atomic.Int32
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			writeJSON(w, 502, `{"error":"bad gateway"}`)
		})
		_, err := fx.Client.Files.Stat(context.Background(), 1)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, domain.ErrServer)).To(BeTrue())
		Expect(attempts.Load()).To(Equal(int32(3))) // initial + 2 retries
	})

	It("propagates ctx cancellation", func() {
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := fx.Client.Files.Stat(ctx, 1)
			done <- err
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		Eventually(done).Should(Receive(HaveOccurred()))
	})

	It("never logs the token", func() {
		var attempts atomic.Int32
		fx.Mux.HandleFunc("/2/drive/1234/files/1", func(w http.ResponseWriter, r *http.Request) {
			if attempts.Add(1) < 3 {
				writeJSON(w, 503, "fail")
				return
			}
			writeJSON(w, 200, `{"data":{"id":1,"name":"","type":"dir"}}`)
		})
		_, _ = fx.Client.Files.Stat(context.Background(), 1)
		Expect(containsSubstring(fx.Logs.all(), testToken)).To(BeFalse())
	})
})
