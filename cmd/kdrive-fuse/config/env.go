// Package config loads the kdrive-fuse binary's runtime configuration
// from environment variables.
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/sethvargo/go-envconfig"
)

// Config holds all knobs exposed to the binary at boot.
type Config struct {
	APIToken       string `env:"KDRIVE_API_TOKEN,required"`
	DriveID        string `env:"KDRIVE_DRIVE_ID,required"`
	RootFolderID   int64  `env:"KDRIVE_ROOT_FOLDER_ID,default=1"`
	Mount          string `env:"KDRIVE_MOUNT,required"`
	BaseURL        string `env:"KDRIVE_BASE_URL,default=https://api.infomaniak.com/2/drive"`
	UploadBaseURL  string `env:"KDRIVE_UPLOAD_BASE_URL,default=https://api.kdrive.infomaniak.com/2/drive"`
	CacheTTLSecs   int    `env:"KDRIVE_CACHE_TTL_SECONDS,default=30"`
	DiskCacheDir   string `env:"KDRIVE_DISK_CACHE_DIR,default="`
	DiskCacheMaxGB int    `env:"KDRIVE_DISK_CACHE_MAX_GB,default=2"`
}

// Load reads environment variables into a Config and ensures the mount dir exists.
func Load(ctx context.Context) (*Config, error) {
	var c Config
	if err := envconfig.Process(ctx, &c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := os.Stat(c.Mount); os.IsNotExist(err) {
		if err := os.MkdirAll(c.Mount, 0o755); err != nil {
			return nil, fmt.Errorf("create mount dir %s: %w", c.Mount, err)
		}
	}
	return &c, nil
}
