# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

FUSE filesystem driver that mounts an Infomaniak kDrive remote at a local path via the kDrive REST API v2. Built because WebDAV is broken on the owner's kDrive account (returns 403 with no `DAV:` header on every endpoint).

Auto-starts at session via `~/.config/systemd/user/kdrive-vfs.service`. Mount point configurable, default `~/kDrive-vfs/`.

## Build / run / ops

```bash
go build -o ~/bin/kdrive-fuse .            # build
systemctl --user restart kdrive-vfs.service # apply new binary
systemctl --user status kdrive-vfs.service  # check
journalctl --user -u kdrive-vfs.service -f  # tail logs
fusermount -u ~/kDrive-vfs                  # manual unmount
```

Config lives in `~/.config/kdrive-fuse/env` (loaded by systemd `EnvironmentFile`). Required: `KDRIVE_API_TOKEN`, `KDRIVE_DRIVE_ID`, `KDRIVE_MOUNT`. Optional: `KDRIVE_ROOT_FOLDER_ID` (default `1` = drive root), `KDRIVE_CACHE_TTL_SECONDS` (default `30`), `KDRIVE_BASE_URL`, `KDRIVE_DISK_CACHE_DIR` (default `~/.cache/kdrive-fuse`), `KDRIVE_DISK_CACHE_MAX_GB` (default `2`).

No test suite yet.

## Architecture

```
main.go         loads config, builds API client + VFS + disk cache, mounts, handles SIGINT/SIGTERM
api/            kDrive REST client — one file per operation
  client.go     Bearer auth, exponential retry (3x, starts 1s), retries on 5xx+429
  types.go      FileInfo shared across endpoints
  list.go       GET /files/{folderID}/files with pagination loop
  stat.go       GET /files/{fileID}
  download.go   GET /files/{fileID}/download — Download() []byte + DownloadStream() io.ReadCloser with Range
  upload.go     POST api.kdrive.infomaniak.com/2/drive/{driveID}/upload (DIFFERENT HOST, see quirks)
  mkdir.go      POST /files/{parentID}/directory with {"name": X}
  delete.go     DELETE /files/{fileID} (soft-delete to kDrive trash)
  rename.go     POST /files/{fileID}/rename + POST /files/{fileID}/move/{destDirID}
vfs/            go-fuse/v2 node implementations
  fs.go         KDriveFS struct + NewRootDirNode constructor
  dir.go        DirNode — Lookup/Readdir/Getattr/Create/Mkdir/Unlink/Rmdir/Rename
  file.go       FileNode + readHandle (disk-cached) + writeHandle (tempfile + upload-on-Flush, supports edit mode)
  cache.go      DirCache — TTL cache for directory listings keyed by folder ID
  diskcache.go  DiskCache — LRU disk cache keyed by (fileID, last_modified_at)
```

### Data flow — read

1. `ls` / `lookup` → `DirNode.list()` → cache hit OR `api.List(folderID)` → cache set
2. `cat file` → `FileNode.Open(readonly)` returns `readHandle`
3. First `Read` → `DiskCache.Open(fileID, last_modified_at, size)`:
   - Cache hit: bump atime, return *os.File
   - Cache miss: evict LRU if over budget, download full body to `~/.cache/kdrive-fuse/{id}_{mtime}`, return handle
4. Subsequent `Read(off)` → `ReadAt` on the cached file

Cache invalidation is implicit: remote mtime change → different cache key. Old entries orphaned, reclaimed by LRU.

### Data flow — write

1. `cp src ~/kDrive-vfs/dst` (new file) → `DirNode.Create` returns `writeHandle` with `existingFileID=0`
2. `echo > existing` → `FileNode.Open(O_WRONLY|O_TRUNC)` returns `writeHandle` with `existingFileID=f.info.ID`
3. Kernel calls `Setattr(size=0)` for truncate — accepted as no-op
4. `Write(data, off)` → `WriteAt` on tempfile
5. `Flush` → compute xxh3 hash, `api.Upload(parentID, existingFileID, name, tempfile, size)` → patches FileNode.info with returned FileInfo → cache invalidate parent dir
6. `Release` → close + remove tempfile

Upload is single-shot (whole file buffered), no retry (body reader consumed).

### Data flow — delete

`rm` / `rmdir` → `DirNode.Unlink`/`Rmdir` → `removeChild` looks up ID from cached listing → `api.Delete` (soft-delete returns `cancel_id`, recoverable from kDrive trash) → cache invalidate.

## kDrive API quirks (learned the hard way)

- **Upload uses a DIFFERENT HOST**: `api.kdrive.infomaniak.com/2/drive/{driveID}/upload` (NOT `api.infomaniak.com`). The `api.infomaniak.com/files/{parentID}/file?type=txt` endpoint looks like upload but only creates empty Office documents (3-byte BOM). Source of truth: `Infomaniak/desktop-kDrive` GitHub repo, `src/libsyncengine/jobs/network/kDrive_API/upload/uploadjob.cpp`.
- **Upload required query params**: `file_name` + `directory_id` + `conflict=error` + `created_at` + `last_modified_at` + `total_size` + `total_chunk_hash=xxh3:<16hex>`. Hash is xxh3-64 of full body as hex (no prefix in hex part, no `XXH3_` prefix from xxhsum).
- **Edit existing file**: same upload endpoint, replace `file_name`/`directory_id`/`conflict` with `file_id=N`. Preserves file ID.
- **Chunked upload for > 100 MB**: session flow at `/drive/{driveID}/upload/session/{start,token/chunk,token/finish}`. Not implemented yet — single-shot for all sizes.
- **Upload body must have explicit Content-Length** — server rejects chunked-encoded. Set `req.ContentLength`.
- **List pagination default is 10** — must loop on `?page=N&per_page=500` until a page returns fewer than 500.
- **Delete is soft** — returns `{"cancel_id": "...", "valid_until": ...}`. File recoverable from trash until `valid_until`.
- **Truncate-on-open requires Setattr** — kernel calls `Setattr(size=0)` before `Open` for `O_TRUNC`. Without `NodeSetattrer`, open returns ENOTSUP and the user sees "Operation not supported".
- **Download redirects to CDN** on `https://*.download.kdrive.infomaniakusercontent.com` — Go's http.Client strips Authorization on cross-host redirect, but the redirect URL is pre-signed so this is fine.
- **WebDAV endpoint `{driveID}.connect.kdrive.infomaniak.com` is NOT WebDAV** — returns 403 on PROPFIND with no `DAV:` header. Don't waste time with rclone/davfs2.

## Inode numbering

kDrive file/folder IDs are used directly as FUSE inode numbers (`uint64(f.ID)`). Stable across restarts. Exception: `Create` uses a temporary inode (`folderID<<32 ^ len(name)`) until upload completes — the FileNode is not re-fetched after upload, so its info stays `{Name: name}` until next directory refresh.

## Known gaps

See `ROADMAP.md`. Top missing ops: chunked upload for > 100 MB files, share links CLI, trash virtual dir, real Setattr persistence (mtime), xattrs for kDrive metadata.
