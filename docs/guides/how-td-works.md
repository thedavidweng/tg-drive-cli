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

An uploaded file is an ordinary media message whose caption carries
reconstructable metadata. Illustrative structure:

```text
beach.jpg
/2024

td:v1 p=/2024/beach.jpg n=beach.jpg s=2482911 h=blake3:… m=image/jpeg

#td_Pictures_<hash> #td_Pictures_<hash>_2024_<hash>
```

Three layers, three different jobs:

1. **Display lines** — name and parent path for humans reading the channel.
2. **`td:v1` metadata** — path, name, size, content hash, MIME type. This is
   what a full scan parses; hashtags are never used for reconstruction.
3. **Hashtags** — one cumulative tag per directory level, purely for native
   client navigation.

Deep paths do not fit Telegram's caption budget (1024 UTF-16 code units).
Instead of failing, `td` sends a minimal caption and puts the full record in
a `td-manifest:v1` reply message. Albums work the same way with one
`td-album:v1` inventory reply per group, because albums keep a single human
caption on their first item. This is why deleting a file sometimes edits two
messages, and why tombstoning can fail on old messages
(`ERR_MESSAGE_NOT_EDITABLE`) — captions are just editable message text, and
Telegram ages out edits.

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
| 1024 UTF-16 caption budget | Telegram media caption cap (`td:v1` overflows into a manifest reply) |
| 4096 UTF-16 manifest budget | Telegram text message cap |
| One channel per root | keeps scan/recovery semantics simple and predictable |
| Incremental scans miss old edits | Telegram does not offer "history diff"; full scans re-read everything |

UTF-16 matters because Telegram counts captions in UTF-16 code units, not
Unicode code points — emoji and some CJK characters count as two.

## Slugs: readable but collision-proof

Hashtag segments must be readable (`Pictures`, pinyin for Chinese) yet unique
across arbitrary paths, so each segment gets a BLAKE3 suffix
(`readable_prefix` + 8 base32 chars, lengthening on collision). That is why
tags look like `#td_Pictures_<hash>_2024_<hash>` rather than
`#td_Pictures_2024`.

## Going deeper

- [`docs/architecture.md`](../architecture.md) — package layout and failure model
- [`docs/contracts/storage-contract.md`](../contracts/storage-contract.md) — schema, locks, reconciliation precedence
- [`docs/adr/`](../adr/) — the decision record for every choice above
