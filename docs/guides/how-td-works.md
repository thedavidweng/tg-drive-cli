# How td works

Why `td` behaves the way it does: what a "file" actually is on Telegram, why
the local database can always be thrown away, and where the limits come from.

For task-oriented docs see the [guides index](../../README.md#documentation);
for frozen shapes see [`docs/contracts/`](../contracts/cli-contract.md).

---

## One idea: Telegram is the filesystem

Most sync tools treat the cloud as a mirror of a local database. `td` inverts
that: **Telegram messages are the source of truth; SQLite is a cache**. Every
design decision follows from this:

- Delete the database and nothing is lost — `td scan --full` rebuilds it from
  the channel.
- The channel stays a normal channel — any Telegram client can browse and
  download without `td`.
- Writes must land on Telegram first; the index is updated as a record of
  what Telegram now holds.

## What a file looks like on Telegram

An uploaded file is an ordinary media message with a human-only caption.
Illustrative structure:

```text
beach.jpg

Weekend trip
```

Three layers, three different jobs:

1. **Caption** — caller-provided human text and display name. `td` does not
   add the remote parent path or its internal `#td_*` tag chain.
2. **Discussion comment** — `td-manifest:v1` or `td-album:v1` stores the
   canonical path, size, content hash, MIME type, and complete tag chain.
3. **SQLite index** — a rebuildable cache of the same Telegram records;
   hashtags are never used as the authoritative reconstruction model.

Albums keep one human caption on their first item and one
`td-album:v1` inventory comment per group. Legacy rows may still carry
`td:v1` captions or in-channel replies; those carriers remain parseable.
Use `td repair --captions --dry-run` followed by `td repair --captions` to
remove the former path scaffold from existing modern captions.

## Directories do not exist on Telegram

Only files are stored. A directory is *derived*: it exists because some file
path passes through it. Consequences:

- Empty directories live only in the local cache and disappear after a full
  scan.
- Directory move/delete is unsupported — there is no directory object to
  move; it would mean rewriting every descendant's caption.
- Renames and moves are file-level caption edits, which is also why they
  require `--confirm`: they mutate real Telegram messages.

## Why writes are gated

Every remote write runs inside an operation lock (one per canonical path) and
a DB transaction, so two processes cannot interleave edits to the same file.
Destructive operations additionally require `--confirm` because their effects
are visible to everyone in the channel and may be hard to undo — a deleted
message is gone from Telegram's API view. Dry-runs exist precisely because
the gates should never tempt you to skip planning.

The one dangerous window: Telegram accepts the media, then the process dies
before the index write. The retry detects the orphaned upload and points at
`td repair --orphaned` instead of silently duplicating the message.

## Why capability checks instead of assumptions

Telegram behavior varies by account age, tier, and channel type: edit
permissions decay with message age, upload limits differ between free and
Premium, invite links need specific channel settings. Rather than assuming,
`td doctor` probes each capability and reports pass/warn/fail. Code paths use
the same capability layer, so behavior degrades explicitly instead of
mysteriously.

## Where the limits come from

| Limit | Origin |
| --- | --- |
| 2 GB / 4 GB per file | Telegram free / Premium account tiers |
| 1024 UTF-16 caption budget | Telegram media caption cap for human text |
| 4096 UTF-16 manifest budget | Telegram text message cap |
| One channel per root | keeps scan/recovery semantics simple and predictable |
| Incremental scans miss old edits | Telegram does not offer "history diff"; full scans re-read everything |

UTF-16 matters because Telegram counts captions in UTF-16 code units, not
Unicode code points — emoji and some CJK characters count as two.

## Slugs: readable but collision-proof

Path-tag segments remain readable (`Pictures`, pinyin for Chinese) yet
unique across arbitrary paths, so each segment gets a BLAKE3 suffix
(`readable_prefix` + 8 base32 chars, lengthening on collision). They remain
in the index and discussion manifests for compatibility, but new captions do
not render them.

## Going deeper

- [`docs/architecture.md`](../architecture.md) — package layout and failure model
- [`docs/contracts/storage-contract.md`](../contracts/storage-contract.md) — schema, locks, reconciliation precedence
- [`docs/adr/`](../adr/) — the decision record for every choice above
