package remoteindex

import (
	"context"
	"path"
	"strings"
	"sync"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// ResolveDir resolves relDir (slash-separated, relative to rootID) to its folder
// id WITHOUT creating anything — the read-only counterpart to Resolver.Resolve,
// for callers that must not mutate the drive (e.g. scoping a search to a
// subtree). An empty, "." or "/" relDir resolves to rootID. It returns
// domain.ErrNotFound if a path segment is missing or names a non-directory.
func ResolveDir(ctx context.Context, l Lister, rootID int64, relDir string) (int64, error) {
	clean := strings.Trim(path.Clean(relDir), "/")
	if clean == "" || clean == "." {
		return rootID, nil
	}
	id := rootID
	for _, seg := range strings.Split(clean, "/") {
		children, err := l.List(ctx, id)
		if err != nil {
			return 0, err
		}
		found := false
		for _, c := range children {
			if c.IsDir() && c.Name == seg {
				id, found = c.ID, true
				break
			}
		}
		if !found {
			return 0, scerr.Wrap(domain.ErrNotFound, scerr.WithDetailf("remoteindex: directory %q not found", relDir))
		}
	}
	return id, nil
}

// Mkdirer creates a directory under a parent folder.
type Mkdirer interface {
	Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
}

// Resolver maps a directory path (relative to a root folder) to its remote
// folder id, creating any missing directories along the way. Resolved paths are
// cached for the resolver's lifetime. It is safe for concurrent use: resolution
// is serialized so a directory is never created twice.
type Resolver struct {
	lister Lister
	mkdir  Mkdirer
	rootID int64

	mu    sync.Mutex
	cache map[string]int64
}

// NewResolver returns a Resolver rooted at rootID.
func NewResolver(l Lister, mk Mkdirer, rootID int64) *Resolver {
	return &Resolver{lister: l, mkdir: mk, rootID: rootID, cache: map[string]int64{}}
}

// Resolve returns the folder id for relDir (slash-separated, relative to the
// root), creating directories that do not yet exist. An empty, "." or "/"
// relDir resolves to the root. Callers should pass a cleaned relative path with
// no ".." components: a ".." is treated as a literal directory name (and is
// rejected by the backend's name validation). If a non-directory of the same
// name already exists, it is ignored and a directory create is attempted.
func (r *Resolver) Resolve(ctx context.Context, relDir string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolve(ctx, path.Clean(relDir))
}

// resolve resolves a cleaned path. The caller must hold r.mu.
func (r *Resolver) resolve(ctx context.Context, clean string) (int64, error) {
	switch clean {
	case "", ".", "/":
		return r.rootID, nil
	}
	if id, ok := r.cache[clean]; ok {
		return id, nil
	}
	parentID, err := r.resolve(ctx, path.Dir(clean))
	if err != nil {
		return 0, err
	}
	name := path.Base(clean)
	children, err := r.lister.List(ctx, parentID)
	if err != nil {
		return 0, err
	}
	for _, c := range children {
		if c.IsDir() && c.Name == name {
			r.cache[clean] = c.ID
			return c.ID, nil
		}
	}
	info, err := r.mkdir.Mkdir(ctx, parentID, name)
	if err != nil {
		return 0, err
	}
	r.cache[clean] = info.ID
	return info.ID, nil
}
