# Chunked Upload (> 100 MB) — Design

**Date:** 2026-06-15
**Status:** Design — pending spec review
**Type:** Feature (infrastructure adapter only; no behaviour change to other layers)

## Motivation

Single-shot uploads buffer the whole file in one request with an explicit
`Content-Length`. For large files (videos, big RAF) this risks request
timeouts and memory pressure, and the kDrive backend *requires* the
upload-session (chunked) flow above 100 MB. This adds the session flow so
large files upload reliably, with per-chunk retry.

## Verified API contract

Sourced from the official client `Infomaniak/desktop-kDrive` (read at `main`
HEAD `b6b104b`), cross-checked against the API docs and our own working
single-shot path. Confidence high on the wire contract; the few unverifiable
response-schema details are listed under Open questions.

All four calls target the **upload host** `uploadBaseURL`
(`https://api.kdrive.infomaniak.com/2/drive/{driveID}`, our
`DefaultUploadBaseURL`) — **not** `baseURL` (`api.infomaniak.com`, used by
List/Stat/Mkdir). Auth: `Authorization: Bearer <token>` on every call. Stay on
API version `/2/` to match the working single-shot path.

### 1. Start — `POST /upload/session/start`  (JSON body)
- Create mode: `file_name`, `directory_id` (string id), `conflict: "error"`.
- Edit mode (replaces the three above): `file_id` (string id).
- Always: `total_size` (string), `total_chunks` (string).
- `Content-Type: application/json`.
- Response: `{"data":{"token":"<sessionToken>", ...}}` → keep `data.token`.
- `created_at`/`last_modified_at` are **finish-only** (not sent at start).

