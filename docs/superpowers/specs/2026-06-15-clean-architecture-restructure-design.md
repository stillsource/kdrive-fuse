# Clean Architecture Restructure — kdrive-fuse

**Date:** 2026-06-15
**Status:** Design approved — pending spec review
**Type:** Internal restructure (no behaviour change)

## Motivation

Two concerns converged:

1. The public import path `github.com/stillsource/kdrive-fuse/kdrive` stutters.
2. The repo should follow the project's own clean-architecture standard
   (`~/test-clean-archi/`, `~/artesca/apiserver/upgrade-api/`), shared across
   the personal Go projects, so it stays consistent and extensible as the
   roadmap grows (chunked upload, trash, xattrs, metrics, multi-drive).

kdrive-fuse is **not** a throwaway script (the clean-architecture anti-pattern):
it is a daily-driver tool with a real roadmap, so the "must evolve" criterion of
`standard_clean_architecture` is met.

## Decisions (settled during brainstorming)

- **Single self-contained application**, one repo (`stillsource/kdrive-fuse`).
  The kDrive client becomes an **internal infrastructure adapter**
  (`pkg/infrastructure/kdriveapi/`), no longer a public importable library →
  the import-stutter question becomes moot (nobody imports it externally).
- **Layout under `pkg/`**, matching the reference projects (not `internal/`),
  for consistency with the established standard.
- **Ports grouped by cohesion** (not one micro-interface per verb):
  `FileReader`, `FileWriter`, `FileManager`, `Sharer`, plus `ListingCache` and
  `ContentCache`.
