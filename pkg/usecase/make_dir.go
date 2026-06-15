package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// MakeDir creates a directory and invalidates the parent listing.
type MakeDir struct {
	files service.FileManager
	cache service.ListingCache
}

func NewMakeDir(files service.FileManager, cache service.ListingCache) *MakeDir {
	return &MakeDir{files: files, cache: cache}
}

func (u *MakeDir) Execute(ctx context.Context, parentID int64, name string) (domain.FileInfo, error) {
	info, err := u.files.Mkdir(ctx, parentID, name)
	if err != nil {
		return domain.FileInfo{}, err
	}
	u.cache.Invalidate(parentID)
	return info, nil
}
