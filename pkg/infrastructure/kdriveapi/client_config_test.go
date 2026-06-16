package kdriveapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

func resp200(jsonBody string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(jsonBody)),
		Header:     make(http.Header),
	}
}

func TestDefaultClientConfig(t *testing.T) {
	c := New("tok", "1")
	if c.maxRetries != 5 {
		t.Errorf("default maxRetries = %d, want 5", c.maxRetries)
	}
	if c.http.Timeout != 60*time.Second {
		t.Errorf("read client timeout = %v, want 60s", c.http.Timeout)
	}
	if c.uploadHTTP == nil {
		t.Fatal("uploadHTTP is nil")
	}
	if c.uploadHTTP.Timeout != 2*time.Minute {
		t.Errorf("upload client timeout = %v, want 2m", c.uploadHTTP.Timeout)
	}
	if c.http == c.uploadHTTP {
		t.Error("read and upload clients should be distinct by default")
	}
}

func TestWithUploadTimeout(t *testing.T) {
	c := New("tok", "1", WithUploadTimeout(90*time.Second))
	if c.uploadHTTP.Timeout != 90*time.Second {
		t.Errorf("upload timeout = %v, want 90s", c.uploadHTTP.Timeout)
	}
	// The read client must keep its own (default) timeout.
	if c.http.Timeout != 60*time.Second {
		t.Errorf("read client timeout = %v, want 60s (unchanged)", c.http.Timeout)
	}
}

func TestWithHTTPClientSetsBoth(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	c := New("tok", "1", WithHTTPClient(custom))
	if c.http != custom || c.uploadHTTP != custom {
		t.Error("WithHTTPClient should set both the read and the upload client")
	}
}

// Uploads must go through the upload client (longer timeout); reads through the
// read client. Set distinct transports and confirm the routing.
func TestUploadUsesUploadClientReadsUseReadClient(t *testing.T) {
	c := New("tok", "1")
	var readHits, uploadHits int
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		readHits++
		return resp200(`{"data":[]}`), nil
	})}
	c.uploadHTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		uploadHits++
		return resp200(`{"data":{"id":1,"name":"x.txt","type":"file"}}`), nil
	})}

	_, err := c.Files.Upload(context.Background(), service.UploadInput{
		ParentID: 1, Name: "x.txt", Body: bytes.NewReader([]byte("hi")), Size: 2,
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if uploadHits == 0 || readHits != 0 {
		t.Errorf("upload should use the upload client only: uploadHits=%d readHits=%d", uploadHits, readHits)
	}

	readHits, uploadHits = 0, 0
	if _, err := c.Files.List(context.Background(), 1); err != nil {
		t.Fatalf("list: %v", err)
	}
	if readHits == 0 || uploadHits != 0 {
		t.Errorf("list should use the read client only: readHits=%d uploadHits=%d", readHits, uploadHits)
	}
}
