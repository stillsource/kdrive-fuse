// Package di is the infrastructure composition root. It builds the object graph
// the binary needs — the kDrive API client, the disk-backed content cache, the
// FUSE composition root, and the root directory node — from a single Config,
// using lazy memoized getters so each dependency is constructed exactly once.
//
// It delegates use-case wiring to fuse.NewKDriveFS (the FUSE composition root),
// so the use-case graph lives in one place and is not duplicated here.
package di

import (
	"log/slog"
	"time"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/metrics"
	"github.com/stillsource/kdrive-fuse/pkg/presentation/fuse"
)

// Config holds the inputs the container needs to reproduce the binary's wiring.
type Config struct {
	// Token is the OAuth bearer token with the "drive" scope.
	Token string
	// DriveID is the numeric drive identifier (as a string).
	DriveID string
	// RootFolderID is the kDrive folder ID mounted at the filesystem root.
	RootFolderID int64

	// BaseURL optionally overrides the list/stat/download/rename/mkdir/delete
	// host. Empty leaves the client default.
	BaseURL string
	// UploadBaseURL optionally overrides the upload host. Empty leaves the
	// client default.
	UploadBaseURL string

	// CacheTTL is the directory-listing cache time-to-live.
	CacheTTL time.Duration

	// DiskCacheDir is the directory backing the content cache.
	DiskCacheDir string
	// DiskCacheBytes is the content cache's LRU budget in bytes.
	DiskCacheBytes int64

	// Logger is the slog logger handed to the API client for request
	// diagnostics. Nil leaves the client default (slog.Default).
	Logger *slog.Logger

	// ReadOnly, when true, makes the FUSE filesystem reject all mutating
	// operations with EROFS. Reads are unaffected.
	ReadOnly bool

	// Metrics is an optional metrics registry. nil disables metrics collection.
	Metrics *metrics.Registry
}

// Container builds and memoizes the infrastructure object graph from a Config.
// It is not safe for concurrent construction; build the graph once at boot.
type Container struct {
	cfg Config

	client  *kdriveapi.Client
	content *contentcache.DiskCache
	kdfs    *fuse.KDriveFS
}

// NewContainer returns a Container that builds the object graph from cfg.
func NewContainer(cfg Config) *Container {
	return &Container{cfg: cfg}
}
