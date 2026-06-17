# Design — `kdrive` CLI suite, first command: `kdrive sync`

Status: proposed · Date: 2026-06-17

## Context

The module already mounts kDrive as a FUSE filesystem (`kdrive-fuse`). The clean
architecture means the kernel-driven FUSE layer is just *one* presentation over a
set of use cases (`ListDir`, `ReadFile`, `SeedContent`, `CommitWrite`,
`DeleteEntry`, `RenameEntry`, `MakeDir`, `ShareFile`) backed by the `kdriveapi`
adapter. A command-line tool is a natural *second* presentation over the same use
cases.

The driving need: archive photos from a local tree to kDrive incrementally.
Today the only way to refresh an edited folder is to delete the whole remote copy
and re-upload it. We want a real incremental sync that detects local edits
(including metadata edits that do not change the file size) and propagates only
the differences, plus the reverse direction (pull a folder down to re-cull or
edit, then push it back).

This document specifies the **CLI skeleton** plus the **`kdrive sync`** command.
Later commands (`verify`, `share`, `ls`, `get`, `put`) are out of scope here and
will each get their own design.

## Goals

- A new `kdrive` binary with subcommand dispatch, sharing wiring and config with
  `kdrive-fuse`.
- `kdrive sync` as **two explicit one-way mirrors**, never run simultaneously:
  - **push** (default): make the remote match the local tree.
  - **`--pull`**: make the local tree match the remote.
- Detect *all* local changes — including in-place edits that keep the exact same
  byte size — without downloading remote content and without a remote content
  hash (the kDrive API exposes neither).
- Talk to the API **directly** (not through the mount): parallel transfers,
  chunked upload for files > 100 MB, retries, progress, independent of mount
  health.
- Be resumable: an interrupted run re-detects and finishes the remaining work on
  the next invocation.

## Non-goals

- No automatic two-way merge / conflict resolution (bisync). The two directions
  are explicit and used one at a time, matching the sequential
  pull → edit → push workflow.
- No rename/move detection in v1: a local rename is handled as delete + add
  (correct, but re-uploads the file). A size+mtime heuristic to turn this into a
  server-side move is a possible later optimisation.
- No real `Setattr`/mtime persistence on the remote (a separate roadmap item).

## Key constraint that shapes the design

The kDrive API gives us, per file, only: `id, name, type, size, created_at,
last_modified_at`. There is **no content hash** on read, and an upload **restamps
`last_modified_at` to the upload time**, so the remote mtime cannot mark "when the
content last changed". Therefore:

- Change detection cannot rely on a remote hash or on comparing local vs remote
  mtime.
- The source of truth for "what was last synced" is a **local manifest**. We
  compare each side against the manifest baseline, not the two sides against each
  other.

## The manifest

A per-(local-root, remote-root) snapshot of the last successful sync.

- Location: `$XDG_STATE_HOME/kdrive/<key>.tsv` (fallback
  `~/.local/state/kdrive/<key>.tsv`), where `<key>` is a stable hash of
  `"<absolute local root>\n<remote root path>"`. Kept out of the photo tree so it
  is never itself synced or polluting backups.
- Format: tab-separated, one line per file. Fields, in order:

  ```
  size <TAB> local_mtime <TAB> remote_id <TAB> remote_mtime <TAB> relpath
  ```

  - `size` — content size in bytes (identical on both sides after a sync).
  - `local_mtime` — local filesystem mtime (Unix seconds) at last sync; drives
    local-side change detection.
  - `remote_id` — kDrive file id; lets us overwrite/delete by id with **no remote
    listing**.
  - `remote_mtime` — remote `last_modified_at` (Unix seconds) at last sync;
    drives remote-side change detection for pull.
  - `relpath` — path relative to the root. Placed **last**: a line is parsed by
    splitting on tabs and taking the first four fields as the integers above and
    the remainder (re-joined) as the path, so tabs in filenames do not break
    parsing.

- Written atomically (temp file + rename). Entries are updated only for
  operations that succeed, so a crash leaves a consistent older baseline; the
  next run re-detects the unfinished work (operations are idempotent — see below).

## `kdrive sync` — push (local → remote)

Make the remote match local. Steady-state push needs **no remote listing** — it
acts purely on the diff between the local walk and the manifest, using
`remote_id` for overwrites and deletes.

