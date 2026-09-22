# Release and CI

## Tools

- GitHub Actions for CI/CD
- release-please for version and changelog PRs
- GoReleaser v2 for release assets
- nfpm through GoReleaser for deb/rpm/apk packages
- cosign keyless signing for checksums
- syft SBOM generation
- Homebrew cask published to `thedavidweng/homebrew-tap`

## Repository secrets

None are required. The pipeline runs end to end on the built-in
`GITHUB_TOKEN`:

- release-please creates the release PR and, after the merge, the tag and
  the GitHub Release.
- The Release Please workflow then dispatches the Release workflow via
  `workflow_dispatch` (the one event type the built-in token is allowed
  to trigger), because tags created with the built-in token do not fire
  the tag trigger.
- The Homebrew cask is not pushed from this repo. The tap repository's
  own Sync Releases workflow updates `Casks/tg-drive-cli.rb` from the
  published release assets (daily cron or manual dispatch).

`RELEASE_PLEASE_TOKEN` is an optional override. `CODECOV_TOKEN` is only
needed for private repos.

## Workflows

- `.github/workflows/ci.yml` runs two jobs:
  - `test` (25m): tidy, fmt, vet, lint, unit tests, race, WASM, `make build`, coverage
  - `snapshot` (30m): `goreleaser build --snapshot --clean` on its own runner
- `.github/workflows/release-please.yml` manages release PRs, tags, and
  releases, then dispatches packaging.
- `.github/workflows/release.yml` runs GoReleaser on `v*` tags or when
  dispatched with a tag name.

## Release process

1. Merge conventional commits into `main`.
2. release-please opens or updates a release PR.
3. Merge the release PR.
4. release-please creates a tag and the GitHub Release.
5. The Release Please workflow dispatches the Release workflow on the
   tag (`workflow_dispatch`), because tags created with the built-in
   token do not fire the tag trigger.
6. GoReleaser uploads archives, packages, checksums, signatures, and
   SBOMs to the release.
7. The tap repository's Sync Releases workflow updates the Homebrew
   cask from the published assets (daily cron; may lag by up to a day,
   or run it manually after a release).

## Local checks

```sh
make goreleaser-check
make snapshot
```

## Asset names

Archives follow the `archives.name_template` in `.goreleaser.yaml`
(<span v-pre><code>{{ .ProjectName }}_{{ .Os }}_&lt;arch&gt;</code></span>, where amd64 renders as `x86_64` and
the macOS universal binary renders as `universal`):

```text
td_darwin_universal.tar.gz
td_linux_x86_64.tar.gz
td_linux_arm64.tar.gz
td_windows_x86_64.zip
td_windows_arm64.zip
```

Packages use GoReleaser's default nfpm name template. For tag `v1.2.3`:

```text
td_1.2.3_linux_amd64.deb
td_1.2.3_linux_arm64.deb
td_1.2.3_linux_amd64.rpm
td_1.2.3_linux_arm64.rpm
td_1.2.3_linux_amd64.apk
td_1.2.3_linux_arm64.apk
```

Signing, checksums, and SBOMs:

```text
checksums.txt
checksums.txt.sig
*.spdx.json
```
