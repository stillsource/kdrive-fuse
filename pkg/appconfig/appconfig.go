// Package appconfig loads the KDRIVE_* runtime configuration shared by the
// kdrive-fuse daemon and the kdrive CLI.
package appconfig

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sethvargo/go-envconfig"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
)

// Config holds the environment knobs common to every kdrive binary.
type Config struct {
	APIToken       string `env:"KDRIVE_API_TOKEN,required"`
	DriveID        string `env:"KDRIVE_DRIVE_ID,required"`
	RootFolderID   int64  `env:"KDRIVE_ROOT_FOLDER_ID,default=1"`
	BaseURL        string `env:"KDRIVE_BASE_URL,default=https://api.infomaniak.com/2/drive"`
	UploadBaseURL  string `env:"KDRIVE_UPLOAD_BASE_URL,default=https://api.kdrive.infomaniak.com/2/drive"`
	CacheTTLSecs   int    `env:"KDRIVE_CACHE_TTL_SECONDS,default=30"`
	DiskCacheDir   string `env:"KDRIVE_DISK_CACHE_DIR,default="`
	DiskCacheMaxGB int    `env:"KDRIVE_DISK_CACHE_MAX_GB,default=2"`
	ReadOnly       bool   `env:"KDRIVE_READONLY,default=false"`
	LogFormat      string `env:"KDRIVE_LOG_FORMAT,default=text"`
	MetricsAddr    string `env:"KDRIVE_METRICS_ADDR,default="`
}

// Load reads the shared KDRIVE_* environment into a Config. Before reading, it
// auto-loads a .env file (the working-directory ".env", or the path in
// KDRIVE_ENV_FILE) if present; real environment variables always take
// precedence over the file. A real .env (secrets) must never be committed —
// see .env.example and .gitignore.
func Load(ctx context.Context) (*Config, error) {
	fileVars, err := dotEnvVars()
	if err != nil {
		return nil, fmt.Errorf("appconfig: %w", err)
	}
	return load(ctx, lookuperFor(fileVars))
}

// load is the testable core: it reads from an explicit Lookuper.
func load(ctx context.Context, l envconfig.Lookuper) (*Config, error) {
	var c Config
	if err := envconfig.ProcessWith(ctx, &envconfig.Config{Target: &c, Lookuper: l}); err != nil {
		return nil, fmt.Errorf("appconfig: %w", err)
	}
	return &c, nil
}

// CacheDir returns the configured disk-cache directory, or the default
// ~/.cache/kdrive-fuse when unset.
func (c *Config) CacheDir() string {
	if c.DiskCacheDir != "" {
		return c.DiskCacheDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "kdrive-fuse")
}

// NewLogger builds the slog logger for the configured KDRIVE_LOG_FORMAT: a JSON
// handler when set to "json" (grep-friendly with jq), otherwise a text handler.
// Any unrecognized value falls back to text so a typo never crashes a daemon.
func (c *Config) NewLogger(w io.Writer) *slog.Logger {
	if strings.EqualFold(c.LogFormat, "json") {
		return slog.New(slog.NewJSONHandler(w, nil))
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

// DI builds the di.Config used to construct the application container,
// attaching the given logger.
func (c *Config) DI(logger *slog.Logger) di.Config {
	return di.Config{
		Token:          c.APIToken,
		DriveID:        c.DriveID,
		RootFolderID:   c.RootFolderID,
		BaseURL:        c.BaseURL,
		UploadBaseURL:  c.UploadBaseURL,
		CacheTTL:       time.Duration(c.CacheTTLSecs) * time.Second,
		DiskCacheDir:   c.CacheDir(),
		DiskCacheBytes: int64(c.DiskCacheMaxGB) * 1024 * 1024 * 1024,
		Logger:         logger,
		ReadOnly:       c.ReadOnly,
	}
}
