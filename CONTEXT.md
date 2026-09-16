# tg-drive-cli Domain Glossary

## Core concepts

- **td** — The CLI binary name. Short for "Telegram Drive".
- **Root** — A binding between a local directory and a Telegram channel, stored in the SQLite cache and `config.toml`.
- **Channel** — A Telegram channel used as the remote storage target.
- **Virtual file tree** — The path hierarchy presented by `td ls`, `td tree`, `td cp`, `td get`, and related commands. It is reconstructed from Telegram media and `td:v1` / `td-manifest:v1` / `td-album:v1` records, then cached in SQLite.
- **Canonical path** — The stable, slash-separated path used in the SQLite index and manifest (e.g. `/Pictures/2024/beach.jpg`).
- **Display name** — The human-readable file or directory name stored in a caption or manifest.
- **Manifest** — Reconstructable machine metadata on Telegram. Since ADR 0018 it lives as a comment in the file's discussion thread: one `td-manifest:v1` comment per ungrouped file, one `td-album:v1` comment per album group. Legacy in-channel replies and `td:v1` captions remain parseable.
- **Caption** — The human text attached to a Telegram media message: display name, parent directory, hashtags. Machine metadata is never written onto captions for new uploads; the machine record is the discussion-thread comment.
- **Discussion group** — The supergroup linked to a drive channel. Every channel post auto-forwards there, and its comment thread carries the post's machine record (ADR 0018).
- **Hashtag** — A `#` prefixed token in a caption used for filtering in native Telegram clients. Not the reconstructable storage model.
- **Node** — A file or directory entry in the SQLite index (`nodes` table).
- **File row** — A row in the `files` table tracking message ID, manifest ID, hash, status, and local path.
- **Status** — The lifecycle of a file row: `pending`, `active`, `deleted`, `superseded`, `missing`, `invalid`, `orphaned`.
- **Content hash** — A `blake3` (or configured) hash of the file bytes, used for integrity checks.
- **Slug** — A collision-resistant hashtag segment (`readable_prefix` + 8-character BLAKE3 suffix) used in `#td_` chains. Not the canonical path.
- **Resumable upload** — A big-file upload whose part state is persisted in `upload_progress` so it can continue after interruption.
- **Operation lock** — A row in `operation_locks` that serializes path-touching operations across processes.
- **File publisher** — The module that publishes a file to the index, turning a canonical path, display name, content hash, and Telegram message into a file row, manifest, hashtag tags, slug mappings, and derived nodes.
- **Adopt** — Claim an existing message of the bound drive channel into the virtual file tree without re-uploading bytes (`td adopt`). The media message stays where it is; td writes machine records and indexes the file.
- **Scan** — Reading a Telegram channel's messages and rebuilding the local SQLite index.
- **Tombstone** — A soft delete: the Telegram message is edited to a tombstone caption instead of being removed.

## Sources of truth

- Product constraints: `DECISIONS.md`
- CLI surface: `docs/contracts/cli-contract.md`
- JSON envelope: `docs/contracts/json-contract.md`
- Storage schema: `docs/contracts/storage-contract.md`
- Config keys: `docs/contracts/config-contract.md`
- ADRs: `docs/adr/`
