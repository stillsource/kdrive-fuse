# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working in this repository.

## What this is

A self-contained Go **application** with two binaries:

- `cmd/kdrive-fuse` — mounts an Infomaniak kDrive remote as a FUSE filesystem, backed by a disk cache.
- `cmd/kdrive` — a command-line companion for operations that don't require a mount (currently: `kdrive sync`, `kdrive share`, `kdrive trash`).

The kDrive REST API v2 client lives **inside** the application as an internal infrastructure adapter (`pkg/infrastructure/kdriveapi`). It is not a public, importable library — there is no `github.com/stillsource/kdrive-fuse/kdrive` package to depend on. The module is layered under `pkg/` following clean architecture: every other layer depends inward on `pkg/domain`.

Built from scratch because WebDAV is not reliably offered on every kDrive account (the `{driveID}.connect.kdrive.infomaniak.com` endpoint returns 403 on PROPFIND with no `DAV:` header).

## Build / run / ops

```bash
make build                                         # ./bin/kdrive-fuse and ./bin/kdrive
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

Shared `KDRIVE_*` env (loaded by both binaries via `pkg/appconfig`):
Required: `KDRIVE_API_TOKEN`, `KDRIVE_DRIVE_ID`.
Optional: `KDRIVE_ROOT_FOLDER_ID` (default `1`), `KDRIVE_BASE_URL`, `KDRIVE_UPLOAD_BASE_URL`, `KDRIVE_CACHE_TTL_SECONDS` (default `30`), `KDRIVE_DISK_CACHE_DIR` (default `~/.cache/kdrive-fuse`), `KDRIVE_DISK_CACHE_MAX_GB` (default `2`), `KDRIVE_READONLY` (default `false`; set to `1` or `true` to reject all writes with EROFS), `KDRIVE_LOG_FORMAT` (default `text`; set to `json` for structured jq-friendly logs), `KDRIVE_METRICS_ADDR` (default empty/off; set to e.g. `:9090` to serve a Prometheus `/metrics` side-car — daemon only), `KDRIVE_WALK_PARALLELISM` (default `8`; concurrent listings when `kdrive search` walks the tree), `KDRIVE_UPLOAD_PARALLELISM` (default `4`; concurrent chunk uploads for the chunked upload-session path — files > 16 MB; runs on the stdlib default HTTP transport, whose connection pool aggregates bandwidth across the parallel chunk requests — do NOT force HTTP/1.1, it breaks the upload host connection).

Daemon-only env (loaded by `cmd/kdrive-fuse/config`):
Required: `KDRIVE_MOUNT`.

Binary config for the daemon lives in `~/.config/kdrive-fuse/env` (loaded by systemd `EnvironmentFile`).

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
└── share_file.go               ShareFile (wired into `kdrive share` CLI subcommand)
pkg/appconfig/                  shared KDRIVE_* env loader used by both binaries
└── appconfig.go                Config + Load; Config.DI(logger) produces a di.Config
pkg/infrastructure/             the adapters — concrete implementations of the service ports
├── kdriveapi/                  internal HTTP adapter for the kDrive REST API v2 (NOT a public library)
│   ├── client.go               Client + New(token, driveID, opts ...Option)
│   ├── options.go              WithBaseURL / WithUploadBaseURL / WithLogger / WithRetries / WithHTTPClient / WithUploadTimeout
│   │                           reads use a 60s http client; uploads a separate 2m client (large/slow transfers); default 5 retries
│   ├── response.go             request / retry / decode plumbing
│   ├── ports.go                the service interfaces the client satisfies
│   ├── files.go                *FilesService (List, Stat, Download, DownloadStream, Upload, Mkdir, Delete, Rename, Move, SetModifiedAt)
│   ├── trash.go                *FilesService trash methods (ListTrash, RestoreTrash, PurgeTrash, EmptyTrash)
│   ├── shares.go               *SharesService (Publish)
│   ├── errors.go               HTTP-status → domain-sentinel mapping
│   └── internal/hash/xxh3.go   xxh3-64 + "xxh3:" prefix for upload hashing
├── listingcache/memory.go      DirCache — TTL cache for directory listings; NewDirCache(ttl)
├── contentcache/disk.go        DiskCache — LRU disk cache keyed by (fileID, last_modified_at);
│                               NewDiskCache(dir, maxBytes, files service.FileReader, obs cacheObserver)
│                               obs (optional, nil = off) gets CacheHit/CacheMiss/SetCacheBytes events
├── metrics/metrics.go          Registry — stdlib zero-dep Prometheus text-exposition collector
│                               (mutex-guarded counters + cache-bytes gauge) + Handler(); New().
│                               *metrics.Registry satisfies the small metricsSink / cacheObserver
│                               interfaces the kdriveapi client and contentcache define themselves,
│                               so neither imports this package. Enabled via KDRIVE_METRICS_ADDR.
├── manifest/                   sync baseline store
│   ├── manifest.go             Manifest (map[rel]Entry) — size, local mtime, remote ID, remote mtime
│   ├── store.go                Load / Save (TSV serialization)
│   └── path.go                 PathFor(localRoot, remoteRoot) → $XDG_STATE_HOME/kdrive/<sha256>.tsv
├── remoteindex/                recursive parallel remote folder snapshot
│   ├── index.go                Build(ctx, Lister, rootID) → map[rel]Entry (id, size, mtime)
│   └── resolver.go             Resolver — Resolve(ctx, relDir) resolves or creates a path to its folder ID
└── di/                         composition root — builds + memoizes the object graph from one Config
    ├── container.go            Config + Container + NewContainer
    ├── client.go               lazy kdriveapi.New (applies base-URL / logger options)
    ├── content_cache.go        lazy contentcache.NewDiskCache
    └── fuse.go                 KDriveFS() + RootNode() — delegates use-case wiring to fuse.NewKDriveFS
pkg/syncer/                     sync engine (push/pull orchestration)
├── plan.go                     PlanPush / Bootstrap — classifies local vs manifest into upload/overwrite/delete
├── plan_pull.go                PlanPull — classifies remote index vs manifest into download/delete-local
├── push.go                     Push(ctx, PushOptions, ...) — walk → bootstrap → plan → guard → execute → save
├── pull.go                     Pull(ctx, PullOptions, ...) — index → load manifest → plan → guard → execute → save
├── run.go                      RunPush — concurrent executor driver (worker pool, Result)
├── run_pull.go                 RunPull — concurrent pull executor driver
├── executor.go                 PushExecutor — upload/overwrite/delete per item
├── guard.go                    GuardDeletes / GuardPullDeletes — refuse > 20% deletion without --force
├── walk.go                     WalkLocal — recursive local tree walk → []LocalFile
└── verify.go                   Verify — post-sync presence + size comparison via a fresh remote index
pkg/presentation/fuse/          FUSE presentation layer — kernel-driven node/handle state machines
├── doc.go                      package-level godoc
├── fs.go                       KDriveFS (holds the use cases + uid/gid) + NewKDriveFS (FUSE composition root)
│                               + NewRootDirNode constructor
├── dir.go                      DirNode — Lookup / Readdir / Getattr / Create / Mkdir / Unlink / Rmdir / Rename
├── file.go                     FileNode + readHandle (disk-cached) + writeHandle (tempfile + commit-on-close)
│                               FileNode also implements fs.NodeGetxattrer + fs.NodeListxattrer, exposing
│                               user.kdrive.{id,created_at,mime_type} as read-only extended attributes.
└── xattr.go                    Pure helpers: kdriveXattrs / getXattrValue / listXattrNames
pkg/presentation/cli/           CLI presentation layer — subcommand dispatcher + sync + share commands
├── root.go                     Run(args, version, stdout, stderr) — dispatches --help/--version/sync/share
├── sync.go                     runSync — flag parsing + PushOptions/PullOptions → syncer.Push / syncer.Pull
├── share.go                    runShare — resolve REMOTE_PATH read-only via remoteindex.Lister, publish via usecase.ShareFile, print ShareURL
└── trash.go                    runTrash — browse and manage kDrive trash (list/restore/purge/empty); trashBackend seam; trasher interface
cmd/kdrive-fuse/                FUSE daemon entry point
├── main.go                     --version + signal handling; loads appconfig + config.LoadFUSE → di.NewContainer → fs.Mount
└── config/env.go               mount-only config: FUSE{Mount} loaded from KDRIVE_MOUNT
cmd/kdrive/                     CLI binary entry point
└── main.go                     os.Args[1:] → cli.Run
```

