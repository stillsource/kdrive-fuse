package service

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Searcher runs a full-text search over the drive.
type Searcher interface {
	Search(ctx context.Context, query string) ([]domain.FileInfo, error)
}
