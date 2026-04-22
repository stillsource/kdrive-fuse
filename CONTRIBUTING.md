# Contributing

## Dev workflow

```bash
git clone https://github.com/stillsource/kdrive-fuse
cd kdrive-fuse
make test
make lint
```

## Guidelines

- All new code must ship with Ginkgo tests. Coverage gate is **90%** on `./kdrive/... ./internal/...`.
- Public API surface stays in `./kdrive/`. FUSE internals live under `./internal/vfs/` (not importable by downstream).
- Error handling: sentinel errors in `errors.go`, rely on `scality/go-errors` for stack traces and structured properties. Never format errors with raw URLs that contain tokens.
- Input validation at the API boundary via `validate.go`. Reject early — before any HTTP call.
- No direct `*Client` in the signature of consumer-facing code: depend on the `kdrive.Files` / `kdrive.Shares` interfaces for mockability.

## Commit messages

Conventional Commits: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`.

## Pull requests

1. `make test-race` green
2. `make lint` green
3. Coverage stays above 90%
4. Update `CHANGELOG.md` (if present) and `ROADMAP.md` if scope changes
