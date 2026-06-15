package service

import (
	"context"
	"os"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// ListingCache caches directory listings by folder ID.
type ListingCache interface {
	Get(folderID int64) ([]domain.FileInfo, bool)
	Set(folderID int64, files []domain.FileInfo)
	Invalidate(folderID int64)
}

// ContentCache yields a readable, disk-cached copy of a file's content.
type ContentCache interface {
	Open(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error)
}
