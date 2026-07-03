# Release and CI/CD

This project follows the same release shape as `monarchmoney-cli`, adjusted for `tg-drive-cli` and binary `td`.

## Tools

- GitHub Actions for CI/CD
- release-please for version/changelog PRs
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

- `.github/workflows/ci.yml` runs format, vet, tests, race tests, lint, and coverage.
- `.github/workflows/release-please.yml` manages release PRs and tags.
- `.github/workflows/release.yml` runs GoReleaser on `v*` tags.

## Release process

1. Merge conventional commits into `main`.
2. release-please opens/updates a release PR.
3. Merge the release PR.
4. release-please creates a tag and GitHub Release.
5. Tag push triggers GoReleaser.
6. GoReleaser uploads archives, packages, checksums, signatures, SBOMs, and Homebrew cask update.

## Local release checks

```sh
make goreleaser-check
make snapshot
```

## Asset names

```text
td_darwin_universal.tar.gz
td_linux_x86_64.tar.gz
td_linux_arm64.tar.gz
td_windows_x86_64.zip
td_windows_arm64.zip
td_linux_x86_64.deb
td_linux_x86_64.rpm
td_linux_x86_64.apk
checksums.txt
checksums.txt.sig
*.spdx.json
```
