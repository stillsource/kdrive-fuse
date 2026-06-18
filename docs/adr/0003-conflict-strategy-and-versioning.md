# 3. Conflict handling and versioning policy

- Status: Accepted
- Date: 2026-06-18

## Context

The one-way mirrors of ADR 0002 can silently clobber an out-of-band edit (e.g. a
change made through the kDrive web UI between a pull and a push) — a real
data-loss risk. We also needed a release/versioning policy for the completion
program (data-safety hardening, the FUSE roadmap, and bonus features).

## Decision

**Conflicts — Option 1 then Option 2, never automatic bisync:**

- *Option 1 (floor):* push becomes drift-aware. Before overwriting or deleting a
  remote file it confirms the remote still matches the manifest's recorded
  `remote_mtime`/size; on divergence it skips and warns unless `--force` (the
  symmetric of the existing pull drift guard).
- *Option 2 (built on Option 1):* a conflict-aware two-way mode does a 3-way
  classification (local vs baseline, remote vs baseline), applies non-conflicting
  changes in the right direction, and **materialises** conflicts as
  `name.conflict-YYYY-MM-DD.ext` — never merging.
- Explicitly **not** automatic bisync (rclone-style auto-merge + delete
  propagation): disproportionate data-loss surface for a personal tool.

**Versioning:** SemVer is keyed off the **user-facing surface** (CLI
flags/commands, `KDRIVE_*` env, the manifest format, machine-readable output and
exit codes), not Go API symbols. The completion work is almost entirely additive
(opt-in flags/commands) → minors; format/default changes are kept
backward-compatible (e.g. the manifest format-version header). A single release is
cut at the end of the program — `1.0.0` if the surface is judged stable, otherwise
a `0.x` minor — with no per-track intermediate releases.

## Consequences

- The silent-clobber risk is removed while the simple sequential workflow is
  preserved.
- The per-PR breakdown of the completion program lives in `ROADMAP.md` and the
  task tracker, not as a plan doc in the repo.
