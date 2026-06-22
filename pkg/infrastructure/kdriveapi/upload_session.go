package kdriveapi

import (
	"bytes"
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
	"golang.org/x/sync/errgroup"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi/internal/hash"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// uploadSessionThreshold is the size ABOVE which Upload switches to the chunked
// upload-session flow (strictly greater: a file of exactly this size stays
// single-shot, matching the kDrive client's bigFileThreshold dispatch).
// chunkSize is the fixed per-chunk size, within the server-accepted 10–100 MB.
// Both are vars (not const) so tests can shrink them without huge allocations.
var (
	uploadSessionThreshold int64 = 16 * 1024 * 1024
	chunkSize              int64 = 16 * 1024 * 1024
)

// uploadSession uploads in.Body via the kDrive upload-session flow:
// POST /upload/session/start -> POST /upload/session/{token}/chunk * N ->
// POST /upload/session/{token}/finish. On any unrecoverable failure it
// best-effort cancels the session (DELETE /upload/session/{token}).
//
// When the body implements io.ReaderAt, chunks are uploaded in parallel using
// s.client.uploadParallelism goroutines (default 4). Otherwise it falls back to
// the sequential loop so streams and other non-seekable readers still work.
func (s *FilesService) uploadSession(ctx context.Context, in service.UploadInput) (domain.FileInfo, error) {
	if _, err := in.Body.Seek(0, io.SeekStart); err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
			scerr.WithDetail("upload session: seek to start"), scerr.CausedBy(err))
	}
	totalChunks := (in.Size + chunkSize - 1) / chunkSize
	if totalChunks == 0 {
		totalChunks = 1
	}

	token, err := s.sessionStart(ctx, in, totalChunks)
	if err != nil {
		return domain.FileInfo{}, err
	}

	ra, canParallel := in.Body.(io.ReaderAt)
	if !canParallel {
		return s.uploadSessionSequential(ctx, in, token, totalChunks)
	}
	return s.uploadSessionParallel(ctx, in, token, totalChunks, ra)
}

// uploadSessionParallel uploads chunks concurrently when the body is an
// io.ReaderAt. Memory is bounded to parallelism × chunkSize.
func (s *FilesService) uploadSessionParallel(
	ctx context.Context, in service.UploadInput, token string, totalChunks int64, ra io.ReaderAt,
) (domain.FileInfo, error) {
	parallelism := s.client.uploadParallelism
	if parallelism <= 0 {
		parallelism = 4
	}

	// Pre-allocate the hash slice so workers write at distinct indices — no mutex.
	chunkHashes := make([]string, totalChunks)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(parallelism)

	for n := int64(1); n <= totalChunks; n++ {
		n := n // capture
		offset := (n - 1) * chunkSize
		clen := chunkSize
		remaining := in.Size - offset
		if remaining < clen {
			clen = remaining
		}

		g.Go(func() error {
			buf := make([]byte, clen)
			if _, err := ra.ReadAt(buf, offset); err != nil {
				return scerr.Wrap(domain.ErrServer,
					scerr.WithDetailf("upload session: read chunk %d", n), scerr.CausedBy(err))
			}
			chunkHash, err := hash.XXH3Stream(bytes.NewReader(buf))
			if err != nil {
				return scerr.Wrap(domain.ErrServer,
					scerr.WithDetailf("upload session: hash chunk %d", n), scerr.CausedBy(err))
			}
			chunkHashes[n-1] = chunkHash
			return s.sessionChunk(gCtx, token, n, clen, chunkHash, buf)
		})
	}

	if err := g.Wait(); err != nil {
		s.sessionCancel(ctx, token)
		return domain.FileInfo{}, err
	}

	// Build total_chunk_hash in chunk order from the pre-filled slice.
	hasher := hash.NewChunkHasher()
	for _, h := range chunkHashes {
		hasher.Add(h)
	}

	info, err := s.sessionFinish(ctx, token, totalChunks, hasher.Sum())
	if err != nil {
		s.sessionCancel(ctx, token)
		return domain.FileInfo{}, err
	}
	s.client.addUploaded(in.Size)
	return info, nil
}

// uploadSessionSequential is the original sequential chunk loop, used when the
// body is not an io.ReaderAt (e.g. a plain io.Reader wrapper).
func (s *FilesService) uploadSessionSequential(
	ctx context.Context, in service.UploadInput, token string, totalChunks int64,
) (domain.FileInfo, error) {
	hasher := hash.NewChunkHasher()
	remaining := in.Size
	for n := int64(1); n <= totalChunks; n++ {
		clen := chunkSize
		if remaining < clen {
			clen = remaining
		}
		buf := make([]byte, clen)
		if _, err := io.ReadFull(in.Body, buf); err != nil {
			s.sessionCancel(ctx, token)
			return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
				scerr.WithDetailf("upload session: read chunk %d", n), scerr.CausedBy(err))
		}
		chunkHash, err := hash.XXH3Stream(bytes.NewReader(buf))
		if err != nil {
			s.sessionCancel(ctx, token)
			return domain.FileInfo{}, scerr.Wrap(domain.ErrServer,
				scerr.WithDetailf("upload session: hash chunk %d", n), scerr.CausedBy(err))
		}
		hasher.Add(chunkHash)
		if err := s.sessionChunk(ctx, token, n, clen, chunkHash, buf); err != nil {
			s.sessionCancel(ctx, token)
			return domain.FileInfo{}, err
		}
		remaining -= clen
	}

	info, err := s.sessionFinish(ctx, token, totalChunks, hasher.Sum())
	if err != nil {
		s.sessionCancel(ctx, token)
		return domain.FileInfo{}, err
	}
	s.client.addUploaded(in.Size)
	return info, nil
}

