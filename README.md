# kdrive-fuse

Mount an [Infomaniak kDrive](https://www.infomaniak.com/en/hosting/kdrive) remote as a local FUSE filesystem, backed by a disk cache. Written from scratch against the kDrive REST API v2 (WebDAV is not offered for all accounts).

`kdrive-fuse` is a self-contained application. The kDrive REST client is an **internal** infrastructure adapter inside the binary — it is not a public, importable Go library.

## Features

- Mounts a kDrive drive (or any subfolder) as a normal local directory tree — `ls`, `cat`, `cp`, `rm`, `mkdir`, `mv` just work
- LRU disk cache at `~/.cache/kdrive-fuse/{fileID}_{last_modified_at}` — invalidates automatically when the remote file changes; configurable byte budget
- Streaming downloads with HTTP `Range`; first read fetches the body, subsequent reads are local
- In-place edits of existing files, committed on close (see notes below)
- Large files (> 100 MB) upload via the kDrive chunked upload-session flow (50 MB chunks, per-chunk retry, cancel-on-failure)
- Files and directories owned by the mounting user, so file managers can delete/edit them
- Soft deletes — removed files stay recoverable from the kDrive trash
- Automatic retry on transient failures (429 / 5xx / transport) with exponential backoff; uploads use a dedicated HTTP client with a longer (2 min) timeout to ride out large transfers and slow/degraded responses
- `xxh3` content hashing for uploads (kDrive requirement)
- Typed errors internally with `scality/go-errors`; tokens never appear in logs or errors
- ≥90% test coverage, CI-enforced (Ginkgo v2 + Gomega, `httptest`-based HTTP tests + real FUSE mount integration tests)

## Requirements

A FUSE userspace helper must be installed: `fuse3` on Linux (`sudo apt-get install fuse3` / `sudo pacman -S fuse3`), or [macFUSE](https://macfuse.io) on macOS.

## Installation

Prebuilt binaries for Linux and macOS (amd64 + arm64) are attached to each [GitHub Release](https://github.com/stillsource/kdrive-fuse/releases) (verify with `checksums.txt`). Or install with Go:

```bash
go install github.com/stillsource/kdrive-fuse/cmd/kdrive-fuse@latest
go install github.com/stillsource/kdrive-fuse/cmd/kdrive@latest
```

Or from source:

```bash
git clone https://github.com/stillsource/kdrive-fuse
cd kdrive-fuse
make build            # produces ./bin/kdrive-fuse and ./bin/kdrive
make install          # optional: copies both binaries to ~/bin
```

## Usage

Point it at a kDrive drive via environment variables:

```bash
export KDRIVE_API_TOKEN="..."
export KDRIVE_DRIVE_ID="1234567"
export KDRIVE_MOUNT="$HOME/kDrive-vfs"
# Optional:
export KDRIVE_ROOT_FOLDER_ID="1"                 # default: drive root
export KDRIVE_DISK_CACHE_DIR="$HOME/.cache/kdrive-fuse"
export KDRIVE_DISK_CACHE_MAX_GB="2"
export KDRIVE_CACHE_TTL_SECONDS="30"
export KDRIVE_READONLY="1"                       # mount read-only (reject all writes with EROFS)
export KDRIVE_LOG_FORMAT="text"                  # log format: text (default) or json (jq-friendly)
export KDRIVE_METRICS_ADDR=":9090"               # serve Prometheus /metrics on this addr (off by default)

kdrive-fuse
```

When `KDRIVE_METRICS_ADDR` is set (e.g. `:9090`), the daemon serves a Prometheus
text-format `/metrics` endpoint on that address. It is off by default. Exposed
metrics: `kdrive_api_requests_total{method,status}`, `kdrive_bytes_uploaded_total`,
`kdrive_bytes_downloaded_total`, `kdrive_cache_hits_total`,
`kdrive_cache_misses_total`, and the `kdrive_cache_bytes_on_disk` gauge.

Or copy [`.env.example`](./.env.example) to `.env`, fill it in, and load it with `set -a; source .env; set +a` before running — `.env` is gitignored, so your token never lands in the repo.

Run it as a systemd user service to auto-mount at login — see the example unit [`examples/kdrive-vfs.service`](./examples/kdrive-vfs.service).

## CLI / sync

The `kdrive` binary is a command-line companion to the FUSE daemon. It provides the following subcommands:

```
kdrive sync [flags] [LOCAL] [REMOTE]
```

Mirrors a local directory tree and its kDrive copy. Both `LOCAL` and `REMOTE` are optional positional arguments:

- `LOCAL` — local directory (default: `~/Pictures/FUJI/112_FUJI`)
- `REMOTE` — path under the drive root (default: `Rémanence`)

By default the sync pushes (local → remote). Add `--pull` to mirror the other way.

```bash
# Push local → remote (default):
kdrive sync

# Push a different pair:
kdrive sync ~/Photos "My Photos"

# Pull remote → local:
kdrive sync --pull ~/Photos "My Photos"

# Dry-run: classify and print the plan without changing anything:
kdrive sync --dry-run

# Verify local vs remote after the run:
kdrive sync --verify
```

**Flags:**

| Flag | Description |
|---|---|
| `--pull` | Mirror remote → local instead of local → remote |
| `--dry-run` | Classify and print the plan; change nothing |
| `--no-delete` | Never delete on the destination |
| `--force` | Override the deletion guard (and, on pull, the local-drift guard) |
| `--assume-new` | (push only) Skip the first-run bootstrap; treat every local file as new |
| `--refresh` | (push only) Re-bootstrap the manifest from a fresh remote index |
| `--verify` | After the run, report local vs remote presence + size differences |
| `--jobs N` | Concurrent transfers (default 8) |

### share

```
kdrive share REMOTE_PATH
```

Prints the public share URL for a file under the drive root. `REMOTE_PATH` is a slash-separated path (e.g. `Photos/2024/cat.jpg`). The link is created via the `Shares.Publish` API if it does not exist yet, and printed to stdout — useful for scripts.

```bash
kdrive share "Photos/2024/cat.jpg"
# https://kdrive.infomaniak.com/app/share/...
```

### trash

```
kdrive trash <subcommand> [arguments]
```

Browse and manage the kDrive trash without a FUSE mount. The four subcommands cover the full lifecycle of trashed items.

| Subcommand | Description |
|---|---|
| `list` | Print all trashed items (id, name, size) |
| `restore <FILE_ID>` | Restore a trashed item to its original location |
| `purge <FILE_ID> --yes` | Permanently delete one trashed item (irreversible) |
| `empty --yes` | Permanently empty the entire trash (irreversible) |

The `--yes` flag is required for `purge` and `empty` to prevent accidental data loss; omitting it prints a warning and exits 1.

```bash
# List what is in the trash:
kdrive trash list

# Restore a specific item by its id:
kdrive trash restore 42

# Permanently delete one item (requires --yes):
kdrive trash purge 42 --yes

# Permanently empty the whole trash (requires --yes):
kdrive trash empty --yes
```

**Note on endpoints:** the Infomaniak Android app lists trash on a v3 path; this client appends `/trash` to the same v2 base it uses for all other routes. Verify against the live API before relying on write operations. Read operations (`list`) are safe to run without risk.

### search

```
kdrive search QUERY...
```

Full-text search across the drive without a FUSE mount. One or more words are joined with spaces and sent as the search query. Matching files are printed to stdout (one per line: id, name, size), so the ids can be piped directly to `kdrive share` or `kdrive trash`.

```bash
# Search for a file by name or content:
kdrive search annual report

# Pipe to share:
kdrive search budget 2025 | awk '{print $1}' | xargs -I{} kdrive share {}
```

**Note on endpoints:** the Infomaniak Android app uses a v3 path (`GET /3/drive/{id}/files/search`); this client appends `/files/search` to the same v2 base it uses for all other routes. If the endpoint is wrong it only errors non-destructively — verify against the live API.

**Change detection — manifest baseline.** Push tracks state in a TSV manifest at `$XDG_STATE_HOME/kdrive/<hash>.tsv` (falling back to `~/.local/state/kdrive/`), keyed by a hash of the (local root, remote root) pair. Each entry records size, local mtime, remote file ID, and remote mtime from the last sync. On a steady-state push the planner compares local size + mtime against the manifest: a file is unchanged, an overwrite, a new upload, or a delete — no remote listing required, because the manifest carries remote IDs.

The kDrive API exposes no content hash for existing files, which is why size + mtime is used as the change signal rather than a checksum. Use `--verify` to confirm presence and size correctness after a push.

On the first push to a non-empty remote (or with `--refresh`), `kdrive sync` bootstraps the manifest from a fresh remote index so existing files are not re-uploaded wholesale.

`kdrive` also reads the same `KDRIVE_API_TOKEN`, `KDRIVE_DRIVE_ID`, and related `KDRIVE_*` environment variables as the daemon.

## Supported operations

| Op | Implemented | Note |
|---|---|---|
| List dir | ✅ | pages until exhausted |
| Stat | ✅ | |
| Download | ✅ | full + range stream |
| Upload | ✅ | single-shot ≤ 100 MB, chunked session > 100 MB; retries transient failures |
| Mkdir | ✅ | |
| Delete | ✅ | soft-delete (trashable, recoverable) |
| Rename | ✅ | |
| Move | ✅ | |
| Share | ✅ | get-or-create public link |
| Chunked upload (> 100 MB) | ✅ | upload-session flow, 50 MB chunks, per-chunk retry |
| Trash browsing / management | ✅ | `kdrive trash` (list/restore/purge/empty) |
| xattrs for kDrive metadata | ❌ | roadmap |

In-place rewrites through the mount (`echo > existing`, truncating edits) are committed on close: the working file is filled lazily and uploaded once the content is final, so the kernel's FLUSH/WRITE ordering can't drop data.

Files and directories are owned by the user who mounted the filesystem (kDrive has no POSIX ownership of its own). Without this they would default to `root`, and a file manager like Nautilus — which decides "can delete / can trash" from write access to the parent directory — would refuse to delete or edit them.

See [`ROADMAP.md`](./ROADMAP.md) for planned work, and [`docs/adr/`](./docs/adr/) for the architecture decisions behind the design.

## Development

```bash
make test          # run tests
make test-race     # with race detector
make test-coverage # HTML report + total percent
make lint          # golangci-lint
make build         # build both binaries (./bin/kdrive-fuse, ./bin/kdrive)
```

CI enforces `go vet`, race-detector tests, coverage ≥ 90%, and `golangci-lint` on every push.

## Releasing

Push a semver tag (`vX.Y.Z`) — the release workflow runs the test suite, then [GoReleaser](https://goreleaser.com) cross-compiles both binaries (`kdrive-fuse` and `kdrive`), writes `checksums.txt`, and publishes a GitHub Release with a changelog grouped from Conventional Commits. The version is embedded via `-ldflags` and reported by `kdrive-fuse --version` / `kdrive --version`.

```bash
git tag vX.Y.Z && git push origin vX.Y.Z   # triggers .github/workflows/release.yml
```

## License

MIT — see [LICENSE](./LICENSE).
