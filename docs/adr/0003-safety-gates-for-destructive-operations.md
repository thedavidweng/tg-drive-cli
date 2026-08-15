# 0003 - Safety gates for destructive operations

## Status

Accepted.

## Context

Remote mutations in `td` can permanently alter or delete the user's Telegram channel state. Scripts and unattended callers need a predictable, non-interactive guard that prevents accidental deletions or overwrites, but `td` must remain usable for batch and recursive workflows. A global `--read-only` mode was rejected because it would change the default behavior of every upload.

## Decision

- Apply an explicit `--confirm` gate to destructive commands:
  - `td rm` requires `--confirm`.
  - `td mv` requires `--confirm`.
  - `td cp --replace` requires `--confirm`.
  - `td import` requires `--confirm` unless `--dry-run` is set.
- Each of these commands also supports `--dry-run`, which returns a plan and makes no remote change.
- Non-destructive writes (`td cp` without `--replace`, `td init`, `td scan`, `td repair`) do not require `--confirm`.
- Read-only mode (`TD_READONLY` / `--read-only`) is **not** implemented at this time.
- Confirmation failures produce `ERR_CONFIRMATION_REQUIRED` (category `safety`, exit code `10`).

## Consequences

- Destructive workflows are now explicit and auditable; a missing `--confirm` is a fast, clear failure.
- `--dry-run` lets scripts and agents preview the effect of `rm`, `mv`, and `cp --replace` before requesting confirmation.
- Exit code `10` is a new, dedicated safety exit code that callers can distinguish from usage or platform errors.
- The default `td cp` experience is unchanged; only `--replace` requires confirmation.
