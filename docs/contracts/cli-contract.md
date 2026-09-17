# CLI contract

Binary: `td`

## Global flags

```text
--config <path>
--db <path>
--session <path>
--json
--quiet
--verbose
--channel <name-or-id>
--wait
--no-wait
```

## Commands

```text
td version
td auth setup
td auth login [--resend]
td auth status
td auth logout
td channels list [--only-drive]
td channels link-discussion
td init <local-root>
  [--create-channel [=<title>]]
  [--bind-channel [=<title>]]
  (bare --bind-channel lists and prompts)
  # init always ensures a linked discussion group for the channel
  # (created on demand); machine records live in its comment threads
td status
td doctor
  td doctor path-codec
td scan [remote-root]
  [--full] [--strict] [--repair] [--include-deleted]
td ls [remote-path]
td tree [remote-path]
  [--depth <n>]
td completion [bash|zsh|fish|powershell]
td cp <local> [local...] <remote-path>
  [--replace] [--skip-existing] [--auto-rename] [--no-hash]
  [--recursive] [--continue-on-error] [--include-empty-dirs]
  [--upload-threads <n>] [--upload-part-size-kb <n>]
  [--confirm] [--dry-run] [--events]
  # typed uploads (single-file and multi-file; rejected with --recursive)
  [--as <photo|video|document>]        # presentation kind (default document)
  [--duration <seconds>]               # video length (--as video)
  [--width <px>] [--height <px>]       # video dimensions (--as video)
  [--streaming]                        # streaming hint (--as video)
  [--thumb <file.jpg>]                 # JPEG thumbnail (document/video kinds)
```

Two argument forms:

- `td cp <local> <remote-path>` — single file, one message per upload; the
  caption contains only the caller's human text and display name. The remote
  parent path and `#td_*` path tags are not rendered into new captions. The
  machine record is a `td-manifest:v1` comment on the message's discussion
  thread (ADR 0018)
- `td cp <local...> <remote-dir>` — two or more sources publish as native
  Telegram media groups (albums): one human-only caption on the first
  member, empty sibling captions, one `td-album:v1` inventory comment on
  the first member's discussion thread per group (ADR 0018). The caption
  contains no generated parent path or `#td_*` path tags. Sets larger than
  10 members split into consecutive groups of up to 10.
  `<remote-dir>` is `/`, a path ending in `/`, or an existing remote
  directory. `--replace` is not supported in this form (`ERR_USAGE`);
  `--skip-existing` and `--auto-rename` apply per file, and a lone survivor
  after skips publishes as an ordinary single message with its own
  inventory comment.

`--recursive` uploads each source directory's direct children as one album
(split at 10); nested directories recurse.

`--as photo` sends a native photo message: Telegram recompresses the bytes,
downloads fetch the largest representation, and strict size/hash verification
does not apply to them. `--as video` keeps the bytes untouched. In multi-file
form the presentation flags apply uniformly to every member. See
`docs/integration-notes.md` for the full semantics of the three content
forms.

```text
td get <remote-path> <local-dest>
  [--recursive] [--replace] [--skip-existing] [--auto-rename] [--continue-on-error]
td mv <remote-from> <remote-to>
  [--confirm] [--dry-run]
td rm <remote-path>
  [--tombstone] [--allow-stale-manifest]
  [--confirm] [--dry-run]
td share [remote-path]
td adopt [message-id] [remote-path]
  [--unmanaged] [--into <dir>]
  [--hash]
  [--rewrite-captions]
  # --rewrite-captions converts a legacy channel to the ADR 0018 comment
  # model: strips machine lines from captions (one human caption per
  # album), deletes per-file td-manifest replies, and posts one
  # td-album:v1 inventory comment per group
  # --hash downloads each adopted file to compute and store its BLAKE3
  # content hash, so post-rebuild downloads verify content
td import saved [message-id...]
  [--into <dir>] [--photos-as <document|photo>]
  [--merge-captions] [--delete-source] [--no-dedupe]
  [--replace|--skip-existing|--auto-rename]
  [--dry-run] [--confirm] [--continue-on-error] [--events]
  # re-upload Saved Messages content into the drive channel; forwarded
  # provenance is recorded as td-origin:v1 and skipped captions as
  # td-dupe:v1 comments in the linked discussion group
  # --photos-as is required for photo messages in --json/non-interactive runs
  # --confirm is required unless --dry-run; --delete-source also requires it
  # old in-place claim forms fail with ERR_USAGE naming td adopt
td repair [path]
td repair --pending
td repair --orphaned [--delete-orphaned --confirm]
td repair --scan-errors
td repair [--hash] [path]
  # --hash downloads every active file under [path] that lacks a content
  # hash, computes its BLAKE3 digest, and backfills it into the index and
  # the machine record on Telegram (comment thread, legacy reply, or
  # caption carrier). A zero/missing size is repaired from the download.
td repair --captions [path]
  [--dry-run] [--continue-on-error]
  # removes td's former parent-path and #td_* caption scaffold from modern
  # comment-carrier rows; exact path/hash/MIME/tags remain in discussion
  # manifests. Legacy td:v1 caption carriers are skipped.
td config get [key]
  [--show-secrets] [--confirm]
td config set <key> <value>
```

