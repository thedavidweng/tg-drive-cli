# Media integration notes

How td-published content appears in native Telegram clients, and the
trade-offs each content form carries. For bridge plugins and other callers
driving td over its CLI and `--json` surface. The presentation request model
itself is decided in ADR 0016.

## The three content forms

| Form | Produced by | Native clients show | Bytes on Telegram |
|---|---|---|---|
| Native photo | `td cp --as photo`, or a photo sent by hand (adopted via `td adopt`) | full-size inline preview, swipeable gallery | recompressed by Telegram |
| Attributed video | `td cp --as video --duration <s> --width <px> --height <px> [--streaming] [--thumb <file.jpg>]` | playable, streamable video card with an instant preview when a thumb is attached | untouched (still a document) |
| Document | `td cp` default, or `--as document` | file card | untouched |

- td never probes media files. Callers supply duration and dimensions from
  their own metadata; td stays media-generic.
- Streaming playback is broadest for H.264/AAC MP4. Exotic containers or
  codecs may still render as a video card requiring external playback even
  with attributes set — platform reality, not a td error.
- Thumbnails are JPEG, uploaded as-is, and apply to document/video kinds
  only. Photos take none: Telegram generates photo previews itself.
- All three forms share one storage model: identical captions (`td:v1`),
  locks, pending-row lifecycle, NDJSON events, JSON envelopes, and error
  codes. Scan, import, and repair treat published typed content exactly like
  adopted native content.

## Photo recompression semantics

Native photos are recompressed server-side. The bytes Telegram stores — and
therefore serves back — are its own representation of the image, not the
uploaded file:

- Downloads fetch the **largest representation Telegram holds**, so restore
  gets the best available fidelity.
- The `td:v1` caption and index rows keep the **original** upload's size and
  blake3 hash as provenance. Those values describe bytes Telegram no longer
  holds, so `td get` skips strict size/hash verification for native photos;
  documents and attributed videos keep strict verification because their
  bytes are untouched.
- Callers needing byte-exact round-trips use the document form (default).

## Album conventions

`td cp` publishes multi-file sets (`td cp f1 f2 … dir/`) and folder uploads
(`--recursive`) as native media groups: one human td:v1 caption on the very
first member, empty sibling captions, and one `td-album:v1` inventory reply
per group. Sets larger than 10 files split into consecutive groups of up to
10 members each; each split group gets its own inventory reply, the caption
stays on the overall first member only.

Callers integrating against td should plan for these platform rules:

- Documents and photos cannot mix within one Telegram media group. One td
  invocation applies a single presentation kind, so its own groups are
  homogeneous; bridges ordering scene videos vs gallery images keep them in
  separate invocations (or separate groups via single-file cp).
- A lone survivor — a one-file directory, or every sibling skipped by
  `--skip-existing` — publishes as an ordinary single message with its own
  caption, because Telegram media groups need at least two members.
- `td mv` / `td rm` of an album member shrink or rewrite that group's one
  inventory reply; scan/import rebuild albums from it after a wipe.

## NFC normalization caveat

Remote paths — and therefore restored filenames — are NFC-normalized per the
storage contract. A file uploaded under a non-NFC local name (for example,
the decomposed form macOS produces for accented characters) is published and
restored under its NFC form. The name after `td get` denotes the same name
but may differ byte-wise from the original local filename.
