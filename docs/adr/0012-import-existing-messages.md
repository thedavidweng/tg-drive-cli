# 0012 - Import existing Telegram messages

## Status

Accepted.

## Context

Users already have media and notes in a bound channel that were uploaded from a
native Telegram client. Re-uploading them with `td cp` wastes bandwidth and can
hit the 2 GB free-tier limit. Telegram allows channel creators to edit media
captions and text in place.

## Decision

- Add `td import` to adopt existing messages without re-uploading bytes.
- Supported kinds: documents (any MIME Telegram stores as a document, including
  video/audio/files), photos, and plain text. Service/empty messages are skipped.
- Import does not edit the human album caption. It claims members in the
  index and writes **one** `td-album:v1` reply for the group (see ADR 0013).
- Never put `td:v1`, `#td_` tags, filenames, or per-item `td-manifest:v1`
  replies on album members.
- Never delete the original media on import failure.
- `td import --rewrite-captions` restores one human caption per album,
  deletes leftover per-file `td-manifest:v1` replies, and upserts the
  single album inventory reply.
- Default path layout for `--unmanaged`: `/videos`, `/photos`, `/audio`,
  `/files`, `/notes`, using the original filename when present.
- `--dry-run` is required before a mutating run in operator workflows.
  Mutating import requires `--confirm`.
- Default `--keep-caption` and `--no-hash`. `--hash` downloads to compute
  BLAKE3 and is optional.

## Consequences

- `scan` still ignores unmanaged messages; import is the explicit claim.
- `GetMessage` is added to the Telegram port so import and text download do
  not have to walk full history.
- Photo and text downloads are first-class alongside documents.
