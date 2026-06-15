package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// CommitWrite uploads buffered content (create or replace) and invalidates the
// parent listing so the new metadata shows up on the next readdir.
type CommitWrite struct {
	files service.FileWriter
	cache service.ListingCache
}

func NewCommitWrite(files service.FileWriter, cache service.ListingCache) *CommitWrite {
	return &CommitWrite{files: files, cache: cache}
}

func (u *CommitWrite) Execute(ctx context.Context, in service.UploadInput, parentID int64) (domain.FileInfo, error) {
	info, err := u.files.Upload(ctx, in)
	if err != nil {
		return domain.FileInfo{}, err
	}
	u.cache.Invalidate(parentID)
	return info, nil
}
