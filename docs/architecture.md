# Architecture

`td` has four layers.

```text
cmd/td
  -> internal/app       CLI wiring and command execution
  -> internal/tgdrive   application services
  -> internal/db        SQLite cache, migrations, transactions
  -> internal/telegram  adapter interface
  -> internal/mtproto   gotd/td implementation
```

## Dependency direction

- `cmd/td` imports only `internal/app`.
- `internal/app` imports command services and output/error packages.
- `internal/tgdrive` owns command behavior.
- `internal/tgdrive` depends on interfaces, not directly on `gotd/td`.
- `internal/mtproto` is the only package that imports `github.com/gotd/td`.
- `internal/db` exposes repository methods and transaction helpers.
- `internal/telegram/fake` supports integration tests.

## Command flow

Example upload:

```text
td cp
  parse flags
  resolve config
  open DB
  resolve channel
  normalize path
  acquire operation locks
  insert pending row
  compute hash/mime
  render manifest/caption
  upload media
  send manifest reply if needed
  commit active DB state
  release locks
  render output
```

## Failure model

- Before Telegram upload: rollback DB transaction.
- After Telegram upload but before DB commit: create repairable pending/orphan state.
- Manifest reply failure: delete uploaded media when possible; otherwise mark orphaned and require `td repair --pending`.
- DB write failure after Telegram edit: run `td scan --full` to reconcile.

## Package responsibilities

| Package | Responsibility |
|---|---|
| `app` | Cobra root, flags, command registration |
| `apperr` | typed errors and exit code mapping |
| `output` | human/JSON rendering |
| `config` | config/env/path loading and redaction |
| `db` | migrations, repositories, transactions |
| `fsmodel` | canonical paths and virtual tree rules |
| `pathcodec` | slug and hashtag generation |
| `manifest` | td:v1 and td-manifest:v1 render/parse |
| `workerlock` | operation lock acquisition and stale takeover |
| `telegram` | interfaces and fake adapter |
| `mtproto` | gotd/td adapter |
| `tgdrive` | use cases: upload, scan, download, move, delete, repair |
| `capability` | doctor checks and capability cache |
