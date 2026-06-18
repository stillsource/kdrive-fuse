# Design — kdrive completion program (hardening + roadmap)

Status: proposed · Date: 2026-06-18

## Context

`v0.2.0` (`971b277`) shipped the FUSE daemon's upload reliability + release
automation, but it **predates the entire `kdrive sync` suite**. The new `kdrive`
binary and its sync command (push, pull, `--verify`, `--refresh`, deletion +
drift guards, Ctrl-C cancellation, ~91 % coverage) sit on `main` across 19
unreleased commits (#10–#19, +12.7k/−0.9k LOC). The tree is clean and there are
no open issues, but **none of the sync work is in any release**. Step 0 of this
program is therefore to cut **`v0.3.0`** (a minor — a burst of new `feat:`,
pre-1.0) from current `main`, giving a clean released baseline before the
completion work begins.

This document plans the remaining work to take the project from "works for the
primary photo-archive case" to "complete": close the known data-safety gaps in
the sync engine, then work through the FUSE daemon roadmap in `ROADMAP.md`, then
the nice-to-have extras.

Two cross-cutting decisions were taken up front (see below): **full scope**
(including the bonus items) and a **conflict strategy of Option 1 then Option 2**
— a one-way drift-aware floor first, then a true conflict-aware two-way mode,
explicitly *not* automatic bisync.

This is a multi-feature program, so it is decomposed into independent PRs, each
with its own plan → implementation → adversarial review → merge-on-green cycle,
in the order below. Each PR keeps the **≥ 90 % coverage gate on `./pkg/...`** and
follows the existing clean-architecture layering (dependencies point inward).

## Cross-cutting decisions

### Conflict handling: Option 1 then Option 2 (never auto-bisync)

The kDrive API exposes **no content hash** on read and **restamps
`last_modified_at` at upload time**, so the only source of truth for "what was
last synced" is the local manifest (size + local mtime + `remote_id` +
`remote_mtime`). Both new modes compare each side against the manifest baseline,
never the two sides directly.

- **Option 1 — one-way mirrors, drift-aware (floor).** Today only *pull* checks
  destination drift before clobbering (`filterDrift`). Push gets the symmetric
  check: before an overwrite or delete, it confirms the remote still matches the
  manifest's recorded `remote_mtime`/`size`; if it drifted (out-of-band web-UI
  edit) it **skips and warns** unless `--force`. Removes the "silent clobber"
  data-loss risk at low cost.
- **Option 2 — conflict-aware two-way (built on Option 1).** A unified mode does
  a 3-way classification (local vs baseline, remote vs baseline) per path,
  applies non-conflicting changes in the correct direction automatically, and
  **materialises conflicts** (`name.conflict-YYYY-MM-DD.ext`) without ever
  merging — the Unison / Syncthing model. Reuses Option 1's remote-drift
  detection.
- **Not** automatic bisync (rclone-style auto-merge + delete propagation):
  disproportionate data-loss surface for a personal tool, and it reverses the
  original explicit decision.

### Standing constraints

- All repository content (commits, docs, PR descriptions) in **English**.
- **No** `Co-Authored-By` / AI co-author trailer on any commit.
- Each PR: branch off `main`, squash-merge via PR (protected base), CI green
  (race + coverage + lint) before merge.

## Track 1 — Sync engine hardening (data-safety first)

### PR A — Transactional manifest / crash-safe checkpointing

**Problem.** `RunPush`/`RunPull` mutate the manifest in memory and `Push`/`Pull`
save it **once at the end**. A crash mid-run leaves a stale baseline, so the next
run re-plans already-completed uploads as new — and a "new" upload of an
already-present file fails with `conflict=error` (409).

**Approach.** Two complementary changes:

1. **Incremental atomic checkpointing.** Flush the manifest atomically
   (temp + rename, already in `manifest.Save`) on a batched cadence — every *K*
   successful operations and/or a short ticker — plus a final flush. Batching
   avoids an O(N²) full rewrite per file on a 7 000-file tree. A crash now loses
   at most the last unflushed batch.
2. **Idempotent upload reconciliation.** When an `OpUpload` (treated as new) hits
   `conflict=error` because a prior interrupted run already created it, reconcile:
   resolve the file id under its parent and fall back to overwrite-by-id, then
   record the entry. Closes the 409 footgun left by the bounded batch loss.

A full WAL/journal was considered and rejected (YAGNI for a single-user tool;
batched checkpoint + idempotency gives crash-safety without the complexity).

**Touches.** `pkg/syncer/run.go`, `run_pull.go` (checkpoint hook),
`pkg/infrastructure/manifest/store.go`, `pkg/syncer/executor.go` (409
reconciliation). **Tests.** Crash-between-op simulation (flush then inject
failure → re-run does not re-upload / does not 409), batched-flush cadence.

### PR B — Configurable + safer deletion guard

**Problem.** The guard threshold is hard-coded at 20 % (`deleteDivisor=5`) and is
**skipped entirely when `baseline==0`**, so a `--refresh` against an empty/wrong
remote can plan deletes for every local file with no guard.

**Approach.** Add `--delete-threshold` (fraction, default `0.20`). When
`baseline==0` *and* the plan contains deletions, treat it as suspicious and
refuse unless `--force`. Keep `--no-delete`.

**Touches.** `pkg/syncer/guard.go`, `pkg/presentation/cli/sync.go`. **Tests.**
threshold parsing/clamping, `baseline==0` + deletes refusal, `--force` override.

### PR C — Push drift detection (Option 1, anti-clobber)

