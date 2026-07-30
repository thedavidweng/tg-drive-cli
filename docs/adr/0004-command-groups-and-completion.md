# 0004 - Command groups and shell completion

## Status

Accepted.

## Context

The `td` command surface grew organically: `cp`, `get`, `mv`, `rm`, `ls`, `tree`, `scan`, `repair`, `share`, `auth`, `config`, `channels`, `init`, `status`, `doctor`, `version`. A flat list makes help harder to scan and makes it harder for agents to discover related operations. Cobra supports command groups and automatic completion generation, both of which are low-cost, high-clarity improvements.

## Decision

- Organize commands into Cobra `GroupID`s:
  - `Core`: `version`, `doctor`, `status`, `config`, `completion`
  - `Authentication`: `auth`
  - `Channels`: `channels`
  - `Files`: `init`, `ls`, `tree`, `cp`, `get`, `mv`, `rm`
  - `Maintenance`: `scan`, `repair`, `share`
- Keep existing command names and aliases unchanged; grouping is presentation only.
- Add `td completion [bash|zsh|fish|powershell]` using Cobra's built-in completion generators.

## Consequences

- `td --help` is easier to read and scales as the command surface grows.
- Shell completion is available without an external script; packaging can call `td completion` at build time to refresh `completions/`.
- Group membership is declared in `NewRootCommand`; new commands must be assigned to a group.
