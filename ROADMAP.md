# Roadmap

## ✅ Shipped

Released through **v0.3.0**. The `kdrive sync` CLI and the `kdrive` binary landed
in **v0.3.0**; the FUSE daemon and its upload-reliability work shipped across the
v0.1–v0.2 line.

### Rename / Move
`NodeRenamer` on `DirNode`, backed by `POST /files/{id}/rename` + `POST /files/{id}/move/{destID}`. Cross-directory rename is decomposed into move-then-rename. Both parent caches invalidated.

### Streaming downloads via disk cache
`Files.DownloadStream(ctx, fileID, off, length)` sets an HTTP `Range` header. `readHandle` opens from the disk cache (full download on first access, local file on subsequent accesses). Benchmark: 887 ms → 1 ms on a 641 KB PDF.

### Write to existing files (in place)
`FileNode.Open(O_WRONLY|O_RDWR)` returns a `writeHandle` over a working tempfile. An edit pulls remote content lazily on the first `Write` (skipped for a truncating rewrite, so no stale tail). The content is committed exactly once it's final — on a `Flush` that follows a `Write` (so `close()` still surfaces upload errors), with `Release` as the safety net for writes after the last flush, a truncate with no write, or a new empty file. Because the commit waits for a write (or `Release`), it's immune to the kernel's FLUSH-before-WRITE ordering on truncating rewrites. Edit mode uses the `file_id` query param; `NodeSetattrer` records the truncate.

### Node ownership (deletable / editable in file managers)
Every node stamps the mounting user's uid/gid (`applyOwner`, defaulted from `os.Getuid()`/`os.Getgid()`). kDrive has no POSIX ownership, so without this nodes report `root`; the mounting user then lacks write on the parent directory and Nautilus refuses to delete or trash (and `rm` reports "write-protected"). The mount sets no `default_permissions`, so these bits aren't enforced by the kernel — they exist so user-space tools behave.

### Readdir pagination
`Files.List` loops on `?page=N&per_page=500` until a page returns fewer than 500 entries. No more silent truncation at 10.

### LRU disk cache
`~/.cache/kdrive-fuse/{fileID}_{last_modified_at}`. Implicit invalidation on mtime change (a new mtime yields a new cache key). LRU by atime (`ModTime`). Budget configurable via `KDRIVE_DISK_CACHE_MAX_GB` (default 2 GB). Eviction runs on write when the budget is exceeded.

### Typed errors
Sentinels via `scality/go-errors`: `ErrNotFound`, `ErrAuth`, `ErrConflict`, `ErrValidation`, `ErrRateLimit`, `ErrServer`. `HTTPError` wraps unknown 4xx/5xx. Automatic stack traces and structured properties (status code, URL, response snippet). Tokens never appear in logs or errors.

### Strict input validation
`pkg/domain/validate.go` rejects names with `/`, `\x00`, control bytes (< 0x20), DEL, `.`, `..`, empty, or > 255 bytes — before any HTTP call.

### Services pattern + functional options
Internal API client constructor `kdriveapi.New(token, driveID, opts ...Option)` with embedded `Files` and `Shares` services. The use cases depend on the `pkg/service` ports (`FileReader` / `FileWriter` / `FileManager` / `Sharer`), not the concrete client; ready-made fakes in `pkg/service/servicefakes/`.

### ≥90% test coverage (CI-enforced)
Ginkgo v2 + Gomega; `httptest` for HTTP paths, real FUSE mount for node integration; race-clean.

### Clean-architecture restructure
Layered under `pkg/` (`domain` / `service` / `usecase` / `infrastructure` / `presentation`), behaviour unchanged. The kDrive client is now an internal adapter (`pkg/infrastructure/kdriveapi`) behind the `pkg/service` ports — no longer a public importable library. Use cases (`pkg/usecase`) hold the application logic; `pkg/infrastructure/di` is the composition root that builds the object graph from a single `Config`.

### Release automation
`v*` tags trigger `.github/workflows/release.yml`: the test suite runs, then GoReleaser builds linux/darwin × amd64/arm64 binaries + `checksums.txt`, embeds the version via `-ldflags` (`kdrive-fuse --version`), and publishes a GitHub Release with a changelog grouped from Conventional Commits. Config in `.goreleaser.yaml`.

### Chunked upload (> 100 MB)
Files larger than 100 MB upload via the kDrive upload-session flow (`POST /upload/session/start` → `POST /upload/session/{token}/chunk` × N → `POST /upload/session/{token}/finish`, `DELETE /upload/session/{token}` to cancel). 50 MB chunks, per-chunk retry on transient failures, cancel-on-failure to release the partial session, and a `ChunkHasher` hash-of-hashes for `total_chunk_hash`. Smaller files keep the single-shot path.

### Upload resilience
kDrive's upload endpoint has intermittent 502 / slow-response windows. Uploads (single-shot and chunked) use a dedicated HTTP client with a 2-minute timeout (vs 60s for reads, which stay snappy), tunable via `WithUploadTimeout`. The default retry count is 5 (6 attempts), so a file's upload rides out a multi-minute transient window instead of failing the copy. Also: the upload retry wraps the body in `io.NopCloser` so the transport can't close the caller's `*os.File` between attempts (a retry would otherwise fail on a closed body).

