# Getting started

Learn `td` by sending your first file to a Telegram channel and reading it
back. You will set up credentials, bind a local directory to a channel,
upload, browse, download, and share.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own files are prefixed
*Illustrative* — their shape matches the frozen contracts in
[`docs/contracts/`](../contracts/cli-contract.md).

---

## 1. Get Telegram API credentials

**Scenario:** `td` logs in as your Telegram user over MTProto. It needs an
`api_id` / `api_hash` pair from my.telegram.org.

1. Open [my.telegram.org](https://my.telegram.org) → **API development tools**.
2. Create an application and copy `api_id` and `api_hash`.
3. Have your phone number ready in international format (for example
   `+1234567890`).

Treat `api_hash` and the session file as account credentials.

## 2. Store the credentials

**Scenario:** save the credentials once so every later command reuses them.

```sh
td auth setup
```

*Illustrative — you type the values at the prompts:*

```text
Create a Telegram app at https://my.telegram.org/apps
api_id: 1234567
api_hash: 0123456789abcdef0123456789abcdef
saved Telegram API credentials to /Users/<you>/.config/tg-drive-cli/config.toml
next: td auth login
```

Prompts are read from stderr; results go to stdout. Credentials can also come
from `TD_API_ID` / `TD_API_HASH`.

**Next step:** log in.

## 3. Log in

**Scenario:** prove to Telegram that this device is your user account. You
only do this once; the session is reused afterwards.

```sh
td auth login
```

*Illustrative — the code arrives in your Telegram app:*

```text
phone (international, e.g. +1234567890): +1234567890
Telegram sent a login code to your phone.
code: 54321
logged in as <display name>; session saved to /Users/<you>/.config/tg-drive-cli/session.json
other td commands now reuse this session; next: td init <local-root> --create-channel
```

If two-factor authentication is enabled you will also be asked for your 2FA
password. Check that the session works:

```sh
td auth status
```

Captured from a real run (display name masked; the phone mask is `td`'s own
redaction):

```text
logged in as <display name> (19*******92)
```

**Next step:** create a drive root.

## 4. Initialize a root

**Scenario:** bind one local directory to one Telegram channel. `td init`
creates the channel (or binds an existing one) and runs an initial scan.

```sh
td init ~/Pictures --create-channel
```

*Illustrative:*

```text
initialized /Users/<you>/Pictures -> channel "Pictures [TD]" (id 1234567890)
next: td cp <local-file> /<remote-path>
```

To adopt a channel that already contains media, use
`--bind-channel` instead and see
[Import an existing channel](import-an-existing-channel.md).

**Next step:** upload a file.

## 5. Upload a file

**Scenario:** copy a local file into the virtual tree. The remote parent path
is created implicitly.

```sh
td cp ~/Pictures/beach.jpg /2024/beach.jpg
```

*Illustrative:*

```text
uploaded /2024/beach.jpg (2.4 MB)
```

The upload sends the media plus machine-readable metadata (`td:v1`) so the
file survives index loss. Files larger than 10 MB upload resumably — rerun
the same command after an interruption and only unconfirmed parts are resent.

A second file at the root gives the tree something to compare against:

```sh
td cp ~/notes.txt /notes.txt
```

*Illustrative:*

```text
uploaded /notes.txt (1.2 KB)
```

Upload a whole directory with `--recursive`:

```sh
td cp --recursive ~/Pictures /Pictures
```

*Illustrative:*

```text
uploaded 42 files (1 skipped, 0 failed)
```

**Next step:** look around.

## 6. Browse the tree

**Scenario:** list directories and show the tree. These read the local cache,
so they are instant and work offline.

```sh
td ls /
```

*Illustrative:*

```text
DIR  2024/
FILE notes.txt     1.2 KB
```

```sh
td ls /2024
```

*Illustrative:*

```text
FILE beach.jpg    2.4 MB
```

```sh
td tree / --depth 2
```

*Illustrative:*

```text
/
├── 2024/
│   └── beach.jpg
└── notes.txt
```

On a freshly initialized root both commands print nothing — an empty tree is
the honest empty state. Check the index health at any time:

```sh
td status
```

Captured from a real run (identifiers masked):

```text
authenticated: true
channel_id: <channel-id>
db_path: /Users/<you>/.local/share/tg-drive-cli/local_cache.db
files: none
last_full_scan_at: 2026-08-14T22:17:54Z
last_scan_at: 2026-08-14T22:17:54Z
last_scanned_message_id: 16
orphaned: 0
scan_errors_pending: 0
stale_locks: 0
stale_pending: 0
upload_limit_bytes: 2147483648
upload_states: 0
user_id: <user-id>
```

**Next step:** download a file back.

## 7. Download a file

**Scenario:** fetch a remote file back to disk.

```sh
td get /2024/beach.jpg ./beach-restore.jpg
```

*Illustrative:*

```text
downloaded /2024/beach.jpg -> ./beach-restore.jpg (2.4 MB)
```

Directories download recursively:

```sh
td get --recursive /2024 ./restore
```

*Illustrative:*

```text
downloaded /2024 -> ./restore (1 files, 0 skipped, 0 failed)
```

(`1 files` is literal — the summary does not pluralize.)

**Next step:** let someone else in.

## 8. Share the drive

**Scenario:** print an invite link and the hashtag that filters this folder
in native Telegram clients.

```sh
td share /2024
```

*Illustrative:*

```text
Channel: Pictures [TD]
Invite: https://t.me/+<invite-hash>
Filter: #td_Pictures_<hash>_2024_<hash>

Open the channel, then search or tap the filter tag. In clients that show global hashtag results, choose the current channel/chat tab.
```

Anyone with the link joins the channel as a viewer and can browse or download
every file in it.

## Where to go from here

- [Recover the index](recover-the-index.md) — rebuild after database loss
- [Organize files](organize-files.md) — move, rename, delete safely
- [Share folders](share-and-navigate.md) — invite links and hashtags in depth
- [Script with JSON](script-with-json.md) — stable envelopes and exit codes
- [Troubleshoot](troubleshoot.md) — doctor, rate limits, common errors
- [CLI reference](cli-reference.md) — every command and flag
