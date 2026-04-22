# Roadmap

## ✅ Shipped in v0.1.0

### Rename / Move
`NodeRenamer` on `DirNode`, backed by `POST /files/{id}/rename` + `POST /files/{id}/move/{destID}`. Cross-directory rename is decomposed into move-then-rename. Both parent caches invalidated.

### Streaming downloads via disk cache
`Files.DownloadStream(ctx, fileID, off, length)` sets an HTTP `Range` header. `readHandle` opens from the disk cache (full download on first access, local file on subsequent accesses). Benchmark: 887 ms → 1 ms on a 641 KB PDF.

### Write to existing files
`FileNode.Open(O_WRONLY|O_RDWR)` returns a `writeHandle` seeded with the remote content (unless `O_TRUNC`). Upload happens in edit mode (`file_id` query param replaces `file_name` / `directory_id` / `conflict`). `NodeSetattrer` accepts truncate-on-open as a no-op so the kernel doesn't bail with ENOTSUP.

### Readdir pagination
`Files.List` loops on `?page=N&per_page=500` until a page returns fewer than 500 entries. No more silent truncation at 10.

### LRU disk cache
`~/.cache/kdrive-fuse/{fileID}_{last_modified_at}`. Implicit invalidation on mtime change (a new mtime yields a new cache key). LRU by atime (`ModTime`). Budget configurable via `KDRIVE_DISK_CACHE_MAX_GB` (default 2 GB). Eviction runs on write when the budget is exceeded.

### Typed errors
Sentinels via `scality/go-errors`: `ErrNotFound`, `ErrAuth`, `ErrConflict`, `ErrValidation`, `ErrRateLimit`, `ErrServer`. `HTTPError` wraps unknown 4xx/5xx. Automatic stack traces and structured properties (status code, URL, response snippet). Tokens never appear in logs or errors.

### Strict input validation
`kdrive/validate.go` rejects names with `/`, `\x00`, control bytes (< 0x20), DEL, `.`, `..`, empty, or > 255 bytes — before any HTTP call.

### Services pattern + functional options
Client constructor `kdrive.New(token, driveID, opts ...Option)` with embedded `Files` and `Shares` services. Interfaces (`kdrive.Files`, `kdrive.Shares`) exposed for downstream mocking; ready-made fakes in `kdrive/kdrivefakes/`.

### 91% test coverage
Ginkgo v2 + Gomega; `httptest` for HTTP paths, real FUSE mount for node integration; race-clean.

---

## Blocking remaining work

### Chunked upload (> 100 MB)
kDrive supports a session flow: `POST /upload/session/start` → `POST /upload/session/{token}/chunk` × N → `POST /upload/session/{token}/finish`, with `DELETE /upload/session/{token}` for cancellation. Current single-shot uploads risk request timeouts and RAM spikes on very large files. Reference implementation: `Infomaniak/desktop-kDrive`, `src/libsyncengine/jobs/network/kDrive_API/upload/upload_session/`.

---

## UX

### `kdshare <path>` CLI
A small binary (or subcommand) that prints the public share URL for a file in the mounted tree. Wraps `client.Shares.Publish`. Useful for scripts.

### `.trash/` virtual directory
Expose the kDrive trash as `~/kDrive-vfs/.trash/` via `GET /files/trash`. `rm .trash/x` purges permanently, `mv .trash/x /target/` restores. Needs a dedicated trash endpoint family in the API.

### Upload conflict handling
Uploads currently use `conflict=error` (fails on duplicate). Alternatives: `conflict=version` (new version) and `conflict=rename` (appends `(1)`). Expose a knob on `UploadInput` and default to `error`, but let the FUSE writeHandle choose `rename` so `cp` of a duplicated filename produces the familiar `foo (1).txt` behavior.

---

## Hygiene

### Setattr `utimens` → `last_modified_at`
`Setattr` currently accepts timestamps but does not persist them. Mapping `in.Atime` / `in.Mtime` to a remote update would make `touch` behave as expected. Requires a `PUT /files/{id}` (or equivalent) call on the API side.

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
Upload and Create already disable auto-retry (the body reader is consumed). Verify that Rename / Move are idempotent (second call returns 404 or success) and document the guarantees; tighten if needed.

---

## Bonus

### Full-text search
`GET /files/search?q=...` exposed as a virtual directory `~/kDrive-vfs/.search/{query}/` whose contents are the matching files.

### Multi-drive mount
Mount multiple drives under `/mnt/kdrive/{drive_id}/`. One `KDriveFS` per drive; the top-level inode lists configured drives.

### Prometheus metrics
HTTP side-car exposing `/metrics`. Counters to track: `kdrive_api_requests_total{op,status}`, `kdrive_bytes_uploaded`, `kdrive_bytes_downloaded`, `kdrive_cache_hit_ratio`, `kdrive_cache_bytes_on_disk`.
