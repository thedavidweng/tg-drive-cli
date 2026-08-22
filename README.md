# td

[![CI](https://github.com/thedavidweng/tg-drive-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/thedavidweng/tg-drive-cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/thedavidweng/tg-drive-cli)](go.mod)
[![License](https://img.shields.io/github/license/thedavidweng/tg-drive-cli)](LICENSE)

Turn a Telegram channel into a recoverable, scriptable file tree.

`td` uploads local files as ordinary Telegram media, stamps each message with
machine-readable metadata, and keeps a rebuildable SQLite index. You get
`ls`, `tree`, upload, download, move, and share — and if the local database is
lost, `td scan --full` rebuilds it from the channel.

The channel stays a normal Telegram channel. Any native client can browse,
download, and filter folders by hashtag.

## Features

- Upload and download files, including recursive trees
- Exact `ls` / `tree` over a local cache
- File-level move, rename, delete, or tombstone
- Import existing channel messages without re-uploading
- Recover the index from Telegram after database loss
- Native hashtag navigation in Telegram clients
- Stable `--json` output for scripts
- Resumable uploads for files larger than 10 MB

## Install

### macOS / Linux

```sh
curl -fsSL https://raw.githubusercontent.com/thedavidweng/tg-drive-cli/main/install.sh | sh
```

Installs into `~/.local/bin` by default (`TD_INSTALL_DIR` overrides). If
Homebrew is present, the script uses the cask instead. Uninstall with
`install.sh uninstall`.

### Windows

```powershell
irm https://raw.githubusercontent.com/thedavidweng/tg-drive-cli/main/install.ps1 | iex
```

Installs `td.exe` into `%LOCALAPPDATA%\tg-drive-cli\bin` (`$env:TD_INSTALL_DIR`
overrides) and adds it to the user `PATH`.

### Homebrew

```sh
brew tap thedavidweng/tap
brew install --cask tg-drive-cli
```

### go install

```sh
go install github.com/thedavidweng/tg-drive-cli/cmd/td@latest
```

Requires Go 1.26 or newer.

### From source

```sh
git clone https://github.com/thedavidweng/tg-drive-cli.git
cd tg-drive-cli
make build          # ./dist/td
```

## Requirements

`td` logs in as your Telegram user over MTProto. It is not a bot.

1. Open [my.telegram.org](https://my.telegram.org) → **API development tools**.
2. Create an application and copy `api_id` and `api_hash`.
3. Have your phone number ready (international format, e.g. `+1234567890`).
   You will confirm a login code and, if enabled, your 2FA password.

Treat `api_hash` and the session file as account credentials.

## Quick start

```sh
td auth setup                  # store api_id / api_hash
td auth login                  # phone code + optional 2FA
td init ~/Pictures --create-channel
td cp ~/Pictures/beach.jpg /2024/beach.jpg
td ls /
td tree /
td share /2024
```

`td init` binds one Telegram channel to a local root and runs an initial scan.
`td ls` and `td tree` read the local cache. `td share` prints an invite link
and the subtree hashtag.

You only log in once. Later commands reuse the saved session.

## How it works

Each remote file is a Telegram media message plus reconstructable metadata:

```text
display name
parent path

td:v1 p=<path> n=<name> s=<size> h=<hash> m=<mime>

#td_Pictures_<hash> #td_Pictures_<hash>_2024_<hash>
```

- **Telegram messages are the source of truth.** SQLite is a cache.
- Machine recovery uses `td:v1`, `td-manifest:v1`, or `td-album:v1` — never
  hashtags alone.
- Deep paths that exceed the media caption budget (1024 UTF-16 code units)
  send a minimal caption and put the rest in a `td-manifest:v1` reply.
- Directories are derived from file paths. They are not stored on Telegram.

After database loss:

```sh
td scan --full
```

## Usage

| Command | Purpose |
| --- | --- |
| `td auth` | Login, status, logout |
| `td init <root>` | Bind a local directory to one channel |
| `td cp` / `td get` | Upload / download |
| `td ls` / `td tree` | Browse the cache |
| `td mv` / `td rm` | Rename or delete |
| `td import` | Adopt existing Telegram messages |
| `td share` | Invite link and subtree hashtag |
| `td scan` / `td repair` | Rebuild or fix the index |
| `td doctor` / `td status` | Health and capability checks |
| `td config` | Read and write settings |

```sh
td cp --recursive ~/Pictures /Pictures
td cp --replace --confirm ~/new.jpg /2024/beach.jpg
td get --recursive /Pictures ./restore
td mv --confirm /2024/beach.jpg /Archive
td rm --confirm /2024/beach.jpg
td import --unmanaged --dry-run --channel "Pictures [TD]"
td scan --full
td doctor
```

Global flags: `--json`, `--quiet`, `--verbose`, `--config`, `--db`,
`--session`, `--channel`, `--wait`, `--no-wait`.

Destructive remote writes (`rm`, `mv`, `cp --replace`, `import`,
`repair --delete-orphaned`) require `--confirm`.

See `td --help` and `td <command> --help` for the full flag list. Frozen
command and JSON shapes live in [`docs/contracts/`](docs/contracts/).

## Configuration

Precedence: CLI flags > `TD_*` environment variables > config file > defaults.

| Purpose | Default |
| --- | --- |
| Config | `~/.config/tg-drive-cli/config.toml` |
| Session | `~/.config/tg-drive-cli/session.json` |
| Cache DB | `~/.local/share/tg-drive-cli/local_cache.db` |

Overrides: `TD_CONFIG`, `TD_SESSION`, `TD_DB`, or `--config` / `--session` /
`--db`. Credentials can also come from `TD_API_ID` and `TD_API_HASH`.

`td auth login` reuses a pending login code instead of requesting a new one
(Telegram flood-waits accounts that request codes repeatedly). Use
`--resend` only when you need a fresh code.

## Hashtags

Each directory level gets a cumulative `#td_` tag so tapping a tag in Telegram
filters that folder. Chinese segments are transliterated to pinyin; other
scripts become ASCII plus a hash suffix.

Modern clients may show **global** hashtag results. Choose the **current
chat** tab. Public channels can use `#tag@username`; private channels cannot.

## Limits

- **One channel** per initialized root.
- **File-level** move and rename only. Directory move/delete are unsupported.
- **Empty directories** exist only in the local cache and disappear after a
  full scan.
- Incremental `td scan` does not see old messages edited or deleted directly
  in Telegram. Use `td scan --full`.
- Telegram **free** accounts: 2 GB per file. **Premium**: 4 GB.
- Caption edits on older messages can fail (`ERR_MESSAGE_NOT_EDITABLE`).
  `td doctor` reports whether edits currently work for your channel.
- Telegram rate-limits RPCs. `--wait` sleeps through safe flood waits;
  `--no-wait` fails immediately with `ERR_TELEGRAM_RATE_LIMITED`.
- Resumable uploads apply to files larger than 10 MB: a failed or interrupted
  upload can be retried as the same command, and only unconfirmed parts are
  re-sent (`--no-hash` is ignored on this path; content identity is verified
  before resuming). If a crash happens in the small window after Telegram
  accepted the media but before the index was written, the retry points you
  at `td repair --orphaned` instead of silently duplicating the message.

Later work is tracked in [GitHub Issues](https://github.com/thedavidweng/tg-drive-cli/issues).

## Privacy

- Files live in Telegram cloud storage. Every channel member can view and
  download them.
- Captions and hashtags expose folder names. Do not put sensitive paths in a
  shared channel.
- Default delete mode removes the Telegram message. `tombstone` redacts the
  caption instead.
- Secrets (`api_hash`, phone, session, invite links, local paths, hashes) are
  redacted unless you pass `--show-secrets`.

See [SECURITY.md](SECURITY.md).

## Troubleshooting

```sh
td doctor              # config, session, auth, channel, permissions, limits
td status              # roots, last scan, pending and orphaned counts
td scan --full         # rebuild after DB loss or old-message drift
td repair --pending    # stale pending uploads / expired locks
td repair --orphaned   # uploads that only partially landed on Telegram
```

- `ERR_MESSAGE_NOT_EDITABLE`: the message is too old to edit. Check `td doctor`.
- Tombstone could not redact a manifest reply: rerun with `--allow-stale-manifest`.
- Rate limited: retry with `--wait`, or raise `rate_limit.max_wait_seconds`.

## Documentation

Guides ([Diátaxis](https://diataxis.fr) taxonomy — all under
[`docs/guides/`](docs/guides/)):

Learning

- [Getting started](docs/guides/getting-started.md) — first login, upload,
  browse, download, share

Task-oriented how-tos

- [Recover the index](docs/guides/recover-the-index.md) — rebuild after
  database loss, repair pending/orphaned uploads
- [Import an existing channel](docs/guides/import-an-existing-channel.md) —
  adopt messages without re-uploading
- [Organize files](docs/guides/organize-files.md) — move, rename, delete,
  tombstone safely
- [Share folders](docs/guides/share-and-navigate.md) — invite links and
  hashtag navigation
- [Script with JSON](docs/guides/script-with-json.md) — envelopes, events,
  exit codes
- [Troubleshoot](docs/guides/troubleshoot.md) — doctor, rate limits, common
  errors

Understanding & reference

- [How td works](docs/guides/how-td-works.md) — the model behind the CLI
- [CLI reference](docs/guides/cli-reference.md) — every command and flag

Developer documentation

- [Architecture](docs/architecture.md)
- [CLI contract](docs/contracts/cli-contract.md)
- [JSON contract](docs/contracts/json-contract.md)
- [Storage contract](docs/contracts/storage-contract.md)
- [Config contract](docs/contracts/config-contract.md)
- [Decisions](DECISIONS.md)
- [Testing](docs/testing.md)
- [Release and CI](docs/release-and-ci.md)
- [Contributing](CONTRIBUTING.md)
- [Security](SECURITY.md)
- [Changelog](CHANGELOG.md)

## License

[Apache-2.0](LICENSE)