- **Errors** keep `scality/go-errors` (richer than the reference's `pkg/errors`).
- **Presentation layer = FUSE** (kernel callbacks), not HTTP. Nodes call
  use cases; thin pass-through ops keep thin use cases to protect the hot paths.
- **Use cases are uniform** across all operations for consistency; the real
  orchestration lives in `WriteFile` (commit-on-close), `ReadFile`/`ListDir`
  (caching).
- This restructure ships **before** chunked upload, so chunked upload lands in
  the new structure.

## Target structure

```
cmd/kdrive-fuse/
  main.go                 mount, signal handling, --version; builds di.Container
  config/env.go           envconfig loader

pkg/
  domain/                 pure entities, zero external deps
    file.go               FileInfo, FileType
    share.go              ShareInfo
    errors.go             sentinels (ErrNotFound, ErrAuth, ErrConflict,
                          ErrValidation, ErrRateLimit, ErrServer) + HTTPError
    validate.go           name / folderID / fileID validation rules

  service/                ports (interfaces the use cases depend on)
    file.go               FileReader, FileWriter, FileManager
    sharer.go             Sharer
    cache.go              ListingCache, ContentCache
    servicefakes/         hand/generated test doubles per port

  usecase/                orchestration; constructor injection, Execute(...)
    list_dir.go           ListDir       (listing cache)
    read_file.go          ReadFile      (content cache, range stream)
    write_file.go         WriteFile     (commit-on-close lifecycle — see invariants)
    delete_entry.go       DeleteEntry   (soft delete)
    rename_entry.go       RenameEntry
    move_entry.go         MoveEntry
    make_dir.go           MakeDir
    share_file.go         ShareFile

  infrastructure/
    kdriveapi/            kDrive REST v2 adapter; implements FileReader/Writer/
                          Manager/Sharer. Functional-options constructor.
                          (← client/options/response/files/files_options/shares
                           + internal hash/xxh3)
    listingcache/         TTL listing cache adapter (← DirCache)
    contentcache/         LRU disk-cache adapter   (← DiskCache)
    di/                   Container + lazy memoised getters (1 file per component)

  presentation/fuse/      DirNode, FileNode, readHandle, writeHandle
                          (← internal/vfs/{fs,dir,file}.go); call use cases
```

## Ports (service/)

```go
// service/file.go
type FileReader interface {
    List(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
    Stat(ctx context.Context, fileID int64) (domain.FileInfo, error)
    DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error)
}
type FileWriter interface {
    Upload(ctx context.Context, in UploadInput) (domain.FileInfo, error)
}
type FileManager interface {
    Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
    Delete(ctx context.Context, fileID int64) error
    Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error)
    Move(ctx context.Context, fileID, destDirID int64) error
}

// service/sharer.go
type Sharer interface {
    Publish(ctx context.Context, fileID int64) (domain.ShareInfo, error)
}

// service/cache.go
type ListingCache interface {
    Get(folderID int64) ([]domain.FileInfo, bool)
    Set(folderID int64, files []domain.FileInfo)
    Invalidate(folderID int64)
}
type ContentCache interface {
    Open(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error)
}
```

`UploadInput` lives with `FileWriter` (service layer) since it is the port's
input contract. `kdriveapi` implements all four operation ports on one struct,
exposed individually to use cases.

## Use cases (usecase/)

Each is a struct with constructor injection and an `Execute`-style method,
depending only on `service` ports + `domain`. Mapping of current logic:

- **ListDir** — listing cache lookup → `FileReader.List` → cache set.
- **ReadFile** — `ContentCache.Open` (download-on-miss) → `ReadAt`.
- **WriteFile** — owns the working-tempfile lifecycle: lazy seed on first write
  (`FileReader.DownloadStream`, skipped for truncate), commit-on-close via
  `FileWriter.Upload`. **Must preserve the exact contract just fixed (PR #3).**
- **DeleteEntry / RenameEntry / MoveEntry / MakeDir** — thin orchestration over
  `FileManager`, plus listing-cache invalidation.
- **ShareFile** — `Sharer.Publish`.

## DI Container (infrastructure/di/)

`Container` struct holds wired singletons; one lazy memoised getter per
component (the reference idiom):

```go
func (c *Container) FileAPI() *kdriveapi.Client          // memoised
func (c *Container) ListingCache() service.ListingCache  // memoised
func (c *Container) ContentCache() service.ContentCache  // memoised
func (c *Container) WriteFile() *usecase.WriteFile        // memoised, wires ports
func (c *Container) RootNode() *fuse.DirNode              // builds presentation tree
```

`main.go`: load config → `di.NewContainer(cfg)` → `c.RootNode()` → `fs.Mount`.

## Invariants to preserve (must not regress)

These were hard-won; the restructure moves code, it does not change behaviour:

1. **Write commit-on-close** (PR #3): commit on a Flush that follows a Write
   (so `close()` surfaces errors), with Release as the safety net; FLUSH-before-WRITE
   on truncating rewrites is a no-op. Lazy seed; truncate suppresses the seed.
2. **Node ownership** (PR #4): every attr block stamps the mounting user's
   uid/gid (`applyOwner`), so files stay deletable/editable in file managers.
3. **Upload retry**: 429/5xx/transport retried with backoff, `io.ReadSeeker`
   body rewound each attempt; non-transient 4xx fails fast.
4. **LRU disk cache** keyed by `{fileID}_{lastModifiedAt}`; implicit invalidation
   on mtime change; budget eviction by atime.
5. **kDrive API quirks**: separate upload host, `xxh3:` hash, `file_id` edit
   param, soft delete, Setattr-driven truncate, CDN redirect. (Documented in
   CLAUDE.md; carried into `kdriveapi`.)

## Migration strategy

- Dedicated branch `refactor/clean-architecture`.
- Move layer by layer **bottom-up** (domain → service → infrastructure →
  usecase → presentation → cmd), running `make test` after each layer so the
  suite stays green throughout.
- Public symbol churn is internal-only (21 references in-repo, no external Go
  consumer found); update imports mechanically.
- Single PR at the end (CI: vet, race, coverage ≥ 90%, golangci-lint).
- Update README.md, ROADMAP.md, CLAUDE.md, .goreleaser.yaml paths, and the
  systemd example to the new layout.

## Testing

- Keep Ginkgo v2 + Gomega and the ≥ 90% coverage gate.
- `kdriveapi` keeps the `httptest`-based HTTP tests.
- Use cases get unit tests against `service/servicefakes`.
- `presentation/fuse` keeps the real-FUSE-mount integration tests (including the
  ownership and commit-on-close regression tests).

## Out of scope

- Chunked upload (> 100 MB) — separate spec, implemented after this lands, in
  the new structure (decisions already captured: per-chunk resume, 100 MB
  threshold / 50 MB chunks).
- Any behaviour change. This is a pure restructure.

## Risks

- **FUSE hot-path latency**: avoid gratuitous indirection; thin use cases for
  pass-through ops.
- **Lifecycle regressions** in the write path: the commit-on-close and ownership
  tests are the guardrail — they must move intact and stay green.
- **Scope creep**: resist "while I'm here" changes; behaviour is frozen.
