# 0011 - Command package

## Status

Accepted.

## Context

`internal/app/app.go` had grown to over 1,300 lines and contained the `runtimeOpts` state plus every Cobra command builder. This made the command surface hard to navigate and meant command definitions were coupled to a single file.

## Decision

- Keep `runtimeOpts` and runtime helpers in `internal/app/app.go`.
- Move all Cobra command builders into a new `internal/app/commands` package.
- Define a `commands.Runtime` interface that exposes only the runtime state command builders need: `JSON()`, `Channel()`, `Renderer()`, `LoadConfig()`, and `OpenApp()`.
- `app.go` creates a `*runtimeOpts` and passes it to `commands.New*Cmd` constructors.
- Move `groupUsage`, `printKV`, and `ensureTelegramConfig` into `internal/app/commands` as exported helpers.

## Consequences

- `internal/app/app.go` is thinned to runtime setup and command wiring.
- Command behavior is isolated and can be browsed by package.
- New commands can be added in `internal/app/commands` without touching `app.go`.
- `internal/app/commands` does not import `internal/app`, so there is no package cycle.
