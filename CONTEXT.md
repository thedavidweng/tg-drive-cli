# tg-drive-cli Domain Glossary

## Core concepts

- **td** — The CLI binary name. Short for "Telegram Drive".
- **Root** — A binding between a local directory and a Telegram channel, stored in the SQLite cache and `config.toml`.
- **Channel** — A Telegram channel used as the remote storage target. `td` creates, lists, binds, and uploads to channels.
- **Virtual file tree** — The path hierarchy presented by `td ls`, `td tree`, `td cp`, `td get`, etc. It is reconstructed from Telegram media messages and captions.
- **Canonical path** — The stable, slash-separated path used in the SQLite index and manifest (e.g. `/Pictures/2024/beach.jpg`).
- **Display name** — The human-readable file or directory name stored in a caption or manifest.
- **Manifest** — A `td-manifest:v1` reply message that stores structured file metadata (path, size, hash, tags, MIME).
- **Caption** — The text attached to a Telegram media message. `td` embeds `td:v1` metadata, hashtags, and a compact manifest in captions.
- **Hashtag** — A `#` prefixed token in a caption used for filtering in native Telegram clients and for reconstruction by `td`.
- **Node** — A file or directory entry in the SQLite index (`nodes` table).
- **File row** — A row in the `files` table tracking message ID, manifest ID, hash, status, and local path.
- **Status** — The lifecycle of a file row: `pending`, `active`, `deleted`, `superseded`, `missing`, `invalid`, `orphaned`.
- **Content hash** — A `blake3` (or configured) hash of the file bytes, used for deduplication and integrity.
- **Slug** — A shortened, collision-resistant segment used for canonical paths that would otherwise exceed Telegram caption length.
- **Resumable upload** — A big-file upload whose part state is persisted in `upload_progress` so it can continue after interruption.
- **Operation lock** — A row in `operation_locks` that serializes path-touching operations across processes.
- **Scan** — The process of reading a Telegram channel's messages and rebuilding the local SQLite index.
- **Tombstone** — A soft-delete style where the Telegram message is edited to a tombstone caption instead of being physically deleted.

## Sources of truth

- Product behavior: `PRODUCT_SPEC.md`
- CLI surface: `docs/contracts/cli-contract.md`
- JSON envelope: `docs/contracts/json-contract.md`
- Storage schema: `docs/contracts/storage-contract.md`
- ADRs: `docs/adr/`