Walk the local tree (pruning `.dtrash` and `à trier`, as `kfuse-verify` does).
For each local file `L` at `rel`:

| Manifest state of `rel` | Classification | Action |
| --- | --- | --- |
| absent | **new** | resolve-or-create the remote parent folder, upload (new file), record the returned id/mtime |
| present, `L.size != B.size` or `L.mtime != B.local_mtime` | **modified** (incl. same-size edit, caught via mtime) | overwrite via `B.remote_id` (`CommitWrite` with `ExistingFileID`), update entry |
| present, unchanged | **unchanged** | skip |

Then, manifest entries whose local file no longer exists are **local deletions**
→ delete remotely via `B.remote_id` (`DeleteEntry`, soft-delete to kDrive trash),
drop the entry. Subject to the deletion guard (below).

Bootstrap (no manifest yet): build a one-shot recursive remote index (see pull)
and seed the manifest by presence + size, so an already-uploaded archive is not
re-pushed wholesale. `--assume-new` skips bootstrap and treats every local file
as new.

## `kdrive sync --pull` (remote → local)

Make local match remote. Pull always builds a **recursive remote index** `R`
(parallel folder listings from the remote root) because remote-side changes can
only be discovered by listing.

For each remote file `R[rel]` (`{id, size, last_modified_at}`):

| Manifest state of `rel` | Classification | Action |
| --- | --- | --- |
| absent | **new remotely** | download to local, record entry |
| present, `R.size != B.size` or `R.last_modified_at != B.remote_mtime` | **modified remotely** | download and overwrite local, update entry |
| present, unchanged | **unchanged** | skip |

Manifest entries absent from `R` are **remote deletions** → delete the local
file, drop the entry (subject to the deletion guard).

Download uses the existing read path (`ReadFile` / `DownloadStream`). Folders are
created locally as needed.

## Safety

- **Deletion guard.** Deletions are on by default (remote deletes go to kDrive
  trash and are recoverable; local deletes are real). If a run would delete more
  than **20 %** of the manifest entries — the classic "empty/!wrong source wipes
  the destination" footgun, e.g. a lost manifest or the wrong root — it **aborts
  before any deletion** unless `--force` is given. `--no-delete` never deletes.
- **Destination-drift guard on pull.** Before overwriting a local file, pull
  checks whether that local file differs from the manifest baseline (i.e. has
  unpushed local edits). If so it **skips and warns** rather than clobbering,
  unless `--force`. (Push relies on the sequential workflow and the
  sole-writer assumption below; an optional remote-drift check can be added
  later.)
- **Sole-writer assumption.** The manifest assumes `kdrive sync` is the only
  writer of the synced subtree. Out-of-band edits (e.g. via the kDrive web UI)
  can make a stored `remote_id` stale; `--refresh` re-bootstraps from a fresh
  remote index to reconcile.
- **`--dry-run`** prints the full classified plan (new / modified / deleted per
  direction) and touches nothing.

## CLI

```
kdrive sync [LOCAL] [REMOTE] [flags]
  defaults: LOCAL  = ~/Pictures/FUJI/112_FUJI
            REMOTE = Rémanence            (a path resolved from the drive root)

  --pull         mirror remote -> local (default is push, local -> remote)
  --dry-run      classify and print the plan; make no changes
  --no-delete    never delete on the destination
  --force        override the deletion guard and the pull drift guard
  --assume-new   skip bootstrap; treat every local file as new (push)
  --refresh      rebuild the baseline from a fresh remote index
  --jobs N       concurrent transfers (default 8)
  --verify       after the run, report presence + size differences
  -h | --help
```

`REMOTE` is a path *inside the drive*, resolved to a folder id starting from
`KDRIVE_ROOT_FOLDER_ID`. Both arguments are optional and default to the photo
archive pairing above.

## Architecture / layout

Respects the existing clean-architecture layering; dependencies point inward.

