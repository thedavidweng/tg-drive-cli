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
td init <local-root>
  [--create-channel [=<title>]]
  [--bind-channel [=<title>]]
  (bare --bind-channel lists and prompts)
td status
td doctor
  td doctor path-codec
td scan [remote-root]
  [--full] [--strict] [--repair] [--include-deleted]
td ls [remote-path]
td tree [remote-path]
  [--depth <n>]
td completion [bash|zsh|fish|powershell]
td cp <local> <remote-path>
  [--replace] [--skip-existing] [--auto-rename] [--no-hash]
  [--recursive] [--continue-on-error] [--include-empty-dirs]
  [--upload-threads <n>] [--upload-part-size-kb <n>]
  [--confirm] [--dry-run] [--events]
td get <remote-path> <local-dest>
  [--recursive] [--replace] [--skip-existing] [--auto-rename] [--continue-on-error]
td mv <remote-from> <remote-to>
  [--confirm] [--dry-run]
td rm <remote-path>
  [--tombstone] [--allow-stale-manifest]
  [--confirm] [--dry-run]
td share [remote-path]
td import [message-id] [remote-path]
  [--unmanaged] [--into <dir>]
  [--keep-caption] [--hash]
  [--rewrite-captions]
  [--confirm] [--dry-run] [--continue-on-error]
  # --rewrite-captions restores album captions, removes per-file
  # td-manifest replies, and upserts one td-album:v1 reply per group
td repair [path]
td repair --pending
td repair --orphaned [--delete-orphaned --confirm]
td repair --scan-errors
td config get [key]
  [--show-secrets] [--confirm]
td config set <key> <value>
```

`--confirm` is required for `td rm`, `td mv`, `td cp --replace`, `td import` (unless `--dry-run`), and `td repair --delete-orphaned`.

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
| 5 | DB/index/repair error | `ERR_DB`, `ERR_SCAN_FAILED`, `ERR_MANIFEST_INVALID`, `ERR_OPERATION_LOCKED`, `ERR_ORPHANED_UPLOAD`, `ERR_REPAIR_REQUIRED`, `ERR_CAPTION_TOO_LONG` |
| 10 | Confirmation/safety error | `ERR_CONFIRMATION_REQUIRED` |
