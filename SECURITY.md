# Security

`tg-drive-cli` stores Telegram session material locally.

## Local files

- Session files must be readable only by the current user.
- Config output redacts `api_hash` and phone by default.
- DB files may contain local paths and file names; treat them as private.

## Telegram storage

Uploaded files are stored in Telegram channels. Any account with channel access can view and download them.

## Reporting

Open a private security advisory on GitHub or contact the maintainer through the repository profile.
