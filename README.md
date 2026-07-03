# tg-drive-cli

Telegram-backed virtual file tree CLI. Binary name: `td`.

## Quickstart

1. Create a Telegram app at [my.telegram.org/apps](https://my.telegram.org/apps).
2. Configure credentials and log in:

```sh
td auth setup
td auth login
td auth status --json
```

3. Initialize a storage channel and upload a file:

```sh
td init ./Pictures --create-channel "Pictures [TD]"
td cp ./Pictures/photo.jpg /photo.jpg
td ls /
```

Session is stored at `~/.config/tg-drive-cli/session.json`. Config at `~/.config/tg-drive-cli/config.toml`.

## Development

```sh
make ci-local
TD_FAKE_TELEGRAM=1 go test ./...
```

Set `TD_FAKE_TELEGRAM=1` to use the in-memory fake Telegram client (no network).

## Docs

- `PRODUCT_SPEC.md` — product behavior
- `IMPLEMENTATION_PLAN.md` — build stages
- `docs/manual-smoke-tests.md` — real Telegram smoke tests
- `docs/contracts/` — CLI, JSON, storage contracts

## Reference projects studied

- [TGDrivePersonal](https://github.com/TechShreyash/TGDrivePersonal) — Pyrogram bot-token storage model
- [Telegram-Drive](https://github.com/caamer20/Telegram-Drive) — grammers phone login and upload flow

`td` uses MTProto user login (like Telegram-Drive) with `td:v1` caption metadata (unlike both references).
