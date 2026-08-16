# 0015 - Wired resumable uploads

## Status

Accepted. Supersedes the resume aspects of ADR 0001 (which specified the
upload.saveBigFilePart mechanism and the upload_progress table but left the
retry path disconnected).

## Context

ADR 0001 designed resumable uploads for files above 10 MB: part state
persists in `upload_progress`, keyed by the pending file row. In practice
resume could never happen:

- Every retry inserted a **fresh** pending row with a fresh row id, hence a
  fresh state key — the persisted state was unreachable.
- A failed large upload left its pending row wedged on the destination path:
  plain retry said "file exists", `--replace` only looked for active rows,
  and the only exit was `td repair --pending` (or `--orphaned`), commands
  most users do not know.

The part-state machine also had concurrency defects: state snapshots for
persistence raced confirmation appends, a failed part discarded late
confirmations from in-flight workers, and `ok=false` part acknowledgements
were retried in an unbounded busy loop.

## Decision

- Retrying an upload to a destination occupied by a **pending** row adopts
  that row when the recorded identity (size, content hash) matches, reusing
  the row id and therefore the resumable state key, and sends only
  unconfirmed parts. Mismatched identity still blocks with `ERR_PATH_EXISTS`.
- `--replace` supersedes pending rows as well as active ones, deleting the
  stale upload state (and rolling back an unpublished media message left by
  a crash).
- Hashing is mandatory on the resumable path: resume identity can never rest
  on an empty hash. `--no-hash` applies only to small files.
- The retry reports `resumed: true` (JSON) / "resumed upload of" (human), so
  transfer time and cost are predictable.
- The upload-state table gains a foreign key to file rows via the version-2
  migration (state rows cascade away with their file rows); Telegram's
  client-chosen big-file id lives in `telegram_file_id`, because resumed
  parts must be sent under the same id.
- Part-state persistence holds the state mutex for the copy and the save
  (serialized writes); after any part failure a final save runs on a
  background context so late confirmations are not lost. `ok=false`
  acknowledgements retry a bounded number of times with backoff, honoring
  context cancellation.
- Message-creating RPCs are never blindly retried: a send that may have
  succeeded server-side must not be re-issued. Flood-wait pacing and
  connection-level retries remain in the middleware; the duplicate-claim
  guard during scan remains the reconciliation path.
- On the big-file path the service passes no reader — workers read by offset
  — so no file handle is held for the upload's duration for nothing.

## Consequences

- A network drop on a multi-gigabyte upload costs only the unconfirmed parts
  on plain retry, without knowing any repair command.
- Users see slightly more manual retries on transient send errors and
  fewer silently duplicated messages (behavior change).
- The schema v2 migration touches every existing database; state rows that
  cannot be mapped to a file row are dropped (they were unreachable by the
  old code path anyway).
- Resume is only as trustworthy as the stored content hash, so the resumable
  path always computes one.
