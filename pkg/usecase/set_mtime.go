package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// SetMtime persists a new last-modified timestamp for a file via the kDrive
// API and invalidates the parent folder's cached listing so subsequent ls
// reflects the updated time.
type SetMtime struct {
	files service.FileManager
	cache service.ListingCache
}

func NewSetMtime(files service.FileManager, cache service.ListingCache) *SetMtime {
	return &SetMtime{files: files, cache: cache}
}

// Execute calls SetModifiedAt on the remote then invalidates the parent listing.
func (u *SetMtime) Execute(ctx context.Context, fileID, modifiedAt, parentID int64) (domain.FileInfo, error) {
	info, err := u.files.SetModifiedAt(ctx, fileID, modifiedAt)
	if err != nil {
		return domain.FileInfo{}, err
	}
	u.cache.Invalidate(parentID)
	return info, nil
}
