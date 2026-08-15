# Release and CI

## Tools

- GitHub Actions for CI/CD
- release-please for version and changelog PRs
- GoReleaser v2 for release assets
- nfpm through GoReleaser for deb/rpm/apk packages
- cosign keyless signing for checksums
- syft SBOM generation
- Homebrew cask published to `thedavidweng/homebrew-tap`

## Required repository secrets

```text
RELEASE_PLEASE_TOKEN          GitHub token that can push tags and create release PRs
HOMEBREW_TAP_GITHUB_TOKEN     PAT with access to the Homebrew tap repo
CODECOV_TOKEN                 optional, only needed for private repos
```

`GITHUB_TOKEN` is used for normal release uploads.

## Workflows

- `.github/workflows/ci.yml` runs two jobs:
  - `test` (25m): tidy, fmt, vet, lint, unit tests, race, WASM, `make build`, coverage
  - `snapshot` (30m): `goreleaser build --snapshot --clean` on its own runner
- `.github/workflows/release-please.yml` manages release PRs and tags.
- `.github/workflows/release.yml` runs GoReleaser on `v*` tags.

## Release process

1. Merge conventional commits into `main`.
2. release-please opens or updates a release PR.
3. Merge the release PR.
4. release-please creates a tag and GitHub Release.
5. The tag push triggers GoReleaser.
6. GoReleaser uploads archives, packages, checksums, signatures, SBOMs, and
   the Homebrew cask update.

## Local checks

```sh
make goreleaser-check
make snapshot
```

## Asset names

Archives follow the `archives.name_template` in `.goreleaser.yaml`
(`{{ .ProjectName }}_{{ .Os }}_<arch>`, where amd64 renders as `x86_64` and
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
