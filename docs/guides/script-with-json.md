# Script with JSON

How to drive `td` from scripts: stable JSON envelopes, NDJSON progress
events, exit codes, and secret redaction.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own data are prefixed *Illustrative*.

---

## Read the envelope, not the prose

**Scenario:** your script needs to branch on results. Pass `--json` (or set
`TD_JSON=1`) and every command emits one envelope on stdout; logs and prompts
stay on stderr.

Success:

```sh
td --json version
```

Captured from a real run:

```json
{"ok":true,"data":{"built_by":"source","commit":"none","date":"unknown","version":"dev"},"meta":{"command":"td version","duration_ms":0,"schema_version":"2026-07-29","request_id":"39873102-920f-4485-951d-517fe6bf495e"}}
```

Failure — same envelope shape, `ok: false`, machine-readable `code`:

```sh
td --json get /nope.jpg ./x
```

Captured from a real run:

```json
{"ok":false,"error":{"code":"ERR_REMOTE_NOT_FOUND","message":"remote path \"/nope.jpg\" not found","category":"validation","retryable":false,"details":{}},"meta":{"command":"td get","duration_ms":14,"schema_version":"2026-07-29","request_id":"14fa7429-e030-4487-9b11-e7e49033dd82"}}
```

The full data shapes per command are frozen in
[`docs/contracts/json-contract.md`](../contracts/json-contract.md).

**Next step:** add exit-code handling.

## Branch on exit codes

**Scenario:** you would rather not parse JSON for control flow. Exit codes
are stable and mapped from error categories.

```sh
td get /2024/beach.jpg ./beach.jpg
case $? in
  0) echo ok ;;
  2) echo "not found or bad path" ;;
  3) echo "auth/config problem" ;;
  4) echo "telegram/platform problem" ;;
  5) echo "index/repair problem" ;;
  10) echo "confirmation required" ;;
esac
```

Captured from real runs:

```text
$ td rm /nope.jpg; echo "exit=$?"
error: deleting a remote file requires --confirm
exit=10
```

```text
$ td ls /nope; echo "exit=$?"
error: remote path "/nope" not found
exit=2
```

The full mapping lives in
[`docs/contracts/cli-contract.md`](../contracts/cli-contract.md).

## Stream progress for big uploads

**Scenario:** an upload runs for minutes and your wrapper wants progress.
`td cp --events` emits one JSON envelope per line: `cp.progress` events while
parts confirm, then the final result.

```sh
td cp --events ~/big.bin /big.bin
```

*Illustrative:*

```json
{"ok":true,"data":{"file_name":"big.bin","part":5,"part_size":524288,"uploaded":2621440,"total":4294967296},"meta":{"command":"cp.progress","duration_ms":120,"schema_version":"2026-07-29","request_id":"..."}}
{"ok":true,"data":{"path":"/big.bin","message_id":1234,"size":4294967296},"meta":{"command":"cp","duration_ms":4200,"schema_version":"2026-07-29","request_id":"..."}}
```

## Handle rate limits programmatically

**Scenario:** Telegram flood-waits your account. By default the command fails
immediately and the error carries retry hints.

Captured from a real run:

```text
$ td channels list
error: flood wait: retry after 11s
```

In JSON mode the envelope carries machine-readable
hints (`details.retry_after_seconds`, `details.retry_at`) — see
[the JSON contract](../contracts/json-contract.md). Pass `--wait` when you
would rather sleep through safe waits (bounded by
`rate_limit.max_wait_seconds`); keep the fail-fast default in schedulers.

## Keep secrets out of logs

**Scenario:** your script captures output for debugging. Redaction is on by
default.

```sh
td config get telegram.api_hash
td config get telegram.phone
```

Captured from real runs:

```text
redacted
+1********92
```

`--show-secrets` prints real values but gates interactively on stderr in text
mode; in JSON mode it additionally requires `--confirm`.

## Next steps

- [JSON contract](../contracts/json-contract.md) — every data shape
- [Troubleshoot](troubleshoot.md) — diagnosing failures in automation
