# 1. Clean architecture under `pkg/`, kDrive client as an internal adapter

- Status: Accepted
- Date: 2026-06-15

## Context

The project began as a kDrive REST client plus a FUSE layer. We wanted the core
logic testable without HTTP or a mount, the storage backend swappable, and no
public Go API surface to maintain (it is an application, not a library).

## Decision

Layer the code under `pkg/`, with dependencies pointing inward:
`presentation` / `infrastructure` → `usecase` → `service` → `domain`, and
`domain` imports nothing internal.

- The kDrive REST client (`pkg/infrastructure/kdriveapi`) is an **internal
  adapter**, not an importable library — there is no public `kdrive` package.
- Use cases depend on the `pkg/service` ports (interfaces), never on the concrete
  client, so they are driven against in-memory fakes in tests.
- `pkg/infrastructure/di` is the composition root; `cmd/*` are thin entry points.

## Consequences

- Swapping the backend means writing a new adapter, not touching use cases.
- A coverage gate of ≥ 90 % on `./pkg/...` is enforceable because the logic
  layers carry no I/O.
- The current package map and the hard-won kDrive API quirks are documented in
  `CLAUDE.md` (the living reference); this ADR records only the decision.
