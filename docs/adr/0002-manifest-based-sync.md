# 2. Manifest-based change detection; two explicit one-way mirrors

- Status: Accepted
- Date: 2026-06-17

## Context

`kdrive sync` needs to detect what changed between a local tree and its kDrive
copy. But the kDrive API exposes **no content hash** for existing files, and an
upload **restamps `last_modified_at`** to the upload time. So change detection can
rely on neither a remote hash nor a local-vs-remote mtime comparison.

## Decision

Track a **local manifest** as the last-synced baseline: per file (relative to the
sync root) record `size`, local mtime, `remote_id`, and remote mtime. Compare each
side against the manifest, never the two sides against each other. The manifest
lives at `$XDG_STATE_HOME/kdrive/<hash>.tsv`, keyed by the (local root, remote
root) pair, outside the synced tree.

`kdrive sync` is **two explicit one-way mirrors** — push (default) and `--pull` —
used one at a time, matching the sequential pull → edit → push workflow. It is
**not** an automatic two-way merge (bisync). It talks to the API directly, not
through the FUSE mount.

## Consequences

- Steady-state push needs no remote listing: the manifest carries `remote_id`, so
  overwrites and deletes go by id.
- Blind spot: an edit that keeps the exact same size **and** mtime is invisible —
  negligible in practice (any real write moves the mtime).
- A rename is initially handled as delete + re-upload; a server-side move
  optimisation and conflict handling are addressed in ADR 0003.