### Design choices

- **Clean architecture / ports & adapters**: `pkg/usecase` types depend only on the `pkg/service` interfaces (ports), never on `kdriveapi` directly. `pkg/infrastructure/kdriveapi` is one adapter that satisfies those ports; swapping the backend means writing a new adapter, not touching use cases.
- **Composition roots**: `pkg/infrastructure/di` is the application composition root — `NewContainer(Config)` builds and memoizes the whole graph (API client → content cache → FUSE filesystem → root node) with lazy getters. It delegates the use-case wiring to `fuse.NewKDriveFS`, which is the FUSE composition root that constructs the listing cache and every use case over the client + content cache. `main.go` just fills a `di.Config` and asks for `RootNode()`.
- **Shared config**: `pkg/appconfig` provides a single `Load()` function that both `cmd/kdrive-fuse` and `cmd/kdrive` use to read the shared `KDRIVE_*` env vars. `Load()` first auto-loads a `.env` file — the working-directory `.env`, or the path in `KDRIVE_ENV_FILE` — via a tiny stdlib parser (`dotenv.go`, systemd `EnvironmentFile` style: `KEY=VALUE`, `#` comments, optional `export`/quotes, values literal so no inline-comment stripping), then composes `envconfig.MultiLookuper(OsLookuper(), MapLookuper(fileVars))` so **real env vars always win over the file**. A missing default `.env` is fine; a missing explicit `KDRIVE_ENV_FILE` is an error. The daemon additionally loads `cmd/kdrive-fuse/config.LoadFUSE()` for `KDRIVE_MOUNT`, which is mount-specific and not needed by the CLI.
- **Services pattern** (inspired by `google/go-github`) inside the API adapter: `client.Files.List(ctx, id)`, `client.Shares.Publish(ctx, id)`.
- **Functional options** (inspired by `slack-go`): `kdriveapi.New(token, driveID, WithBaseURL(...), WithLogger(...), ...)`.
- **Typed errors with `scality/go-errors`** in `pkg/domain`: sentinels + automatic stack traces + structured properties. Callers check with `errors.Is(err, domain.ErrNotFound)`.
- **Strict input validation** in `pkg/domain` before any HTTP call (reject `/`, NUL, control bytes, `.`/`..`, 255-byte cap).

