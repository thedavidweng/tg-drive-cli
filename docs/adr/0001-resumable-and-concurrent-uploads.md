# 0001 - Resumable and concurrent uploads

## Status

Accepted.

## Context

`td` uploads files to Telegram channels. Files can be large (up to 4 GB for Premium accounts), so a single-threaded, non-resumable upload is slow and fragile. A network interruption, process crash, or Telegram rate limit part-way through a multi-gigabyte upload should not force a full restart from byte zero. The Telegram API supports resumable big-file uploads via `upload.saveBigFilePart`, and the `gotd/td` uploader supports multi-threading for smaller files.

## Decision

- Use the official `gotd/td` `uploader.Upload` with `WithThreads` and `WithPartSize` for files **≤ 10 MB**. This uses `upload.saveFilePart` and is not resumable, but the files are small enough that restart cost is acceptable.
- For files **> 10 MB**, implement a custom resumable upload path using `upload.saveBigFilePart` with a generated `file_id`.
- Part size is computed from the file size, starting at 128 KB and doubling up to the Telegram maximum of 512 KB.
- Upload state is persisted in the local SQLite cache (`upload_progress` table) keyed by the pending file row (`file:<id>`) and validated against `total_bytes` and `content_hash`. On retry, confirmed parts are skipped.
- The worker count is controlled by `[upload].threads` (default 4) and can be overridden per `td cp` with `--upload-threads`. Part size can be set via `[upload].part_size_kb` or `--upload-part-size-kb`.

## Consequences

- Large uploads can be interrupted and resumed without re-uploading already-confirmed parts.
- The SQLite `upload_progress` table is an additional per-file state surface; the row and pending file row are retained on error so `td repair --pending` can retry.
- Resumable uploads re-open the local file per worker, which avoids sharing a single file descriptor across concurrent goroutines.
