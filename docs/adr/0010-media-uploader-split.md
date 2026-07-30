# 0010 - Media uploader split

## Status

Accepted.

## Context

`telegramgotd.Client.UploadMedia` had a single function that selected between the gotd `uploader.Upload` path for small files and a custom resumable big-file upload path. The decision logic, state loading, and the two upload strategies were mixed together.

## Decision

- Define a `mediaUploader` interface inside `adapters/native/telegramgotd` with `upload(ctx, api, req) (tg.InputFileClass, error)`.
- Implement `smallUploader` for the gotd uploader path.
- Implement `bigUploader` for the resumable big-file path (state loading, validation, `resumableUploadBig`, and cleanup).
- `Client.UploadMedia` now selects the appropriate uploader with `selectMediaUploader(req)` and delegates the upload step.

## Consequences

- Small-file and big-file upload strategies are isolated and can be tested independently.
- The selection logic is explicit and resides in one place.
- `Client.UploadMedia` focuses on peer resolution, upload delegation, and media-message sending.
