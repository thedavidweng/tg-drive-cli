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
td cp ./testdata/local/deep.txt /very/deep/path/that/forces/manifest/deep.txt
td ls /
td tree /
td get /a.txt ./restore/a.txt
td mv /a.txt /b.txt
td rm /b.txt
td scan --full
td share /
```
