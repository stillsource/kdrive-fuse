package service

import "context"

// SearchHit is one file matching a search, identified by its path relative to
// the search root (slash-separated) plus the fields needed to script follow-up
// commands (e.g. piping the id to `kdrive share` / `kdrive trash`).
type SearchHit struct {
	Path string
	ID   int64
	Size int64
}

// Searcher finds files in the drive whose path matches a query.
//
// kDrive's server-side /files/search is not a usable filename filter (the v2
// path ignores the query and returns the whole drive; the v3 path is an opaque
// relevance ranking with indexing lag), so the implementation lists the tree
// and filters client-side instead.
type Searcher interface {
	Search(ctx context.Context, query string) ([]SearchHit, error)
}
