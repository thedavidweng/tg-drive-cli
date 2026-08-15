# tg-drive-cli Decisions

This document exists to prevent implementation drift. Do not replace these choices without opening a decision-changing issue.

## Product

- Build a CLI-first Telegram-backed file tree.
- Binary name is `td`.
- One Telegram channel per initialized root.
- File-level move and rename only.
- Directory move/delete, multi-channel storage, forum topics, watch mode, WebDAV/FUSE, dedupe, encryption, and multi-account switching are out of scope ([issues](https://github.com/thedavidweng/tg-drive-cli/issues)).
- Empty directories are local-only and disappear after a full scan.

## Repository

- Module path: `github.com/thedavidweng/tg-drive-cli`.
- Main package: `./cmd/td`.
- License: Apache-2.0.
- Go version: 1.26.
- Package layout is `cmd/td` + `internal/{app,service,config,output}` + `core/*` + `adapters/*`.

## CLI

- CLI framework: Cobra.
- Human output is default.
- `--json` produces stable JSON envelopes.
- JSON success and error envelopes are global and invariant.
- Logs and prompts go to stderr.
- JSON output goes to stdout.
- Environment prefix is `TD_` only.

## Telegram

- Use MTProto user login through `gotd/td`.
- Require `TD_API_ID`, `TD_API_HASH`, and phone login.
- Do not use Bot API for storage operations.
- Treat Telegram behavior as capability-based.
- `td doctor` reports edit/delete/upload/invite capabilities.
- Caption edit on old messages is not assumed.
- File upload limit is 2 GB by default and 4 GB only when Premium capability is detected or configured.

## Storage

- SQLite is a cache and operation index.
- Telegram messages are the recoverable source.
- Use WAL.
- Use foreign keys.
- Use `files.node_id on delete set null`.
- Use partial unique indexes for active and pending paths.
- Use `operation_locks` with path-level keys.
- Use one lock namespace: `path:<channel_id>:<canonical_path>`.

## Paths

- Canonical paths are NFC-normalized UTF-8 POSIX paths.
- Canonical paths are case-sensitive.
- Canonical paths always begin with `/`.
- Reject `.` and `..` segments.
- Reject file-vs-directory conflicts.
- If `td mv file existing-dir`, move into the directory using the source basename.

## Manifest and caption

- Machine reconstruction uses `td:v1`, `td-manifest:v1`, or `td-album:v1`, not hashtags.
- A Telegram media album is one human post and one machine inventory: human caption on the first item, one `td-album:v1` reply for the group.
- Media caption budget is 1024 UTF-16 code units with 16-unit margin.
- Text manifest budget is 4096 UTF-16 code units with 16-unit margin.
- Minimal caption overflow returns `ERR_CAPTION_TOO_LONG`.
- Hashtags are shallow-to-deep and are truncated from the deep side.

## Slugs and hashtags

- Hashtags are native Telegram UX helpers.
- Hashtags are not the authoritative storage model.
- Slugs use pinyin for Chinese, unidecode for other Unicode, and BLAKE3 suffixes.
- Runtime slug suffix is 8 base32 characters, then 13, then 26 on collision; still colliding fails with `ERR_SLUG_COLLISION`. Readable prefix is at most 24 characters.
- `path_segment_slugs` stores segment slug mappings.
- `path_tags` stores per-file final tag chains.

## Release

- Use GitHub Actions.
- Use release-please for release PRs and changelog.
- Use GoReleaser v2 for artifacts.
- Publish darwin/linux/windows archives.
- Publish deb/rpm/apk packages through nfpm.
- Publish Homebrew cask to `thedavidweng/homebrew-tap`.
- Sign checksums with cosign.
- Generate SBOMs with syft.
