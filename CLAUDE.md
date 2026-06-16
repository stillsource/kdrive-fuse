# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working in this repository.

## What this is

A self-contained Go **application** that mounts an Infomaniak kDrive remote as a FUSE filesystem, backed by a disk cache. The product is the `cmd/kdrive-fuse` binary.

The kDrive REST API v2 client lives **inside** the application as an internal infrastructure adapter (`pkg/infrastructure/kdriveapi`). It is no longer a public, importable library — there is no `github.com/stillsource/kdrive-fuse/kdrive` package to depend on. The module is layered under `pkg/` following clean architecture: every other layer depends inward on `pkg/domain`.

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

Test coverage target: **≥ 90%** on `./pkg/...` (the logic layers; `cmd` is composition glue), enforced by CI. Tests still run on `./pkg/... ./cmd/...`.

## Architecture

Clean architecture, layered under `pkg/`. Dependencies point inward: `presentation` and `infrastructure` depend on `usecase` + `service` + `domain`; `usecase` depends on `service` + `domain`; `domain` depends on nothing internal.

```
pkg/domain/                     canonical, dependency-free core — imports nothing internal
├── doc.go                      package-level godoc
├── file.go                     FileInfo, FileType (entity types)
├── share.go                    ShareInfo
├── errors.go                   sentinels (ErrNotFound, ErrAuth, ErrConflict, ErrValidation, ErrRateLimit, ErrServer)
│                               + HTTPError, built on scality/go-errors
└── validate.go                 ValidateName / ValidateFolderID / ValidateFileID (reject / NUL ctrl . .. 255-byte cap)
pkg/service/                    the ports — the interfaces the use cases depend on (the "what", not the "how")
├── file.go                     FileReader / FileWriter / FileManager
├── cache.go                    ContentCache (disk-backed file content)
├── sharer.go                   Sharer (public-link creation)
├── upload_input.go             UploadInput (the write request DTO)
└── servicefakes/               in-memory fakes for FileReader/FileWriter/FileManager/Sharer (stub/results/calls)
pkg/usecase/                    application logic — one type per operation, wired over service ports
├── list_dir.go                 ListDir (cache-through directory listing)
├── read_file.go                ReadFile (content-cache read)
├── seed_content.go             SeedContent (lazy pull of remote content for edits)
├── commit_write.go             CommitWrite (upload + parent-cache invalidate)
├── delete_entry.go             DeleteEntry, rename_entry.go RenameEntry, make_dir.go MakeDir
└── share_file.go               ShareFile (defined; not yet wired into the FUSE tree — see kdshare in ROADMAP)
pkg/infrastructure/             the adapters — concrete implementations of the service ports
├── kdriveapi/                  internal HTTP adapter for the kDrive REST API v2 (NOT a public library)
│   ├── client.go               Client + New(token, driveID, opts ...Option)
│   ├── options.go              WithBaseURL / WithUploadBaseURL / WithLogger / WithRetries / WithHTTPClient / WithUploadTimeout
│   │                           reads use a 60s http client; uploads a separate 2m client (large/slow transfers); default 5 retries
│   ├── response.go             request / retry / decode plumbing
│   ├── ports.go                the service interfaces the client satisfies
│   ├── files.go                *FilesService (List, Stat, Download, DownloadStream, Upload, Mkdir, Delete, Rename, Move)
│   ├── shares.go               *SharesService (Publish)
│   ├── errors.go               HTTP-status → domain-sentinel mapping
│   └── internal/hash/xxh3.go   xxh3-64 + "xxh3:" prefix for upload hashing
├── listingcache/memory.go      DirCache — TTL cache for directory listings; NewDirCache(ttl)
├── contentcache/disk.go        DiskCache — LRU disk cache keyed by (fileID, last_modified_at);
│                               NewDiskCache(dir, maxBytes, files service.FileReader)
└── di/                         composition root — builds + memoizes the object graph from one Config
    ├── container.go            Config + Container + NewContainer
    ├── client.go               lazy kdriveapi.New (applies base-URL / logger options)
    ├── content_cache.go        lazy contentcache.NewDiskCache
    └── fuse.go                 KDriveFS() + RootNode() — delegates use-case wiring to fuse.NewKDriveFS
pkg/presentation/fuse/          presentation layer — kernel-driven node/handle state machines
├── doc.go                      package-level godoc
├── fs.go                       KDriveFS (holds the use cases + uid/gid) + NewKDriveFS (FUSE composition root)
│                               + NewRootDirNode constructor
├── dir.go                      DirNode — Lookup / Readdir / Getattr / Create / Mkdir / Unlink / Rmdir / Rename
└── file.go                     FileNode + readHandle (disk-cached) + writeHandle (tempfile + commit-on-close)
cmd/kdrive-fuse/                binary entry point
├── main.go                     --version + signal handling; builds di.NewContainer → RootNode → fs.Mount
└── config/env.go               envconfig loader (KDRIVE_* → di.Config)
```

