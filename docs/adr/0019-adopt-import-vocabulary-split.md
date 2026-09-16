# 0019 - Adopt / import vocabulary split

## Status

Accepted.

## Context

The in-place claim command shipped as `td import` (ADR 0012), but every
other layer of the product already called the behavior **adopt**: the human
output says `import done: 3 adopted`, the `--unmanaged` help text says
"adopt every unmanaged media/text message", the guide is titled "How to
adopt messages that already exist", and ADR 0012 itself says "to adopt
existing messages". Only the command surface says import.

Meanwhile `import` carries its universal meaning across tools — bring
content in from an external source, usually moving bytes (docker import,
photo import; Homebrew reserves `brew adopt` for claiming an existing
installation in place). Saved Messages re-homing needs exactly that word as
its command surface, and keeping both behaviors under `import` would hang two
contradictory byte promises on one command.

## Decision

- Rename the in-place claim to `td adopt`; semantics and flags are
  unchanged (ADR 0012 behavior).
- Reserve `td import <source>` for external-chat ingest: bring content in
  from another Telegram chat by re-uploading fresh bytes. The implemented
  first source is `saved`.
- Hard cutover in one release: the repository has no published releases, so
  there is no compatibility surface worth a deprecation window. Old forms
  (bare `td import`, `td import <id> <path>`, `td import --unmanaged`,
  `td import --rewrite-captions`) fail with `ERR_USAGE` naming the split;
  adopt-only flags on the new `td import` fail with `ERR_FLAG_CONFLICT`.
- Rename the JSON plan/result envelope with the command: the `imported`
  counter becomes `adopted` and the per-item action `import` becomes
  `adopt`. Human output already said "adopted" and stays unchanged.
- The three-way family splits by content source: `td cp` (local disk →
  drive, bytes move), `td import <source>` (external chat → drive, bytes
  move), `td adopt` (already in the drive channel → index, bytes never
  move).
- ADR 0012 stands as history; it is not rewritten.

## Consequences

- The CLI contract, JSON contract, CLI reference, guides, README, and
  completions all rename with the command.
- `td import saved` is the first external-chat ingest and points old
  in-place callers at `td adopt`.
- The glossary defines **Adopt**, **Import**, **Saved Messages**, and the
  provenance records used by the saved import.
- Scripts written against `td import`'s old surface break in one release —
  acceptable before the first published release, and the error message
  tells the operator exactly where each old form went.
