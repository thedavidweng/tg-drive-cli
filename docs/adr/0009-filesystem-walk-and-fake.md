# 0009 - Filesystem Walk and fake adapter

## Status

Accepted.

## Context

`ports.FileSystem` originally exposed `Stat`, `Open`, `MkdirAll`, `CreateTemp`, `Rename`, and `Remove`. The upload and download paths still called `os.Stat`, `os.Open`, `filepath.Walk`, and `os.MkdirAll` directly, so the port was incomplete and there was no in-memory second adapter for tests.

## Decision

- Add `Walk` to `ports.FileSystem` with a `ports.WalkFunc` callback.
- Implement `Walk` in `adapters/native/localfs.FS` using `filepath.Walk`.
- Introduce `adapters/fakefs.FS`, an in-memory `ports.FileSystem` implementation for tests and browser-like targets.
- Route all service-level file and directory I/O through `a.files()`: `UploadFile`, `UploadRecursive`, `DownloadRecursive`, and `RepairPending` no longer call `os` directly for these operations.

## Consequences

- All local filesystem access in the service layer flows through a single port.
- Tests can drive the service with `fakefs.FS` instead of writing temp files to disk.
- `adapters/native/localfs` remains the default production adapter.
