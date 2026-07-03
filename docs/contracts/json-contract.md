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
