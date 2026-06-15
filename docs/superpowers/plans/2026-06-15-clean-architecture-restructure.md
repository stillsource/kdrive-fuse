# Clean Architecture Restructure — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure kdrive-fuse into the project's clean-architecture layout (`pkg/{domain,service,usecase,infrastructure,presentation}`) without changing any behaviour.

**Architecture:** A single self-contained application. The kDrive REST client becomes an internal infrastructure adapter (`pkg/infrastructure/kdriveapi`). Use cases orchestrate cohesion-grouped service ports (`FileReader`/`FileWriter`/`FileManager`/`Sharer`/`ListingCache`/`ContentCache`); FUSE is the presentation layer and keeps its kernel-callback handle state machines, calling use cases for I/O. Wiring lives in a DI container.

**Tech Stack:** Go, `hanwen/go-fuse/v2`, `scality/go-errors`, `zeebo/xxh3`, Ginkgo v2 + Gomega, golangci-lint, GoReleaser.

---

## Refactor conventions (read first)

This is a **behaviour-frozen restructure of existing, tested code** — not green-field TDD. Therefore:

- **The existing test suite is the safety net.** Every task ends with `go build ./... && make test` **green** and a commit. Within a task the tree may briefly not compile; that is fine, but never commit a red state.
- **Moves are shown as commands** (`git mv`, package-decl rename, import fixups), not by pasting unchanged file bodies.
- **New artifacts** (domain package, ports, use cases, DI container) are shown as **complete source**.
- **No `Co-Authored-By` trailer** on any commit. All commit messages, docs, and the PR description are **in English**.
- **Preserve exact exported signatures** when moving (only the package name and `kdrive.X` → `domain.X` type references change), unless a step explicitly says to export a previously-unexported symbol.
- Module path is `github.com/stillsource/kdrive-fuse`. New import paths:
  - `github.com/stillsource/kdrive-fuse/pkg/domain`
  - `.../pkg/service`
  - `.../pkg/usecase`
  - `.../pkg/infrastructure/kdriveapi`
  - `.../pkg/infrastructure/listingcache`
  - `.../pkg/infrastructure/contentcache`
  - `.../pkg/infrastructure/di`
  - `.../pkg/presentation/fuse`

