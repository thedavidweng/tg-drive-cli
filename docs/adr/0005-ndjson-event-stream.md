# 0005 - NDJSON event stream for long operations

## Status

Accepted.

## Context

Big-file uploads through `td cp` can take minutes or hours. Polling is impossible for a CLI, and writing human-readable progress to stderr is not machine-parsable. Agents and UIs need a stream of discrete progress events and a final completion event that can be consumed line by line.

## Decision

- `td cp --events` emits a stream of newline-delimited JSON (NDJSON) objects to stdout.
- Each line is the same JSON envelope as normal output (`ok`, `data`, `meta`), with `meta.command` set to the event name.
- Progress event command: `cp.progress`. Data follows `telegram.UploadProgressState`:
  ```json
  {"file_name":"...","part":5,"part_size":524288,"uploaded":2621440,"total":4294967296}
  ```
- Final event command: `cp`. Data is the same success payload returned by `td cp`.
- Dry-run events use command `cp.dry-run`.
- Events are written through `Renderer.Event`, which bypasses human output and always writes NDJSON regardless of `--json`.

## Consequences

- Agents can stream `td cp --events` and track progress without parsing stderr.
- Final and progress events share `meta.request_id`, so a single invocation is easy to correlate.
- NDJSON is one object per line; consumers can use `jq` or a simple line reader.
- Progress events are emitted for the `cp` upload path. Extending the same stream to other long commands is tracked in issue #27.
