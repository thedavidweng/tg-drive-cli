# 0007 - File publisher module

## Status

Accepted. Repository seam extracted in ADR 0008.

## Context

`td` publishes a file to a Telegram channel and records the result in the local SQLite index. The steps — render the Caption and optional Manifest, generate the Hashtag slug chain, send the manifest reply, and insert or update the File row, Slug mappings, Hashtag tags, and derived directory Nodes — are repeated across `UploadFile`, `MoveFile`, `RepairPath`, `RepairOrphaned`, and `indexScannedFile`. Duplicating this recipe makes the upload/repair/scan code hard to test and easy to drift out of sync.

## Decision

- Introduce a **File publisher** module in `core/publisher` with a concrete `Publisher` type.
- The publisher owns: slug-chain generation, caption/manifest rendering, optional manifest-reply sending, and the index transaction that writes File rows, Slug mappings, Hashtag tags, and derived Nodes.
- `Scan` uses a read-only `Reindex` path that persists index state without sending or editing Telegram messages.
- The publisher depends on `telegram.Client` and, after ADR 0008, on `ports.FileIndex`.

## Consequences

- Caption/manifest/indexing rules live in one place; future changes to the caption budget, slug logic, or Node derivation need only update the publisher.
- `UploadFile`, `MoveFile`, `RepairPath`, `RepairOrphaned`, and `indexScannedFile` shrink to orchestration.
- Unit tests can drive the publisher with a fake `telegram.Client` and a temporary SQLite database.
