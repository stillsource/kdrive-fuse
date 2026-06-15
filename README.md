# kdrive-fuse

A Go library and FUSE filesystem for [Infomaniak kDrive](https://www.infomaniak.com/en/hosting/kdrive), written from scratch against the kDrive REST API v2 (WebDAV is not offered for all accounts).

- **`kdrive/`** — public Go client library (import path: `github.com/stillsource/kdrive-fuse/kdrive`)
- **`cmd/kdrive-fuse/`** — FUSE binary that mounts a kDrive remote as a local filesystem with a disk cache

## Features

- Services-based client (`client.Files.*`, `client.Shares.*`) inspired by `google/go-github`
- Functional options for HTTP client, base URLs, logger, retries
- Typed sentinel errors (`ErrNotFound`, `ErrAuth`, `ErrConflict`, `ErrValidation`, `ErrRateLimit`) with `scality/go-errors` stack traces and structured properties
- Strict input validation on filenames (reject `/`, NUL, control bytes, `.`/`..`, 255-byte cap)
- `xxh3` content hashing for uploads (kDrive requirement)
- Streaming downloads with HTTP `Range`
- Edit mode for existing files (`Upload` with `ExistingFileID`)
- Automatic retry on transient failures (429 / 5xx / transport) with exponential backoff — including uploads, whose `io.ReadSeeker` body is rewound before each attempt
- LRU disk cache (FUSE): `~/.cache/kdrive-fuse/{fileID}_{last_modified_at}` invalidates automatically when the remote changes
- 91% test coverage (Ginkgo v2 + Gomega, `httptest`-based HTTP tests + real FUSE mount integration tests)

## Installation

Prebuilt binaries for Linux and macOS (amd64 + arm64) are attached to each [GitHub Release](https://github.com/stillsource/kdrive-fuse/releases) (verify with `checksums.txt`). Or install with Go:

```bash
go install github.com/stillsource/kdrive-fuse/cmd/kdrive-fuse@latest
```

Or from source:

```bash
git clone https://github.com/stillsource/kdrive-fuse
cd kdrive-fuse
make build
./bin/kdrive-fuse
```

## Library usage

```go
import "github.com/stillsource/kdrive-fuse/kdrive"

client := kdrive.New("YOUR_TOKEN", "YOUR_DRIVE_ID",
    kdrive.WithLogger(slog.Default()),
)

infos, err := client.Files.List(ctx, rootFolderID)
if err != nil {
    if errors.Is(err, kdrive.ErrAuth) {
        // rotate token
    }
}

for _, f := range infos {
    fmt.Println(f.ID, f.Name, f.Size, f.IsDir())
}

// Upload a new file
info, err := client.Files.Upload(ctx, kdrive.UploadInput{
    ParentID: rootFolderID,
    Name:     "hello.txt",
    Body:     bytes.NewReader([]byte("hi")),
    Size:     2,
})

// Edit an existing file (replace content)
_, err = client.Files.Upload(ctx, kdrive.UploadInput{
    ExistingFileID: info.ID,
    Body:           bytes.NewReader([]byte("updated")),
    Size:           7,
})

// Share a file
share, err := client.Shares.Publish(ctx, info.ID)
fmt.Println(share.ShareURL)
```

Mocking in your own tests:

```go
import "github.com/stillsource/kdrive-fuse/kdrive/kdrivefakes"

fake := &kdrivefakes.FilesFake{
    ListResults: map[int64]kdrivefakes.ListResult{
        1: {Files: []kdrive.FileInfo{{ID: 10, Name: "hello.txt"}}},
    },
}
var _ kdrive.Files = fake
```

## FUSE binary

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

kdrive-fuse
```

Run it as a systemd user service to auto-mount at login (example unit in `docs/kdrive-vfs.service` if you need it).

## Supported operations

| Op | Implemented | Note |
|---|---|---|
| List dir | ✅ | pages until exhausted |
| Stat | ✅ | |
| Download | ✅ | full + range stream |
| Upload | ✅ | single-shot (≤ 100 MB); retries transient failures |
| Mkdir | ✅ | |
| Delete | ✅ | soft-delete (trashable, recoverable) |
| Rename | ✅ | |
| Move | ✅ | |
| Share | ✅ | get-or-create public link |
| Chunked upload (> 100 MB) | ❌ | roadmap |
| Trash browsing | ❌ | roadmap |
| xattrs for kDrive metadata | ❌ | roadmap |

> **Known limitation:** rewriting a file *in place* through the FUSE mount (e.g. `echo > existing`) is not yet reliable — on a truncating rewrite the kernel can send FLUSH before the WRITEs and the new content gets dropped. Creating new files, reading, renaming, and deleting all work; the library `Upload` (with `ExistingFileID`) edits reliably. A write-path redesign is tracked in [`ROADMAP.md`](./ROADMAP.md).

See [`ROADMAP.md`](./ROADMAP.md) for planned work.

## Development

```bash
make test          # run tests
make test-race     # with race detector
make test-coverage # HTML report + total percent
make lint          # golangci-lint
make build         # build binary
```

CI enforces `go vet`, race-detector tests, coverage ≥ 90%, and `golangci-lint` on every push.

## Releasing

Push a semver tag (`vX.Y.Z`) — the release workflow runs the test suite, then [GoReleaser](https://goreleaser.com) cross-compiles the binaries, writes `checksums.txt`, and publishes a GitHub Release with a changelog grouped from Conventional Commits. The version is embedded via `-ldflags` and reported by `kdrive-fuse --version`.

```bash
git tag v0.2.0 && git push origin v0.2.0   # triggers .github/workflows/release.yml
```

## License

MIT — see [LICENSE](./LICENSE).
