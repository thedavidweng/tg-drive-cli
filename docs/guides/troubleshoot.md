# Troubleshoot

How to find out what is wrong: capability checks with `td doctor`, index
state with `td status`, rate limits, and the errors you are most likely to
meet.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own data are prefixed *Illustrative*.

---

## Start with doctor

**Scenario:** something fails and you do not know which layer — config,
session, channel, or Telegram capabilities. `td doctor` checks them all
without changing anything.

```sh
td doctor
```

Captured from a real run (free-tier account):

```text
auth               pass
caption_counter    pass
channel            pass
config             pass
db                 pass
delete             pass
edit_old_caption   pass
file_size_limit    warn — free-tier upload limit (2147483648 bytes); Telegram Premium raises it
invite_link        pass
path_codec         pass
session_file       pass
upload             pass
max_upload         2.0 GB
```

Read it as: every row must `pass` for that capability to be assumed. A `warn`
is informational; a `fail` is your problem. Notably, `edit_old_caption` can
flip to fail at any time — Telegram refuses edits on old messages
(`ERR_MESSAGE_NOT_EDITABLE`), so never build logic that assumes edits work.

For path/slug internals:

```sh
td doctor path-codec
```

Captured from a real run:

```text
fixed-vectors        pass
db-check             pass
db-rows              2
corrupt-rows         0
```

**Next step:** if doctor passes but data looks wrong, inspect the index.

## Inspect the index

**Scenario:** files missing, counts look off, or an operation left state
behind.

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

What to look for:

| Field | Suspicious when | Fix |
| --- | --- | --- |
| `stale_pending` | > 0 after a crashed upload | `td repair --pending` |
| `orphaned` | > 0 | `td repair --orphaned` (see [Recover the index](recover-the-index.md)) |
| `stale_locks` | > 0 after a killed process | `td repair --pending` clears locks |
| `scan_errors_pending` | > 0 | `td repair --scan-errors` |
| `files` | empty but the channel has media | [import](import-an-existing-channel.md) or `td scan --full` |

## Rate limits (flood waits)

**Scenario:** Telegram answers with a flood wait. By default `td` fails
immediately with the retry time; `--wait` sleeps through safe waits instead
(up to `rate_limit.max_wait_seconds`, 300 s by default).

Captured from real runs:

```text
$ td channels list --only-drive
error: flood wait: retry after 11s
```

```text
$ td channels list --only-drive
error: flood wait: retry after 5s
```

Choices:

- rerun with `--wait` and let safe waits sleep off;
- keep the fail-fast default in schedulers and retry later — JSON mode reports
  `details.retry_after_seconds`;
- make waiting the default with `rate_limit.default_wait = true` in the
  config, or raise `rate_limit.max_wait_seconds` if long waits get cut off.

Repeated login-code requests also flood-wait; `td auth login` reuses a pending
code and only asks for a fresh one with `--resend`.

## Common errors

| Error | Meaning | What to do |
| --- | --- | --- |
| `ERR_CONFIRMATION_REQUIRED` | destructive op without `--confirm` | add `--confirm` (exit code 10) |
| `ERR_REMOTE_NOT_FOUND` | path not in the index | check `td tree /`; rescan if the index is stale |
| `ERR_MESSAGE_NOT_EDITABLE` | Telegram refused an edit on an old message | expected for old messages; `rm --tombstone --allow-stale-manifest` where applicable |
| `ERR_TELEGRAM_RATE_LIMITED` | flood wait exceeded the wait budget | retry with `--wait` or later |
| `ERR_FILE_TOO_LARGE` | over the account limit | free: 2 GB, premium: 4 GB (`td doctor`) |
| `ERR_CAPTION_TOO_LONG` | deep path exceeded the caption budget | shorten the path; metadata goes to a manifest reply automatically near the limit |
| `ERR_OPERATION_LOCKED` | another process holds the path lock | wait, or clear stale locks via `td repair --pending` |
| `ERR_ORPHANED_UPLOAD` | crash between media accept and index write | `td repair --orphaned` |
| `ERR_AUTH_REQUIRED` | no session | `td auth setup`, then `td auth login` |

Exit codes map error categories: 1 general, 2 usage/path, 3 auth/config,
4 telegram/platform, 5 db/repair, 10 confirmation. The authoritative table is
in [`docs/contracts/cli-contract.md`](../contracts/cli-contract.md).

## Escalation path

1. `td doctor` — capabilities
2. `td status` — index state
3. targeted repair — [Recover the index](recover-the-index.md)
4. still wrong? `td scan --full` rebuilds everything from Telegram

## Next steps

- [How td works](how-td-works.md) — why failures look the way they do
- [Script with JSON](script-with-json.md) — handle errors programmatically
