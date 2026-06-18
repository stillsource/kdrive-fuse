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
	stater    fileStater
}

// NewPushExecutor builds a PushExecutor that reads files under localRoot, places
// new files under the folders resolver resolves/creates, uploads via w, and
// deletes via mgr. stater is used to read the authoritative remote mtime after
// a move.
func NewPushExecutor(localRoot string, resolver *remoteindex.Resolver, w service.FileWriter, mgr fileDeleter, lister remoteindex.Lister, mover fileMover, stater fileStater) *PushExecutor {
	return &PushExecutor{localRoot: localRoot, resolver: resolver, writer: w, manager: mgr, lister: lister, mover: mover, stater: stater}
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

// Move drives the remote file remoteID to its target location (parent folder of
// toRel) and name (base of toRel), issuing a Move and/or Rename only for the
// dimensions that still differ from the LIVE remote state. It returns the
// authoritative remote mtime read via Stat.
//
// It Stats the current state first and decides from it, which gives three
// properties at once:
//   - Crash re-run is a no-op. If the first run relocated the file but crashed
//     before the manifest was checkpointed, the re-run issues zero mutating
//     calls instead of depending on kDrive treating a move/rename-to-current-
//     state as a no-op (an unverified server behavior).
//   - Partial first run self-heals. Move applied but Rename not (or vice versa)
//     ⇒ the re-run issues only the missing half.
//   - Out-of-band drift is corrected. If another client relocated/renamed the
//     file since the manifest snapshot, we still drive it to the target rather
//     than acting on the stale manifest path (fromRel).
//
// Name decisions are exact: the API always returns the name, so cur.Name vs the
// target base name is authoritative. Parent decisions use cur.ParentID when the
// API reports it (> 0); when parent_id is absent (0), we fall back to the
// manifest's intent (source vs target directory) so we don't issue a spurious
// move-to-current-parent — 0 never equals a valid folder id, so a blind compare
// would always "move". fromRel is therefore only a fallback signal, never the
// primary source of truth.
func (e *PushExecutor) Move(ctx context.Context, fromRel, toRel string, remoteID int64) (int64, error) {
	cur, err := e.stater.Stat(ctx, remoteID)
	if err != nil {
		return 0, err
	}
	destParent, err := e.resolver.Resolve(ctx, path.Dir(toRel))
	if err != nil {
		return 0, fmt.Errorf("resolve dest of %s: %w", toRel, err)
	}
	needMove := cur.ParentID != destParent
	if cur.ParentID == 0 { // API omitted parent_id: trust the manifest's intent
		needMove = path.Dir(fromRel) != path.Dir(toRel)
	}
	mutated := false
	renamed := false
	var renameMtime int64
	if needMove {
		if err := e.mover.Move(ctx, remoteID, destParent); err != nil {
			return 0, err
		}
		mutated = true
	}
	if cur.Name != path.Base(toRel) {
		info, err := e.mover.Rename(ctx, remoteID, path.Base(toRel))
		if err != nil {
			return 0, err
		}
		renamed, renameMtime = true, info.LastModifiedAt
		mutated = true
	}
	switch {
	case !mutated:
		// Already in the target state — cur's mtime is authoritative.
		return cur.LastModifiedAt, nil
	case renamed:
		// Rename runs last and returns the file's current FileInfo, so its mtime
		// is authoritative even when a Move preceded it — no extra Stat needed.
		return renameMtime, nil
	default:
		// Only a Move ran (Move returns no body); Stat for the authoritative mtime.
		info, err := e.stater.Stat(ctx, remoteID)
		if err != nil {
			return 0, err
		}
		return info.LastModifiedAt, nil
	}
}

func (e *PushExecutor) local(rel string) string {
	return filepath.Join(e.localRoot, filepath.FromSlash(rel))
}
