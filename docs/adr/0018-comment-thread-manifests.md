# 0018 - Machine records live in discussion-group comment threads

## Status

Accepted. Extends ADR 0013 (album caption conventions) and ADR 0014
(reconciliation precedence). Supersedes the in-channel reply carrier for new
writes; legacy records stay parseable forever.

## Context

ADR 0013 made reconstructable metadata a sibling of the media it describes:
one `td:v1` compact caption (or a `td-manifest:v1` reply) per ungrouped file,
one `td-album:v1` inventory reply per album. That kept Telegram the
recoverable source of truth, but the machine text is visible in the channel
timeline. Live use (real albums with long base64 paths wrapping across the
caption) showed the noise dominates the human reading experience.

The alternatives fail on durability:

- A single pinned index message edited in place concentrates the whole index
  into one mutable object with no API-accessible edit history: one bad edit
  destroys every record, edits are not atomic with the media send, and the
  4096-unit text budget forces multi-message paging.
- Spoiler entities only hide the noise, keeping the caption budget and the
  visible clutter.

Telegram channels can link a **discussion supergroup**. Every channel post is
auto-forwarded into that group (`fwd_from.channel_post` points back at the
post id), and the post's comment section is the message thread rooted at that
forwarded header. Comments are ordinary supergroup messages: the account can
send, edit, and delete them indefinitely, and they are invisible in the
channel timeline beyond a comment count.

One platform constraint matters: Telegram creates comment threads only for
posts sent after the discussion group was linked. The adapter bootstraps
older posts on first record write by forwarding the post into the group —
the manual forward carries `fwd_from.channel_post` exactly like the
auto-forward, so it becomes the thread root the whole mapping already
understands. If forwarding is impossible (protected content), the record
falls back to the legacy in-channel reply carrier.

## Decision

- Every drive channel has a **linked discussion group**. `td init` creates
  and links one; `td channels link-discussion` adds one to an existing
  binding; `td doctor` reports it.
- The machine record of every new upload is a **comment** on the file's post
  thread:
  - Ungrouped file: one `td-manifest:v1` comment on the file's message.
  - Album: one `td-album:v1` comment on the first member's message.
- Media captions carry **human text only** (display name, parent directory,
  hashtag chain). No `td:v1` compact line, no manifest-reply fallback.
- Legacy records — `td:v1` captions, in-channel `td-manifest:v1` /
  `td-album:v1` replies — remain first-class scan inputs. Rows keep the
  carrier they were published with (`files.manifest_chat_tg_id` empty means
  in-channel reply); `td mv` / `td rm` / `td repair` edit a row through its
  own carrier.
- `td import --rewrite-captions` converts a legacy channel to the comment
  model: strip machine lines from captions, post comments, delete legacy
  per-file replies, upsert album inventory comments.
- Reconciliation precedence (refining ADR 0014): for one post, a comment
  record wins over caption metadata, which wins over the in-channel reply.
  Tombstone comments (`td-manifest:v1 deleted=true`) win over everything for
  their post. A media group with neither a comment inventory nor a reply
  inventory is `ERR_ALBUM_INVENTORY_INVALID`, as before.
- All machine-record writes (upload, mv, rm, import, repair) require the
  discussion group to be linked; reads and scans work on legacy channels.
- Full scans walk the channel and the discussion group; each peer has its own
  completeness proof and cursor. A suspicious read on either peer aborts with
  `ERR_SCAN_INCOMPLETE`.

## Consequences

- The channel timeline reads as human content only; machine text is one tap
  away in the comment thread.
- The recoverable source of truth stays distributed: one independent record
  per file/album, same blast radius as the reply model. No single mutable
  index, no 4096-unit paging, no edit-history exposure.
- The caption budget pressure disappears for new uploads; the
  minimal-caption + manifest-reply overflow path becomes legacy-only.
- A deleted forwarded header (thread root) orphans that post's records; scan
  reports the file as missing/invalid exactly like a deleted reply today.
- The discussion group mirrors every channel post; it is a machine surface,
  not a browsing surface.
- `files.manifest_message_id` ids are only meaningful together with
  `files.manifest_chat_tg_id` (the peer they live in).