func (s *FilesService) sessionURL(path string) string {
	return s.client.uploadBaseURL + "/" + s.client.driveID + path
}

func (s *FilesService) sessionStart(ctx context.Context, in service.UploadInput, totalChunks int64) (string, error) {
	body := map[string]string{
		"total_size":   strconv.FormatInt(in.Size, 10),
		"total_chunks": strconv.FormatInt(totalChunks, 10),
	}
	if in.ExistingFileID > 0 {
		body["file_id"] = strconv.FormatInt(in.ExistingFileID, 10)
	} else {
		body["file_name"] = in.Name
		body["directory_id"] = strconv.FormatInt(in.ParentID, 10)
		body["conflict"] = conflictMode(in.Conflict)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", scerr.Wrap(domain.ErrServer, scerr.WithDetailf("marshal session start: %v", err))
	}
	resp, err := s.doSessionAttempt(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL("/upload/session/start"), bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(jsonBody))
		return req, nil
	}, "session start")
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	drainAndClose(resp.Body)
	if err != nil {
		return "", scerr.Wrap(domain.ErrServer, scerr.WithDetail("decode session start"), scerr.CausedBy(err))
	}
	if out.Data.Token == "" {
		return "", scerr.Wrap(domain.ErrServer, scerr.WithDetail("session start: empty token"))
	}
	return out.Data.Token, nil
}

func (s *FilesService) sessionChunk(ctx context.Context, token string, number, size int64, chunkHash string, buf []byte) error {
	q := url.Values{}
	q.Set("chunk_number", strconv.FormatInt(number, 10))
	q.Set("chunk_size", strconv.FormatInt(size, 10))
	q.Set("chunk_hash", chunkHash)
	reqURL := s.sessionURL("/upload/session/"+token+"/chunk") + "?" + q.Encode()
	resp, err := s.doSessionAttempt(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = size
		return req, nil
	}, fmt.Sprintf("session chunk %d", number))
	if err != nil {
		return err
	}
	drainAndClose(resp.Body)
	return nil
}

func (s *FilesService) sessionFinish(ctx context.Context, token string, totalChunks int64, totalHash string) (domain.FileInfo, error) {
	now := time.Now().Unix()
	body := map[string]any{
		"total_chunk_hash": totalHash,
		"total_chunks":     totalChunks,
		"created_at":       now,
		"last_modified_at": now,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("marshal session finish: %v", err))
	}
	resp, err := s.doSessionAttempt(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL("/upload/session/"+token+"/finish"), bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.client.token)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(jsonBody))
		return req, nil
	}, "session finish")
	if err != nil {
		return domain.FileInfo{}, err
	}
	var out struct {
		Data domain.FileInfo `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	drainAndClose(resp.Body)
	if err != nil {
		return domain.FileInfo{}, scerr.Wrap(domain.ErrServer, scerr.WithDetail("decode session finish"), scerr.CausedBy(err))
	}
	return out.Data, nil
}

// sessionCancel best-effort deletes a session after an unrecoverable failure.
// Errors are logged, not returned (the caller already holds the real error).
func (s *FilesService) sessionCancel(ctx context.Context, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.sessionURL("/upload/session/"+token), nil)
	if err != nil {
		s.client.log.Warn("kdrive session cancel build", slog.String("err", err.Error()))
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.client.token)
	resp, err := s.client.uploadHTTP.Do(req)
	if err != nil {
		s.client.log.Warn("kdrive session cancel", slog.String("err", err.Error()))
		return
	}
	drainAndClose(resp.Body)
}

// doSessionAttempt runs build() with transient retry (5xx / 429 / transport),
// mirroring the single-shot loop. build must produce a FRESH request (fresh body)
// each call so the body replays on retry. On success the caller owns resp.Body.
func (s *FilesService) doSessionAttempt(ctx context.Context, build func() (*http.Request, error), op string) (*http.Response, error) {
	var lastErr error
	backoff := s.client.initialBackoff
	for attempt := 0; attempt <= s.client.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
		}
		req, err := build()
		if err != nil {
			return nil, scerr.Wrap(domain.ErrValidation, scerr.WithDetailf("build %s req: %v", op, err))
		}
		resp, err := s.client.uploadHTTP.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryableError(err) {
				return nil, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("%s transport", op), scerr.CausedBy(err))
			}
			s.client.log.Warn("kdrive session request failed",
				slog.String("op", op), slog.Int("attempt", attempt+1), slog.String("err", err.Error()))
			continue
		}
		if shouldRetry(resp.StatusCode) && attempt < s.client.maxRetries {
			drainAndClose(resp.Body)
			lastErr = fmt.Errorf("transient %d", resp.StatusCode)
			s.client.log.Warn("kdrive session transient status",
				slog.String("op", op), slog.Int("status", resp.StatusCode), slog.Int("attempt", attempt+1))
			continue
		}
		if resp.StatusCode >= 400 {
			apiErr := fromResponse(resp, op)
			drainAndClose(resp.Body)
			return nil, apiErr
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("kdrive: %s retries exhausted", op)
	}
	return nil, scerr.Wrap(domain.ErrServer, scerr.WithDetailf("%s retries exhausted", op), scerr.CausedBy(lastErr))
}
