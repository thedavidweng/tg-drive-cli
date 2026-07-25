# JSON contract

All JSON command output uses an envelope.

## Success

```json
{
  "ok": true,
  "data": {}
}
```

## Error

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

`ERR_TELEGRAM_RATE_LIMITED` errors carry machine-readable retry hints in
`details`:

```json
{
  "ok": false,
  "error": {
    "code": "ERR_TELEGRAM_RATE_LIMITED",
    "message": "telegram rate limited this account: retry after 23h41m26s (at 2026-07-26 13:15 PDT)",
    "details": {
      "retry_after_seconds": 85286,
      "retry_at": "2026-07-26T20:15:00Z"
    }
  }
}
```

## Version

```json
{
  "ok": true,
  "data": {
    "version": "0.1.0",
    "commit": "abc123",
    "date": "2026-07-02T00:00:00Z",
    "built_by": "goreleaser"
  }
}
```

## Upload result

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures/2024/beach.jpg",
    "channel_id": "123456789",
    "message_id": 8821,
    "manifest_message_id": null,
    "size": 2482911,
    "hash": "blake3:fullhexvalue"
  }
}
```
