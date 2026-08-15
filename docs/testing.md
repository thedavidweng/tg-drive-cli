# Testing plan

## Unit tests

- output envelope
- error code mapping
- config path resolution
- secret redaction
- DB migrations
- WAL and foreign keys
- operation lock acquisition/stale takeover/release
- path normalization
- file/dir collision
- slug generation
- slug collision extension
- UTF-16 caption counting
- manifest rendering/parsing
- caption fallback

## Offline trial mode

Set `TD_FAKE_TELEGRAM=1` to run the CLI against the in-memory fake Telegram
client (no network). Add `TD_FAKE_TELEGRAM_STATE=<path>` to persist the fake's
state (login, channels, uploaded bytes) across invocations, which makes the
complete workflow — login (code `12345`), init, cp, ls, get, mv, rm, scan,
share — runnable end to end without a real account.

## Integration tests with fake Telegram

- upload single file
- deep path manifest reply
- manifest failure rollback
- full scan rebuild
- incremental scan
- scan error de-dupe/resolution
- download hash verification
- move editable file
- move non-editable file
- delete mode
- tombstone mode
- repair pending
- repair orphaned
- recursive upload crash recovery

## Manual smoke tests

Manual tests require a real Telegram account, `api_id`, and `api_hash`.

```sh
td auth login
td doctor
td init ./testdata/local --create-channel
td cp ./testdata/local/a.txt /a.txt
td ls /
td tree /
td get /a.txt ./restore/a.txt
td mv --confirm /a.txt /b.txt
td rm --confirm /b.txt
td scan --full
td share /
```