### Design choices

- **Clean architecture / ports & adapters**: `pkg/usecase` types depend only on the `pkg/service` interfaces (ports), never on `kdriveapi` directly. `pkg/infrastructure/kdriveapi` is one adapter that satisfies those ports; swapping the backend means writing a new adapter, not touching use cases.
- **Composition roots**: `pkg/infrastructure/di` is the application composition root — `NewContainer(Config)` builds and memoizes the whole graph (API client → content cache → FUSE filesystem → root node) with lazy getters. It delegates the use-case wiring to `fuse.NewKDriveFS`, which is the FUSE composition root that constructs the listing cache and every use case over the client + content cache. `main.go` just fills a `di.Config` and asks for `RootNode()`.
- **Services pattern** (inspired by `google/go-github`) inside the API adapter: `client.Files.List(ctx, id)`, `client.Shares.Publish(ctx, id)`.
- **Functional options** (inspired by `slack-go`): `kdriveapi.New(token, driveID, WithBaseURL(...), WithLogger(...), ...)`.
- **Typed errors with `scality/go-errors`** in `pkg/domain`: sentinels + automatic stack traces + structured properties. Callers check with `errors.Is(err, domain.ErrNotFound)`.
- **Strict input validation** in `pkg/domain` before any HTTP call (reject `/`, NUL, control bytes, `.`/`..`, 255-byte cap).

The DI container builds the graph once at boot, so every flow below runs through the use cases the FUSE nodes hold (`KDriveFS.ListDir`, `.ReadFile`, `.SeedContent`, `.CommitWrite`, `.DeleteEntry`, `.RenameEntry`, `.MakeDir`), which in turn call the `pkg/service` ports satisfied by the `kdriveapi` / `listingcache` / `contentcache` adapters.

### Data flow — read

1. `ls` / `lookup` → `DirNode.list()` → `ListDir` use case → `DirCache` hit OR `FileReader.List(ctx, folderID)` → cache set
2. `cat file` → `FileNode.Open(readonly)` returns `readHandle`
3. First `Read` → `ReadFile` use case → `ContentCache.Open(fileID, last_modified_at, size)`:
   - Cache hit: bump atime, return `*os.File`
   - Cache miss: evict LRU if over budget, download full body to `~/.cache/kdrive-fuse/{id}_{mtime}`, return handle
4. Subsequent `Read(off)` → `ReadAt` on the cached file

Cache invalidation is implicit: a remote mtime change produces a different cache key. Old entries are orphaned and reclaimed by the LRU sweeper.

### Data flow — write

1. `cp src ~/kDrive-vfs/dst` (new file) → `DirNode.Create` returns a `writeHandle` with `existingFileID=0`
2. `echo > existing` → `FileNode.Open(O_WRONLY)` returns a `writeHandle` (`existingFileID=f.info.ID`) over an **empty** working tempfile — no eager seed
3. Kernel sends `Setattr(size=N)` for truncate — may arrive before or after `Open`; either way it's recorded (the `FileNode.wh` back-reference, or a zero `f.info.Size` seen at `Open`) and **suppresses the seed**, so a short rewrite never keeps a stale tail
4. `Write(data, off)` → on the **first** write of a non-truncating edit, the `SeedContent` use case pulls the remote content into the working file (`DownloadStream`, lazy seed); then `WriteAt`
5. **Commit** (the `CommitWrite` use case: `FileWriter.Upload` → patch `FileNode.info` → invalidate parent dir cache in `DirCache`) happens once the content is final: on a `Flush` that follows a `Write` (so `close()` surfaces upload errors), or on `Release` as a safety net (writes after the last flush, a truncate with no write, a new empty file). A `Flush` before any write is a no-op
6. `Release` → commit if still pending, then close + remove the working file

Each commit is single-shot (whole file buffered, capped at 100 MB in practice). Transient failures (429 / 5xx / transport errors) are retried with exponential backoff: the body is an `io.ReadSeeker` rewound before each attempt. A non-transient 4xx (e.g. hash mismatch) fails fast without retry. Because the commit waits for a write (or `Release`), it is immune to the kernel sending FLUSH before the WRITEs on a truncating rewrite.

### Data flow — delete

`rm` / `rmdir` → `DirNode.Unlink` / `Rmdir` → `removeChild` looks up the ID from the cached listing → `DeleteEntry` use case → `FileManager.Delete` (soft-delete, returns `cancel_id` and stays recoverable from kDrive trash) → cache invalidate.

## kDrive API quirks (learned the hard way)

