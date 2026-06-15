package kdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/kdrive/internal/hash"
	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// Files is the contract for file and directory operations. FilesService implements it.
// Consumers that want to mock the lib in their tests should depend on this interface,
// not on *FilesService directly. kdrivefakes.FilesFake is a ready-made implementation
// suitable for unit tests.
type Files interface {
	List(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
	Stat(ctx context.Context, fileID int64) (domain.FileInfo, error)
	Download(ctx context.Context, fileID int64) ([]byte, error)
	DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error)
	Upload(ctx context.Context, in service.UploadInput) (domain.FileInfo, error)
	Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
	Delete(ctx context.Context, fileID int64) error
	Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error)
	Move(ctx context.Context, fileID, destDirID int64) error
}

// FilesService implements Files against the live kDrive API.
type FilesService struct {
	client *Client
}

// Static interface check — ensures FilesService satisfies Files.
var _ Files = (*FilesService)(nil)

// listPageSize is the per-page parameter used by List (kDrive's default is 10).
const listPageSize = 500

// List returns the direct children of folderID, paging until exhausted.
func (s *FilesService) List(ctx context.Context, folderID int64) ([]domain.FileInfo, error) {
	if err := domain.ValidateFolderID(folderID); err != nil {
		return nil, err
	}
	var all []domain.FileInfo
	for page := 1; ; page++ {
		var resp struct {
			Data []domain.FileInfo `json:"data"`
		}
		endpoint := fmt.Sprintf("/files/%d/files?per_page=%d&page=%d", folderID, listPageSize, page)
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

// Stat fetches full metadata for a single file or directory.
func (s *FilesService) Stat(ctx context.Context, fileID int64) (domain.FileInfo, error) {
	if err := domain.ValidateFileID(fileID); err != nil {
		return domain.FileInfo{}, err
	}
	var resp struct {
		Data domain.FileInfo `json:"data"`
	}
	endpoint := fmt.Sprintf("/files/%d", fileID)
	if err := s.client.decodeJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return domain.FileInfo{}, err
	}
	return resp.Data, nil
}

// Download fetches the full content of a file into memory.
// Small files only; for large files use DownloadStream.
func (s *FilesService) Download(ctx context.Context, fileID int64) ([]byte, error) {
	rc, err := s.DownloadStream(ctx, fileID, 0, 0)
	if err != nil {
		return nil, err
	}
	defer rc.Close() //nolint:errcheck // cleanup only
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, scerr.Wrap(domain.ErrServer,
			scerr.WithDetailf("read download body: %v", err),
			scerr.CausedBy(err),
		)
	}
	return data, nil
}

