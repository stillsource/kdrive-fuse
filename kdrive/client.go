package kdrive

import (
	"log/slog"
	"net/http"
	"time"
)

// Default endpoints for the kDrive REST API v2.
const (
	DefaultBaseURL       = "https://api.infomaniak.com/2/drive"
	DefaultUploadBaseURL = "https://api.kdrive.infomaniak.com/2/drive"
)

const (
	defaultHTTPTimeout    = 60 * time.Second
	defaultMaxRetries     = 3
	defaultInitialBackoff = time.Second
)

// Client is the top-level kDrive API client. It groups operations into services
// (Files, Shares). Safe for concurrent use.
type Client struct {
	http           *http.Client
	log            *slog.Logger
	baseURL        string
	uploadBaseURL  string
	token          string
	driveID        string
	maxRetries     int
	initialBackoff time.Duration

	Files  *FilesService
	Shares *SharesService
}

// New returns a Client scoped to a single drive.
//
// token is an OAuth bearer token with the "drive" scope.
// driveID is the numeric drive identifier (as a string).
//
// Options override sensible defaults (stdlib http.Client, production URLs,
// slog.Default, 3 retries / 1s backoff).
func New(token, driveID string, opts ...Option) *Client {
	c := &Client{
		http:           &http.Client{Timeout: defaultHTTPTimeout},
		log:            slog.Default(),
		baseURL:        DefaultBaseURL,
		uploadBaseURL:  DefaultUploadBaseURL,
		token:          token,
		driveID:        driveID,
		maxRetries:     defaultMaxRetries,
		initialBackoff: defaultInitialBackoff,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.Files = &FilesService{client: c}
	c.Shares = &SharesService{client: c}
	return c
}
