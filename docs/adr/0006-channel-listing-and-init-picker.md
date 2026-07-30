# 0006 - Channel listing and interactive init picker

## Status

Accepted.

## Context

`td init` previously required the user to know the exact channel title or ID before binding. New users often have many Telegram channels and do not know which one to use. An agent driving setup cannot easily list and select a channel without external tools. The Telegram API (via `gotd/td`) can enumerate dialogs, and most users create channels with a `[TD]` marker for drive use.

## Decision

- Add `td channels list [--only-drive]` to list accessible channels.
- `--only-drive` filters to channels whose title contains `[td]` (case-insensitive).
- `td init` without a channel, `--create-channel`, or `--bind-channel=<title>` now lists channels and prompts for selection.
- `td init --bind-channel` (no value) also lists and prompts.
- In `--json` mode without an explicit channel, `init` returns `ERR_CHANNEL_NOT_FOUND` with the candidate list in `details.channels` instead of hanging on a prompt.
- `ListChannels` returns `id`, `title`, `username`, and `invite_link` for display and downstream selection.

## Consequences

- Interactive setup is now a single command (`td init <root>`) even when the user has many channels.
- Agents can use `td channels list --only-drive --json` to pick a channel deterministically and then pass it to `td init`.
- The prompt writes to stderr and reads from stdin, so it works in a TTY and fails cleanly in non-interactive JSON mode.
