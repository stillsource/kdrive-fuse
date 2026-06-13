# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working in this repository.

## What this is

A Go client library and FUSE filesystem for the Infomaniak kDrive REST API v2.

Two products live in one repo:

- **`kdrive/`** — a public, importable Go client library. Module path: `github.com/stillsource/kdrive-fuse/kdrive`.
- **`cmd/kdrive-fuse/`** — a FUSE binary that mounts a kDrive remote as a local filesystem, backed by a disk cache.

Built from scratch because WebDAV is not reliably offered on every kDrive account (the `{driveID}.connect.kdrive.infomaniak.com` endpoint returns 403 on PROPFIND with no `DAV:` header).

## Build / run / ops

```bash
make build                                         # ./bin/kdrive-fuse
make test                                          # unit + integration tests
make test-race                                     # with race detector
make test-coverage                                 # HTML report + total %
make lint                                          # golangci-lint
make install                                       # install binary to ~/bin

systemctl --user restart kdrive-vfs.service        # apply a new binary
systemctl --user status kdrive-vfs.service
journalctl --user -u kdrive-vfs.service -f         # tail logs
fusermount -u ~/kDrive-vfs                         # manual unmount
```

Binary config lives in `~/.config/kdrive-fuse/env` (loaded by systemd `EnvironmentFile`).
Required: `KDRIVE_API_TOKEN`, `KDRIVE_DRIVE_ID`, `KDRIVE_MOUNT`.
Optional: `KDRIVE_ROOT_FOLDER_ID` (default `1`), `KDRIVE_BASE_URL`, `KDRIVE_UPLOAD_BASE_URL`, `KDRIVE_CACHE_TTL_SECONDS` (default `30`), `KDRIVE_DISK_CACHE_DIR` (default `~/.cache/kdrive-fuse`), `KDRIVE_DISK_CACHE_MAX_GB` (default `2`).

Test coverage target: **≥ 90%** on `./kdrive/... ./internal/...`, enforced by CI.

## Architecture

```
kdrive/                         public library (import path: github.com/stillsource/kdrive-fuse/kdrive)
├── doc.go                      package-level godoc
├── client.go                   Client + New(token, driveID, opts ...Option)
├── options.go                  WithHTTPClient / WithBaseURL / WithUploadBaseURL / WithLogger / WithRetries
├── response.go                 internal request / retry / decode plumbing
├── errors.go                   sentinels (ErrNotFound, ErrAuth, ErrConflict, ErrValidation, ErrRateLimit, ErrServer)
│                               + HTTPError, built on scality/go-errors
├── validate.go                 validateName / validateFolderID / validateFileID
├── files.go                    FilesService + Files interface
│                               (List, Stat, Download, DownloadStream, Upload, Mkdir, Delete, Rename, Move)
├── files_options.go            UploadInput
├── files_types.go              FileInfo, FileType
├── shares.go                   SharesService + Shares interface (Publish)
├── shares_types.go             ShareInfo
├── internal/hash/xxh3.go       xxh3-64 + "xxh3:" prefix for upload hashing
└── kdrivefakes/                FilesFake + SharesFake (stub/results/calls pattern, concurrency-safe getters)
internal/vfs/                   FUSE implementation — not importable by downstream
├── fs.go                       KDriveFS shared state + NewRootDirNode constructor
├── dir.go                      DirNode — Lookup / Readdir / Getattr / Create / Mkdir / Unlink / Rmdir / Rename
├── file.go                     FileNode + readHandle (disk-cached) + writeHandle (tempfile + upload-on-Flush)
├── cache.go                    DirCache — TTL cache for directory listings
└── diskcache.go                DiskCache — LRU disk cache keyed by (fileID, last_modified_at)
cmd/kdrive-fuse/                binary entry point
├── main.go                     signal handling + mount
└── config/env.go               envconfig loader
```

### Design choices

- **Services pattern** (inspired by `google/go-github`): `client.Files.List(ctx, id)`, `client.Shares.Publish(ctx, id)`. Extensible — new resources ship as new services.
- **Functional options** (inspired by `slack-go`): `kdrive.New(token, driveID, WithHTTPClient(...), WithLogger(...), ...)`.
- **Interface-first for consumers**: VFS and the portfolio depend on `kdrive.Files` / `kdrive.Shares` interfaces, not `*Client`. Mockable via `kdrivefakes`.
- **Typed errors with `scality/go-errors`**: sentinels + automatic stack traces + structured properties. Consumer checks with `errors.Is(err, kdrive.ErrNotFound)`.
- **Strict input validation** before any HTTP call (reject `/`, NUL, control bytes, `.`/`..`, 255-byte cap).

### Data flow — read

1. `ls` / `lookup` → `DirNode.list()` → cache hit OR `Files.List(ctx, folderID)` → cache set
2. `cat file` → `FileNode.Open(readonly)` returns `readHandle`
3. First `Read` → `DiskCache.Open(fileID, last_modified_at, size)`:
   - Cache hit: bump atime, return `*os.File`
   - Cache miss: evict LRU if over budget, download full body to `~/.cache/kdrive-fuse/{id}_{mtime}`, return handle
4. Subsequent `Read(off)` → `ReadAt` on the cached file