The DI container builds the graph once at boot, so every flow below runs through the use cases the FUSE nodes hold (`KDriveFS.ListDir`, `.ReadFile`, `.SeedContent`, `.CommitWrite`, `.DeleteEntry`, `.RenameEntry`, `.MakeDir`), which in turn call the `pkg/service` ports satisfied by the `kdriveapi` / `listingcache` / `contentcache` adapters.

### kdrive CLI / sync / share

`cmd/kdrive` is a command-line companion to the FUSE daemon. `pkg/presentation/cli.Run` dispatches subcommands: `sync` and `share`.

**`kdrive share REMOTE_PATH`** resolves a path under the drive root to a file ID (read-only listing via `remoteindex.Lister` — never creates directories), calls `usecase.NewShareFile(sharer).Execute(ctx, fileID)` which wraps `service.Sharer.Publish`, and prints `domain.ShareInfo.ShareURL` to stdout. The backend seam (`shareBackend` package var) mirrors the `syncBackend` pattern so tests can inject fakes. Intermediate segments must be directories; the final segment must be a non-directory file.

**`kdrive trash`** manages the kDrive trash via four subcommands: `list` (pages all trashed items, prints id/name/size), `restore <FILE_ID>` (POST /trash/{id}/restore), `purge <FILE_ID> --yes` (DELETE /trash/{id}, permanent), `empty --yes` (DELETE /trash, permanent). Destructive operations (`purge`, `empty`) require `--yes` or exit 1 with an irreversibility warning. The `trashBackend` package-var seam and `trasher` interface (in `trash.go`) follow the same pattern as `shareBackend`/`syncBackend`. Backed by four new methods on `*kdriveapi.FilesService`: `ListTrash`, `RestoreTrash`, `PurgeTrash`, `EmptyTrash` (in `pkg/infrastructure/kdriveapi/trash.go`).

