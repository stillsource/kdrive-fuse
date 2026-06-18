# Architecture Decision Records

This directory records the **decisions** behind kdrive-fuse — the *why*, with an
explicit status — so the reasoning survives even as the code changes.

- ADRs are immutable once accepted. A decision that changes gets a **new** ADR
  that supersedes the old one (mark the old one `Superseded by NNNN`).
- ADRs are **not** a description of the current code. The living architecture
  reference is [`CLAUDE.md`](../../CLAUDE.md); the user-facing surface is
  [`README.md`](../../README.md); planned work is [`ROADMAP.md`](../../ROADMAP.md).
- Step-by-step implementation plans are disposable scaffolding and are **not**
  kept in the repo (see `.gitignore`).

| ADR | Title | Status |
|---|---|---|
| [0001](./0001-clean-architecture.md) | Clean architecture under `pkg/`, kDrive client as an internal adapter | Accepted |
| [0002](./0002-manifest-based-sync.md) | Manifest-based change detection; two explicit one-way mirrors | Accepted |
| [0003](./0003-conflict-strategy-and-versioning.md) | Conflict handling and versioning policy | Accepted |
