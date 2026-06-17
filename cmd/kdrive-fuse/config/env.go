// Package config loads the kdrive-fuse daemon's FUSE-specific configuration.
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/sethvargo/go-envconfig"
)

// FUSE holds the mount-only configuration specific to the daemon.
type FUSE struct {
	Mount string `env:"KDRIVE_MOUNT,required"`
}

// LoadFUSE reads KDRIVE_MOUNT and ensures the mount directory exists.
func LoadFUSE(ctx context.Context) (*FUSE, error) {
	var c FUSE
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