**`kdrive search [--path SUBDIR] TERM...`** is a **client-side filename filter**, not a server search: a file matches when its path (folders + name) contains every whitespace-separated term, case-insensitively. It lists the tree (`remoteindex.Build` from the configured root, or from the `--path` subtree resolved read-only via `remoteindex.ResolveDir` — `--path` results stay full, drive-root-relative) and filters locally, prints one line per match sorted by path (`\t{id}\t{path}\t({size} bytes)\n`), "no matches" on zero results, empty query exits 2 with usage. The walk concurrency is `KDRIVE_WALK_PARALLELISM` (default 8); `--path` scopes the walk to a subtree (much faster on a large drive). The `searchBackend` package-var seam (in `search.go`) returns `filesearch.New(client.Files, app.RootFolderID)` as a `service.Searcher`. Backed by `pkg/infrastructure/filesearch`; port `pkg/service/searcher.go` (`Searcher` → `[]service.SearchHit{Path,ID,Size}`); use case `pkg/usecase/search_files.go`. **Why not the API's `/files/search`:** verified live that v2 ignores `q` (returns the whole drive, and trips the API's 10000-element pagination cap) and v3 is an opaque relevance ranking with indexing lag — neither is a reliable filename filter. Trade-off: a search walks the whole drive.

**`kdrive sync [flags] [LOCAL] [REMOTE]`** mirrors a local directory tree and a kDrive folder (push by default; `--pull` for the reverse). It does not require a FUSE mount.

**Data flow — push:**

