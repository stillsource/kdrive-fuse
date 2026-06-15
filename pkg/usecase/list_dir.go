package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// ListDir returns a directory's children, backed by the listing cache.
type ListDir struct {
	files service.FileReader
	cache service.ListingCache
}

func NewListDir(files service.FileReader, cache service.ListingCache) *ListDir {
	return &ListDir{files: files, cache: cache}
}

func (u *ListDir) Execute(ctx context.Context, folderID int64) ([]domain.FileInfo, error) {
	if files, ok := u.cache.Get(folderID); ok {
		return files, nil
	}
	files, err := u.files.List(ctx, folderID)
	if err != nil {
		return nil, err
	}
	u.cache.Set(folderID, files)
	return files, nil
}
