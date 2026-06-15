package usecase

import (
	"context"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// SeedContent streams the current remote content of a file (for partial edits).
type SeedContent struct {
	files service.FileReader
}

func NewSeedContent(files service.FileReader) *SeedContent {
	return &SeedContent{files: files}
}

func (u *SeedContent) Execute(ctx context.Context, fileID int64) (io.ReadCloser, error) {
	return u.files.DownloadStream(ctx, fileID, 0, 0)
}
