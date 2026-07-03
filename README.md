# tg-drive-cli agent handoff pack

This folder is the complete implementation handoff for `tg-drive-cli`, a Go CLI that stores a virtual file tree in Telegram channels.

The binary name is `td`.

## Start here

1. Read `AGENTS.md`.
2. Read `PRODUCT_SPEC.md`.
3. Follow `IMPLEMENTATION_PLAN.md` in order.
4. Use `docs/release-and-ci.md` for CI/CD, GoReleaser, release-please, Homebrew, packages, signing, and SBOM.
5. Keep `docs/acceptance.md` passing before moving to the next stage.

## Repository decisions

- Module: `github.com/thedavidweng/tg-drive-cli`
- Binary: `td`
- Language: Go 1.26
- License: Apache-2.0
- CLI framework: Cobra
- MTProto client: gotd/td
- SQLite driver: modernc.org/sqlite
- Config: TOML
- Release: GoReleaser v2
- Release PRs: release-please
- CI: GitHub Actions

## Working rule

Do not add alternative architecture. Implement the plan as written. When code and docs diverge, update docs in the same change.
