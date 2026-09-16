# 0020 - Re-upload Saved Messages into the drive

## Status

Accepted and implemented.

## Context

Telegram Saved Messages stores forwarded copies of media and text. The
forwarded copy remains dependent on the origin message: when the origin
channel deletes that post, the saved item can disappear too. Saved Messages
also has no linked discussion group, so it cannot carry the drive's
authoritative machine records in place.

The drive already has a separate vocabulary for these operations. `td adopt`
claims a message that is already in the bound drive channel without moving
bytes. External-chat ingest belongs under `td import`, and must create a new
drive message.

## Decision

- Implement `td import saved [message-id...]` as a re-upload from the
  authenticated user's Saved Messages chat.
- Download each media item once, hash the bytes with BLAKE3, and use that hash
  for dedupe before publishing. Text-only items become `.txt` documents.
- Mirror Saved Messages 2.0 sub-chats as directories below `/saved`.
- Preserve albums as native media groups, splitting at Telegram's ten-member
  limit. Preserve source video attributes. Require an explicit photo
  presentation choice: `document` keeps bytes, while `photo` uses Telegram's
  native photo representation.
- Keep source captions above the normal rendered file caption. When dedupe
  skips an item, preserve its caption in a `td-dupe:v1` discussion comment;
  `--merge-captions` additionally appends it to the matched human caption.
- Write `td-origin:v1` discussion comments for successful imports. These
  records contain source message and forwarded-origin snapshots, but are
  additive annotations rather than tree-authoritative records.
- Keep Saved Messages sources by default. `--delete-source --confirm` removes
  only items whose drive message was verified after import or duplicate
  matching. Failed items are never deleted.
- Require `--confirm` for non-dry-run imports. Require `--photos-as` for
  photo-containing JSON and non-interactive runs; interactive TTY runs may
  answer a prompt.

## Consequences

- Imported bytes no longer depend on an origin channel's retention.
- A rerun is safe: identical content becomes a duplicate and does not create
  a second file by default.
- Provenance and duplicate captions survive in the discussion group, while
  the normal manifest remains authoritative for path reconstruction.
- Native photo imports cannot promise byte identity because Telegram may
  recompress them.
- An import needs a bound drive channel and linked discussion group before it
  can plan or publish, including dry runs, because the result promises
  reconstructable records.
- Saved Messages deletion is optional and guarded by verification and an
  explicit confirmation flag.