### `kdrive sync` — CLI sync command (v0.3.0)
`kdrive` binary (`cmd/kdrive`) with a `sync` subcommand that mirrors a local tree and its kDrive copy. Push by default (local → remote); `--pull` mirrors the other way. Change detection uses a manifest baseline (size + mtime; the kDrive API exposes no content hash) stored at `$XDG_STATE_HOME/kdrive/<hash>.tsv`. Steady-state push needs no remote listing because the manifest carries remote IDs. Flags: `--pull`, `--dry-run`, `--no-delete`, `--force`, `--assume-new`, `--refresh`, `--verify`, `--jobs N`. Deletion guard refuses to delete > 20% of the baseline without `--force`. Bootstrap from a remote index on first run or `--refresh`. `Verify` reports presence + size discrepancies post-sync.

Supporting packages: `pkg/appconfig` (shared `KDRIVE_*` env loader), `pkg/infrastructure/manifest` (TSV store), `pkg/infrastructure/remoteindex` (`Build` + `Resolver`), `pkg/presentation/cli` (`Run` dispatcher + `sync` command), `pkg/syncer` (planner, runners, executors, `WalkLocal`, guards, `Verify`).

### Setattr `utimens` → `last_modified_at` (touch support)
`Setattr` now persists mtime via `POST /files/{id}/last-modified` with body `{"last_modified_at": <unix seconds>}` — the endpoint confirmed in Infomaniak's desktop-kDrive client. The `SetModifiedAt` method on `FilesService` mirrors the `Rename` adapter; the `SetMtime` use case invalidates the parent listing on success. `FileNode.Setattr` resolves the parent `DirNode`'s `folderID`, calls `SetMtime.Execute`, and patches `f.info.LastModifiedAt`. On a `ReadOnly` mount, an mtime `Setattr` returns `EROFS`. `touch file` now works as expected.

### Upload conflict handling
`UploadInput.Conflict` selects the conflict mode for new uploads: `""` (default) → `conflict=error`; `"version"` → keep existing as a prior version; `"rename"` → append ` (1)` to the name. The field is ignored in edit mode (uses `file_id`). The FUSE `writeHandle` sets `Conflict: "rename"` for new-file creates so `cp` of a duplicate filename produces the familiar `foo (1).txt` behavior instead of failing. The sync path leaves `Conflict` empty (defaults to `error`) so the conflict-reconciliation logic in `PushExecutor.Upload` (detect `ErrConflict` → overwrite by id) still works correctly. Applies to both single-shot and chunked upload paths.

### `kdrive share REMOTE_PATH` CLI
`kdrive share` resolves a remote path to its file ID (read-only listing, never creates directories), calls `usecase.ShareFile` which wraps `client.Shares.Publish`, and prints the public URL to stdout. Useful for scripts. Wired in `pkg/presentation/cli/share.go`.

---

## UX

### `.trash/` virtual directory
Expose the kDrive trash as `~/kDrive-vfs/.trash/` via `GET /files/trash`. `rm .trash/x` purges permanently, `mv .trash/x /target/` restores. Needs a dedicated trash endpoint family in the API.


---

## Hygiene

### xattrs for kDrive metadata
Implement `NodeGetxattrer` + `NodeListxattrer`:
- `user.kdrive.id` → numeric ID
- `user.kdrive.mime_type`
- `user.kdrive.created_by`
- `user.kdrive.share_url` (lazy — generate via `Shares.Publish` on first read)

Makes `getfattr -d` a useful primitive for scripts.

### `--readonly` flag
Env `KDRIVE_READONLY=1` disables Create / Mkdir / Unlink / Rmdir / Rename — they return EROFS. Safe for mounting a shared or audited drive.

### Structured JSON logs
Switch from `slog.NewTextHandler` to `slog.NewJSONHandler` so records are grep-friendly with `jq`. Keep the text handler as a `--log-format=text` opt-out.

### Idempotency for non-idempotent ops
Upload now retries transient failures (429 / 5xx / transport) by rewinding its `io.ReadSeeker` body before each attempt; non-transient 4xx fail fast. Verify that Rename / Move are idempotent (second call returns 404 or success) and document the guarantees; tighten if needed.

---

## Bonus

### Full-text search
`GET /files/search?q=...` exposed as a virtual directory `~/kDrive-vfs/.search/{query}/` whose contents are the matching files.

### Multi-drive mount
Mount multiple drives under `/mnt/kdrive/{drive_id}/`. One `KDriveFS` per drive; the top-level inode lists configured drives.

### Prometheus metrics
HTTP side-car exposing `/metrics`. Counters to track: `kdrive_api_requests_total{op,status}`, `kdrive_bytes_uploaded`, `kdrive_bytes_downloaded`, `kdrive_cache_hit_ratio`, `kdrive_cache_bytes_on_disk`.
