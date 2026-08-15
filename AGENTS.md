# Agent instructions

This repository is implemented. Do not reopen completed V1 scope. Future work lives in GitHub Issues.

## Source of truth

- Frozen CLI/JSON/storage interfaces: `docs/contracts/`
- Product behavior (V1 limits): `PRODUCT_SPEC.md` — contracts win on conflict
- Decisions: `DECISIONS.md` and `docs/adr/`
- CI/CD: `docs/release-and-ci.md`
- Testing: `docs/testing.md`

## Documentation rules

- Current-state docs only.
- No archived plans.
- No “Phase N will later...” language in reference docs.
- Future work lives in GitHub Issues, not in reference docs.
- Code is the source of truth for implementation details.
- Contracts document frozen interfaces: command names, JSON envelopes, DB invariants, manifest formats.

## Code rules

- Go code must be formatted with `gofmt -s` and `goimports`.
- Every command supports stable JSON output when `--json` is present.
- JSON output goes to stdout. Logs, prompts, and diagnostics go to stderr.
- Every remote write operation must use an operation lock and a DB transaction.
- Never assume Telegram capabilities. Use the capability layer and `td doctor` checks.
- Never count Telegram captions with `len(string)` or rune count. Use UTF-16 code units.
- Never rely on hashtags for machine reconstruction. Use `td:v1`, `td-manifest:v1`, or `td-album:v1` metadata.

## PR checklist

Before opening or finishing a PR:

- `make fmt-check` passes
- `make lint` passes
- `make test` passes
- `make test-race` passes
- `make build` passes
- Changed commands update `docs/contracts/cli-contract.md`
- Changed JSON output updates `docs/contracts/json-contract.md`
- Changed storage/manifest behavior updates `docs/contracts/storage-contract.md`

## Agent skills

### Issue tracker

Issues live in the `thedavidweng/tg-drive-cli` GitHub repository. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context layout: `CONTEXT.md` at the repo root plus ADRs in `docs/adr/`. See `docs/agents/domain.md`.

## Architectural decisions

- New dependencies, abstractions, or changes to public JSON/exit-code contracts require an ADR in `docs/adr/NNNN-slug.md` (Status / Context / Decision / Consequences).
- ADRs are numbered sequentially and record why a design choice was made, not how it is implemented.