- **Upload uses a DIFFERENT HOST**: `api.kdrive.infomaniak.com/2/drive/{driveID}/upload` (NOT `api.infomaniak.com`). The `api.infomaniak.com/files/{parentID}/file?type=txt` endpoint looks like upload but only creates empty Office documents (3-byte BOM). Source of truth: `Infomaniak/desktop-kDrive` on GitHub, `src/libsyncengine/jobs/network/kDrive_API/upload/uploadjob.cpp`.
- **Upload required query params**: `file_name` + `directory_id` + `conflict=error` + `created_at` + `last_modified_at` + `total_size` + `total_chunk_hash=xxh3:<16hex>`. The hash is xxh3-64 of the full body as 16 lowercase hex chars, prefixed with `xxh3:` (no `XXH3_` prefix — `xxhsum -H3` emits that; strip it).
- **Edit existing file**: same upload endpoint, but replace `file_name` / `directory_id` / `conflict` with `file_id=N`. File ID is preserved.
- **Chunked upload for > 100 MB**: implemented via the upload-session flow on `uploadBaseURL` — `POST /upload/session/start` → `POST /upload/session/{token}/chunk` × N → `POST /upload/session/{token}/finish`, with `DELETE /upload/session/{token}` to cancel. Triggered when `Size > 100 MB`; smaller files stay single-shot. The session's `total_chunk_hash` is the hash-of-hashes (xxh3-64 of the concatenated per-chunk xxh3-64 digests — see `ChunkHasher`), not a hash of the whole body.
- **Upload body must have an explicit `Content-Length`** — the server rejects chunked-encoded requests. Set `req.ContentLength`.
- **List pagination default is 10** — loop on `?page=N&per_page=500` until a page returns fewer than 500 entries.
- **Delete is soft** — returns `{"cancel_id": "...", "valid_until": ...}`. The file is recoverable from trash until `valid_until`.
- **Truncate-on-open requires Setattr** — the kernel sends `Setattr(size=0)` before `Open` for `O_TRUNC`. Without `NodeSetattrer` the open returns ENOTSUP and userspace sees "Operation not supported".
- **Download redirects to a CDN** at `https://*.download.kdrive.infomaniakusercontent.com` — Go's `http.Client` strips `Authorization` on cross-host redirects, but the redirect URL is pre-signed so this is fine.
- **WebDAV at `{driveID}.connect.kdrive.infomaniak.com` is not really WebDAV** — PROPFIND returns 403 with no `DAV:` header. Don't bother with rclone or davfs2; use this client.

## POSIX attributes

kDrive has no POSIX ownership. Every node stamps the mounting user's uid/gid (`KDriveFS.Uid`/`Gid`, defaulted from `os.Getuid()`/`os.Getgid()` in `NewKDriveFS`, applied via `applyOwner` in Getattr/Setattr/Lookup/Mkdir/Create). Without it nodes default to root (uid 0); the mounting user then has no write on the parent directory, so `rm` reports "write-protected" and Nautilus refuses to delete/trash (it derives "can delete" from parent-dir write access). The mount sets no `default_permissions`, so the kernel doesn't enforce these bits — they exist so user-space tools and file managers behave.

## Inode numbering

kDrive file/folder IDs are used directly as FUSE inode numbers (`uint64(f.ID)`). Stable across restarts.

Exception: `Create` uses a temporary inode (`folderID<<32 ^ len(name)`) until the first `Flush` completes. The `FileNode.info` is patched with the real ID via the upload callback, so subsequent `Open(O_WRONLY)` enters edit mode correctly.

## Tests

Ginkgo v2 + Gomega. Each package has its own `*_suite_test.go` entry point.

- `pkg/infrastructure/kdriveapi/*_test.go` — `httptest.Server` + handler fixtures, white-box. See `kdrive_suite_test.go` for the shared `newTestFixture` helper and `recordingHandler` (token-leak assertions).
- `pkg/domain/*_test.go` — validation + error-mapping unit tests.
- `pkg/usecase/*_test.go` — use cases driven against the `pkg/service/servicefakes` fakes (no HTTP, no mount).
- `pkg/infrastructure/{listingcache,contentcache}/*_test.go` — unit tests for `DirCache` and the LRU `DiskCache`.
- `pkg/presentation/fuse/*_test.go` — unit tests for the pure handle helpers (writeHandle, readHandle) plus real FUSE mount integration tests that exercise `DirNode` / `FileNode` via syscalls on a temp mountpoint. See `node_test.go` `newMountFixture` — the fakes must be fully populated **before** calling `fs.Mount` (concurrent kernel goroutines make mid-test mutation race-prone).

CI (`.github/workflows/ci.yml`) runs `go vet`, the race detector, coverage gate (≥ 90%), and `golangci-lint` on every push.

## Known gaps

See `ROADMAP.md`. Top missing work: `kdshare` CLI, `.trash/` virtual directory, real `Setattr` persistence (`touch` mtime), kDrive xattrs surface, Prometheus metrics.
