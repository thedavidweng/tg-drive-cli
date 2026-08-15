# Security

`td` stores Telegram session material on the local machine. Treat the session
file, `api_hash`, and the SQLite cache as account credentials.

## Local files

- Session and config files must be readable only by the current user.
- `td status` and `td config get` redact `api_hash`, phone numbers, invite
  links, local paths, and hashes unless `--show-secrets` is passed.
- The cache database stores local paths and folder names. Treat it as private.

## Telegram storage

Uploaded files live in Telegram cloud storage. Anyone with channel access can
view and download them. Captions and hashtags expose path names.

## Reporting

Please do not open a public issue for a vulnerability.

Open a [private security advisory](https://github.com/thedavidweng/tg-drive-cli/security/advisories/new)
or contact the maintainer through the repository profile.