1. `WalkLocal(localRoot)` → `[]LocalFile` (relative path, size, mtime)
2. `manifest.Load(manifestPath)` → baseline `Manifest` (or empty on first run)
3. If manifest is empty (and not `--assume-new`) or `--refresh`: `remoteindex.Build` walks the remote tree concurrently (bounded to 8 parallel `List` calls) → `map[rel]Entry`; `Bootstrap` seeds the manifest so existing remote files are not re-uploaded
4. `PlanPush` classifies each local file vs the manifest: absent → `OpUpload`, size/mtime changed → `OpOverwrite` (uses the manifest's stored remote ID, no listing needed), manifest entry with no local file → `OpDelete`
5. `GuardDeletes` rejects > 20% deletions of the baseline without `--force`
6. `RunPush` executes the plan with a worker pool (`--jobs`, default 8): uploads via `*FilesService`, directory resolution via `remoteindex.Resolver` (resolve-or-create, cached, serialized); updates the manifest on each success
7. `manifest.Save(manifestPath)` persists the updated baseline

**Manifest location:** `$XDG_STATE_HOME/kdrive/<sha256(absLocal+"\n"+remote)>.tsv` (falls back to `~/.local/state/kdrive/`). One TSV file per (local root, remote root) pair; lives outside the synced tree.

**Change detection rationale:** the kDrive API exposes no content hash for already-uploaded files, so size + mtime is the change signal. A file present on both sides at the same size is treated as correct (uploads are hash-verified by kDrive on ingest). Use `--verify` for an explicit post-sync check.

**Pull direction:** `PlanPull` classifies the remote index vs the manifest: new remote file → `PullDownload`, remote file gone → `PullDeleteLocal`. A download that would overwrite a locally-modified file (differs from baseline) is skipped with a warning unless `--force`.

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

## UploadInput.Conflict — new-file conflict mode

`service.UploadInput.Conflict` selects the `conflict=` query param / start-body field sent on **new** uploads (ignored when `ExistingFileID > 0` / edit mode):

- `""` (zero value) → `conflict=error` (fail on duplicate name; default for sync)
- `"version"` → keep the existing file as a prior version
- `"rename"` → append ` (1)` to the name (used by FUSE new-file creates)

The sanitization is in `kdriveapi.conflictMode(c string)` (in `files.go`); unrecognized values fall back to `"error"`. Both the single-shot path (`files.go`) and the chunked session start (`upload_session.go`) use `conflictMode(in.Conflict)`.

**FUSE vs sync defaults:**
- `pkg/presentation/fuse/file.go` `commitLocked`: sets `Conflict: "rename"` when `existingFileID == 0` (new file) so `cp` of a duplicate name yields `foo (1).txt` instead of an error.
- `pkg/syncer/executor.go`: leaves `Conflict` empty → defaults to `"error"` → `ErrConflict` triggers the existing reconciliation logic (find child by name → overwrite by id).

## kDrive API quirks (learned the hard way)

- **Upload uses a DIFFERENT HOST**: `api.kdrive.infomaniak.com/2/drive/{driveID}/upload` (NOT `api.infomaniak.com`). The `api.infomaniak.com/files/{parentID}/file?type=txt` endpoint looks like upload but only creates empty Office documents (3-byte BOM). Source of truth: `Infomaniak/desktop-kDrive` on GitHub, `src/libsyncengine/jobs/network/kDrive_API/upload/uploadjob.cpp`.
- **Upload required query params**: `file_name` + `directory_id` + `conflict=error` + `created_at` + `last_modified_at` + `total_size` + `total_chunk_hash=xxh3:<16hex>`. The hash is xxh3-64 of the full body as 16 lowercase hex chars, prefixed with `xxh3:` (no `XXH3_` prefix — `xxhsum -H3` emits that; strip it).
- **Edit existing file**: same upload endpoint, but replace `file_name` / `directory_id` / `conflict` with `file_id=N`. File ID is preserved.
- **Chunked upload for > 16 MB**: implemented via the upload-session flow on `uploadBaseURL` — `POST /upload/session/start` → `POST /upload/session/{token}/chunk` × N → `POST /upload/session/{token}/finish`, with `DELETE /upload/session/{token}` to cancel. Triggered when `Size > 16 MB`; smaller files stay single-shot. Chunks are uploaded in parallel (`KDRIVE_UPLOAD_PARALLELISM`, default 4) when the body is an `io.ReaderAt`; non-seekable bodies fall back to sequential. HTTP/2 is explicitly disabled on the upload transport so each goroutine opens a separate TCP connection that aggregates bandwidth. The session's `total_chunk_hash` is the hash-of-hashes (xxh3-64 of the concatenated per-chunk xxh3-64 digests — see `ChunkHasher`), assembled in chunk order regardless of parallel upload order.
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

See `ROADMAP.md`. The completion program (data-safety hardening + the FUSE roadmap + bonus extras) is done. The only **deferred** item is **multi-drive mount** (YAGNI for a single drive — revisit if a second drive is added). One minor open item: explicitly verify + document Rename/Move idempotency.

Prometheus metrics shipped: set `KDRIVE_METRICS_ADDR` (e.g. `:9090`) to serve a stdlib zero-dep `/metrics` side-car on the daemon (off by default). The `metrics.Registry` (`pkg/infrastructure/metrics`) is wired through `di.Config.Metrics` (nil = off); the kDrive client and disk cache report to it via small self-defined interfaces, so the CLI and all existing tests are unaffected.

`touch` now works: `Setattr` persists mtime via `FileNode.Setattr` → `SetMtime.Execute` → `FilesService.SetModifiedAt` → `POST /files/{id}/last-modified` with body `{"last_modified_at": <unix seconds>}`. On a `ReadOnly` mount, an mtime `Setattr` returns `EROFS`. The parent listing is invalidated on success so subsequent `ls` reflects the new time.
