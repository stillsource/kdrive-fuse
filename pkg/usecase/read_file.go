package usecase

import (
	"context"
	"os"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// ReadFile opens a disk-cached, readable copy of a file's content.
type ReadFile struct {
	cache service.ContentCache
}

func NewReadFile(cache service.ContentCache) *ReadFile {
	return &ReadFile{cache: cache}
}

func (u *ReadFile) Execute(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error) {
	return u.cache.Open(ctx, fileID, lastModifiedAt, size)
}
