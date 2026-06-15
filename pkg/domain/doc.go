// Package domain holds the canonical, dependency-free core of kdrive-fuse:
// the entity types (FileInfo, ShareInfo), the typed error sentinels, and the
// input-validation rules shared across the application.
//
// It imports nothing internal — every other layer (infrastructure, use cases,
// presentation) depends inward on domain, never the reverse.
package domain
