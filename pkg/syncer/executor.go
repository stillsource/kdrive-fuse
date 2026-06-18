package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// fileDeleter is the subset of service.FileManager needed by PushExecutor.
type fileDeleter interface {
	Delete(ctx context.Context, fileID int64) error
}

// PushExecutor is the concrete Executor: it resolves the remote parent folder,
// opens the local file, and uploads or deletes through the service ports.
type PushExecutor struct {
	localRoot string
	resolver  *remoteindex.Resolver
	writer    service.FileWriter
	manager   fileDeleter
	lister    remoteindex.Lister
}

// NewPushExecutor builds a PushExecutor that reads files under localRoot, places
// new files under the folders resolver resolves/creates, uploads via w, and
// deletes via mgr.
func NewPushExecutor(localRoot string, resolver *remoteindex.Resolver, w service.FileWriter, mgr fileDeleter, lister remoteindex.Lister) *PushExecutor {
	return &PushExecutor{localRoot: localRoot, resolver: resolver, writer: w, manager: mgr, lister: lister}
}

// Upload creates a new remote file from localRoot/rel under its resolved parent.
// If the create conflicts because a prior interrupted run already uploaded this
// file (conflict=error), it reconciles by finding the file under the parent and
// overwriting it by id, so a re-run is idempotent.
func (e *PushExecutor) Upload(ctx context.Context, rel string, size int64) (int64, int64, error) {
	parentID, err := e.resolver.Resolve(ctx, path.Dir(rel))
	if err != nil {
		return 0, 0, fmt.Errorf("resolve parent of %s: %w", rel, err)
	}
	f, err := os.Open(e.local(rel))
	if err != nil {
		return 0, 0, err
	}
	info, err := e.writer.Upload(ctx, service.UploadInput{
		ParentID: parentID,
		Name:     path.Base(rel),
		Body:     f,
		Size:     size,
	})
	_ = f.Close() //nolint:errcheck // read-only
	if err == nil {
		return info.ID, info.LastModifiedAt, nil
	}
	if !errors.Is(err, domain.ErrConflict) {
		return 0, 0, err
	}
	// A prior run already created this file. Find it and overwrite by id.
	existingID, found, lerr := e.findChild(ctx, parentID, path.Base(rel))
	if lerr != nil {
		return 0, 0, lerr
	}
	if !found {
		return 0, 0, err // can't reconcile; surface the original conflict
	}
	mtime, oerr := e.Overwrite(ctx, rel, existingID, size)
	if oerr != nil {
		return 0, 0, oerr
	}
	return existingID, mtime, nil
}

// findChild lists parentID and returns the id of the non-directory child named
// name, if present.
func (e *PushExecutor) findChild(ctx context.Context, parentID int64, name string) (int64, bool, error) {
	children, err := e.lister.List(ctx, parentID)
	if err != nil {
		return 0, false, err
	}
	for _, c := range children {
		if !c.IsDir() && c.Name == name {
			return c.ID, true, nil
		}
	}
	return 0, false, nil
}

// Overwrite replaces remote file remoteID with the content of localRoot/rel.
func (e *PushExecutor) Overwrite(ctx context.Context, rel string, remoteID, size int64) (int64, error) {
	f, err := os.Open(e.local(rel))
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only
	info, err := e.writer.Upload(ctx, service.UploadInput{
		ExistingFileID: remoteID,
		Name:           path.Base(rel),
		Body:           f,
		Size:           size,
	})
	if err != nil {
		return 0, err
	}
	return info.LastModifiedAt, nil
}

// Delete removes remote file remoteID.
func (e *PushExecutor) Delete(ctx context.Context, _ string, remoteID int64) error {
	return e.manager.Delete(ctx, remoteID)
}

func (e *PushExecutor) local(rel string) string {
	return filepath.Join(e.localRoot, filepath.FromSlash(rel))
}
