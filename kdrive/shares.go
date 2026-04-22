package kdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	scerr "github.com/scality/go-errors"
)

// Shares is the contract for share-link operations. SharesService implements it.
type Shares interface {
	Publish(ctx context.Context, fileID int64) (ShareInfo, error)
}

// SharesService implements Shares against the live kDrive API.
type SharesService struct {
	client *Client
}

var _ Shares = (*SharesService)(nil)

// Publish returns the first existing public share link for fileID, creating a new
// non-password-protected, non-expiring share if none exists.
func (s *SharesService) Publish(ctx context.Context, fileID int64) (ShareInfo, error) {
	if err := validateFileID(fileID); err != nil {
		return ShareInfo{}, err
	}
	endpoint := fmt.Sprintf("/files/%d/shares", fileID)

	// Look for an existing share first.
	var existing struct {
		Data []ShareInfo `json:"data"`
	}
	if err := s.client.decodeJSON(ctx, http.MethodGet, endpoint, nil, &existing); err == nil {
		for _, sh := range existing.Data {
			if sh.ShareURL != "" {
				return sh, nil
			}
		}
	}

	payload, err := json.Marshal(map[string]any{
		"type":               "public",
		"password_protected": false,
		"expiration_date":    0,
	})
	if err != nil {
		return ShareInfo{}, scerr.Wrap(ErrServer, scerr.WithDetailf("marshal share: %v", err))
	}
	var created struct {
		Data ShareInfo `json:"data"`
	}
	if err := s.client.decodeJSON(ctx, http.MethodPost, endpoint, payload, &created); err != nil {
		return ShareInfo{}, err
	}
	if created.Data.ShareURL == "" {
		return ShareInfo{}, scerr.Wrap(ErrServer, scerr.WithDetail("share created but url empty"))
	}
	return created.Data, nil
}
