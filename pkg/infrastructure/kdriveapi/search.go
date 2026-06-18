package kdriveapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Search returns files matching the full-text query, paging until exhausted.
func (s *FilesService) Search(ctx context.Context, query string) ([]domain.FileInfo, error) {
	if query == "" {
		return nil, scerr.Wrap(domain.ErrValidation, scerr.WithDetail("search: query must not be empty"))
	}
	var all []domain.FileInfo
	for page := 1; ; page++ {
		var resp struct {
			Data []domain.FileInfo `json:"data"`
		}
		endpoint := fmt.Sprintf("/files/search?q=%s&per_page=%d&page=%d",
			url.QueryEscape(query), listPageSize, page)
		if err := s.client.decodeJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		if len(resp.Data) < listPageSize {
			break
		}
	}
	return all, nil
}