Cache invalidation is implicit: a remote mtime change produces a different cache key. Old entries are orphaned and reclaimed by the LRU sweeper.

### Data flow — write

1. `cp src ~/kDrive-vfs/dst` (new file) → `DirNode.Create` returns a `writeHandle` with `existingFileID=0`
2. `echo > existing` → `FileNode.Open(O_WRONLY|O_TRUNC)` returns a `writeHandle` with `existingFileID=f.info.ID`
3. Kernel sends `Setattr(size=N)` for truncate — may arrive *before* `Open` (zeroes `f.info.Size`, so `Open` skips the seed) or *after* `Open` with no file handle (so it truncates the active `writeHandle`'s tempfile via the `FileNode.wh` back-reference). Both orders are handled so a short rewrite never keeps a stale tail.
4. `Write(data, off)` → `WriteAt` on the tempfile
5. `Flush` → compute xxh3, `Files.Upload(ctx, UploadInput{...})` → patch `FileNode.info` with the returned `FileInfo` → invalidate parent dir cache
6. `Release` → close + remove the tempfile

Upload is single-shot (whole file buffered, capped at 100 MB in practice), no retry (body reader is consumed on the first attempt).

### Data flow — delete

`rm` / `rmdir` → `DirNode.Unlink` / `Rmdir` → `removeChild` looks up the ID from the cached listing → `Files.Delete` (soft-delete, returns `cancel_id` and stays recoverable from kDrive trash) → cache invalidate.

## kDrive API quirks (learned the hard way)

- **Upload uses a DIFFERENT HOST**: `api.kdrive.infomaniak.com/2/drive/{driveID}/upload` (NOT `api.infomaniak.com`). The `api.infomaniak.com/files/{parentID}/file?type=txt` endpoint looks like upload but only creates empty Office documents (3-byte BOM). Source of truth: `Infomaniak/desktop-kDrive` on GitHub, `src/libsyncengine/jobs/network/kDrive_API/upload/uploadjob.cpp`.
- **Upload required query params**: `file_name` + `directory_id` + `conflict=error` + `created_at` + `last_modified_at` + `total_size` + `total_chunk_hash=xxh3:<16hex>`. The hash is xxh3-64 of the full body as 16 lowercase hex chars, prefixed with `xxh3:` (no `XXH3_` prefix — `xxhsum -H3` emits that; strip it).
- **Edit existing file**: same upload endpoint, but replace `file_name` / `directory_id` / `conflict` with `file_id=N`. File ID is preserved.
- **Chunked upload for > 100 MB**: session flow at `/drive/{driveID}/upload/session/{start,{token}/chunk,{token}/finish}`. Not implemented yet — single-shot for every size.
- **Upload body must have an explicit `Content-Length`** — the server rejects chunked-encoded requests. Set `req.ContentLength`.
- **List pagination default is 10** — loop on `?page=N&per_page=500` until a page returns fewer than 500 entries.
- **Delete is soft** — returns `{"cancel_id": "...", "valid_until": ...}`. The file is recoverable from trash until `valid_until`.
- **Truncate-on-open requires Setattr** — the kernel sends `Setattr(size=0)` before `Open` for `O_TRUNC`. Without `NodeSetattrer` the open returns ENOTSUP and userspace sees "Operation not supported".
- **Download redirects to a CDN** at `https://*.download.kdrive.infomaniakusercontent.com` — Go's `http.Client` strips `Authorization` on cross-host redirects, but the redirect URL is pre-signed so this is fine.
- **WebDAV at `{driveID}.connect.kdrive.infomaniak.com` is not really WebDAV** — PROPFIND returns 403 with no `DAV:` header. Don't bother with rclone or davfs2; use this client.

## Inode numbering

kDrive file/folder IDs are used directly as FUSE inode numbers (`uint64(f.ID)`). Stable across restarts.

Exception: `Create` uses a temporary inode (`folderID<<32 ^ len(name)`) until the first `Flush` completes. The `FileNode.info` is patched with the real ID via the upload callback, so subsequent `Open(O_WRONLY)` enters edit mode correctly.

## Tests

Ginkgo v2 + Gomega.

- `kdrive/*_test.go` — `httptest.Server` + handler fixtures. All files in `package kdrive` (white-box), single `TestKdrive` entry point. See `kdrive_suite_test.go` for the shared `newTestFixture` helper and `recordingHandler` (token-leak assertions).
- `internal/vfs/*_test.go` — unit tests for pure helpers (DirCache, DiskCache, writeHandle, readHandle) plus real FUSE mount integration tests that exercise `DirNode` / `FileNode` via syscalls on a temp mountpoint. See `node_test.go` `newMountFixture` — the fake must be fully populated **before** calling `fs.Mount` (concurrent kernel goroutines make mid-test mutation race-prone).

CI (`.github/workflows/ci.yml`) runs `go vet`, the race detector, coverage gate (≥ 90%), and `golangci-lint` on every push.

## Known gaps

See `ROADMAP.md`. Top missing work: chunked upload for files > 100 MB, `kdshare` CLI, `.trash/` virtual directory, real `Setattr` persistence (`touch` mtime), kDrive xattrs surface, Prometheus metrics.
