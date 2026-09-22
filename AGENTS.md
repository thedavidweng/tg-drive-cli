# AGENTS.md

Instructions for every coding agent working in this repo. This is the only agent-instruction file — never create CLAUDE.md, .cursorrules, .windsurfrules, .clinerules, GEMINI.md, or any other per-tool variant (CI fails the build if one appears).

This repository is implemented. Do not reopen completed V1 scope. Future work
lives in GitHub Issues.

## Source of truth

- Frozen CLI / JSON / storage / config interfaces: `docs/contracts/`
- Current product constraints: `DECISIONS.md`
- Architecture: `docs/architecture.md`
- Decisions: `docs/adr/`
- Domain glossary: `CONTEXT.md`
- CI/CD: `docs/release-and-ci.md`
- Testing: `docs/testing.md`

Contracts win on conflict with other docs. Code wins on implementation detail.

## Documentation rules

- Current-state docs only.
- No archived plans.
- No “Phase N will later...” language in reference docs.
- Future work lives in GitHub Issues, not in reference docs.
- Code is the source of truth for implementation details.
- Contracts document frozen interfaces: command names, JSON envelopes, DB
  invariants, manifest formats.

## Code rules

- Go code must be formatted with `gofumpt` (strict extra-rules).
- Every command supports stable JSON output when `--json` is present.
- JSON output goes to stdout. Logs, prompts, and diagnostics go to stderr.
- Every remote write operation must use an operation lock and a DB transaction.
- Never assume Telegram capabilities. Use the capability layer and `td doctor`
  checks.
- Never count Telegram captions with `len(string)` or rune count. Use UTF-16
  code units.
- Never rely on hashtags for machine reconstruction. Use `td:v1`,
  `td-manifest:v1`, or `td-album:v1` metadata.

## Verification

`mise run check` (or `make ci-local && make build`) must pass before every push. CI runs the same gates.

## PR checklist

Before opening or finishing a PR:

- `mise run check` passes
- Changed commands update `docs/contracts/cli-contract.md`
- Changed JSON output updates `docs/contracts/json-contract.md`
- Changed storage/manifest behavior updates `docs/contracts/storage-contract.md`
- Changed config keys or defaults update `docs/contracts/config-contract.md`

## Issue tracker

Issues live in `thedavidweng/tg-drive-cli` on GitHub. Use `gh issue create`.
Pull requests are not a triage queue.

## Architectural decisions

- New dependencies, abstractions, or changes to public JSON/exit-code
  contracts require an ADR in `docs/adr/NNNN-slug.md` (Status / Context /
  Decision / Consequences).
- ADRs are numbered sequentially and record why a design choice was made, not
  how it is implemented.