**Invariants that must not regress** (guarded by named tests — keep them and keep them green):
1. Write commit-on-close (PR #3): commit on a Flush following a Write, Release as safety net, FLUSH-before-WRITE is a no-op; lazy seed, truncate suppresses seed. Tests: `internal/vfs/file_handle_test.go`, the "Truncating rewrite…" mount test.
2. Node ownership (PR #4): every attr block stamps the mounting user's uid/gid. Tests: the three "owner" tests in `node_test.go`.
3. Upload retry (429/5xx/transport, body rewound). Tests in `kdrive/files_test.go`.
4. LRU disk cache keyed by `{fileID}_{lastModifiedAt}`.

---

## Task 0: Baseline

**Files:** none (branch `refactor/clean-architecture` already exists with the design spec).

- [ ] **Step 1: Confirm branch and clean tree**

Run: `git rev-parse --abbrev-ref HEAD` → expect `refactor/clean-architecture`.
Run: `git status --short` → expect empty (spec already committed).

- [ ] **Step 2: Capture the green baseline**

Run: `go build ./... && make test`
Expected: build OK, all suites `ok`. Record the coverage number from `make test-coverage` (expect ≥ 90%).

---

## Task 1: domain layer

Extract the pure types, sentinel errors, and validation rules into `pkg/domain`. Validation funcs become **exported** (`ValidateName`, `ValidateFolderID`, `ValidateFileID`) because the `kdriveapi` adapter will call them across the package boundary.

**Files:**
- Create: `pkg/domain/file.go` (← `kdrive/files_types.go`)
- Create: `pkg/domain/share.go` (← `kdrive/shares_types.go`)
- Create: `pkg/domain/errors.go` (sentinels + `HTTPError` only; see step 3)
- Create: `pkg/domain/validate.go` (← `kdrive/validate.go`, exported names)
- Modify: every file under `kdrive/` and `internal/vfs/` referencing the moved symbols.

- [ ] **Step 1: Move the type files**

```bash
mkdir -p pkg/domain
git mv kdrive/files_types.go  pkg/domain/file.go
git mv kdrive/shares_types.go pkg/domain/share.go
git mv kdrive/validate.go     pkg/domain/validate.go
```

- [ ] **Step 2: Rename package + export validation in the moved files**

In `pkg/domain/file.go`, `pkg/domain/share.go`, `pkg/domain/validate.go`: change `package kdrive` → `package domain`.
In `pkg/domain/validate.go`: rename `validateName`→`ValidateName`, `validateFolderID`→`ValidateFolderID`, `validateFileID`→`ValidateFileID` (function declarations only).

- [ ] **Step 3: Split errors — sentinels/HTTPError to domain, keep the HTTP mapper in the adapter**

Inspect `kdrive/errors.go`. Move into a new `pkg/domain/errors.go` (`package domain`): the sentinel `var` block (`ErrNotFound, ErrAuth, ErrConflict, ErrValidation, ErrRateLimit, ErrServer`) and the `HTTPError` type with its methods. **Leave in `kdrive/errors.go`** any function that inspects an `*http.Response` (e.g. `fromResponse`) — it is HTTP infrastructure; have it reference `domain.ErrX` / `domain.HTTPError`. If `kdrive/errors.go` becomes empty, `git rm` it.

Run: `git add -A pkg/domain kdrive/errors.go`

- [ ] **Step 4: Fix all references to the moved symbols**

In every remaining file under `kdrive/` and under `internal/vfs/` that used `FileInfo`, `FileType`, `ShareInfo`, `ErrNotFound`/etc., `HTTPError`, or `validateName`/etc.:
- add `import "github.com/stillsource/kdrive-fuse/pkg/domain"`,
- prefix the moved symbols with `domain.` (and use the exported `ValidateName`/`ValidateFolderID`/`ValidateFileID`).

Note: the `kdrive.Files`/`kdrive.Shares` interfaces and `UploadInput` stay in `kdrive/` for now; only the moved symbols get the `domain.` prefix.

Run: `goimports -w ./kdrive ./internal/vfs` (or `gofmt -w` + manual import add).

- [ ] **Step 5: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok`. If the FUSE fake/test files reference `kdrive.FileInfo`, update those to `domain.FileInfo` too.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: extract pure types, errors and validation into pkg/domain"
```

---

## Task 2: service ports

Add the cohesion-grouped port interfaces. They depend only on `domain` and stdlib. `UploadInput` moves here (it is the `FileWriter` input contract).

**Files:**
- Create: `pkg/service/file.go`
- Create: `pkg/service/sharer.go`
- Create: `pkg/service/cache.go`
- Remove later: `kdrive/files_options.go` (UploadInput) — moved in step 1.

- [ ] **Step 1: Move UploadInput into service**

```bash
mkdir -p pkg/service
git mv kdrive/files_options.go pkg/service/upload_input.go
```
Edit `pkg/service/upload_input.go`: `package kdrive` → `package service`. Keep the struct identical (`ParentID, ExistingFileID int64; Name string; Body io.ReadSeeker; Size int64`).

- [ ] **Step 2: Write the port interfaces**

`pkg/service/file.go`:

```go
package service

import (
	"context"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// FileReader reads file metadata and content from the remote.
type FileReader interface {
	List(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
	Stat(ctx context.Context, fileID int64) (domain.FileInfo, error)
	DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error)
}

// FileWriter creates or replaces remote file content.
type FileWriter interface {
	Upload(ctx context.Context, in UploadInput) (domain.FileInfo, error)
}

// FileManager performs structural operations on files and directories.
type FileManager interface {
	Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
	Delete(ctx context.Context, fileID int64) error
	Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error)
	Move(ctx context.Context, fileID, destDirID int64) error
}
```

`pkg/service/sharer.go`:

```go
package service

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Sharer publishes a public share link for a file.
type Sharer interface {
	Publish(ctx context.Context, fileID int64) (domain.ShareInfo, error)
}
```

`pkg/service/cache.go`:

```go
package service

import (
	"context"
	"os"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// ListingCache caches directory listings by folder ID.
type ListingCache interface {
	Get(folderID int64) ([]domain.FileInfo, bool)
	Set(folderID int64, files []domain.FileInfo)
	Invalidate(folderID int64)
}

// ContentCache yields a readable, disk-cached copy of a file's content.
type ContentCache interface {
	Open(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error)
}
```

- [ ] **Step 3: Fix UploadInput references**

Anything using `kdrive.UploadInput` now uses `service.UploadInput` (the `kdrive` client's `Upload` signature and the vfs writeHandle). Add the `service` import and adjust.

- [ ] **Step 4: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok` (the `service` package compiles even though only `UploadInput` is consumed yet).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: add cohesion-grouped service ports"
```

---

## Task 3: kdriveapi adapter

Move the HTTP client into `pkg/infrastructure/kdriveapi` and assert it implements the ports.

**Files:**
- Move: `kdrive/{client,options,response,files,shares,errors,doc}.go` → `pkg/infrastructure/kdriveapi/`
- Move: `kdrive/internal/hash/` → `pkg/infrastructure/kdriveapi/internal/hash/`
- Move: `kdrive/kdrivefakes/` → `pkg/service/servicefakes/`
- Create: `pkg/infrastructure/kdriveapi/ports.go` (static interface assertions)

- [ ] **Step 1: Move the client files**

```bash
mkdir -p pkg/infrastructure/kdriveapi
git mv kdrive/client.go    pkg/infrastructure/kdriveapi/client.go
git mv kdrive/options.go   pkg/infrastructure/kdriveapi/options.go
git mv kdrive/response.go  pkg/infrastructure/kdriveapi/response.go
git mv kdrive/files.go     pkg/infrastructure/kdriveapi/files.go
git mv kdrive/shares.go    pkg/infrastructure/kdriveapi/shares.go
git mv kdrive/doc.go       pkg/infrastructure/kdriveapi/doc.go
# errors.go only if it still exists after Task 1 step 3:
[ -f kdrive/errors.go ] && git mv kdrive/errors.go pkg/infrastructure/kdriveapi/errors.go
git mv kdrive/internal pkg/infrastructure/kdriveapi/internal
# move the test files alongside their package:
git mv kdrive/*_test.go pkg/infrastructure/kdriveapi/ 2>/dev/null || true
```

- [ ] **Step 2: Rename package + drop the in-package interfaces**

In every moved `*.go` (incl. tests): `package kdrive` → `package kdriveapi`, and fix the internal hash import path to `.../pkg/infrastructure/kdriveapi/internal/hash`.
Delete the `Files interface { … }` and `Shares interface { … }` declarations (and their `var _ Files = …` lines) — these are now `service` ports. Keep `FilesService`, `SharesService`, `Client`, `New`, options.
Change `Upload`'s parameter type to `service.UploadInput` and add the `service` import.

- [ ] **Step 3: Move the fakes to servicefakes**

```bash
mkdir -p pkg/service/servicefakes
git mv pkg/infrastructure/kdriveapi/kdrivefakes/* pkg/service/servicefakes/ 2>/dev/null || git mv kdrive/kdrivefakes/* pkg/service/servicefakes/
rmdir kdrive/kdrivefakes 2>/dev/null || true
```
In the moved fakes: `package kdrivefakes` → `package servicefakes`; reference `domain.FileInfo`/`domain.ShareInfo` and `service.UploadInput`; keep the `var _ service.FileReader = …` style assertions (replace the old `var _ kdrive.Files`).

- [ ] **Step 4: Assert the adapter satisfies the ports**

`pkg/infrastructure/kdriveapi/ports.go`:

```go
package kdriveapi

import "github.com/stillsource/kdrive-fuse/pkg/service"

var (
	_ service.FileReader  = (*Client)(nil)
	_ service.FileWriter  = (*Client)(nil)
	_ service.FileManager = (*Client)(nil)
	_ service.Sharer      = (*Client)(nil)
)
```

Note: `Client` embeds `Files *FilesService` and `Shares *SharesService`. If the operations are reached via those sub-services rather than promoted methods, instead assert on the services (`_ service.FileReader = (*FilesService)(nil)`, `_ service.Sharer = (*SharesService)(nil)`) and have the DI container expose `client.Files` / `client.Shares` as the ports. Pick whichever matches the current method receivers — do not add forwarding methods just to satisfy a promoted-method assertion.

- [ ] **Step 5: Fix consumers**

`internal/vfs` and `cmd` import `kdrive` for `New` and the `Files`/`Shares` interfaces. Update imports to `kdriveapi` for `New`, and to `service` for the interface types (e.g. `KDriveFS.Files` becomes `service.FileReader`+`service.FileManager`+`service.FileWriter` as needed — but defer the vfs port-splitting to Task 6; for now, make `KDriveFS.Files` typed as a small local interface or `kdriveapi`'s concrete service so it compiles).

Simplest interim: in `internal/vfs/fs.go`, type the field as the concrete `*kdriveapi.FilesService` (or `*kdriveapi.Client`) to keep compiling; Task 6 swaps it to ports.

- [ ] **Step 6: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok`. The kdriveapi httptest suite runs under its new path.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: move kDrive REST client into pkg/infrastructure/kdriveapi"
```

---

## Task 4: cache adapters

Move the two caches into infrastructure adapters implementing the cache ports.

**Files:**
- Move: `internal/vfs/cache.go` → `pkg/infrastructure/listingcache/memory.go`
- Move: `internal/vfs/diskcache.go` → `pkg/infrastructure/contentcache/disk.go`
- Move their tests alongside.

- [ ] **Step 1: Move + rename packages**

```bash
mkdir -p pkg/infrastructure/listingcache pkg/infrastructure/contentcache
git mv internal/vfs/cache.go            pkg/infrastructure/listingcache/memory.go
git mv internal/vfs/diskcache.go        pkg/infrastructure/contentcache/disk.go
git mv internal/vfs/cache_test.go       pkg/infrastructure/listingcache/ 2>/dev/null || true
git mv internal/vfs/diskcache_test.go   pkg/infrastructure/contentcache/ 2>/dev/null || true
```
Rename `package vfs` → `package listingcache` / `package contentcache` in the moved files. Rename the exported types if desired (`DirCache` may stay as `DirCache` or become `Cache`); **keep it simple — keep `DirCache` and `DiskCache` names** to minimise churn. Update `NewDiskCache`'s `files kdrive.Files` parameter to `files service.FileReader`.

- [ ] **Step 2: Assert port satisfaction**

Add to `pkg/infrastructure/listingcache/memory.go`:
```go
var _ service.ListingCache = (*DirCache)(nil)
```
Add to `pkg/infrastructure/contentcache/disk.go`:
```go
var _ service.ContentCache = (*DiskCache)(nil)
```
(Import `service`; reference `domain.FileInfo` where the cache stores listings.)

- [ ] **Step 3: Fix consumers**

`internal/vfs/fs.go` (`KDriveFS`) references `*DirCache`/`*DiskCache` — update imports to the new packages, or (interim) type the fields as `service.ListingCache` / `service.ContentCache`.

- [ ] **Step 4: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok` (cache unit tests run under their new paths).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move dir/disk caches into infrastructure cache adapters"
```

---

## Task 5: use cases

Introduce the use-case layer. Each use case is a struct with constructor injection over `service` ports and a single method. Unit-test each against `servicefakes`. These extract the orchestration currently inline in the vfs nodes; the FUSE handle state machines stay in presentation (Task 6) and will call these.

**Files (create):**
- `pkg/usecase/list_dir.go` + `pkg/usecase/list_dir_test.go`
- `pkg/usecase/read_file.go`
- `pkg/usecase/commit_write.go` + `_test.go`
- `pkg/usecase/seed_content.go`
- `pkg/usecase/delete_entry.go` + `_test.go`
- `pkg/usecase/rename_entry.go`, `move_entry.go`, `make_dir.go`, `share_file.go`

- [ ] **Step 1: ListDir use case**

`pkg/usecase/list_dir.go`:

```go
package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// ListDir returns a directory's children, backed by the listing cache.
type ListDir struct {
	files service.FileReader
	cache service.ListingCache
}

func NewListDir(files service.FileReader, cache service.ListingCache) *ListDir {
	return &ListDir{files: files, cache: cache}
}

func (u *ListDir) Execute(ctx context.Context, folderID int64) ([]domain.FileInfo, error) {
	if files, ok := u.cache.Get(folderID); ok {
		return files, nil
	}
	files, err := u.files.List(ctx, folderID)
	if err != nil {
		return nil, err
	}
	u.cache.Set(folderID, files)
	return files, nil
}
```

- [ ] **Step 2: ListDir test**

`pkg/usecase/list_dir_test.go` (Ginkgo). Use `servicefakes` for `FileReader` and a real `listingcache.NewDirCache` (or a fake). Assert: cache miss calls `List` once and populates; second call is served from cache (List not called again). Mirror the existing `internal/vfs` "list caches subsequent calls" test.

Run: `go test ./pkg/usecase/ -run TestUsecase -v` → expect PASS.

- [ ] **Step 3: ReadFile use case**

`pkg/usecase/read_file.go`:

```go
package usecase

import (
	"context"
	"os"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// ReadFile opens a disk-cached, readable copy of a file's content.
type ReadFile struct {
	cache service.ContentCache
}

func NewReadFile(cache service.ContentCache) *ReadFile {
	return &ReadFile{cache: cache}
}

func (u *ReadFile) Execute(ctx context.Context, fileID, lastModifiedAt, size int64) (*os.File, error) {
	return u.cache.Open(ctx, fileID, lastModifiedAt, size)
}
```

- [ ] **Step 4: SeedContent + CommitWrite use cases**

`pkg/usecase/seed_content.go`:

```go
package usecase

import (
	"context"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// SeedContent streams the current remote content of a file (for partial edits).
type SeedContent struct {
	files service.FileReader
}

func NewSeedContent(files service.FileReader) *SeedContent {
	return &SeedContent{files: files}
}

func (u *SeedContent) Execute(ctx context.Context, fileID int64) (io.ReadCloser, error) {
	return u.files.DownloadStream(ctx, fileID, 0, 0)
}
```

`pkg/usecase/commit_write.go`:

```go
package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// CommitWrite uploads buffered content (create or replace) and invalidates the
// parent listing so the new metadata shows up on the next readdir.
type CommitWrite struct {
	files service.FileWriter
	cache service.ListingCache
}

func NewCommitWrite(files service.FileWriter, cache service.ListingCache) *CommitWrite {
	return &CommitWrite{files: files, cache: cache}
}

func (u *CommitWrite) Execute(ctx context.Context, in service.UploadInput, parentID int64) (domain.FileInfo, error) {
	info, err := u.files.Upload(ctx, in)
	if err != nil {
		return domain.FileInfo{}, err
	}
	u.cache.Invalidate(parentID)
	return info, nil
}
```

`pkg/usecase/commit_write_test.go`: with a `servicefakes` `FileWriter` and a real/fake `ListingCache`, assert a successful upload returns the info and invalidates `parentID`; an upload error returns the error and does **not** invalidate. (Preserves the current onUploaded behaviour.)

- [ ] **Step 5: Structural use cases (delete/rename/move/mkdir/share)**

`pkg/usecase/delete_entry.go`:

```go
package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// DeleteEntry soft-deletes a file or directory and invalidates the parent listing.
type DeleteEntry struct {
	files service.FileManager
	cache service.ListingCache
}

func NewDeleteEntry(files service.FileManager, cache service.ListingCache) *DeleteEntry {
	return &DeleteEntry{files: files, cache: cache}
}

func (u *DeleteEntry) Execute(ctx context.Context, fileID, parentID int64) error {
	if err := u.files.Delete(ctx, fileID); err != nil {
		return err
	}
	u.cache.Invalidate(parentID)
	return nil
}
```

`pkg/usecase/make_dir.go`:

```go
package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// MakeDir creates a directory and invalidates the parent listing.
type MakeDir struct {
	files service.FileManager
	cache service.ListingCache
}

func NewMakeDir(files service.FileManager, cache service.ListingCache) *MakeDir {
	return &MakeDir{files: files, cache: cache}
}

func (u *MakeDir) Execute(ctx context.Context, parentID int64, name string) (domain.FileInfo, error) {
	info, err := u.files.Mkdir(ctx, parentID, name)
	if err != nil {
		return domain.FileInfo{}, err
	}
	u.cache.Invalidate(parentID)
	return info, nil
}
```

`pkg/usecase/rename_entry.go`:

```go
package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// RenameEntry moves and/or renames an entry across the given parents,
// mirroring the FUSE Rename contract (move-then-rename), and invalidates
// both affected listings.
type RenameEntry struct {
	files service.FileManager
	cache service.ListingCache
}

func NewRenameEntry(files service.FileManager, cache service.ListingCache) *RenameEntry {
	return &RenameEntry{files: files, cache: cache}
}

// Execute relocates fileID into destDirID when it differs from srcDirID, then
// renames to newName when it differs from oldName. Caches for both dirs are
// invalidated on success.
func (u *RenameEntry) Execute(ctx context.Context, fileID, srcDirID, destDirID int64, oldName, newName string) error {
	if destDirID != srcDirID {
		if err := u.files.Move(ctx, fileID, destDirID); err != nil {
			return err
		}
	}
	if newName != oldName {
		if _, err := u.files.Rename(ctx, fileID, newName); err != nil {
			return err
		}
	}
	u.cache.Invalidate(srcDirID)
	u.cache.Invalidate(destDirID)
	return nil
}
```

`pkg/usecase/share_file.go`:

```go
package usecase

import (
	"context"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// ShareFile returns (creating if needed) a public share link for a file.
type ShareFile struct {
	sharer service.Sharer
}

func NewShareFile(sharer service.Sharer) *ShareFile {
	return &ShareFile{sharer: sharer}
}

func (u *ShareFile) Execute(ctx context.Context, fileID int64) (domain.ShareInfo, error) {
	return u.sharer.Publish(ctx, fileID)
}
```

(`MoveEntry` is subsumed by `RenameEntry`; do not create a separate file — the FUSE `Rename` callback is the only caller and it needs the combined move+rename. This keeps YAGNI.)

- [ ] **Step 6: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok`, including the new `pkg/usecase` tests.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: add use-case layer with unit tests"
```

---

## Task 6: presentation/fuse

Move the FUSE nodes into `pkg/presentation/fuse` and rewire them to call use cases instead of holding ports + inline orchestration. The handle state machines (`readHandle`, `writeHandle`) — which implement `fuse.FileReader/FileWriter/FileFlusher/FileReleaser` — **stay here**; only their I/O calls route through use cases. **Preserve the commit-on-close and ownership logic exactly.**

**Files:**
- Move: `internal/vfs/{fs,dir,file}.go` → `pkg/presentation/fuse/{fs,dir,file}.go`
- Move: `internal/vfs/{node_test,file_handle_test}.go` → `pkg/presentation/fuse/`
- After moving, `internal/vfs/` should be empty → remove it.

- [ ] **Step 1: Move + rename package**

```bash
mkdir -p pkg/presentation/fuse
git mv internal/vfs/fs.go               pkg/presentation/fuse/fs.go
git mv internal/vfs/dir.go              pkg/presentation/fuse/dir.go
git mv internal/vfs/file.go             pkg/presentation/fuse/file.go
git mv internal/vfs/node_test.go        pkg/presentation/fuse/node_test.go
git mv internal/vfs/file_handle_test.go pkg/presentation/fuse/file_handle_test.go
# move any remaining vfs test/helper files too:
git mv internal/vfs/*.go pkg/presentation/fuse/ 2>/dev/null || true
rmdir internal/vfs 2>/dev/null; rmdir internal 2>/dev/null || true
```
Rename `package vfs` → `package fuse` in all moved files (including tests and the suite entry point).

- [ ] **Step 2: Reshape `KDriveFS` to hold use cases**

In `pkg/presentation/fuse/fs.go`, change `KDriveFS` to carry the use cases (and keep `Uid`/`Gid`):

```go
type KDriveFS struct {
	ListDir     *usecase.ListDir
	ReadFile    *usecase.ReadFile
	SeedContent *usecase.SeedContent
	CommitWrite *usecase.CommitWrite
	DeleteEntry *usecase.DeleteEntry
	RenameEntry *usecase.RenameEntry
	MakeDir     *usecase.MakeDir

	Uid uint32
	Gid uint32
}
```
Keep `applyOwner` and `NewRootDirNode` exactly. `NewKDriveFS` is replaced by DI wiring (Task 7); for now provide a constructor that takes the use cases + a uid/gid (default from `os.Getuid()/os.Getgid()`), preserving the ownership behaviour. **Keep the three owner tests green.**

- [ ] **Step 3: Rewire `dir.go` callbacks to use cases**

- `Readdir`/`Lookup`/`list` → `d.kdfs.ListDir.Execute(ctx, d.folderID)`.
- `Mkdir` → `d.kdfs.MakeDir.Execute(ctx, d.folderID, name)`.
- `Unlink`/`Rmdir`/`removeChild` → resolve the child from the listing (still via `ListDir`), keep the `IsDir()` type-mismatch checks (ENOTDIR/EISDIR/ENOENT) **unchanged**, then `d.kdfs.DeleteEntry.Execute(ctx, f.ID, d.folderID)`.
- `Rename` → resolve `src` from the listing, then `d.kdfs.RenameEntry.Execute(ctx, src.ID, d.folderID, target.folderID, name, newName)`; keep the `EXDEV`/`ENOENT` guards.
- `Create` → build a `writeHandle` that commits via `CommitWrite` (see step 4); keep the temporary-inode scheme and `applyOwner`.

Behaviour, errno mapping, cache-invalidation points, and `applyOwner` calls must match the pre-move code exactly.

- [ ] **Step 4: Rewire `file.go` handles to use cases**

- `readHandle.ensureOpen` → `h.kdfs.ReadFile.Execute(ctx, h.info.ID, h.info.LastModifiedAt, h.info.Size)` instead of touching the disk cache directly.
- `writeHandle` keeps its full state machine (`wrote`/`truncated`/`seeded`/`uploaded`, `truncateTo`, `seedLocked`, `Flush`, `Release`, `commitLocked`). Replace only the two I/O calls:
  - seed: `h.seed.Execute(ctx, h.existingFileID)` (a `*usecase.SeedContent`) returning the `io.ReadCloser` to copy into the tempfile.
  - commit: `h.commit.Execute(ctx, service.UploadInput{...}, h.parentID)` (a `*usecase.CommitWrite`); on success patch `FileNode.info` via the existing `onUploaded` callback (which no longer needs to invalidate the cache — `CommitWrite` does that now; keep `onUploaded` for the `FileNode.info` patch + real-inode handling).
- `FileNode.Getattr`/`Setattr` keep `applyOwner` and the truncate-propagation to `writeHandle`. **Do not change the commit-on-close semantics.**

- [ ] **Step 5: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok`. The moved mount tests must pass unchanged in behaviour, including:
- the three owner tests,
- "Truncating rewrite uploads only the new bytes…",
- the `file_handle_test.go` write-contract tests.
Update the test fixtures (`newMountFixture`, the bare-node unit tests) to construct `KDriveFS` with use cases wired over `servicefakes` + real cache adapters.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: move FUSE nodes to pkg/presentation/fuse, route I/O through use cases"
```

---

## Task 7: DI container

Wire everything in `pkg/infrastructure/di`, following the reference idiom (one `Container`, lazy memoised getter per component).

**Files:**
- Create: `pkg/infrastructure/di/container.go`
- Create: `pkg/infrastructure/di/{fileapi,caches,usecases,root}.go` (split getters by concern)

- [ ] **Step 1: Container struct + constructor**

`pkg/infrastructure/di/container.go`:

```go
package di

import (
	"time"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
	"github.com/stillsource/kdrive-fuse/pkg/presentation/fuse"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

// Config is the resolved runtime configuration the container needs.
type Config struct {
	Token          string
	DriveID        string
	RootFolderID   int64
	BaseURL        string // optional override
	UploadBaseURL  string // optional override
	CacheTTL       time.Duration
	DiskCacheDir   string
	DiskCacheBytes int64
	Uid            uint32
	Gid            uint32
}

// Container builds and memoises the object graph.
type Container struct {
	cfg Config

	client       *kdriveapi.Client
	listingCache *listingcache.DirCache
	contentCache *contentcache.DiskCache

	listDir     *usecase.ListDir
	readFile    *usecase.ReadFile
	seedContent *usecase.SeedContent
	commitWrite *usecase.CommitWrite
	deleteEntry *usecase.DeleteEntry
	renameEntry *usecase.RenameEntry
	makeDir     *usecase.MakeDir

	kdfs *fuse.KDriveFS
}

func NewContainer(cfg Config) *Container { return &Container{cfg: cfg} }
```

- [ ] **Step 2: Getters — fileapi + caches**

`pkg/infrastructure/di/fileapi.go`:

```go
package di

import "github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi"

func (c *Container) Client() *kdriveapi.Client {
	if c.client == nil {
		var opts []kdriveapi.Option
		if c.cfg.BaseURL != "" {
			opts = append(opts, kdriveapi.WithBaseURL(c.cfg.BaseURL))
		}
		if c.cfg.UploadBaseURL != "" {
			opts = append(opts, kdriveapi.WithUploadBaseURL(c.cfg.UploadBaseURL))
		}
		c.client = kdriveapi.New(c.cfg.Token, c.cfg.DriveID, opts...)
	}
	return c.client
}
```

`pkg/infrastructure/di/caches.go`:

```go
package di

import (
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/listingcache"
)

func (c *Container) ListingCache() *listingcache.DirCache {
	if c.listingCache == nil {
		c.listingCache = listingcache.NewDirCache(c.cfg.CacheTTL)
	}
	return c.listingCache
}

func (c *Container) ContentCache() *contentcache.DiskCache {
	if c.contentCache == nil {
		dc, err := contentcache.NewDiskCache(c.cfg.DiskCacheDir, c.cfg.DiskCacheBytes, c.fileReader())
		if err != nil {
			panic(err) // mount cannot proceed without a content cache
		}
		c.contentCache = dc
	}
	return c.contentCache
}

// fileReader exposes the client's read port (adjust receiver if ops live on c.Client().Files).
func (c *Container) fileReader() *kdriveapi.Client { return c.Client() }
```

(Adjust the `fileReader`/port wiring to match where the operations live — `*Client` vs `*Client.Files` — per Task 3 step 4.)

- [ ] **Step 3: Getters — use cases + root node**

`pkg/infrastructure/di/usecases.go`: one memoised getter per use case, e.g.

```go
func (c *Container) ListDir() *usecase.ListDir {
	if c.listDir == nil {
		c.listDir = usecase.NewListDir(c.Client(), c.ListingCache())
	}
	return c.listDir
}
```
(Repeat for `ReadFile`, `SeedContent`, `CommitWrite`, `DeleteEntry`, `RenameEntry`, `MakeDir`, passing the right ports.)

`pkg/infrastructure/di/root.go`:

```go
package di

import (
	"github.com/stillsource/kdrive-fuse/pkg/presentation/fuse"
)

func (c *Container) KDriveFS() *fuse.KDriveFS {
	if c.kdfs == nil {
		c.kdfs = &fuse.KDriveFS{
			ListDir:     c.ListDir(),
			ReadFile:    c.ReadFile(),
			SeedContent: c.SeedContent(),
			CommitWrite: c.CommitWrite(),
			DeleteEntry: c.DeleteEntry(),
			RenameEntry: c.RenameEntry(),
			MakeDir:     c.MakeDir(),
			Uid:         c.cfg.Uid,
			Gid:         c.cfg.Gid,
		}
	}
	return c.kdfs
}

func (c *Container) RootNode() *fuse.DirNode {
	return fuse.NewRootDirNode(c.KDriveFS(), c.cfg.RootFolderID)
}
```

- [ ] **Step 4: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok` (the container compiles; it has no tests of its own — wiring is exercised by `cmd` and the mount tests).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: add DI container wiring the object graph"
```

---

## Task 8: cmd wiring

Point `main.go` at the container.

**Files:**
- Modify: `cmd/kdrive-fuse/main.go`
- Modify (maybe): `cmd/kdrive-fuse/config/env.go`

- [ ] **Step 1: Map env config to `di.Config`**

In `main.go`, after loading env config, build `di.Config{…}` (defaulting `Uid/Gid` from `os.Getuid()/os.Getgid()`, `CacheTTL` from `KDRIVE_CACHE_TTL_SECONDS`, etc.), then:
```go
c := di.NewContainer(cfg)
root := c.RootNode()
server, err := fs.Mount(mountpoint, root, opts)
```
Remove the old direct construction of `kdrive.New` / `NewKDriveFS` / `NewDiskCache`. Keep `version`, `--version` handling, signal handling, and the startup log line.

- [ ] **Step 2: Build and test green**

Run: `go build ./... && make test`
Expected: all `ok` (`cmd` `main_test.go` for `wantsVersion` still passes).

- [ ] **Step 3: Live smoke test**

```bash
make build && make install
systemctl --user restart kdrive-vfs.service && sleep 2
systemctl --user is-active kdrive-vfs.service        # expect: active
ls ~/kDrive-vfs | head                               # expect: listing
stat -c '%U' ~/kDrive-vfs/<some-dir>                 # expect: your user, not root
```
Create/edit/delete a throwaway file under a self-made test dir to confirm write + delete still work, then clean it up. **Do not touch real photo files.**

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: build the FUSE mount from the DI container in cmd"
```

---

## Task 9: docs, release config, final verification

**Files:**
- Modify: `README.md`, `ROADMAP.md`, `CLAUDE.md`
- Modify: `.goreleaser.yaml` (only if it references moved paths — the build target `./cmd/kdrive-fuse` is unchanged, so likely no change)
- Modify: `docs/kdrive-vfs.service` (only if paths changed — likely none)

- [ ] **Step 1: Update CLAUDE.md architecture map**

Replace the architecture tree and the `kdrive/`-vs-`internal/vfs/` descriptions with the new `pkg/` layout. Update the "Data flow" sections to mention use cases. Update the import path note (no longer a public `kdrive` library — it is an internal adapter). Keep the kDrive API quirks and the POSIX attributes / inode sections.

- [ ] **Step 2: Update README.md**

The library-usage section (`import ".../kdrive"`, `kdrive.New`, fakes) no longer reflects a public API. Rewrite the README around the **FUSE binary** as the product; drop or relocate the library-usage snippet (or reframe it as "internal architecture"). Update install/build commands (unchanged paths) and the operations table (unchanged).

- [ ] **Step 3: Update ROADMAP.md**

Add a "Shipped" entry: "Clean-architecture restructure — layered under `pkg/`, behaviour unchanged." Keep the chunked-upload "Blocking remaining work" entry.

- [ ] **Step 4: Full verification**

```bash
go build ./... && make test
go test ./... -race -count=1
go vet ./...
golangci-lint run ./...
go test ./pkg/... -coverprofile=/tmp/cov.out -coverpkg=./pkg/... -count=1 && go tool cover -func=/tmp/cov.out | tail -1
```
Expected: build OK, all `ok`, race clean, vet silent, lint `0 issues`, coverage ≥ 90%.
Note: the coverage `-coverpkg` target changes from `./kdrive/...,./internal/...` to `./pkg/...` — update the `Makefile` `test-coverage` target and the CI workflow's coverage step accordingly.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: update README, ROADMAP, CLAUDE and coverage target for the pkg/ layout"
```

---

## Task 10: pull request

- [ ] **Step 1: Push the branch**

```bash
git push -u origin refactor/clean-architecture
```

- [ ] **Step 2: Open the PR (English description, no Claude attribution)**

Use the github-perso MCP `create_pull_request` (owner `stillsource`, repo `kdrive-fuse`, base `main`, head `refactor/clean-architecture`). Title: `refactor: clean-architecture restructure under pkg/`. Body (English): summary, the bottom-up layer order, the explicit "behaviour frozen — invariants preserved" list, and the verification results. **No `Co-Authored-By` trailer; no Claude attribution.**

- [ ] **Step 3: Wait for CI, then merge**

Poll `pull_request_read get_check_runs` until `test` and `lint` are `success`. Merge via the github-perso MCP `merge_pull_request` (squash). Then locally: `git checkout main && git pull && git branch -d refactor/clean-architecture && git push origin --delete refactor/clean-architecture`.

- [ ] **Step 4: Redeploy and verify live**

```bash
make build && make install
systemctl --user restart kdrive-vfs.service && sleep 2
systemctl --user is-active kdrive-vfs.service
```
Smoke-test list/read/write/delete on a self-made throwaway path; clean up.

---

## Self-review notes

- **Spec coverage:** every spec section maps to a task — domain (T1), service ports (T2), kdriveapi (T3), caches (T4), use cases (T5), presentation/fuse (T6), DI (T7), cmd (T8), docs+verify (T9), PR (T10). Invariants are called out in the preamble and re-asserted in T6.
- **Refinement vs spec:** the spec listed a `WriteFile` use case "owning the tempfile lifecycle"; this plan keeps the FUSE handle state machine in presentation (it implements `fuse.*` interfaces and is kernel-driven) and splits the I/O into `SeedContent` + `CommitWrite` use cases. Same behaviour, correct layering. Flagged here intentionally.
- **YAGNI:** `MoveEntry` folded into `RenameEntry` (only the FUSE Rename callback needs it).
- **Type consistency:** port method sets match the current `kdrive` `Files`/`Shares` signatures (verified against `files.go`, `shares.go`, `cache.go`, `diskcache.go`); use-case constructors take exactly the ports they call; `KDriveFS` fields match the DI `KDriveFS()` getter.
