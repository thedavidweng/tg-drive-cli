# 0008 - File index port

## Status

Accepted.

## Context

The `Publisher` module introduced in ADR 0007 writes File rows, Hashtag tags, Slug mappings, and derived directory Nodes inside a single SQLite transaction. Keeping this SQL inside `core/publisher` couples the publisher to the `sqlitestore` adapter and makes it impossible to test the publisher against a fake index.

## Decision

- Introduce a `ports.FileIndex` repository seam in `core/ports`.
- Move the index write transaction into `adapters/native/sqlitestore` as the `DB.Index` method.
- `core/publisher.Publisher` now depends on `ports.FileIndex` instead of `*sqlitestore.DB`.
- The `FileIndexRequest` carries the file metadata, slug maps, tags, and transaction flags; the publisher keeps the orchestration (caption/manifest/Telegram) and passes the unit of work to the repository.

## Consequences

- The publisher no longer knows SQL or `sqlitestore`; it can be unit-tested with a fake `ports.FileIndex`.
- SQLite-specific index logic is isolated in the SQLite adapter.
- Other adapters can implement `ports.FileIndex` without changing `core/publisher`.
