# Contributing

Thanks for helping improve kdrive-fuse. It is a self-contained Go application
with two binaries (`cmd/kdrive-fuse`, `cmd/kdrive`); the kDrive REST client is an
**internal** adapter, not a public importable library.

## Dev workflow

```bash
git clone https://github.com/stillsource/kdrive-fuse
cd kdrive-fuse
cp .env.example .env          # fill in KDRIVE_API_TOKEN / KDRIVE_DRIVE_ID / KDRIVE_MOUNT
set -a; source .env; set +a   # load it (`.env` is gitignored)

make build                    # ./bin/kdrive-fuse and ./bin/kdrive
make test                     # unit + integration
make test-race                # with the race detector
make test-coverage            # HTML report + total %
make lint                     # golangci-lint
```

A FUSE userspace helper is required to run or mount: `fuse3` on Linux, macFUSE on
macOS.

## Architecture

Clean architecture, layered under `pkg/`; dependencies point inward
(`presentation` / `infrastructure` → `usecase` → `service` → `domain`, and
`domain` imports nothing internal):

- `pkg/domain` — entities, typed sentinel errors (`scality/go-errors`), and input
  validation. Imports nothing internal.
- `pkg/service` — the ports (interfaces) the use cases depend on, plus
  `servicefakes` for tests.
- `pkg/usecase` — one type per operation, wired over the ports (never over the
  concrete client).
- `pkg/infrastructure` — adapters: `kdriveapi` (internal HTTP client for the
  kDrive REST API v2), `listingcache`, `contentcache`, `manifest`, `remoteindex`,
  `syncer`, and `di` (the composition root).
- `pkg/presentation` — `fuse` (kernel-driven nodes/handles) and `cli` (the
  `kdrive` subcommand dispatcher + `sync`).
- `pkg/appconfig` — shared `KDRIVE_*` env loader used by both binaries.
- `cmd/kdrive-fuse`, `cmd/kdrive` — thin entry points.

See [`CLAUDE.md`](./CLAUDE.md) for the full package map and the hard-won kDrive
API quirks.

## Conventions

- New code ships with Ginkgo v2 + Gomega tests. The coverage gate is **≥ 90 % on
  `./pkg/...`** (the logic layers; `cmd` is composition glue), enforced by CI.
- Use cases depend on `pkg/service` ports, never on `kdriveapi` directly — keep
  the concrete client out of consumer-facing signatures so flows stay mockable.
- Typed errors via `pkg/domain` sentinels; never format an error with a URL that
  carries a token (the API tests assert tokens never leak).
- Validate names and ids at the boundary (`pkg/domain/validate.go`) before any
  HTTP call.

## Commit messages

Conventional Commits: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`,
`ci:`, `perf:`. The release changelog is grouped from these prefixes. Write commit
messages, docs, and PR descriptions in **English**, and do **not** add a
`Co-Authored-By` trailer.

## Pull requests

1. `make test-race` green
2. `make lint` green (0 issues)
3. Coverage stays ≥ 90 % on `./pkg/...`
4. Update `ROADMAP.md` (and `README.md` / `CLAUDE.md`) when scope or behavior
   changes
