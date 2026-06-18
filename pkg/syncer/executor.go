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

// fileMover relocates a remote file: to another folder (Move) and/or to a new
// name (Rename).
type fileMover interface {
	Move(ctx context.Context, fileID, destDirID int64) error
	Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error)
}

// PushExecutor is the concrete Executor: it resolves the remote parent folder,
// opens the local file, and uploads or deletes through the service ports.
type PushExecutor struct {
	localRoot string
	resolver  *remoteindex.Resolver
	writer    service.FileWriter
	manager   fileDeleter
	lister    remoteindex.Lister
	mover     fileMover
}

// NewPushExecutor builds a PushExecutor that reads files under localRoot, places
// new files under the folders resolver resolves/creates, uploads via w, and
// deletes via mgr.
func NewPushExecutor(localRoot string, resolver *remoteindex.Resolver, w service.FileWriter, mgr fileDeleter, lister remoteindex.Lister, mover fileMover) *PushExecutor {
	return &PushExecutor{localRoot: localRoot, resolver: resolver, writer: w, manager: mgr, lister: lister, mover: mover}
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
	// A prior interrupted run already created this file, so reconcile to an
	// overwrite-by-id (the re-run is idempotent). findChild matches purely by
	// name, so if the colliding remote file was created out-of-band (not by us)
	// this overwrites it — acceptable for a one-way, local-is-authoritative push,
	// and recoverable from the kDrive trash. Detecting genuine out-of-band drift
	// before clobbering is the job of the push drift guard (a later change).
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
// name, if present. kDrive can technically hold duplicate names in one folder;
// the first non-dir match wins (this tool's own uploads use conflict=error, so
// it never creates duplicates itself).
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

// Delete removes remote file remoteID. A delete of an already-gone file
// (domain.ErrNotFound) is treated as success: the desired end-state (the file is
// gone) is already reached, so a re-run after a crash that deleted it without
// checkpointing the manifest completes cleanly instead of reporting a failure.
func (e *PushExecutor) Delete(ctx context.Context, _ string, remoteID int64) error {
	if err := e.manager.Delete(ctx, remoteID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

// Move relocates remoteID from fromRel to toRel: Move to the destination folder
// if the parent changed, then Rename if the base name changed.
func (e *PushExecutor) Move(ctx context.Context, fromRel, toRel string, remoteID int64) error {
	if path.Dir(fromRel) != path.Dir(toRel) {
		destParent, err := e.resolver.Resolve(ctx, path.Dir(toRel))
		if err != nil {
			return fmt.Errorf("resolve dest of %s: %w", toRel, err)
		}
		if err := e.mover.Move(ctx, remoteID, destParent); err != nil {
			return err
		}
	}
	if path.Base(fromRel) != path.Base(toRel) {
		if _, err := e.mover.Rename(ctx, remoteID, path.Base(toRel)); err != nil {
			return err
		}
	}
	return nil
}

func (e *PushExecutor) local(rel string) string {
	return filepath.Join(e.localRoot, filepath.FromSlash(rel))
}
