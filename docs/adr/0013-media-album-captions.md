# 0013 - Media albums are the unit of storage

## Status

Accepted. Supersedes the local-index-only adopt policy in the first revision.

## Context

Telegram media groups (`grouped_id`) render as one timeline block: several
photos or videos plus a single human caption. Stamping `td:v1` or a
`td-manifest:v1` reply on every member turns that block into a pile of
posts and destroys native readability.

The first revision of this ADR stored adopted-file metadata only in SQLite.
That broke the product rule that Telegram is the recoverable source of
truth.

## Decision

- The human unit and the machine unit are the same: the album.
- The first item of a group keeps the human caption (hashtags, description).
  Sibling captions stay empty.
- Reconstructable metadata for the whole group lives in **one**
  `td-album:v1` text reply on the first item. Never one reply per file.
- Ungrouped files keep one `td:v1` caption or one `td-manifest:v1` reply.
- `td scan --full` rebuilds the index from `td-album:v1`, `td:v1`, and
  `td-manifest:v1` only. Files with no Telegram machine record become
  missing.
- `td mv` / `td rm` of an album member edit or shrink that one inventory
  reply. They do not touch the human caption.
- `td import` claims members in the index and upserts the album inventory.
  It does not rewrite the human caption.

## Consequences

- Wiping SQLite and running `td scan --full` restores adopted albums.
- A group of N media adds at most one extra timeline message.
- Native Telegram users still see a grouped post with a readable caption.
- `td cp` uploads one message per file. Folder upload as `sendMultiMedia`
  albums is tracked in issue #26 and would reuse this `td-album:v1`
  inventory.
