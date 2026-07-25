# Manual smoke tests

Use a private Telegram test channel. Do not use personal production channels.

## Required environment

Create a Telegram app at https://my.telegram.org/apps, then either:

```sh
td auth setup
```

or:

```sh
export TD_API_ID=...
export TD_API_HASH=...
export TD_PHONE=...
```

`td auth setup` also pre-creates the session and database directories, so run
it (or export the variables) before anything else.

## Prerequisites

- Build the binary: `make build`.
- The upload fixture `testdata/local/a.txt` is committed to the repo; no setup
  is needed. Downloads land in `testdata/restore/`, which `td get` creates on
  demand (the directory is gitignored).

## Login: read this before running `td auth login`

- **You only need to log in once.** The session persists to
  `~/.config/tg-drive-cli/session.json` (`storage.session_path`); every later
  command reuses it silently. `td auth status --json` tells you whether a
  login is even necessary.
- **Re-running `td auth login` while a code is pending is safe**: the pending
  code is reused instead of requesting a new one (state is kept in
  `login_state.json` next to the session file for 10 minutes). Pass
  `--resend` only when you genuinely need a fresh code.
- **Do not spam code requests.** Telegram flood-waits accounts after a few
  `SendCode` calls — observed block: ~24 hours (`FLOOD_WAIT 85286`). If you
  hit it, the CLI prints the wait duration and resume time; retrying earlier
  does not help and may extend the block.
- A mistyped code re-prompts up to 3 times without sending a new code. An
  expired code triggers exactly one automatic resend.
- If your code was accepted but the 2FA password failed, re-running
  `td auth login` resumes at the password prompt — it does not ask for a
  code again.

## Test sequence

Note on `td init <local-root>`: the argument is the **local root directory**
that the channel index is bound to (recorded as `root_local_path`), *not* an
upload source. Uploads are always explicit `td cp <local> <remote>` calls; the
sequence below happens to upload a file that lives inside the root, but any
local path works.

```sh
td auth setup
td auth login
td auth status --json
td init ./testdata/local --create-channel
td doctor --json
td cp ./testdata/local/a.txt /a.txt --json
td ls / --json
td tree / --json
td get /a.txt ./testdata/restore/a.txt --json
td mv /a.txt /renamed.txt --json
td rm /renamed.txt --json
td scan --full --json
td share / --json
```

## Old edit capability

Keep a test message for at least several days. Run:

```sh
td doctor --json
```

The doctor must report whether old media caption edit is supported, unsupported, or unknown for the current account/channel.

## Findings from the 2026-07-25 real-account run

The login step flood-waited the account after repeated `SendCode` calls, so
steps after `td auth login` were not reached and still need a real-account
pass. All seven issues found in that run are resolved:

| # | Finding | Resolution |
|---|---------|------------|
| 1 | `testdata/local/a.txt` missing from repo | Fixture committed; prerequisites documented above |
| 2 | `td auth login` failed with `ERR_DB` (DB parent dir not created) | `sqlitestore.Open` now creates the parent directory |
| 3 | `td init <local-root>` semantics unclear | Clarified above the test sequence |
| 4 | Every `td auth login` sent a new code, invalidating the previous one; repeated attempts flood-waited the account | Pending code hash persists to `login_state.json` and is reused across invocations; `--resend` requests a fresh code explicitly; mistyped codes re-prompt without resending |
| 5 | `FloodWaitError` surfaced as a raw error without duration | Rate-limit errors now include the wait duration and resume time (`details.retry_after_seconds`, `details.retry_at`), plus guidance on stderr |
| 6 | Session persistence undocumented | Documented in the login section above |
| 7 | `td auth setup` did not create the DB directory | Setup now pre-creates session + DB directories and verifies the DB opens |
