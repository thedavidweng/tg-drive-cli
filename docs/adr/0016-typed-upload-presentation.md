# 0016 - Typed upload presentation

## Status

Accepted. Implements the typed-request plumbing of the typed media uploads
spec (issue #29); album/media-group publishing (#26) builds on it.

## Context

Everything `td cp` published landed in native Telegram clients as a generic
file card: no inline photo preview, no in-chat video playback, no thumbnail
until Telegram happened to generate one. Channels managed by td therefore did
not read like channels humans share media into, and programmatic consumers
(bridge plugins driving td over its stable `--json` surface) had no way to ask
for the friendlier forms.

The storage model must not move: video sent with attributes is still a
document — Telegram stores the bytes untouched — so blake3 verification,
manifests, scan, and restore are unaffected. Only native photos are
server-recompressed, an accepted product trade-off; callers needing exactness
keep the document path.

## Decision

- Presentation metadata lives **in the upload request**, not in config: there
  is no `upload.presentation` config section, and one caller's bridge should
  not change another caller's channel. The core Telegram port's
  `UploadRequest` gains optional fields — media kind (`photo`, `video`, or the
  implicit `document` default), a video attribute block (duration seconds,
  width, height, streaming hint), and thumbnail bytes. Zero values reproduce
  today's document-only send exactly: no attribute block, no thumb.
- Kind mapping in the gotd adapter: `photo` → native uploaded-photo message;
  `video` → uploaded document carrying a `documentAttributeVideo` block
  (streaming enabled when requested) plus optional thumb; default → current
  filename+MIME document. Thumbnails are uploaded as input files and
  referenced by the document send.
- Callers supply duration and dimensions; td never probes media files itself
  (no ffprobe dependency).
- CLI flag names on `td cp` (single-file uploads only; presentation flags are
  rejected with `--recursive` because one kind cannot describe every file in
  a tree):
  - `--as <photo|video|document>` — kind override (default document)
  - `--duration <seconds>` — float video length (`--as video`)
  - `--width <px>` / `--height <px>` — video dimensions (`--as video`)
  - `--streaming` — streaming hint (`--as video`)
  - `--thumb <file.jpg>` — JPEG thumbnail source (document/video kinds)
- Invalid combinations fail fast with `ERR_USAGE` (exit 2): unknown kind,
  negative attribute values, attribute flags without `--as video`, thumbnails
  with `--as photo`. The service validates too, so Go-API consumers get the
  same taxonomy.
- Message-creating RPCs follow the existing never-blindly-retried rule (ADR
  0010 lineage): flood-wait pacing stays in the rate-limiter middleware,
  duplicate-claim reconciliation stays in scan.
- Locking, transactions, pending→active lifecycle (including pending-row
  adoption on retry), tombstones, and directory GC apply unchanged to typed
  uploads. No schema migration and no manifest format change: `td:v1`,
  `td-manifest:v1`, `td-album:v1` are untouched (ADR 0013 semantics carry
  over). Captions are identical for typed and plain sends, so caption budget
  enforcement behaves identically.
- NDJSON events and the cp JSON envelope carry no kind-specific fields:
  typed uploads are indistinguishable from plain ones on the wire. Consumers
  that need to know what was published read the fake/real message state or a
  later spec surface.
- Scan/import treat attributed videos as ordinary documents (history reads
  surface them as documents carrying a video attribute block), so adopted and
  freshly published typed content share one storage model.

## Consequences

- Native clients show uploaded videos as playable, streamable cards with
  instant previews when a thumbnail is supplied, without giving up byte
  exactness.
- Streaming playback is broadest for H.264/AAC MP4; exotic containers may
  still render as a video card requiring external playback even with
  attributes set. This is platform reality, not a td error.
- The fake Telegram adapter models kinds, attributes, and thumbs (including
  persistence across restarts), keeping the service-level integration seam
  offline-testable.
- Album/media-group publishing (#26) consumes this request model unchanged.
