// Package filesearch implements a client-side filename search over a kDrive
// tree. It walks the folder tree (via remoteindex.Build) and returns files
// whose path matches every query term.
//
// Why client-side: kDrive's server-side /files/search is not a usable filename
// filter. The v2 path ignores the query and returns the whole drive; the v3
// path is an opaque relevance ranking (cursor-paginated, indexing-lagged) that
// does not reliably surface obvious filename matches. A local list-and-filter
// is predictable and scriptable at the cost of walking the tree per query — so
// the walk is bounded by a configurable parallelism and can be scoped to a
// subtree.
package filesearch

import (
	"context"
	"sort"
	"strings"

	scerr "github.com/scality/go-errors"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// Searcher finds files by matching query terms against their path, walking the
// remote tree rooted at rootID.
type Searcher struct {
	lister      remoteindex.Lister
	rootID      int64
	parallelism int    // concurrent listings during the walk; <=0 => remoteindex default
	prefix      string // slash-separated location of rootID relative to the drive root ("" at root)
}

// New returns a Searcher that walks the tree rooted at rootID via l, bounding
// the walk to parallelism concurrent listings (<=0 keeps the default). prefix is
// the slash-separated location of rootID relative to the drive root (empty when
// rootID is the drive root); it is prepended to every result path so a
// subtree-scoped search still reports full, drive-root-relative paths.
func New(l remoteindex.Lister, rootID int64, parallelism int, prefix string) *Searcher {
	return &Searcher{lister: l, rootID: rootID, parallelism: parallelism, prefix: strings.Trim(prefix, "/")}
}

// Search returns every file whose full path (prefix + slash-separated relative
// path, lower-cased) contains all whitespace-separated terms of query
// (case-insensitive AND), sorted by path. Matching against the full path means a
// term can hit either a file name or an ancestor directory. An all-whitespace or
// empty query is a validation error.
func (s *Searcher) Search(ctx context.Context, query string) ([]service.SearchHit, error) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, scerr.Wrap(domain.ErrValidation, scerr.WithDetail("search: query must not be empty"))
	}
	idx, err := remoteindex.Build(ctx, s.lister, s.rootID, remoteindex.WithParallelism(s.parallelism))
	if err != nil {
		return nil, err
	}
	var hits []service.SearchHit
	for rel, e := range idx {
		full := rel
		if s.prefix != "" {
			full = s.prefix + "/" + rel
		}
		if matchesAll(strings.ToLower(full), terms) {
			hits = append(hits, service.SearchHit{Path: full, ID: e.ID, Size: e.Size})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Path < hits[j].Path })
	return hits, nil
}

// matchesAll reports whether lower contains every term.
func matchesAll(lower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(lower, t) {
			return false
		}
	}
	return true
}
