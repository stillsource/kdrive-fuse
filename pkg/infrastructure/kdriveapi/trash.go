package kdriveapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// ListTrash returns the drive's trashed items, paging until exhausted.
func (s *FilesService) ListTrash(ctx context.Context) ([]domain.FileInfo, error) {
	var all []domain.FileInfo
	for page := 1; ; page++ {
		var resp struct {
			Data []domain.FileInfo `json:"data"`
		}
		endpoint := fmt.Sprintf("/trash?per_page=%d&page=%d", listPageSize, page)
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

// RestoreTrash restores a trashed file or folder to its original location.
func (s *FilesService) RestoreTrash(ctx context.Context, fileID int64) error {
	if err := domain.ValidateFileID(fileID); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("/trash/%d/restore", fileID)
	resp, err := s.client.do(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}

// PurgeTrash permanently deletes one trashed item.
func (s *FilesService) PurgeTrash(ctx context.Context, fileID int64) error {
	if err := domain.ValidateFileID(fileID); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("/trash/%d", fileID)
	resp, err := s.client.do(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}

// EmptyTrash permanently empties the whole trash.
func (s *FilesService) EmptyTrash(ctx context.Context) error {
	resp, err := s.client.do(ctx, http.MethodDelete, "/trash", nil)
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}
