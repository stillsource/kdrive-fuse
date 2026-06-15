package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// ShareFile returns (creating if needed) a public share link for a file.
type ShareFile struct {
	sharer service.Sharer
}

func NewShareFile(sharer service.Sharer) *ShareFile {
	return &ShareFile{sharer: sharer}
}

func (u *ShareFile) Execute(ctx context.Context, fileID int64) (domain.ShareInfo, error) {
	return u.sharer.Publish(ctx, fileID)
}
