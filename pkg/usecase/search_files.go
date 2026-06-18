package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// SearchFiles finds files in the drive whose path matches a query.
type SearchFiles struct {
	searcher service.Searcher
}

func NewSearchFiles(searcher service.Searcher) *SearchFiles {
	return &SearchFiles{searcher: searcher}
}

func (u *SearchFiles) Execute(ctx context.Context, query string) ([]service.SearchHit, error) {
	return u.searcher.Search(ctx, query)
}
