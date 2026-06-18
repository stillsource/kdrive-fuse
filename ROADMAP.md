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

### Prometheus metrics (`KDRIVE_METRICS_ADDR`)
Optional `/metrics` HTTP side-car on the FUSE daemon, off by default and enabled by setting `KDRIVE_METRICS_ADDR` (e.g. `:9090`). Built with the Go standard library only — a stdlib zero-dep Prometheus text exposition (no `github.com/prometheus/client_golang`). A mutex-guarded `Registry` in `pkg/infrastructure/metrics` collects the counters and renders the exposition via an `http.Handler`; the kDrive client and the disk cache are instrumented through small interfaces they define themselves (so neither imports the metrics package — `*metrics.Registry` satisfies them), and `di.Config` carries an optional `*metrics.Registry` (nil = disabled, the default, so the CLI and tests are unaffected). Exposed: `kdrive_api_requests_total{method,status}`, `kdrive_bytes_uploaded_total`, `kdrive_bytes_downloaded_total`, `kdrive_cache_hits_total`, `kdrive_cache_misses_total`, and the `kdrive_cache_bytes_on_disk` gauge. Cache effectiveness is exposed as the idiomatic hits/misses counters rather than a `cache_hit_ratio` gauge — the ratio is computed at query time in Prometheus. **Migration path:** if the counter set grows, swap to `github.com/prometheus/client_golang` (`Registry` → `prometheus.Registerer`, the observe methods → `prometheus.Counter`/`Gauge`, `Handler` → `promhttp.Handler`).

### Upload conflict handling
`UploadInput.Conflict` selects the conflict mode for new uploads: `""` (default) → `conflict=error`; `"version"` → keep existing as a prior version; `"rename"` → append ` (1)` to the name. The field is ignored in edit mode (uses `file_id`). The FUSE `writeHandle` sets `Conflict: "rename"` for new-file creates so `cp` of a duplicate filename produces the familiar `foo (1).txt` behavior instead of failing. The sync path leaves `Conflict` empty (defaults to `error`) so the conflict-reconciliation logic in `PushExecutor.Upload` (detect `ErrConflict` → overwrite by id) still works correctly. Applies to both single-shot and chunked upload paths.

### `kdrive share REMOTE_PATH` CLI
`kdrive share` resolves a remote path to its file ID (read-only listing, never creates directories), calls `usecase.ShareFile` which wraps `client.Shares.Publish`, and prints the public URL to stdout. Useful for scripts. Wired in `pkg/presentation/cli/share.go`.

### `kdrive trash` CLI (list/restore/purge/empty)
Delivered as a `kdrive trash` CLI subcommand rather than a FUSE virtual `.trash/` directory (to avoid overloading `rm`/`mv` with destructive/restore semantics that would surprise users). Four operations: `list` (read-only, safe to run), `restore <FILE_ID>`, `purge <FILE_ID> --yes` (permanent single-item delete), `empty --yes` (permanent full-empty). Destructive operations require `--yes` and refuse otherwise with a warning. Wired in `pkg/presentation/cli/trash.go`; backed by four new methods on `*kdriveapi.FilesService` (`ListTrash`, `RestoreTrash`, `PurgeTrash`, `EmptyTrash`) in `pkg/infrastructure/kdriveapi/trash.go`.

**Endpoint note:** the Infomaniak Android app lists trash on a v3 path (`GET /3/drive/{id}/trash`); this client appends `/trash` to the same v2 base used for all other routes (`GET /2/drive/{id}/trash`). Verify read-only `list` against the live API first; the v2 path should work for the operations that are non-destructive on a wrong endpoint — they just error.

### xattrs for kDrive metadata
`FileNode` exposes read-only extended attributes via `fs.NodeGetxattrer` + `fs.NodeListxattrer`:
- `user.kdrive.id` — numeric file ID
- `user.kdrive.created_at` — creation timestamp (Unix seconds)
- `user.kdrive.mime_type` — MIME type (only when non-empty)

`getfattr -d <file>` is now a useful scripting primitive. Pure helpers live in `pkg/presentation/fuse/xattr.go`; the interfaces are wired on `FileNode` only (`DirNode` has only a folder ID, not full metadata). Two attributes were intentionally omitted: `user.kdrive.share_url` (generating a public link as a side-effect of reading an xattr would publish every file on `getfattr -d` — a footgun; use `kdrive share` instead) and `user.kdrive.created_by` (not present in `domain.FileInfo`).

### Crash-safe Rename/Move (client-side idempotency)
`--detect-moves` relocations are idempotent on a crash re-run without relying on unverified server semantics. `PushExecutor.Move` Stats the live remote state first and issues a `Move`/`Rename` only for the dimensions (parent, name) that still differ from the target. A re-run where the first run already relocated the file but crashed before the manifest was checkpointed performs zero mutating calls; a partial run (Move applied, Rename not) self-heals by issuing only the missing half. Because the decision comes from the live state rather than the manifest's stale source path, an out-of-band relocation since the last snapshot is corrected (driven to target) instead of acted on blindly. The name decision is exact (the API always returns the name); the parent decision uses `parent_id` when present and falls back to the manifest's source-vs-target intent when the response omits it (`0` never equals a valid folder id, so a blind compare would spuriously move). This completes the idempotency story alongside Upload (409 → overwrite-by-id) and Delete (already-gone → success).

---

## ✅ Shipped — operational flags & CLI extras

### `--readonly` mount (`KDRIVE_READONLY`)
`KDRIVE_READONLY=1` makes the FUSE mount reject every mutation with EROFS — Create / Mkdir / Unlink / Rmdir / Rename, plus a writable `Open` and a size-changing `Setattr`. Reads still work. Safe for a shared or audited drive.

### Structured JSON logs (`KDRIVE_LOG_FORMAT`)
`KDRIVE_LOG_FORMAT=json` switches the daemon and the CLI client logger to `slog.NewJSONHandler` (jq-friendly); default `text` keeps `journalctl` human-readable. Standard-library `log/slog` only — no logging dependency.

### `kdrive search QUERY...` CLI
Delivered as a `kdrive search` CLI subcommand rather than a FUSE virtual `.search/` directory (to avoid custom Lookup/Readdir interception and query-as-path escaping). Joins positional args with spaces, calls `GET /files/search?q=<query>&per_page=500&page=N` (paginated until exhausted), and prints one line per match (id, name, size) to stdout. Empty query exits 2 with usage; zero results print "no matches". Ids are scriptable — pipe to `kdrive share` or `kdrive trash`. Wired in `pkg/presentation/cli/search.go`; backed by `FilesService.Search` in `pkg/infrastructure/kdriveapi/search.go`; port `service.Searcher`; use case `usecase.SearchFiles`.

**Endpoint note:** the Infomaniak Android app calls `GET /3/drive/{id}/files/search`; this client appends `/files/search` to the v2 base used for all other routes. If the v2 path returns an error it is non-destructive — verify against the live API.

---

## Future / deferred

### Multi-drive mount — deferred (YAGNI for a single-drive setup)
Mount multiple drives under `/mnt/kdrive/{drive_id}/` (one `KDriveFS` per drive; a top-level inode listing configured drives). Deferred: a large refactor of the single-drive assumption (`appconfig`, the di container, the mount root) for speculative value on a personal single-drive setup. Revisit if a second drive is ever added.

### Full-text search as a virtual directory — superseded
The original `~/kDrive-vfs/.search/{query}/` virtual-directory idea was delivered instead as the `kdrive search` CLI (above). Not planned as a vdir.
