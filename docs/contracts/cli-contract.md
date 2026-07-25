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
td auth login
td auth status
td auth logout
td init <local-root>
td status
td doctor
td scan [remote-root]
td ls [remote-path]
td tree [remote-path]
td cp <local> <remote-path>
td get <remote-path> <local-dest>
td mv <remote-from> <remote-to>
td rm <remote-path>
td share [remote-path]
td repair [path]
td repair --pending
td repair --orphaned
td repair --scan-errors
td config get [key]
td config set <key> <value>
```

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
ERR_DB
ERR_OPERATION_LOCKED
ERR_ORPHANED_UPLOAD
ERR_REPAIR_REQUIRED
ERR_SLUG_COLLISION
```

## Exit code mapping

| Exit | Meaning | Error codes |
|---:|---|---|
| 0 | Success | none |
| 1 | General error | uncategorized errors |
| 2 | Usage/input error | `ERR_USAGE`, `ERR_FLAG_CONFLICT`, `ERR_PATH_INVALID`, `ERR_PATH_EXISTS`, `ERR_PATH_IS_DIRECTORY`, `ERR_PATH_ANCESTOR_IS_FILE`, `ERR_PATH_CONFLICT`, `ERR_LOCAL_PATH_EXISTS`, `ERR_LOCAL_NOT_FOUND`, `ERR_REMOTE_NOT_FOUND`, `ERR_CROSS_CHANNEL_MOVE`, `ERR_DIRECTORY_MOVE_UNSUPPORTED`, `ERR_DIRECTORY_DELETE_UNSUPPORTED`, `ERR_EMPTY_DIRS_UNSUPPORTED`, `ERR_SLUG_COLLISION` |
| 3 | Auth/config error | `ERR_AUTH_REQUIRED`, `ERR_CONFIG_MISSING`, `ERR_CONFIG_INVALID` |
| 4 | Telegram/platform error | `ERR_CHANNEL_NOT_FOUND`, `ERR_CHANNEL_PERMISSION`, `ERR_FILE_TOO_LARGE`, `ERR_MESSAGE_NOT_EDITABLE`, `ERR_TELEGRAM_RATE_LIMITED`, `ERR_TELEGRAM_RPC` |
| 5 | DB/index/repair error | `ERR_DB`, `ERR_SCAN_FAILED`, `ERR_MANIFEST_INVALID`, `ERR_OPERATION_LOCKED`, `ERR_ORPHANED_UPLOAD`, `ERR_REPAIR_REQUIRED`, `ERR_CAPTION_TOO_LONG` |