```
cmd/kdrive/                    thin entry point: subcommand dispatch + di wiring
pkg/presentation/cli/          CLI presentation layer (mirror of presentation/fuse/)
  ├── root.go                  dispatch, global flags, --help
  └── sync.go                  the `sync` command: parse flags, build engine, report
pkg/usecase/                   sync logic, driven over the service ports (no HTTP)
  ├── sync_plan.go             diff a local walk + remote index against the manifest -> a Plan
  └── sync_run.go              execute a Plan (worker pool) over CommitWrite/DeleteEntry/Download/MakeDir
pkg/infrastructure/manifest/   manifest store: typed load/save of the TSV, XDG path keying
pkg/infrastructure/remoteindex/ recursive remote listing -> { relpath: {id,size,mtime} } (parallel)
```

- **Config** is factored out of `cmd/kdrive-fuse/config` into a shared package so
  both binaries read the same `KDRIVE_*` environment.
- **Wiring** reuses `pkg/infrastructure/di`: the CLI builds a `Container` and pulls
  the use cases / API client from it, exactly as `main.go` does for the mount.
- **Remote path resolver** (resolve-or-create a path to a folder id) lives with
  the remote-index / a small use case over `ListDir` + `MakeDir`; folder ids are
  cached within a run so a deep day-folder is resolved once.
- **Reused use cases**: `CommitWrite` (new + overwrite, single-shot and chunked
  already handled by the adapter), `DeleteEntry`, `MakeDir`, `ListDir`,
  `ReadFile`/`DownloadStream`.

### Data flow — push (steady state)

1. Walk local (pruned) → classify each file against the manifest → `Plan`
   (uploads, overwrites, skips) + deletions from manifest orphans.
2. Guard check on the deletion count.
3. Worker pool executes: resolve/create parent (cached) and `CommitWrite` for
   new; `CommitWrite` with `ExistingFileID` for overwrite; `DeleteEntry` for
   deletions.
4. Each success updates its manifest entry; the manifest is flushed atomically.
5. Optional `--verify` pass; non-zero exit if any operation failed.

### Data flow — pull

1. Build the recursive remote index (parallel listings).
2. Classify each remote entry against the manifest → `Plan`; manifest orphans
   absent remotely → local deletions.
3. Guards (deletion count, local drift) → execute downloads / local deletes via
   the worker pool → update + flush manifest → optional verify.

## Error handling & resume

- Transient API failures (429 / 5xx / transport) are already retried with
  backoff inside the adapter; uploads use the longer-timeout client.
- A failed per-file operation is logged, **does not** update its manifest entry,
  and makes the command exit non-zero. Re-running re-detects exactly the
  unfinished items (overwrite/upload are idempotent; delete-by-id of an
  already-deleted file is treated as success).
- A 4xx that is not transient (e.g. hash mismatch) fails that file fast without
  retry.

## Testing

- `pkg/usecase/sync_plan` and `sync_run`: table-driven and behaviour tests over
  `servicefakes` — no HTTP, no mount. Cover every classification cell, the
  deletion guard, the drift guard, bootstrap, `--assume-new`, and resume after a
  mid-run failure.
- `pkg/infrastructure/manifest`: round-trip load/save, tab/newline-in-name
  parsing, atomic-write behaviour, XDG keying.
- `pkg/infrastructure/remoteindex`: recursive listing against an `httptest`
  server, including pagination.
- `pkg/presentation/cli`: flag parsing, `--dry-run` output, exit codes, driven
  over fakes.
- Coverage gate stays at **≥ 90 % on `./pkg/...`**, enforced by CI.

## Suggested decomposition (separate PRs)

1. Shared config package + `cmd/kdrive` skeleton with dispatch and `--help`
   (no behaviour yet).
2. `manifest` store (load/save/atomic/XDG) — fully unit-tested.
3. `remoteindex` (recursive parallel listing + path resolver).
4. `sync_plan` (pure diff → Plan) + `sync_run` (executor, worker pool).
5. `kdrive sync` push (wires 1–4) + `--dry-run`, deletion guard.
6. `kdrive sync --pull` + drift guard.
7. `--verify`, `--refresh`, docs (README/ROADMAP), polish.

## Future (out of scope)

- `kdrive verify` (replaces the `kfuse-verify` bash script, comparing via the API
  listing).
- `kdrive share` (wire the existing `ShareFile` use case — the planned
  `kdshare`).
- Rename/move detection (delete+add → server-side move heuristic).
- Optional automatic bisync mode if the explicit-direction model proves limiting.
