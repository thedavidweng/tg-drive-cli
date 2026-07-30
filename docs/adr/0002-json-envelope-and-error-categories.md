# 0002 - JSON envelope with metadata and error categories

## Status

Accepted.

## Context

`td --json` is the primary interface for scripts and AI agents. The original envelope only had `ok` and `data` (or `error.code`, `error.message`, `error.details`). Without command name, duration, schema version, or a stable request identifier, agents cannot correlate logs, measure performance, or detect stale output. Errors also lacked machine-readable classification and retry guidance.

## Decision

- Every JSON success and error response is wrapped in the same envelope:
  ```json
  {
    "ok": true,
    "data": {},
    "meta": {
      "command": "cp",
      "duration_ms": 125,
      "schema_version": "2026-07-29",
      "request_id": "...",
      "warnings": []
    }
  }
  ```
- `meta.command` is `cobra.CommandPath()` (e.g. `td cp`).
- `meta.request_id` is a UUID generated once per invocation in `PersistentPreRun`.
- `meta.schema_version` is a constant in `internal/output` and matches `docs/contracts/json-contract.md`.
- `meta.duration_ms` is the wall-clock time from `PersistentPreRun` to the response.
- Errors gain `category` (`auth`, `config`, `validation`, `api`, `platform`, `internal`, `safety`) and `retryable` plus optional `retry_after_ms`. `ERR_TELEGRAM_RATE_LIMITED` populates `retry_after_ms` from the underlying flood-wait duration.

## Consequences

- Existing `--json` callers will see extra `meta` fields; this is additive and does not remove prior fields.
- Error `details` remains command-specific, while `category` and `retryable` let agents decide whether to retry, escalate, or ask the user without parsing the message text.
- `schema_version` gives us a migration handle if the envelope shape changes again.