`--confirm` is required for `td rm`, `td mv`, `td cp --replace`, `td adopt`,
`td import saved` (unless `--dry-run`), and `td repair --delete-orphaned`.

JSON envelopes include `meta` as specified in `docs/contracts/json-contract.md`. The short envelopes below omit `meta` for brevity.

## Global JSON success envelope

```json
{
  "ok": true,
  "data": {}
}
```

## Global JSON error envelope

```json
{
  "ok": false,
  "error": {
    "code": "ERR_CODE",
    "message": "human readable message",
    "details": {}
  }
}
```

## Error codes

```text
ERR_USAGE
ERR_FLAG_CONFLICT
ERR_AUTH_REQUIRED
ERR_AUTH_FAILED
ERR_CONFIG_MISSING
ERR_CONFIG_INVALID
ERR_CHANNEL_NOT_FOUND
ERR_CHANNEL_PERMISSION
ERR_PATH_INVALID
ERR_PATH_EXISTS
ERR_PATH_IS_DIRECTORY
ERR_PATH_ANCESTOR_IS_FILE
ERR_PATH_CONFLICT
ERR_LOCAL_PATH_EXISTS
ERR_LOCAL_NOT_FOUND
ERR_REMOTE_NOT_FOUND
ERR_FILE_TOO_LARGE
ERR_CAPTION_TOO_LONG
ERR_MANIFEST_INVALID
ERR_ALBUM_INVENTORY_INVALID
ERR_SCAN_INCOMPLETE
ERR_CROSS_CHANNEL_MOVE
ERR_DIRECTORY_MOVE_UNSUPPORTED
ERR_DIRECTORY_DELETE_UNSUPPORTED
ERR_EMPTY_DIRS_UNSUPPORTED
ERR_MESSAGE_NOT_EDITABLE
ERR_SCAN_FAILED
ERR_TELEGRAM_RATE_LIMITED
ERR_TELEGRAM_RPC
ERR_CONFIRMATION_REQUIRED
ERR_DB
ERR_OPERATION_LOCKED
ERR_ORPHANED_UPLOAD
ERR_REPAIR_REQUIRED
ERR_SLUG_COLLISION
ERR_UNKNOWN
```

## Exit code mapping

| Exit | Meaning | Error codes |
|---:|---|---|
| 0 | Success | none |
| 1 | General error | uncategorized errors |
| 2 | Usage/input error | `ERR_USAGE`, `ERR_FLAG_CONFLICT`, `ERR_PATH_INVALID`, `ERR_PATH_EXISTS`, `ERR_PATH_IS_DIRECTORY`, `ERR_PATH_ANCESTOR_IS_FILE`, `ERR_PATH_CONFLICT`, `ERR_LOCAL_PATH_EXISTS`, `ERR_LOCAL_NOT_FOUND`, `ERR_REMOTE_NOT_FOUND`, `ERR_CROSS_CHANNEL_MOVE`, `ERR_DIRECTORY_MOVE_UNSUPPORTED`, `ERR_DIRECTORY_DELETE_UNSUPPORTED`, `ERR_EMPTY_DIRS_UNSUPPORTED`, `ERR_SLUG_COLLISION` |
| 3 | Auth/config error | `ERR_AUTH_REQUIRED`, `ERR_AUTH_FAILED`, `ERR_CONFIG_MISSING`, `ERR_CONFIG_INVALID` |
| 4 | Telegram/platform error | `ERR_CHANNEL_NOT_FOUND`, `ERR_CHANNEL_PERMISSION`, `ERR_FILE_TOO_LARGE`, `ERR_MESSAGE_NOT_EDITABLE`, `ERR_TELEGRAM_RATE_LIMITED`, `ERR_TELEGRAM_RPC` |
| 5 | DB/index/repair error | `ERR_DB`, `ERR_SCAN_FAILED`, `ERR_MANIFEST_INVALID`, `ERR_ALBUM_INVENTORY_INVALID`, `ERR_SCAN_INCOMPLETE`, `ERR_OPERATION_LOCKED`, `ERR_ORPHANED_UPLOAD`, `ERR_REPAIR_REQUIRED`, `ERR_CAPTION_TOO_LONG` |
| 10 | Confirmation/safety error | `ERR_CONFIRMATION_REQUIRED` |