**Approach.** Before executing an `OpOverwrite` or `OpDelete`, stat the remote by
`remote_id` and compare `last_modified_at`/`size` to the manifest entry. On
mismatch, skip + warn (`skip (remote changed): <rel>`) unless `--force`.
Stat-by-id of only the files being changed (usually few); a full-tree change can
reuse `remoteindex.Build`. New uploads need no check (resolve-or-create +
`conflict=error` already protect a colliding path).

**Touches.** `pkg/syncer/executor.go`, `plan.go`/`push.go` (drift filter
mirroring `filterDrift`), service `Stat` port (already exists). **Tests.** drift
detected → skip+warn, `--force` overrides, no-drift → proceeds.

### PR D — Rename / move detection

**Approach.** When the plan contains both a delete (manifest entry, local file
gone) and an upload (local file absent from manifest) with the **same size and
mtime**, treat the pair as a move: issue a server-side `Move`
(`FilesService.Move`, already implemented) instead of delete + re-upload,
carrying the `remote_id` to the new path. On any ambiguity (multiple candidates
with the same size+mtime) fall back to delete + add to stay safe. Symmetric local
move on pull.

**Touches.** `pkg/syncer/plan.go`/`plan_pull.go` (pair detection → `OpMove`),
`executor.go`/`run*.go` (move execution + manifest re-key). **Tests.** unique
match → one move + manifest re-key, ambiguous → safe fallback, content-differs
edge documented.

### PR E — Conflict-aware two-way sync (Option 2)

**Approach.** A unified mode (`kdrive sync --two-way`) builds the local walk, the
recursive remote index, and the manifest, then classifies each path 3-way:
local-only-changed → push; remote-only-changed → pull; both-changed →
**conflict** → materialise the side that would be lost as
`name.conflict-YYYY-MM-DD.ext` and apply the other, never merging; neither → skip.
Delete-vs-edit conflicts default to keep-the-edit + warn. Reuses
`remoteindex.Build`, the planners, the executors, and PR C's drift logic.

**Touches.** new `pkg/syncer/plan_twoway.go` + `twoway.go` orchestration,
`pkg/presentation/cli/sync.go` (`--two-way` flag + reporting). **Blocked by PR C.**
**Tests.** every 3-way cell, conflict materialisation naming, delete/modify rule,
dry-run plan output.

## Track 2 — FUSE daemon roadmap (`ROADMAP.md`)

Ordered cheapest/safest first.

- **PR F — `--readonly` (`KDRIVE_READONLY=1`).** Create/Mkdir/Unlink/Rmdir/Rename
  return `EROFS`. Touches `pkg/presentation/fuse/{dir,file}.go`, `appconfig`.
- **PR G — Structured JSON logs.** `slog.NewJSONHandler` with `--log-format=text`
  opt-out. Touches the daemon logger wiring + `appconfig`.
- **PR H — `Setattr` mtime persistence.** Map `in.Atime`/`in.Mtime` to a remote
  `last_modified_at` update so `touch` works. Needs a `FilesService` update call
  + a `SetTimes` use case + port. Touches `kdriveapi/files.go`, `pkg/usecase`,
  `pkg/service`, `fuse/file.go`.
- **PR I — Upload conflict knob (`--conflict=error|version|rename`).** Expose the
  mode on `UploadInput`; default `error`; the FUSE writeHandle uses `rename` so a
  duplicate `cp` yields the familiar `foo (1).txt`. Touches
  `service/upload_input.go`, `kdriveapi/files.go`, `fuse/file.go`.
- **PR J — `kdshare` subcommand.** `kdrive share <mounted-path>` prints the public
  URL by wiring the existing `ShareFile` use case (`Shares.Publish`). Touches
  `pkg/presentation/cli/{root,share}.go`.
- **PR K — xattrs.** `NodeGetxattrer` + `NodeListxattrer` exposing
  `user.kdrive.{id,mime_type,created_by,share_url}` (share_url lazy via
  `Shares.Publish` on first read). Touches `fuse/file.go`/`dir.go`.
- **PR L — `.trash/` virtual directory.** Expose kDrive trash as
  `~/kDrive-vfs/.trash/`; `rm` purges, `mv` restores. Needs a trash endpoint
  family on the API adapter + a virtual dir node.

## Track 3 — Bonus (in scope per "do everything")

- **PR M — Prometheus metrics side-car.** Optional HTTP listener exposing
  `/metrics` (transfers, cache hits, errors). Gated by an env/flag.
- **PR N — Multi-drive mount.** Mount multiple drives under
  `/mnt/kdrive/{drive_id}/`. Likely a config list + a top-level dir node per
  drive; revisit `appconfig` to accept multiple drives.
- **PR O — Full-text search virtual dir.** `~/.search/{query}/` backed by
  `GET /files/search?q=...`, results as virtual entries.

## Sequencing

A → B → C → D → E (E blocked by C), then F → G → H → I → J → K → L, then
M → N → O. Track 1 lands first because it protects data that already lives in
kDrive today. Docs (`README`/`ROADMAP`/`CLAUDE`) and the Obsidian vault are
updated as features land (and a final pass at the end).

Releases: **`v0.3.0` now** (Step 0 — the unreleased sync suite); `v0.4.0` after
Track 1 (data-safety hardening); `v0.5.0` after Track 2 (FUSE roadmap); `1.0.0`
as the capstone once Track 3 lands and the CLI/API surface is considered stable.

## Testing

Per-PR unit + behaviour tests over `pkg/service/servicefakes` and `httptest`
(no live API, no mount), mirroring the existing suite. FUSE-facing PRs add mount
integration tests on a temp mountpoint (fakes fully populated before `Mount`).
Coverage gate stays **≥ 90 % on `./pkg/...`**, enforced by CI. Each PR ends with
an adversarial review before merge.
