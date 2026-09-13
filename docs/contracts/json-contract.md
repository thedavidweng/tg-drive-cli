# JSON contract

All JSON command output uses an envelope.

## Success

```json
{
  "ok": true,
  "data": {},
  "meta": {
    "command": "cp",
    "duration_ms": 125,
    "schema_version": "2026-07-29",
    "request_id": "...",
    "warnings": []
  }
}
```

## Error

```json
{
  "ok": false,
  "error": {
    "code": "ERR_CODE",
    "message": "human readable message",
    "category": "api",
    "retryable": true,
    "retry_after_ms": 5000,
    "details": {}
  },
  "meta": {
    "command": "cp",
    "duration_ms": 10,
    "schema_version": "2026-07-29",
    "request_id": "..."
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

## Upload result (single file)

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures/2024/beach.jpg",
    "channel_id": "123456789",
    "message_id": 8821,
    "manifest_message_id": null,
    "size": 2482911,
    "hash": "blake3:fullhexvalue",
    "invite_link": "https://t.me/+Abc123"
  }
}
```

The `invite_link` field is omitted when the channel has no public/join link.
`manifest_message_id` is the machine record's message id; since ADR 0018 it
lives in the linked discussion group's comment thread (the row's
`manifest_chat_tg_id` records the peer), so the id is not addressable in the
drive channel itself.

A retry that adopted a pending upload and sent only its unconfirmed parts adds
`"resumed": true` (files above 10 MB; the identity — size and content hash —
must match the interrupted attempt).

## Upload result (multi-file album)

`td cp <local...> <remote-dir>` and `td cp --recursive` report aggregate
counters plus one entry per sent media group. A lone survivor after skips is
an ordinary single upload: it counts toward `uploaded` but appears in no
group.

```json
{
  "ok": true,
  "data": {
    "uploaded": 12,
    "skipped": 0,
    "errors": [],
    "albums": [
      {
        "grouped_id": 730001,
        "reply_message_id": 8835,
        "message_ids": [8821, 8822, 8823],
        "paths": ["/albums/one.bin", "/albums/two.bin", "/albums/three.bin"]
      }
    ],
    "channel_id": "-100123456789",
    "invite_link": "https://t.me/+Abc123"
  }
}
```

- `uploaded` / `skipped` / `errors` mirror the recursive counters; with the
  default fail policy a planning conflict aborts the whole command before any
  Telegram write, so `errors` stays empty on success.
- `albums` lists every media group in send order; each carries Telegram's
  `grouped_id`, the `td-album:v1` inventory message's id (a comment in the
  linked discussion group per ADR 0018), member message ids, and member
  paths. Empty (`[]`) when nothing grouped.
- `--recursive` emits the same shape plus its historical keys.

## NDJSON event stream

Long-running commands such as `td cp --events` emit one JSON envelope per line:

```json
{"ok":true,"data":{"file_name":"big.bin","part":5,"part_size":524288,"uploaded":2621440,"total":4294967296},"meta":{"command":"cp.progress","duration_ms":120,"schema_version":"2026-07-29","request_id":"..."}}
{"ok":true,"data":{"path":"/big.bin","message_id":1234,"size":4294967296},"meta":{"command":"cp","duration_ms":4200,"schema_version":"2026-07-29","request_id":"..."}}
```

## Channel list

```json
{
  "ok": true,
  "data": {
    "channels": [
      {"id": 123456789, "title": "Pictures [TD]", "username": "", "invite_link": "https://t.me/+Abc123"}
    ]
  }
}
```

## Scan

```json
{
  "ok": true,
  "data": {
    "mode": "full",
    "channel": "123456789",
    "active": 1240,
    "deleted": 0,
    "invalid": 3,
    "missing": 0
  }
}
```

Incremental scans may include `full_scan_warning`. `--include-deleted` adds `tombstones`. A full scan that continued an interrupted run adds `"resumed": true`.

## List

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures/2024",
    "entries": [
      {"type": "dir", "name": "06", "path": "/Pictures/2024/06"},
      {"type": "file", "name": "beach.jpg", "path": "/Pictures/2024/beach.jpg", "size": 2482911, "hash": "blake3:fullhexvalue"}
    ]
  }
}
```

File entries always carry `hash` — the stored BLAKE3 content hash, `""` when
unknown (rows adopted without `--hash`). Directory entries never carry it.
The field is additive: consumers written against earlier versions keep
working.

The example omits two conditional keys for brevity: file entries of active
rows also carry `"status": "active"`, and directory entries may carry
`"ephemeral": true`.

## Tree

```json
{
  "ok": true,
  "data": {
    "path": "/",
    "tree": [{"type": "dir", "name": "Pictures", "path": "/Pictures", "children": []}]
  }
}
```

## Import / adopt

```json
{
  "ok": true,
  "data": {
    "dry_run": true,
    "imported": 3,
    "skipped": 1,
    "failed": 0,
    "deleted": 0,
    "items": [
      {"message_id": 61, "kind": "video", "path": "/videos/The Bet.mp4", "action": "import", "size": 55113768, "file_name": "The Bet.mp4"},
      {"message_id": 114, "kind": "reply", "action": "delete", "reason": "per-file td-manifest:v1 reply"},
      {"message_id": 3, "kind": "photo", "action": "keep", "grouped_id": 99, "caption": "#tag dump"},
      {"message_id": 3, "kind": "album", "action": "album-manifest", "grouped_id": 99, "reason": "one inventory reply for 6 files"}
    ]
  }
}
```

## Recursive download

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures",
    "local": "./restore",
    "downloaded": 10,
    "skipped": 1,
    "failed": 0,
    "errors": []
  }
}
```

## Recursive upload result

```json
{
  "ok": true,
  "data": {
    "uploaded": 42,
    "skipped": 1,
    "failed": 0,
    "errors": [],
    "channel_id": "123456789",
    "invite_link": "https://t.me/+Abc123"
  }
}
```
