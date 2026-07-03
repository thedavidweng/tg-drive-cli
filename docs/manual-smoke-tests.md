# Manual smoke tests

Use a private Telegram test channel. Do not use personal production channels.

## Required environment

```sh
export TD_API_ID=...
export TD_API_HASH=...
export TD_PHONE=...
```

## Test sequence

```sh
td auth login
td auth status --json
td init ./testdata/local --create-channel
td doctor --json
td cp ./testdata/local/a.txt /a.txt --json
td ls / --json
td tree / --json
td get /a.txt ./restore/a.txt --json
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