// DownloadStream returns a streaming reader for file content.
// If length > 0, requests Range bytes=off-(off+length-1).
// If length == 0 and off > 0, requests Range bytes=off-.
// If length == 0 and off == 0, returns the full body.
// Caller must Close.
func (s *FilesService) DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error) {
	if err := domain.ValidateFileID(fileID); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("/files/%d/download", fileID)
	var headers map[string]string
	switch {
	case length > 0:
		headers = map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", off, off+length-1)}
	case off > 0:
		headers = map[string]string{"Range": fmt.Sprintf("bytes=%d-", off)}
	}
	resp, err := s.client.doRaw(ctx, http.MethodGet,
		s.client.baseURL+"/"+s.client.driveID+endpoint,
		"", nil, headers)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Upload sends the content of in.Body as a kDrive file using the single-shot
// upload endpoint. If in.ExistingFileID > 0, replaces that file's content;
// otherwise creates a new file named in.Name in in.ParentID.
func (s *FilesService) Upload(ctx context.Context, in service.UploadInput) (domain.FileInfo, error) {
	if in.Body == nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrValidation, scerr.WithDetail("upload: body required"))
	}
	if in.Size < 0 {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrValidation, scerr.WithDetail("upload: size must be >= 0"))
	}
	if in.ExistingFileID > 0 {
		if err := domain.ValidateFileID(in.ExistingFileID); err != nil {
			return domain.FileInfo{}, err
		}
	} else {
		if err := domain.ValidateFolderID(in.ParentID); err != nil {
			return domain.FileInfo{}, err
		}
		if err := domain.ValidateName(in.Name); err != nil {
			return domain.FileInfo{}, err
		}
	}

	if _, err := in.Body.Seek(0, io.SeekStart); err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrValidation,
			scerr.WithDetail("upload body: seek to start for hashing"),
			scerr.CausedBy(err),
		)
	}
	hashStr, err := hash.XXH3Stream(in.Body)
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetail("hash upload body"), scerr.CausedBy(err))
	}
	if _, err := in.Body.Seek(0, io.SeekStart); err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
			scerr.WithDetail("upload body: seek to start for upload"),
			scerr.CausedBy(err),
		)
	}

	q := url.Values{}
	q.Set("total_size", strconv.FormatInt(in.Size, 10))
	q.Set("total_chunk_hash", hashStr)
	now := strconv.FormatInt(time.Now().Unix(), 10)
	q.Set("last_modified_at", now)
	if in.ExistingFileID > 0 {
		q.Set("file_id", strconv.FormatInt(in.ExistingFileID, 10))
	} else {
		q.Set("file_name", in.Name)
		q.Set("directory_id", strconv.FormatInt(in.ParentID, 10))
		q.Set("created_at", now)
		q.Set("conflict", "error")
	}

	endpoint := "/upload?" + q.Encode()
	reqURL := s.client.uploadBaseURL + "/" + s.client.driveID + endpoint

	// The body is an io.ReadSeeker, so we rewind it and retry transient failures
	// (429 / 5xx / transport errors) with exponential backoff, mirroring doRaw.
	// A non-transient 4xx (e.g. hash mismatch) is returned without retry.
	var lastErr error
	backoff := s.client.initialBackoff
	for attempt := 0; attempt <= s.client.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return domain.FileInfo{}, err
			}
			backoff *= 2
			if _, err := in.Body.Seek(0, io.SeekStart); err != nil {
				return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
					scerr.WithDetail("upload body: seek to start for retry"),
					scerr.CausedBy(err),
				)
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, in.Body)
		if err != nil {
			return domain.FileInfo{}, scerr.Wrap(domain.ErrValidation, scerr.WithDetailf("build upload req: %v", err))
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = in.Size

		resp, err := s.client.http.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryableError(err) {
				return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
					scerr.WithDetail("upload transport"),
					scerr.CausedBy(err),
				)
			}
			s.client.log.Warn("kdrive upload failed",
				slog.Int("attempt", attempt+1),
				slog.String("err", err.Error()),
			)
			continue
		}

		if shouldRetry(resp.StatusCode) && attempt < s.client.maxRetries {
			drainAndClose(resp.Body)
			lastErr = fmt.Errorf("transient %d", resp.StatusCode)
			s.client.log.Warn("kdrive upload transient status",
				slog.Int("status", resp.StatusCode),
				slog.Int("attempt", attempt+1),
			)
			continue
		}

		if resp.StatusCode >= 400 {
			apiErr := fromResponse(resp, "POST /upload")
			drainAndClose(resp.Body)
			return domain.FileInfo{}, apiErr
		}

		var out struct {
			Data domain.FileInfo `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		drainAndClose(resp.Body)
		if err != nil {
			return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
				scerr.WithDetail("decode upload response"),
				scerr.CausedBy(err),
			)
		}
		return out.Data, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("kdrive: upload retries exhausted")
	}
	return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
		scerr.WithDetail("upload retries exhausted"),
		scerr.CausedBy(lastErr),
	)
}

// Mkdir creates a directory named name inside parentID.
func (s *FilesService) Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error) {
	if err := domain.ValidateFolderID(parentID); err != nil {
		return domain.FileInfo{}, err
	}
	if err := domain.ValidateName(name); err != nil {
		return domain.FileInfo{}, err
	}
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("marshal mkdir: %v", err))
	}
	endpoint := fmt.Sprintf("/files/%d/directory", parentID)
	var resp struct {
		Data domain.FileInfo `json:"data"`
	}
	if err := s.client.decodeJSON(ctx, http.MethodPost, endpoint, body, &resp); err != nil {
		return domain.FileInfo{}, err
	}
	return resp.Data, nil
}

// Delete moves a file or directory to the kDrive trash (soft delete).
func (s *FilesService) Delete(ctx context.Context, fileID int64) error {
	if err := domain.ValidateFileID(fileID); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("/files/%d", fileID)
	resp, err := s.client.do(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}

// Rename changes a file or directory's name in place.
func (s *FilesService) Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error) {
	if err := domain.ValidateFileID(fileID); err != nil {
		return domain.FileInfo{}, err
	}
	if err := domain.ValidateName(newName); err != nil {
		return domain.FileInfo{}, err
	}
	body, err := json.Marshal(map[string]string{"name": newName})
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("marshal rename: %v", err))
	}
	endpoint := fmt.Sprintf("/files/%d/rename", fileID)
	var resp struct {
		Data domain.FileInfo `json:"data"`
	}
	if err := s.client.decodeJSON(ctx, http.MethodPost, endpoint, body, &resp); err != nil {
		return domain.FileInfo{}, err
	}
	return resp.Data, nil
}

// Move relocates a file or directory into destDirID (preserves its name).
func (s *FilesService) Move(ctx context.Context, fileID, destDirID int64) error {
	if err := domain.ValidateFileID(fileID); err != nil {
		return err
	}
	if err := domain.ValidateFolderID(destDirID); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("/files/%d/move/%d", fileID, destDirID)
	resp, err := s.client.do(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}
