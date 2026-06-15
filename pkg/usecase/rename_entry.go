package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// RenameEntry moves and/or renames an entry across the given parents,
// mirroring the FUSE Rename contract (move-then-rename), and invalidates
// both affected listings.
type RenameEntry struct {
	files service.FileManager
	cache service.ListingCache
}

func NewRenameEntry(files service.FileManager, cache service.ListingCache) *RenameEntry {
	return &RenameEntry{files: files, cache: cache}
}

// Execute relocates fileID into destDirID when it differs from srcDirID, then
// renames to newName when it differs from oldName. Caches for both dirs are
// invalidated on success.
func (u *RenameEntry) Execute(ctx context.Context, fileID, srcDirID, destDirID int64, oldName, newName string) error {
	if destDirID != srcDirID {
		if err := u.files.Move(ctx, fileID, destDirID); err != nil {
			return err
		}
	}
	if newName != oldName {
		if _, err := u.files.Rename(ctx, fileID, newName); err != nil {
			return err
		}
	}
	u.cache.Invalidate(srcDirID)
	u.cache.Invalidate(destDirID)
	return nil
}
