# 0003 - Safety gates for destructive operations

## Status

Accepted.

## Context

Remote mutations in `td` can permanently alter or delete the user's Telegram channel state. Scripts and agents running unattended need a predictable, non-interactive guard that prevents accidental deletions or overwrites. The `monarchmoney-cli` safety model uses read-only, dry-run, and confirm layers, but `td` must remain usable for batch and recursive workflows. A full three-tier model with a global `--read-only` mode was rejected for now because it would change the default behavior of every upload.

## Decision

- Apply an explicit `--confirm` gate to destructive commands:
  - `td rm` requires `--confirm`.
  - `td mv` requires `--confirm`.
  - `td cp --replace` requires `--confirm`.
- Each of these commands also supports `--dry-run`, which returns a plan and makes no remote change.
- Non-destructive writes (`td cp` without `--replace`, `td init`, `td scan`, `td repair`) do not require `--confirm`.
- Read-only mode (`TD_READONLY` / `--read-only`) is **not** implemented at this time.
- Confirmation failures produce `ERR_CONFIRMATION_REQUIRED` (category `safety`, exit code `10`).

## Consequences

- Destructive workflows are now explicit and auditable; a missing `--confirm` is a fast, clear failure.
- `--dry-run` lets scripts and agents preview the effect of `rm`, `mv`, and `cp --replace` before requesting confirmation.
- Exit code `10` is a new, dedicated safety exit code that callers can distinguish from usage or platform errors.
- The default `td cp` experience is unchanged; only `--replace` requires confirmation.
