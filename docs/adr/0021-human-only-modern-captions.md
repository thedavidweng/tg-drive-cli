# 0021 - Keep path metadata out of modern media captions

## Status

Accepted and implemented.

## Context

ADR 0018 moved authoritative machine records into linked discussion-group
comment threads, but its original caption description still included the
remote parent path and generated `#td_*` path chain. Those lines are
implementation metadata, not useful media context. They are especially
confusing for application-managed paths such as `/stash-browse/<scene-id>/`.

The distinction between `td adopt` and new uploads exposed the problem:
`td adopt` correctly leaves an existing human caption untouched, while
`td cp`, Saved Messages imports, single-file moves, and repair operations
could render the internal path scaffold into captions.

## Decision

- Modern captions contain only caller-provided human text and the display
  name.
- Modern captions never contain the remote parent path or path-derived
  `#td_*` chain.
- The canonical path, BLAKE3 hash, MIME, and complete tag chain remain in
  the authoritative `td-manifest:v1` or `td-album:v1` discussion comment.
- Legacy `td:v1` captions and in-channel reply carriers remain parseable and
  are not rewritten by the modern renderer.
- `td repair --captions [path]` provides an idempotent, exact-match cleanup
  for existing modern captions. It skips legacy carriers, preserves human
  text, supports dry-run and per-item continuation, and tolerates messages
  Telegram no longer permits editing.
- Path-derived tags remain available to the index and manifest logic. The
  `td share` hashtag value is legacy/compatibility data and is not a
  promise that new captions contain that tag.

## Consequences

- New channel posts show only human media context; internal browse paths do
  not leak into the channel timeline.
- Native Telegram hashtag navigation for newly uploaded path tags no longer
  works. `td ls`, `td tree`, `td share`, and discussion manifests remain the
  supported path and sharing surfaces.
- Existing modern posts require one explicit `td repair --captions --dry-run`
  review followed by `td repair --captions` to remove the old scaffold.
- Caption rendering no longer spends budget on path scaffolding. Legacy
  caption overflow behavior remains unchanged.
