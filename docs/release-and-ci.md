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

- `.github/workflows/ci.yml` runs:
  - `go mod tidy` drift check (`make mod-tidy-check`)
  - format check (`make fmt-check`)
  - vet (`make vet`)
  - `golangci-lint` with `.golangci.yml`
  - tests, race tests, WASM core build
  - single-platform build (`make build`)
  - cross-platform `goreleaser build --snapshot --clean` to catch release-breakers
  - coverage upload to Codecov
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

Archives follow the `archives.name_template` in `.goreleaser.yaml`
(`{{ .ProjectName }}_{{ .Os }}_<arch>`, where amd64 renders as `x86_64` and the
macOS universal binary renders as `universal`):

```text
td_darwin_universal.tar.gz
td_linux_x86_64.tar.gz
td_linux_arm64.tar.gz
td_windows_x86_64.zip
td_windows_arm64.zip
```

The `nfpms` block sets no `file_name_template`, so deb/rpm/apk packages use
GoReleaser's default nfpm name template
(`{{ .PackageName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}` + the format
extension). `.Version` is the tag without its leading `v`, so for tag `v1.2.3`
the packages are:

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
