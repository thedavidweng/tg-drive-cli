# CLI reference

Every command and flag of `td`. All blocks are captured verbatim from
`td --help` / `td <command> --help` runs.

Live help always wins over this page: `td --help`, `td <command> --help`.
Frozen interface guarantees live in
[`docs/contracts/cli-contract.md`](../contracts/cli-contract.md).

---

## Program help

Captured from a real run:

```text
Telegram-backed virtual file tree CLI.

Environment:
  TD_API_ID, TD_API_HASH  Telegram API credentials (https://my.telegram.org/apps)
  TD_PHONE                account phone number (international format)
  TD_CONFIG               config file path        (default ~/.config/tg-drive-cli/config.toml)
  TD_SESSION              session file path       (default ~/.config/tg-drive-cli/session.json)
  TD_DB                   local cache DB path     (default ~/.local/share/tg-drive-cli/local_cache.db)
  TD_CHANNEL              channel title or ID (same as --channel)
  TD_JSON                 set to 1 for JSON output (same as --json)
  TD_WAIT                 set to 1 to wait through safe flood waits (same as --wait)

Usage:
  td [command]

Core
  completion  Generate shell completion scripts
  config      Manage configuration
  doctor      Check local and Telegram capabilities
  status      Show index status
  version     Print version information

Authentication
  auth        Authentication commands

Channels
  channels    List Telegram channels

Files
  adopt       Adopt existing Telegram messages into the virtual file tree
  cp          Upload local file or directory
  get         Download remote file or directory
  import      Import content from an external Telegram chat into the drive
  init        Initialize a local root
  ls          List remote directory
  mv          Move or rename remote file
  rm          Delete remote file
  tree        Show remote tree

Maintenance
  repair      Repair index inconsistencies
  scan        Scan Telegram channel and rebuild index
  share       Share invite link and hashtag

Additional Commands:
  help        Help about any command

Flags:
      --channel string   channel title or ID
      --config string    config file path
      --db string        SQLite database path
  -h, --help             help for td
      --json             write JSON output
      --no-wait          fail immediately on Telegram flood waits
      --quiet            suppress non-essential output
      --session string   Telegram session path
      --verbose          enable verbose diagnostics
  -v, --version          version for td
      --wait             wait through safe Telegram flood waits

Use "td [command] --help" for more information about a command.
```

## Global flags

Available on every command (captured from real `--help` output):

| Flag | Purpose |
| --- | --- |
| `--channel string` | channel title or ID |
| `--config string` | config file path |
| `--db string` | SQLite database path |
| `--json` | write JSON output |
| `--no-wait` | fail immediately on Telegram flood waits |
| `--quiet` | suppress non-essential output |
| `--session string` | Telegram session path |
| `--verbose` | enable verbose diagnostics |
| `--wait` | wait through safe Telegram flood waits |

Per-command sections below show only the flags unique to that command.

## Authentication

### td auth setup

Configure Telegram API credentials.

```text
Usage:
  td auth setup [flags]
```

### td auth login

Login to Telegram interactively.

```text
Telegram sends a login code to your phone. If you re-run this command
while a code is still pending, the pending code is reused instead of
requesting a new one (repeated code requests get the account
rate-limited for up to 24 hours). Use --resend to request a fresh code.
After a successful login the session is saved and other commands do not
require logging in again.

Usage:
  td auth login [flags]

Flags:
      --resend   request a fresh login code instead of reusing a pending one
```

### td auth status

Show auth status.

```text
Usage:
  td auth status [flags]
```

### td auth logout

Logout.

```text
Usage:
  td auth logout [flags]
```

## Channels

### td channels list

List channels.

```text
Usage:
  td channels list [flags]

Flags:
      --only-drive   show only [TD] channels
```

## Files

### td init

Initialize a local root.

```text
Usage:
  td init <local-root> [flags]

Flags:
      --bind-channel string[="?"]        bind an existing channel; bare flag lists and prompts
      --create-channel string[="auto"]   create a new channel; bare flag derives the title, or pass --create-channel=<title>
```

### td cp

Upload local file, directory, or multi-file album.

```text
Usage:
  td cp <local> [local...] <remote-path> [flags]

Flags:
      --as string                 present the upload as photo, video, or document (default document)
      --auto-rename               auto rename on conflict
      --confirm                   confirm destructive operations such as --replace
      --continue-on-error         continue on upload errors
      --dry-run                   preview the upload plan without executing
      --duration float            video duration in seconds (with --as video)
      --events                    emit NDJSON progress events during upload
      --height int                video height in pixels (with --as video)
      --include-empty-dirs        include empty directories (unsupported in V1)
      --no-hash                   skip content hash
      --recursive                 upload directory recursively
      --replace                   replace existing remote file (single-file form only)
      --skip-existing             skip existing remote file
      --streaming                 mark the video as streamable (with --as video)
      --thumb string              JPEG file to attach as the upload thumbnail
      --upload-part-size-kb int   upload part size in KB (0 uses config)
      --upload-threads int        parallel upload goroutines (0 uses config)
      --width int                 video width in pixels (with --as video)
```

`--replace` requires `--confirm`.

Two or more sources publish as native Telegram albums: one human caption on
the first member, one `td-album:v1` inventory comment on the first member's
discussion thread per group of up to 10 members (larger sets split into
consecutive groups). The destination must be `/`, end with `/`, or name an
existing remote directory; `--replace` is not available in this form — use
`--skip-existing` or `--auto-rename` instead. `--recursive` uploads each
source directory's direct children as one album and recurses into nested
directories.

