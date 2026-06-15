package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// DeleteEntry soft-deletes a file or directory and invalidates the parent listing.
type DeleteEntry struct {
	files service.FileManager
	cache service.ListingCache
}

func NewDeleteEntry(files service.FileManager, cache service.ListingCache) *DeleteEntry {
	return &DeleteEntry{files: files, cache: cache}
}

func (u *DeleteEntry) Execute(ctx context.Context, fileID, parentID int64) error {
	if err := u.files.Delete(ctx, fileID); err != nil {
		return err
	}
	u.cache.Invalidate(parentID)
	return nil
}
