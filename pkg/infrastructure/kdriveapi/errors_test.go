package kdriveapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("sentinelForStatus", func() {
	DescribeTable("maps",
		func(status int, want error) {
			got := sentinelForStatus(status)
			if want == nil {
				Expect(got).To(BeNil())
			} else {
				Expect(got).To(Equal(want))
			}
		},
		Entry("404", http.StatusNotFound, domain.ErrNotFound),
		Entry("401", http.StatusUnauthorized, domain.ErrAuth),
		Entry("403", http.StatusForbidden, domain.ErrAuth),
		Entry("409", http.StatusConflict, domain.ErrConflict),
		Entry("400", http.StatusBadRequest, domain.ErrValidation),
		Entry("422", http.StatusUnprocessableEntity, domain.ErrValidation),
		Entry("429", http.StatusTooManyRequests, domain.ErrRateLimit),
		Entry("500", http.StatusInternalServerError, error(nil)),
		Entry("200", http.StatusOK, error(nil)),
	)
})

var _ = Describe("shouldRetry", func() {
	It("retries 5xx", func() {
		Expect(shouldRetry(500)).To(BeTrue())
		Expect(shouldRetry(502)).To(BeTrue())
		Expect(shouldRetry(599)).To(BeTrue())
	})
	It("retries 429", func() {
		Expect(shouldRetry(429)).To(BeTrue())
	})
	It("does not retry 4xx (other)", func() {
		Expect(shouldRetry(400)).To(BeFalse())
		Expect(shouldRetry(404)).To(BeFalse())
	})
	It("does not retry 2xx/3xx", func() {
		Expect(shouldRetry(200)).To(BeFalse())
		Expect(shouldRetry(302)).To(BeFalse())
	})
})

var _ = Describe("isRetryableError", func() {
	It("nil is not retryable", func() {
		Expect(isRetryableError(nil)).To(BeFalse())
	})
	It("ctx.Canceled is not retryable", func() {
		Expect(isRetryableError(context.Canceled)).To(BeFalse())
	})
	It("ctx.DeadlineExceeded is not retryable", func() {
		Expect(isRetryableError(context.DeadlineExceeded)).To(BeFalse())
	})
	It("generic errors are retryable", func() {
		Expect(isRetryableError(errors.New("dial failed"))).To(BeTrue())
	})
})

var _ = Describe("fromResponse", func() {
	makeResp := func(status int, body string) *http.Response {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}

	It("maps 404 to ErrNotFound and preserves body snippet", func() {
		resp := makeResp(http.StatusNotFound, `{"error":"missing"}`)
		err := fromResponse(resp, "GET /files/42")
		Expect(errors.Is(err, domain.ErrNotFound)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("missing"))
	})

	It("maps 401 to ErrAuth", func() {
		resp := makeResp(http.StatusUnauthorized, `{}`)
		err := fromResponse(resp, "GET /files/42")
		Expect(errors.Is(err, domain.ErrAuth)).To(BeTrue())
	})

	It("wraps 5xx as ErrServer with HTTPError cause", func() {
		resp := makeResp(http.StatusBadGateway, "server down")
		err := fromResponse(resp, "GET /x")
		Expect(errors.Is(err, domain.ErrServer)).To(BeTrue())
		var httpErr *domain.HTTPError
		Expect(errors.As(err, &httpErr)).To(BeTrue())
		Expect(httpErr.StatusCode).To(Equal(http.StatusBadGateway))
		Expect(httpErr.Body).To(Equal("server down"))
	})

	It("truncates body snippets beyond 512 bytes", func() {
		big := strings.Repeat("X", 2048)
		resp := makeResp(http.StatusBadGateway, big)
		err := fromResponse(resp, "GET /x")
		var httpErr *domain.HTTPError
		Expect(errors.As(err, &httpErr)).To(BeTrue())
		Expect(len(httpErr.Body)).To(BeNumerically("<=", 512))
	})
})

var _ = Describe("HTTPError", func() {
	It("formats nicely", func() {
		e := &domain.HTTPError{StatusCode: 502, Body: "bad gateway"}
		Expect(e.Error()).To(ContainSubstring("502"))
		Expect(e.Error()).To(ContainSubstring("bad gateway"))
	})
})
