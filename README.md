# tg-drive-cli

`tg-drive-cli` (binary: `td`) turns a Telegram channel into a recoverable,
scriptable virtual file tree. It uploads local files as Telegram media messages,
stamps each one with machine-readable `td:v1` caption metadata and a native
`#td_` hashtag chain, and keeps a local SQLite cache so the CLI can give you an
exact folder tree (`ls`, `tree`), uploads/downloads, moves, and shares.

Because every file lives in Telegram cloud storage, you can browse the same
files from any native Telegram client, filter by hashtag, and — if your local
database is ever lost — rebuild the whole index from the channel with
`td scan --full`.

- Single Telegram channel per initialized root (V1)
- MTProto user-account login (not a bot)
- Files are the source of truth; SQLite is a rebuildable cache
- Path hierarchy is encoded in captions + hashtags, not in Telegram folders

---

## Requirements

### Telegram account and API credentials

`td` logs in as your own Telegram user over MTProto, so you need a Telegram
account and your own API application credentials:

1. Sign in at [my.telegram.org](https://my.telegram.org) and open **API
   development tools**.
2. Create an application to obtain an **`api_id`** (integer) and **`api_hash`**
   (string).
3. Have your phone number ready (international format, e.g. `+1234567890`); you
   will confirm a login code and, if enabled, your 2FA password.

Keep `api_hash` and your session file private — they grant access to your
account.

### Build/runtime

- A released binary (see install below), **or** Go 1.26+ to build from source.

---

## Install

### macOS / Linux (curl | sh)

```sh
curl -fsSL https://raw.githubusercontent.com/thedavidweng/tg-drive-cli/main/install.sh | sh
```

The script installs a released binary into `~/.local/bin` by default (override
with `TD_INSTALL_DIR`). If Homebrew is present, it installs the cask instead.
Uninstall with `install.sh uninstall`.

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/thedavidweng/tg-drive-cli/main/install.ps1 | iex
```

Installs `td.exe` into `%LOCALAPPDATA%\tg-drive-cli\bin` (override with
`$env:TD_INSTALL_DIR`) and adds it to your user `PATH`.

### Homebrew

A cask is published to the `thedavidweng/tap` tap by GoReleaser:

```sh
brew tap thedavidweng/tap
brew install --cask tg-drive-cli
```

### go install

```sh
go install github.com/thedavidweng/tg-drive-cli/cmd/td@latest
```

### Build from source

```sh
git clone https://github.com/thedavidweng/tg-drive-cli.git
cd tg-drive-cli
make build          # produces ./dist/td
# or:
go build -o td ./cmd/td
```

---

## Auth setup

```sh
td auth setup     # store api_id / api_hash (prompts, or reads TD_API_ID / TD_API_HASH)
td auth login     # phone code + optional 2FA, then persists the session
td auth status    # show the authenticated user (secrets redacted)
td auth logout    # drop the local session
```

`td auth login` reads `api_id`/`api_hash` from flags, `TD_*` env vars, or the
config file, prompts for anything missing, sends a login code to your phone,
handles a 2FA password when required, and persists the session. You only log
in once: later commands reuse the saved session.

Re-running `td auth login` while a code is still pending reuses that code
instead of requesting a new one (Telegram flood-waits accounts that request
codes repeatedly — up to 24 hours). Use `td auth login --resend` to explicitly
request a fresh code. A mistyped code re-prompts without sending a new one.

Default locations (override with `TD_CONFIG`, `TD_SESSION`, `TD_DB` or the
`--config`, `--session`, `--db` flags):

| Purpose | Path |
| --- | --- |
| Config | `~/.config/tg-drive-cli/config.toml` |
| Session | `~/.config/tg-drive-cli/session.json` |
| Local cache DB | `~/.local/share/tg-drive-cli/local_cache.db` |

---

## Quickstart

```sh
td auth login
td init ~/Pictures --create-channel
td cp ~/Pictures/beach.jpg /2024/beach.jpg
td ls /
td tree /
td share /2024
```

`td init` creates (or binds) one channel for the root and runs an initial scan.
`td cp` uploads the file and writes its `td:v1` metadata. `td ls`/`td tree` read
the local cache. `td share` prints an invite link and the subtree hashtag.

---

## Commands

### Global flags

Available on every command:

```text
--json              machine-readable JSON output
--quiet             suppress non-essential output
--verbose           verbose diagnostics
--config <path>     config file path            (TD_CONFIG)
--db <path>         SQLite database path         (TD_DB)
--session <path>    Telegram session path        (TD_SESSION)
--channel <name|id> target channel               (TD_CHANNEL)
--wait              sleep through safe Telegram flood waits   (TD_WAIT)
--no-wait           fail immediately on flood waits
```

`--wait`/`--no-wait` are mutually exclusive. Precedence for shared settings is:
CLI flags > `TD_*` env vars > config file > defaults.

### `td init <local-root>`

```sh
td init ~/Pictures                              # use configured/default channel
td init ~/Pictures --create-channel             # new channel titled after the dir
td init ~/Pictures --create-channel "Pictures [TD]"
td init ~/Pictures --bind-channel "Pictures [TD]"
td init ~/Pictures --channel "Pictures [TD]"
```

Creates config if missing, creates or binds one Telegram channel, records the
mapping, runs an initial scan, and prints the root + channel summary.

### `td cp <local> <remote-path>`

```sh
td cp ~/Pictures/beach.jpg /2024/beach.jpg
td cp --recursive ~/Pictures /Pictures
td cp --replace --confirm ~/new.jpg /2024/beach.jpg
td cp --skip-existing ~/beach.jpg /2024/beach.jpg
td cp --auto-rename ~/beach.jpg /2024/beach.jpg      # -> beach (1).jpg
td cp --no-hash ~/big.iso /iso/big.iso               # skip content hashing
td cp --recursive --continue-on-error ~/Pictures /Pictures
```

Flags: `--recursive`, `--replace`, `--skip-existing`, `--auto-rename`,
`--no-hash`, `--continue-on-error`, `--include-empty-dirs`. Only one of
`--replace`/`--skip-existing`/`--auto-rename` may be set. Default (no conflict
flag) fails if the remote path already exists.

### `td get <remote-path> <local-dest>`

```sh
td get /2024/beach.jpg ./beach.jpg
td get --recursive /Pictures ./restore
td get --replace /2024/beach.jpg ./beach.jpg
td get --skip-existing /2024/beach.jpg ./beach.jpg
td get --auto-rename /2024/beach.jpg ./beach.jpg
td get --recursive --continue-on-error /Pictures ./restore
```

Downloads to a temp file and verifies size/hash when available. Default fails
with `ERR_LOCAL_PATH_EXISTS` if the destination already exists.

### `td ls [remote-path]` and `td tree [remote-path]`

```sh
td ls /
td ls /Pictures/2024
td ls --json /Pictures/2024
td tree /
td tree /Pictures --depth 3
```

Both read the SQLite cache. `td tree --depth` limits depth (0 = unlimited).

### `td mv <remote-from> <remote-to>`

```sh
td mv --confirm /2024/beach.jpg /2024/beach-final.jpg      # rename
td mv --confirm /2024/beach.jpg /Archive                    # into existing dir -> /Archive/beach.jpg
```

File-level, same-channel moves only (V1). Editing an existing message's caption
is preferred; see the edit-capability caveat below. Directory moves return
`ERR_DIRECTORY_MOVE_UNSUPPORTED`; cross-channel moves return
`ERR_CROSS_CHANNEL_MOVE`.

### `td rm <remote-path>`

```sh
td rm --confirm /2024/beach.jpg
td rm --confirm --tombstone /2024/beach.jpg                 # redact caption instead of delete
td rm --confirm --tombstone --allow-stale-manifest /2024/old.jpg
```

Default applies the configured delete mode (`delete` or `tombstone`).
`--tombstone` forces tombstone mode for this call; `--allow-stale-manifest`
suppresses the failure when a manifest reply cannot be redacted.

### `td import [message-id] [remote-path]`

Adopt a message already on Telegram (no re-upload):

```sh
td import --unmanaged --dry-run --channel NSFW
td import --unmanaged --confirm --channel NSFW
td import 61 /videos/The\ Bet.mp4 --confirm --channel NSFW
```

Documents, photos, and text are placed under `/videos`, `/photos`, `/audio`,
`/files`, and `/notes` using the original filename when Telegram has one.
Media albums stay one timeline block: the human caption (hashtags +
description) lives on the first item. Reconstructable metadata for the
whole group is a single `td-album:v1` reply, not one reply per file.
To restore captions after a bad adopt and attach the album inventory:

```sh
td import --rewrite-captions --confirm --channel NSFW
```

### `td share [remote-path]`

```sh
td share            # whole channel
td share /2024      # subtree: invite link + recommended hashtag
```

Prints the channel invite link, the recommended `#td_` filter for the subtree,
a `#tag@username` scoped tag when the channel has a public username, and a note
to pick the current-channel tab in clients that show global hashtag results.

### `td scan [remote-root]`

```sh
td scan                    # incremental
td scan --full             # rebuild the whole index from Telegram
td scan --strict           # exit non-zero on invalid managed messages
td scan --repair           # fix DB-only inconsistencies
td scan --include-deleted  # also record tombstones
```

`--full` is the recovery path after DB loss. Incremental scan cannot detect
old messages that were edited or deleted directly in Telegram — run `--full` to
catch that drift.

### `td repair [path]`

```sh
td repair /2024/beach.jpg      # regenerate caption/manifest for a known path
td repair --pending            # inspect/clear stale pending uploads
td repair --orphaned           # fix orphaned uploads (partial Telegram success)
td repair --orphaned --delete-orphaned --confirm
td repair --scan-errors        # clear resolved scan errors
```

### `td status`, `td doctor`, `td version`

```sh
td status      # auth, roots, channels, DB path, last scan, pending/orphaned counts, upload limit
td doctor      # capability checks (see caveats below)
td version
```

### `td config`

```sh
td config get                       # dump config (secrets redacted)
td config get telegram.api_id
td config get telegram.api_hash --show-secrets
td config get telegram.api_hash --show-secrets --confirm --json
td config set delete.mode tombstone
```

`--show-secrets` requires interactive confirmation, or `--confirm` in `--json`
mode.

---

## Storage model

A remote file is a normal **Telegram media message** carrying:

- a `td:v1` caption with the base64url-encoded canonical path, display name,
  size, content hash, and MIME type, plus
- a shallow-to-deep `#td_` hashtag chain, and
- an optional `td-manifest:v1` **reply** message for metadata that does not fit
  in the caption.

The primary identity of a file is `(channel_id, message_id, canonical_path)`.
**Telegram messages are the recoverable source of truth.** SQLite
(`local_cache.db`) is only a local query cache and operation index — it is
rebuildable. If you delete or lose the database, run:

```sh
td scan --full
```

to re-read the channel history and reconstruct the index. Directory rows are
derived from active file paths, not stored in Telegram.

---

## Caption and manifest model

Telegram media captions have a documented ~1024-character budget. `td` counts
**UTF-16 code units** (not bytes, not runes: BMP characters are 1 unit,
supplementary-plane emoji are 2) and leaves a small safety margin.

A normal caption renders as:

```text
beach.jpg
Pictures/2024/06/15/Vacation/

td:v1 p=<base64url path> n=<base64url name> s=<size> h=<hash> m=<mime>

#td_Pictures_n4j7x2ab #td_Pictures_n4j7x2ab_2024_l9p2q8dd
```

When the full caption would exceed the budget (typically **deep paths**), `td`
uploads the media with a minimal caption (`td:v1 manifest=reply`) plus the
shallow hashtags that still fit, and sends the full metadata as a
`td-manifest:v1` reply, recording its `manifest_message_id`. Machine recovery
never depends on hashtags alone. If even the minimal caption cannot fit (e.g. an
extremely long filename), `td` returns `ERR_CAPTION_TOO_LONG`.

---

## Native Telegram hashtag navigation

Each directory level gets a cumulative hashtag, so tapping a tag in any Telegram
client filters that folder. For `/Pictures/2024/06/15/Vacation/beach.jpg`:

```text
#td_Pictures_<8-char-hash>
#td_Pictures_<8-char-hash>_2024_<8-char-hash>
#td_Pictures_<8-char-hash>_2024_<8-char-hash>_Vacation_<8-char-hash>
```

Each segment is `readable_prefix` plus an 8-character base32 BLAKE3 suffix (13, then 26, on collision). The chain reads shallow → deep.

**Caveat — global vs current-chat results:** modern Telegram clients may show
**global** hashtag results when you tap a tag. Choose the **current
chat/channel** tab to see only this channel's files. For public channels with a
username you can use the scoped form `#tag@username`; private channels cannot use
`@username` scoping, so rely on the current-channel tab.

**Non-Latin segments:** Chinese path segments are transliterated to **pinyin**
slugs for the hashtag (e.g. `文件` → `wenjian_<hash>`); other scripts are
transliterated to ASCII where possible, with a hash suffix guaranteeing
uniqueness. The original segment is preserved in metadata.

---

## File size limits

- Telegram **free** accounts: up to **2 GB** per file.
- Telegram **Premium** accounts: up to **4 GB** per file.

`td cp` checks the current account's limit before uploading and returns
`ERR_FILE_TOO_LARGE` if the file is too big. `td doctor` reports the detected
upload limit.

---

## Caveats and limitations

### Old-message caption edits

`td mv` (and tombstone deletes) work by editing an existing message's caption.
Telegram may refuse to edit older messages depending on account, chat type, and
permissions, in which case the operation returns `ERR_MESSAGE_NOT_EDITABLE`
(or `ERR_MESSAGE_EDIT_EXPIRED`). `td doctor` reports whether caption edits are
currently possible for your channel.

### Empty directories

Directories are derived from file paths, so **empty directories are local-only**
and disappear after a full scan (Telegram stores files, not folders). Recursive
upload skips empty directories; `td cp --recursive --include-empty-dirs` returns
`ERR_EMPTY_DIRS_UNSUPPORTED` in V1.

### Flood waits

Telegram rate-limits RPCs. Use `--wait` to sleep through safe flood waits (up to
the configured maximum) or `--no-wait` to fail fast with
`ERR_TELEGRAM_RATE_LIMITED`.

---

## Privacy

- Uploaded files live in **Telegram cloud storage**; every **channel member can
  view and download** them.
- **Captions and hashtags expose your path/folder names.** Do not upload files
  whose paths are sensitive to a channel you share.
- Deleting with the default `delete` mode removes the Telegram message; the
  `tombstone` mode instead redacts the caption/manifest and keeps a marker. If a
  tombstone cannot redact the manifest reply, `td` warns and exits non-zero
  unless you pass `--allow-stale-manifest`.
- Secrets (`api_hash`, phone, session, invite links, local paths, hashes) are
  redacted in `td status`, `td config get`, logs, and JSON errors unless you
  explicitly pass `--show-secrets`.

---

## Troubleshooting

```sh
td doctor                         # check config, session, DB, auth, channel, upload/delete perms,
                                  # upload size limit, invite-link ability, edit capability,
                                  # UTF-16 caption counter, path codec, scan smoke test
td status                         # roots, channels, last scan, pending/orphaned counts
td scan --full                    # rebuild the index after DB loss or old-message drift
td repair --pending               # clear stale pending uploads / expired operation locks
td repair --orphaned              # fix uploads that partially succeeded on Telegram
td repair --scan-errors           # clear resolved scan errors
```

- Move/delete fails with `ERR_MESSAGE_NOT_EDITABLE`: the message is too old to
  edit; check `td doctor`, or retry the delete with a mode that fits.
- Tombstone manifest could not be redacted: rerun with `--allow-stale-manifest`.
- Rate limited: retry with `--wait`, or raise `rate_limit.max_wait_seconds`.

---

## V1 limitations

- **Single channel** per root.
- **File-level move/rename only** — no directory move or directory delete
  (`ERR_DIRECTORY_MOVE_UNSUPPORTED`).
- **No watch mode** (no automatic upload on local changes).
- Empty directories are not recoverable after DB loss.
- Incremental scan does not detect old messages edited/deleted directly in
  Telegram; use `td scan --full`.
- Destructive remote ops (`rm`, `mv`, `cp --replace`, `repair --delete-orphaned`)
  require `--confirm`.

---

## Development

```sh
make ci-local                 # fmt-check, vet, tests, race tests
make build                    # ./dist/td
./scripts/generate-completions.sh   # write shell completions into completions/
TD_FAKE_TELEGRAM=1 go test ./...    # in-memory fake Telegram client (no network)
```

To try the full CLI workflow offline — no Telegram account, no network — point
the fake client at a state file so it persists across commands (login code is
`12345`):

```sh
export TD_FAKE_TELEGRAM=1 TD_FAKE_TELEGRAM_STATE=/tmp/td-demo/fake.json
export TD_API_ID=1 TD_API_HASH=hash TD_PHONE=+1000
td auth login   # enter code 12345
td init ./files --create-channel
td cp ./files/a.txt /a.txt && td ls /
```

See `PRODUCT_SPEC.md` for the full product/storage contract,
`docs/release-and-ci.md` for the release pipeline, and `docs/` for CLI, JSON,
and storage contracts plus manual smoke tests.
