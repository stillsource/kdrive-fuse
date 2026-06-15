package service

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Sharer publishes a public share link for a file.
type Sharer interface {
	Publish(ctx context.Context, fileID int64) (domain.ShareInfo, error)
}