### 2. Chunk — `POST /upload/session/{token}/chunk`  (octet-stream body, params in query)
- Query: `chunk_hash="xxh3:"+<16 hex>` (xxh3-64 of *this chunk's raw bytes*),
  `chunk_number` (1-based), `chunk_size` (actual byte length; last chunk is smaller).
- Body: raw chunk bytes. `Content-Type: application/octet-stream`. Explicit
  `Content-Length` (server rejects chunked transfer-encoding).
- Response: HTTP status only (no body consumed).
- **POST, not PUT.**

### 3. Finish — `POST /upload/session/{token}/finish`  (JSON body)
- `total_chunk_hash="xxh3:"+<16 hex>` — **hash-of-hashes** (see Hashing).
- `total_chunks` (JSON **number** here vs string at start).
- `created_at` (unix s), `last_modified_at` (unix s).
- `Content-Type: application/json`.
- Response: same node envelope as single-shot → `{"data": FileInfo}`.

### 4. Cancel — `DELETE /upload/session/{token}`  (no `/cancel` suffix)
- Mandatory cleanup on any unrecoverable failure (incl. finish failure);
  otherwise later uploads fail. Best-effort, no body.

### Hashing
- Algorithm: xxh3-64 → 16 lowercase hex (canonical big-endian), wire-prefixed
  `"xxh3:"`. Our `hash.XXH3Stream` already produces this form.
- Per-chunk `chunk_hash`: xxh3 over that chunk's raw bytes.
- `total_chunk_hash`: **not** a content hash. It is xxh3-64 over the
  concatenation of the per-chunk **16-hex ASCII strings** (the
  `xxh3:`-prefix **stripped**), fed in chunk order 1..N. In Go: keep a running
  `xxh3.New()`, and for each chunk write `[]byte(hex16)` (the 16 hex chars,
  no prefix); the finish value is `fmt.Sprintf("xxh3:%016x", running.Sum64())`.
  Feeding raw digest bytes (instead of the hex strings) makes finish reject.

### Sizes & threshold
- Threshold: `in.Size > 100*1024*1024` → session; **exactly** 100 MB stays
  single-shot (strictly greater, matching the client's `bigFileThreshold`).
- Chunk size: fixed **50 MB** (`50*1024*1024`). Within the client's accepted
  `[10 MB, 100 MB]` range. With 50 MB chunks the 10 000-chunk client cap implies
  a practical ceiling around ~488 GB — far beyond any real use here.

## Design — what changes

**Infrastructure adapter only.** No changes to `pkg/domain`, `pkg/service`
(ports), `pkg/usecase`, or `pkg/presentation/fuse`. `UploadInput`
(`Body io.ReadSeeker` + `Size`) already suffices; `CommitWrite.Execute` and
`writeHandle.commitLocked` are untouched — `Upload` stays the single
`FileWriter` port entry and large files flow through it transparently.

### New file: `pkg/infrastructure/kdriveapi/upload_session.go` (package `kdriveapi`)
Calls the package-private helpers directly: `sleepCtx`, `shouldRetry`,
`isRetryableError`, `fromResponse`, `drainAndClose`.

- New package-level **vars** (not `const`) next to `listPageSize`, so tests can
  shrink them while production keeps the real values:
  `uploadSessionThreshold = int64(100 * 1024 * 1024)`, `chunkSize = int64(50 * 1024 * 1024)`.
- `func (s *FilesService) uploadSession(ctx, in service.UploadInput) (domain.FileInfo, error)`:
  1. `totalChunks = ceil(in.Size / chunkSize)`.
  2. **start**: build the JSON body (create vs edit by `in.ExistingFileID`),
     `json.Marshal`, POST to `uploadBaseURL + "/" + driveID + "/upload/session/start"`,
     decode `data.token`.
  3. **chunks**: for `n = 1..totalChunks`, `in.Body.Seek(offset, io.SeekStart)`,
     read `chunkLen = min(chunkSize, remaining)` bytes (`io.LimitReader`),
     compute the chunk hash, POST the chunk with the three query params + an
     explicit `Content-Length = chunkLen`; accumulate the chunk's 16-hex string
     into the running total-hash state. Per-chunk retry uses **our** policy
     (`shouldRetry`/`isRetryableError` + `sleepCtx` backoff, `maxRetries`);
     re-seek to *this chunk's* offset before each retry; a non-transient 4xx
     (e.g. hash mismatch) fails fast.
  4. **finish**: JSON body with `total_chunk_hash`, `total_chunks` (number),
     `created_at`/`last_modified_at` (now, unix s); decode `{data: FileInfo}`.
  5. **cancel on failure**: any unrecoverable chunk error or a finish failure →
     best-effort `DELETE` on the session (deferred), then return the error.
- A small per-attempt request helper (build → Do → classify) **duplicated**
  (~25 lines) from the single-shot loop rather than refactoring `Upload`'s
  tested loop — zero regression risk on the single-shot path.

### Modified: `pkg/infrastructure/kdriveapi/files.go`
`FilesService.Upload`: after the existing validation block (and before the
single-shot hash/seek), add the dispatch:
```go
if in.Size > uploadSessionThreshold {
    return s.uploadSession(ctx, in)
}
```
The entire existing single-shot path stays byte-for-byte unchanged. (No size
cap exists today; this is net-new dispatch, not the tightening of a cap.)

### Modified: `pkg/service/upload_input.go`
Widen the `UploadInput` doc comment to cover both single-shot and chunked
paths. No struct field change.

## Retry / resume semantics
- Per-chunk re-POST on transient failure; the **session survives** chunk
  retries (no session restart, no resume GET — recovery is pure chunk re-POST).
- Uses our error model (transient = 5xx/429/transport; non-transient 4xx fails
  fast), **not** the desktop client's 5-trial/per-chunk-timeout policy — we do
  not claim parity.
- First implementation is **sequential** chunking, which keeps the
  total-hash feed order (1..N) trivially correct. (Parallel chunking is a
  possible later optimization but must preserve hash-feed order.)

## Error handling
- Start failure → return wrapped error (no session to cancel).
- Chunk failure after retries → `DELETE` session, return wrapped error.
- Finish failure → `DELETE` session (mandatory), return wrapped error.
- Reuse the typed sentinels via `fromResponse` (4xx/5xx → `domain.Err*`).
- Tokens never logged (consistent with the existing client).

## Testing
`pkg/infrastructure/kdriveapi/upload_session_test.go`, Ginkgo + `httptest`
(white-box `package kdriveapi`, reusing the suite + `roundTripFunc`):
- Happy path: a >100 MB body (use a small `chunkSize` override or a modest size
  that yields ≥2 chunks — see note) drives start → N chunks → finish; assert
  per-chunk `chunk_number`/`chunk_size`/`chunk_hash`, the JSON start/finish
  bodies, and that the returned `FileInfo` comes from finish's `{data}`.
- **Hash-of-hashes**: the test computes the expected `total_chunk_hash`
  independently and asserts the finish body matches.
- Threshold dispatch: `Size == 100 MB` → single-shot endpoint hit, not session;
  `Size == 100 MB + 1` → session.
- Edit mode: `ExistingFileID > 0` → start body carries `file_id` (no
  `file_name`/`directory_id`/`conflict`).
- Per-chunk retry: a chunk returns 429 then 200 → retried, body re-sent from the
  correct offset, upload succeeds.
- Fail-fast: a chunk returns a non-transient 4xx → no retry, session cancelled
  (DELETE observed), error is the typed sentinel.
- Finish failure → DELETE observed.
- Coverage stays ≥ 90% on `./pkg/...`.

Note for the test: to avoid allocating >100 MB in unit tests, make `chunkSize`
and `uploadSessionThreshold` overridable for tests (package-level vars, or a
test that sets a smaller body and a small threshold). Prefer package-level
`var` (not `const`) for the threshold/chunk size so a test can shrink them;
production defaults unchanged. This is the one small concession to testability.

## Out of scope
- Parallel chunk upload.
- A `resume`/`list-uploaded-chunks` flow (the API/client has none).
- Any change to the FUSE write lifecycle (commit-on-close is unchanged).

## Open questions (confirm with a live preprod call; low risk)
- Finish response envelope beyond `{data:{id,size,created_at,last_modified_at}}`
  is assumed identical to single-shot.
- `total_chunks` as string-at-start / number-at-finish: match the client
  exactly (string at start, number at finish) as the safe default.
- xxh3 cross-library equivalence (`zeebo/xxh3` vs the C++ `XXH3_64bits`): pin
  with a round-trip/known-vector check in the test if feasible.

## Risks
- **Don't break single-shot**: mitigated by duplicating the attempt loop
  (not refactoring) and keeping the 11 single-shot tests green.
- **Hash-of-hashes correctness**: the single most error-prone detail; the test
  asserts it independently.
- **Large-file unit tests**: avoid real >100 MB allocations via overridable
  threshold/chunk-size vars.
