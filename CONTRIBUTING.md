# Contributing

Thanks for wanting to improve `td`. Please open or comment on a
[GitHub Issue](https://github.com/thedavidweng/tg-drive-cli/issues) before
starting large work.

## Setup

```sh
git clone https://github.com/thedavidweng/tg-drive-cli.git
cd tg-drive-cli
make bootstrap
make ci-local
```

You need Go 1.26 or newer. `make lint` also needs
[golangci-lint](https://golangci-lint.run/) v2.

## Tests

```sh
make test
make test-race
make ci-local          # fmt-check, vet, tests, race, WASM compile
```

To exercise the CLI without a Telegram account or network, use the in-memory
fake (login code is `12345`):

```sh
export TD_FAKE_TELEGRAM=1 TD_FAKE_TELEGRAM_STATE=/tmp/td-demo/fake.json
export TD_API_ID=1 TD_API_HASH=hash TD_PHONE=+1000
td auth login
td init ./testdata/local --create-channel
td cp ./testdata/local/a.txt /a.txt
td ls /
```

Real-account checks are documented in
[`docs/manual-smoke-tests.md`](docs/manual-smoke-tests.md). Use a private
test channel, not a production one.

## Contracts

Command names, JSON envelopes, storage schema, and config keys are frozen in
[`docs/contracts/`](docs/contracts/). Update the matching contract in the
same change:

| Change | Contract |
| --- | --- |
| Command or flag | `docs/contracts/cli-contract.md` |
| JSON envelope or error | `docs/contracts/json-contract.md` |
| Schema or manifest | `docs/contracts/storage-contract.md` |
| Config key or default | `docs/contracts/config-contract.md` |

New dependencies, new abstractions, or public JSON/exit-code changes also
need an ADR in `docs/adr/NNNN-slug.md`.

## Style

- Format with `gofumpt` (strict extra-rules).
- JSON goes to stdout. Logs, prompts, and diagnostics go to stderr.
- Remote writes use an operation lock and a DB transaction.
- Do not assume Telegram capabilities. Use the capability layer and
  `td doctor`.
- Count Telegram captions in UTF-16 code units, not bytes or runes.
- Machine recovery uses `td:v1`, `td-manifest:v1`, or `td-album:v1`. Hashtags
  are navigation only.

## Commits and pull requests

Use [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat: add scan rebuild
fix: handle UTF-16 caption budget
docs: update storage contract
test: add slug collision cases
ci: update release workflow
```

Before opening a PR:

- [ ] `mise run check` passes
- [ ] Contracts updated if the public surface changed