`--as photo` sends a native photo message: Telegram recompresses the bytes,
so downloads fetch the largest representation and strict size/hash
verification does not apply. `--as video` keeps the bytes untouched and can
carry duration, dimensions, a streaming hint, and a thumbnail. In multi-file
form these flags apply uniformly to every member and are rejected with
`--recursive`. See `docs/integration-notes.md` for the full semantics of the
three content forms.

### td get

Download remote file or directory.

```text
Usage:
  td get <remote-path> <local-dest> [flags]

Flags:
      --auto-rename         auto rename on conflict
      --continue-on-error   continue on download errors
      --recursive           download directory recursively
      --replace             replace existing local file
      --skip-existing       skip existing local file
```

### td ls

List remote directory.

```text
Usage:
  td ls [remote-path] [flags]
```

### td tree

Show remote tree.

```text
Usage:
  td tree [remote-path] [flags]

Flags:
      --depth int   max depth
```

### td mv

Move or rename remote file.

```text
Usage:
  td mv <remote-from> <remote-to> [flags]

Flags:
      --confirm   confirm the move
      --dry-run   preview what would be moved
```

`--confirm` is required unless `--dry-run`.

### td rm

Delete remote file.

```text
Usage:
  td rm <remote-path> [flags]

Flags:
      --allow-stale-manifest   do not fail when the manifest reply cannot be redacted
      --confirm                confirm the deletion
      --dry-run                preview what would be deleted
      --tombstone              tombstone instead of deleting the Telegram message
```

`--confirm` is required unless `--dry-run`.

### td adopt

Adopt existing Telegram messages into the virtual file tree. The in-place
claim lives here since ADR 0019; `td import` handles external-chat ingest.

```text
Usage:
  td adopt [message-id] [remote-path] [flags]

Flags:
      --confirm             confirm adopting existing messages into the local index
      --continue-on-error   continue adopting after a per-message error
      --dry-run             print the adopt plan without editing Telegram
      --hash                download each adopted file to compute and store its BLAKE3 content hash
      --into string         remote directory prefix for --unmanaged (default "/")
      --rewrite-captions    restore one human caption per album, delete per-file replies, and write one td-album:v1 inventory
      --unmanaged           adopt every unmanaged media/text message in the channel
```

`--confirm` is required unless `--dry-run`.

### td import

Import content from an external Telegram chat into the drive by re-uploading
fresh bytes. The implemented source is `saved`, which imports Saved Messages.
Forwarded origin details are preserved in `td-origin:v1` comments. Hash
duplicates are skipped by default and their captions are preserved in
`td-dupe:v1` comments.

```text
Usage:
  td import <source> [message-id...] [flags]

Flags:
      --auto-rename         auto-rename items whose destination already exists
      --confirm             confirm republishing saved content into the drive channel
      --continue-on-error   continue importing after a per-item error
      --delete-source       delete the saved originals of published and duplicate items (requires --confirm)
      --dry-run             print the import plan without touching Telegram
      --events              emit NDJSON progress events during the import
      --into string         remote directory the imported content lands in (default "/saved")
      --merge-captions      append a skipped duplicate's caption to the matched file's caption
      --no-dedupe           import even when the content hash already exists in the tree
      --photos-as string    republish photo messages as document (keeps the bytes) or photo (native, recompressed)
      --replace             replace an existing file at the destination
      --skip-existing       skip items whose destination already exists
```

`--confirm` is required unless `--dry-run`. A photo import requires
`--photos-as` in JSON and non-interactive runs. `--delete-source` also
requires `--confirm`.

### td share

Share invite link and hashtag.

```text
Usage:
  td share [remote-path] [flags]
```

## Maintenance

### td scan

Scan Telegram channel and rebuild index.

```text
Usage:
  td scan [remote-root] [flags]

Flags:
      --full              full scan
      --include-deleted   record tombstoned files
      --repair            repair DB-only inconsistencies
      --strict            exit on invalid messages
```

### td repair

Repair index inconsistencies.

```text
Usage:
  td repair [path] [flags]
      --hash              download files missing a content hash and backfill it into the index and machine records
      --orphaned          repair orphaned messages
      --pending           repair pending uploads
      --scan-errors       repair scan errors
```

`--hash` streams each active file under `[path]` (default: the whole
channel) that has no stored BLAKE3 hash, computes the digest, and writes it
back into the SQLite index and the machine record on Telegram (comment
thread, legacy reply, or caption — whichever carrier the row uses). A zero
or missing file size is repaired from the same download. Rows that already
carry a hash are skipped, so reruns are no-ops.

## Core

### td status

Show index status.

```text
Usage:
  td status [flags]
```

### td doctor

Check local and Telegram capabilities.

```text
Usage:
  td doctor [flags]
```

Subcommand:

```text
Usage:
  td doctor path-codec [flags]
```

Run path codec self-test and verify stored slug mappings.

### td config

Manage configuration.

```text
Usage:
  td config get [key] [flags]

Flags:
      --confirm        confirm showing secrets in JSON mode
      --show-secrets   show secret values
```

```text
Usage:
  td config set <key> <value> [flags]
```

### td completion

Generate shell completion scripts.

```text
Usage:
  td completion [bash|zsh|fish|powershell] [flags]
```

### td version

Print version information.

```text
Usage:
  td version [flags]
```

## Exit codes

| Exit | Meaning |
| ---: | --- |
| 0 | success |
| 1 | general error |
| 2 | usage/input error (bad paths, not found) |
| 3 | auth/config error |
| 4 | Telegram/platform error |
| 5 | DB/index/repair error |
| 10 | confirmation/safety error |

The authoritative mapping, including every error code per exit code, lives in
[`docs/contracts/cli-contract.md`](../contracts/cli-contract.md).
