# Acceptance checklist

This file is the release gate. Every item must pass before the project is considered usable.

## Local gates

```sh
make ci-local
make snapshot
make goreleaser-check
```

## Functional gates

- `td auth login` works with phone code.
- `td auth login` handles 2FA.
- `td init <path> --create-channel` creates a private channel and records it.
- `td cp file /path/file` uploads one file.
- Deep paths use manifest reply and round-trip through scan.
- `td scan --full` rebuilds an empty DB.
- `td scan` incremental imports new messages.
- `td ls` and `td tree` read from SQLite.
- `td get` downloads and verifies size/hash.
- `td mv` renames files when Telegram permits edit.
- `td mv` returns `ERR_DIRECTORY_MOVE_UNSUPPORTED` for directories.
- `td rm` works in delete mode.
- `td rm` works in tombstone mode and redacts manifest reply.
- `td repair --pending` recovers pending rows.
- `td doctor` reports capability status.
- JSON envelopes are stable for every command.

## Telegram behavior gates

Run with a real account and private test channel:

- New caption edit works.
- Old caption edit check reports pass/warn/fail.
- Delete check reports pass/warn/fail.
- Invite link creation check reports pass/warn/fail.
- Upload size limit check reports 2GB or 4GB capability.
- Hashtag helper output includes a note to choose current channel/current chat results in modern clients.

## Release gates

- CI passes on push and pull request.
- release-please creates a release PR from conventional commits.
- Tag `vX.Y.Z` triggers GoReleaser.
- GitHub Release contains archives, checksums, SBOM, and package artifacts.
- Homebrew cask is updated when `HOMEBREW_TAP_GITHUB_TOKEN` is configured.
