package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// SearchFiles executes a full-text search across the drive.
type SearchFiles struct {
	searcher service.Searcher
}

func NewSearchFiles(searcher service.Searcher) *SearchFiles {
	return &SearchFiles{searcher: searcher}
}

func (u *SearchFiles) Execute(ctx context.Context, query string) ([]domain.FileInfo, error) {
	return u.searcher.Search(ctx, query)
}
