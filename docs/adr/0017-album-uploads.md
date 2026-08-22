# 0017 - Album uploads via sendMultiMedia

## Status

Accepted. Implements issue #26 on top of the album inventory of ADR 0013 and
the typed-request model of ADR 0016.

## Context

`td cp` published one Telegram message per file, so multi-file uploads read
as a pile of posts instead of one album block — even though adopted albums
already use a single human caption plus one `td-album:v1` inventory reply
(ADR 0013). Native clients group media only when it is sent as a media group
(`messages.sendMultiMedia`); posting N messages with identical text cannot
reproduce that presentation.

## Decision

- The core Telegram port gains `UploadMediaGroup`: 1..10 `UploadRequest`s in,
  one result per request out, all sharing one non-zero `GroupedID`. The
  contract mirrors what Telegram guarantees: only the first member's caption
  is honored, sibling captions stay empty. Single uploads keep
  `UploadMedia` unchanged.
- Multi-file `td cp <local...> <remote-dir>` and per-directory grouping under
  `--recursive` publish through that port. Each source directory's direct
  children form one album; nested directories recurse.
- Sets larger than 10 members split into consecutive groups of up to 10, td
  itself doing the splitting (callers, including bridge plugins, never cut
  the set themselves). Each split group gets its own `td-album:v1` reply; the
  human caption stays on the overall first member only.
- A lone survivor — a one-file directory, or every sibling skipped by
  `--skip-existing` — publishes as an ordinary single message with its own
  caption, because Telegram media groups require at least two members.
- One invocation carries one presentation kind (ADR 0016 flags apply
  uniformly to every member), so td never has to split by kind; mixing kinds
  across invocations remains the caller's concern.
- `--replace` is refused in multi-file form (`ERR_USAGE`). Replace semantics
  (tombstones, old-message cleanup) stay single-path; `--skip-existing`,
  `--auto-rename`, and fail-fast cover batch conflicts. A planning conflict
  aborts before any Telegram write.
- Crash windows mirror the single-upload policy exactly: message ids are
  recorded before publication, a failed group send deletes fresh pending rows
  but keeps resumable big-file state, and a publish failure abandons the whole
  chunk (messages and inventory deleted when possible, otherwise orphaned for
  `td repair --orphaned`). Pending-row adoption makes a plain retry resume an
  interrupted batch.
- Album members carry no per-file manifest replies: their reconstructable
  record is the group's one `td-album:v1` inventory, so the publisher gains a
  `SkipManifestReply` mode instead of a new manifest format. No schema
  migration.
- The cp JSON envelope grows aggregate counters (`uploaded`, `skipped`,
  `errors`) plus one `albums` entry per sent group (`grouped_id`,
  `reply_message_id`, `message_ids`, `paths`).

## Consequences

- Native clients show multi-file uploads as grouped album blocks with a
  readable first caption, and `td scan --full` rebuilds them from the channel
  like adopted albums.
- Whether native clients render pure-document media groups as visual albums
  varies by platform; photos and videos are the well-trodden path. This is
  presentation-only: storage, hashes, and reconstruction are unaffected.
- Orphaned big-file album members (delete failed during abandonment) are
  repaired as individual files by `td repair --orphaned`; `td import
  --rewrite-captions` re-normalizes them into album shape if that ever
  matters.
