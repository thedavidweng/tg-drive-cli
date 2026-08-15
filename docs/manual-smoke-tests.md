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

`td auth setup` also creates the session and database directories.

## Prerequisites

- Build the binary: `make build`.
- The upload fixture `testdata/local/a.txt` is in the repo. Downloads land in
  `testdata/restore/`, which `td get` creates on demand (gitignored).

## Login

- You only need to log in once. The session persists to
  `~/.config/tg-drive-cli/session.json`. `td auth status --json` reports
  whether a login is necessary.
- Re-running `td auth login` while a code is pending reuses that code. Pass
  `--resend` only when you need a fresh one.
- Do not spam code requests. Telegram flood-waits accounts after a few
  `SendCode` calls (observed blocks last about 24 hours). The CLI prints the
  wait duration and resume time; retrying earlier does not help.
- A mistyped code re-prompts without sending a new one. An expired code
  triggers one automatic resend.
- If the code was accepted but the 2FA password failed, re-running
  `td auth login` resumes at the password prompt.

## Sequence

`td init <local-root>` binds the **local root directory** for the channel
index (`root_local_path`). It is not an upload source. Uploads are always
explicit `td cp <local> <remote>` calls.

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
td mv --confirm /a.txt /renamed.txt --json
td rm --confirm /renamed.txt --json
td scan --full --json
td share / --json
```

## Old edit capability

Keep a test message for at least several days, then run:

```sh
td doctor --json
```

Doctor must report whether old media caption edit is supported, unsupported,
or unknown for the current account and channel.
