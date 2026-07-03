#!/bin/sh
set -eu
VERSION="${1:-dev}"
mkdir -p dist
LDFLAGS="-s -w -X github.com/thedavidweng/tg-drive-cli/internal/version.Version=$VERSION -X github.com/thedavidweng/tg-drive-cli/internal/version.BuiltBy=release.sh"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o dist/td-darwin-arm64 ./cmd/td
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o dist/td-linux-amd64 ./cmd/td
ls -lh dist/
