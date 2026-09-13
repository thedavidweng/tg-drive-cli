# 0014 - Reconciliation precedence for contradictory Telegram state

## Status

Accepted. Refined by ADR 0018: comment records rank above caption metadata,
which ranks above the legacy in-channel reply; caption tombstones stay
sticky over everything.

## Context

Telegram is the recoverable source of truth, so `td scan --full` rebuilds the
index from channel messages alone. Real channels can hold contradictory
state:

- A tombstone delete whose manifest reply could not be redacted (edit failure,
  `--allow-stale-manifest`): the media caption says deleted, the reply says
  live. The naive order of reconciliation resolved the reply first and
  resurrected the file — the exact scenario tombstone users hit.
- A missing or corrupted `td-album:v1` inventory: album members carry no
  metadata of their own, so a silent skip made the whole album vanish from
  the index with zero scan errors.
- Two messages claiming the same path (duplicates from a crash window or a
  manual forward).
- A Telegram pagination quirk returning a short page mid-history: the read
  looked complete, and the finalizer mass-marked every file missing.

These contradictions need fixed, documented semantics; "whatever the
reconciliation loop happened to visit first" is not a contract.

## Decision

Full-scan reconciliation resolves contradictions in a fixed order:

1. A media caption that carries machine metadata always wins over the
   manifest reply — including tombstones. The manifest reply only wins when
   the caption carries no machine metadata of its own.
2. Album inventories reach parity with per-file manifests: a missing,
   undeliverable, or unparseable `td-album:v1` inventory records a scan error
   (`ERR_ALBUM_INVENTORY_INVALID`) surfaced through the existing scan-errors
   table and strict mode, instead of silently dropping the members.
3. Duplicate path claims resolve newest-message-wins; the older duplicate
   becomes a scan error.
4. The missing-finalizer only runs when the history read provably reached the
   channel's oldest message (empty page, boundary crossing, or the total
   count Telegram reports). A suspicious early termination aborts the scan
   with `ERR_SCAN_INCOMPLETE` and leaves the index untouched.

Slug assignment during scans is deterministic (message-id order — the same
chronological order uploads are assigned in), so a rebuilt index reproduces
the same tag chains as the original upload-time assignment and previously
shared hashtag links keep working.

## Consequences

- Tombstoned files stay deleted across scans even when their manifest reply
  is stale; recovery from a stale reply requires explicitly re-uploading.
- Corrupt or missing inventories become visible, actionable scan errors
  instead of invisible data loss; repairing the inventory message resolves
  the error on the next scan.
- A pathological pagination quirk fails the scan loudly instead of
  fabricating a total data loss.
- The precedence rules are frozen in `docs/contracts/storage-contract.md`;
  changing them changes what users mean by "deleted".
