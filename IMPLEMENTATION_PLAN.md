# tg-drive-cli Implementation Plan

This is the execution plan for coding agents. It is intentionally explicit. Do not redesign the product while implementing it.

## Fixed decisions

| Area | Decision |
|---|---|
| Repository | `github.com/thedavidweng/tg-drive-cli` |
| Binary | `td` |
| Main package | `./cmd/td` |
| Minimum Go | `go 1.26` |
| License | Apache-2.0 |
| CLI | Cobra |
| Config | TOML via `github.com/pelletier/go-toml/v2` |
| SQLite | `modernc.org/sqlite` |
| MTProto | `github.com/gotd/td` |
| Logging | stdlib `log/slog` |
| JSON rendering | local `internal/output` package |
| Hash | BLAKE3 via `lukechampine.com/blake3` |
| Slug transliteration | `github.com/mozillazg/go-unidecode`, plus Chinese pinyin fallback via `github.com/mozillazg/go-pinyin` |
| Config dir | `~/.config/tg-drive-cli` on Unix; `%APPDATA%\tg-drive-cli` on Windows |
| Data dir | `~/.local/share/tg-drive-cli` on Unix; `%LOCALAPPDATA%\tg-drive-cli` on Windows |
| Session file | `session.json` |
| DB file | `local_cache.db` |
| Global env prefix | `TD_` only |
| Caption budget | 1024 UTF-16 code units, 16-unit margin |
| Text manifest budget | 4096 UTF-16 code units, 16-unit margin |
| Upload size | default 2 GB, Premium capability 4 GB after doctor detection/config override |
| Directory move | Out of V1; return `ERR_DIRECTORY_MOVE_UNSUPPORTED` |
| Empty directories | Out of V1 remote persistence; local-only cache entries disappear after full scan |
| Delete mode | `delete` by default; `tombstone` optional |
| Concurrency | SQLite WAL + `operation_locks` + active/pending partial indexes |
| Release | GoReleaser v2, release-please, GitHub Actions |

## Global acceptance gates

These gates apply after every stage:

```sh
make fmt-check
make lint
make test
make test-race
make build
```

The project can ship only when:

```sh
make ci-local
make snapshot
make goreleaser-check
```

also pass.

---

## Stage 0 — Repository baseline

### Goal

Create a buildable Go CLI repository with release-ready project metadata and no Telegram behavior yet.

### Tasks

1. Keep the module path exactly `github.com/thedavidweng/tg-drive-cli`.
2. Create `cmd/td/main.go` that calls `internal/app.Execute()`.
3. Create `internal/app` with a Cobra root command.
4. Create `internal/version` with build-time variables:
   - `Version`
   - `Commit`
   - `Date`
   - `BuiltBy`
5. Add root command flags:
   - `--config <path>`
   - `--db <path>`
   - `--session <path>`
   - `--json`
   - `--quiet`
   - `--verbose`
   - `--channel <name-or-id>`
   - `--wait`
6. Add commands:
   - `td version`
   - `td doctor`
7. Add docs and release metadata already present in this handoff.
8. Wire `make build`, `make test`, `make lint`, `make fmt-check`.

### Implementation notes

- `td --version` must print the same version as `td version` in human mode.
- `td version --json` must use the shared JSON envelope.
- Root command must not initialize Telegram or DB for `version` and shell completion commands.

### Acceptance

```sh
go run ./cmd/td version
go run ./cmd/td version --json
make ci-local
```

Expected JSON shape:

```json
{
  "ok": true,
  "data": {
    "version": "dev",
    "commit": "none",
    "date": "unknown",
    "built_by": "source"
  }
}
```

---

## Stage 1 — Output, errors, and exit codes

### Goal

Implement a stable machine contract before adding commands.

### Tasks

1. Create `internal/output`.
2. Implement `Renderer` with human and JSON modes.
3. Implement global JSON success envelope:

```json
{
  "ok": true,
  "data": {}
}
```

4. Implement global JSON error envelope:

```json
{
  "ok": false,
  "error": {
    "code": "ERR_CODE",
    "message": "human readable message",
    "details": {}
  }
}
```

5. Create `internal/apperr`.
6. Define every error code from `docs/contracts/cli-contract.md`.
7. Map errors to exit codes:

| Exit | Codes |
|---:|---|
| 0 | success |
| 1 | default uncategorized error |
| 2 | `ERR_USAGE`, `ERR_PATH_INVALID`, `ERR_FLAG_CONFLICT` |
| 3 | `ERR_AUTH_REQUIRED`, `ERR_CONFIG_MISSING`, `ERR_CONFIG_INVALID` |
| 4 | `ERR_TELEGRAM_RPC`, `ERR_TELEGRAM_RATE_LIMITED`, `ERR_CHANNEL_PERMISSION`, `ERR_CHANNEL_NOT_FOUND`, `ERR_FILE_TOO_LARGE`, `ERR_MESSAGE_NOT_EDITABLE` |
| 5 | `ERR_DB`, `ERR_SCAN_FAILED`, `ERR_MANIFEST_INVALID`, `ERR_OPERATION_LOCKED`, `ERR_REPAIR_REQUIRED` |

8. Ensure human errors go to stderr.
9. Ensure JSON errors go to stdout only when `--json` is set.

### Acceptance

- Unit tests cover every error code mapping.
- `td doctor --json` returns the JSON envelope even before full doctor implementation.
- Invalid command exits `2`.

---

## Stage 2 — Config and platform paths

### Goal

Implement deterministic config/session/DB path resolution.

### Tasks

1. Create `internal/config`.
2. Implement OS-specific path functions:
   - Unix config: `~/.config/tg-drive-cli/config.toml`
   - Unix session: `~/.config/tg-drive-cli/session.json`
   - Unix DB: `~/.local/share/tg-drive-cli/local_cache.db`
   - Windows config: `%APPDATA%\tg-drive-cli\config.toml`
   - Windows session: `%APPDATA%\tg-drive-cli\session.json`
   - Windows DB: `%LOCALAPPDATA%\tg-drive-cli\local_cache.db`
3. Implement precedence:
   - CLI flags
   - env vars
   - config file
   - defaults
4. Use env vars only with `TD_` prefix:
   - `TD_CONFIG`
   - `TD_SESSION`
   - `TD_DB`
   - `TD_API_ID`
   - `TD_API_HASH`
   - `TD_PHONE`
   - `TD_CHANNEL`
   - `TD_JSON`
   - `TD_WAIT`
5. Implement config struct exactly as `docs/contracts/config-contract.md`.
6. Implement redaction helpers for all secrets.
7. Add `td config get [key]` and `td config set <key> <value>`.

### Security decisions

- `td config get` redacts `api_hash` and phone by default.
- `td config get --show-secrets` requires interactive confirmation unless `--json --confirm` is set.
- Session file permissions on Unix must be `0600`.
- Config dir permissions on Unix must be `0700`.
- Windows uses user ACLs where possible and warns when ACL hardening fails.

### Acceptance

- Unit tests cover path resolution on Unix and Windows via injected env.
- Secret redaction tests cover status, config, logs, and JSON output.
- `td config get --json` never leaks `api_hash` by default.

---

## Stage 3 — SQLite schema, migrations, WAL

### Goal

Create the local cache and all invariants before remote operations.

### Tasks

1. Create `internal/db`.
2. Open SQLite with:

```sql
pragma foreign_keys = ON;
pragma journal_mode = WAL;
pragma busy_timeout = 5000;
```

3. Implement migration table `schema_version`.
4. Add schema from `docs/contracts/storage-contract.md` exactly.
5. Use `files.node_id references nodes(id) on delete set null`.
6. Add partial indexes:

```sql
create unique index idx_files_active_path
  on files(channel_id, canonical_path)
  where status = 'active';

create unique index idx_files_pending_path
  on files(channel_id, canonical_path)
  where status = 'pending';
```

7. Add `operation_locks` with `owner_token`, `expires_at`, and path-level key.
8. Add `scan_errors` with de-dupe and resolved lifecycle.
9. Add transaction helper.
10. Add test DB helper.

### DB invariants

- Active file paths are unique per channel.
- Pending file paths are unique per channel.
- Historical deleted/superseded/missing/invalid rows may share old paths.
- Files leaving `active` must clear `node_id` in the same transaction.
- Directory GC may delete derived directory nodes after `node_id` has been cleared.

### Acceptance

- Migration test creates a fresh DB.
- WAL mode test confirms `journal_mode=wal`.
- Partial unique index tests cover active, pending, deleted, superseded.
- Foreign key + directory GC test covers deleted file rows with null `node_id`.

---

## Stage 4 — Path model and file tree invariants

### Goal

Make path behavior unambiguous.

### Tasks

1. Create `internal/fsmodel`.
2. Implement canonical path normalization:
   - UTF-8
   - NFC
   - POSIX separator `/`
   - leading slash
   - no empty segment
   - no `.`
   - no `..`
   - case-sensitive
3. Implement file-vs-directory conflict checks:
   - reject upload to `/a/b.txt` when active file `/a` exists
   - reject upload to `/a` when active descendant `/a/...` exists
4. Implement destination resolution for file move:
   - if `to` is active directory, move into it with source basename
   - if `to` is missing, treat as final path
   - if `to` is active file, apply conflict policy
5. Implement directory move detection:
   - if `from` is active directory, return `ERR_DIRECTORY_MOVE_UNSUPPORTED`
6. Implement virtual directory derivation.
7. Implement directory GC.

### Acceptance

- Unit tests cover every path case in `testdata/paths`.
- File/dir collision tests pass.
- `mv file existing-dir` resolves to `existing-dir/basename`.
- `mv dir x` returns `ERR_DIRECTORY_MOVE_UNSUPPORTED`.
- Empty directories disappear after full scan and this is documented.

---

## Stage 5 — Slug codec and hashtag chain

### Goal

Generate readable, collision-resistant Telegram hashtags.

### Tasks

1. Create `internal/pathcodec`.
2. Implement segment slug algorithm:
   - NFC normalize
   - Chinese segment: pinyin transliteration first
   - other Unicode: unidecode transliteration
   - non `[A-Za-z0-9]` becomes `_`
   - collapse `_`
   - trim `_`
   - fallback `x`
   - prefix max 24 characters
   - hash suffix base32(BLAKE3(segment))[0:5]
3. Implement collision detection.
4. If slug collision occurs in the same parent path, extend hash suffix to 8 chars.
5. If collision still occurs, fail with `ERR_SLUG_COLLISION`.
6. Store segment slug mappings in `path_segment_slugs`.
7. Generate shallow-to-deep hashtag chains.
8. Store per-file final tags in `path_tags`.

### Acceptance

- Tests cover Chinese, emoji, spaces, underscores, punctuation, case, repeated names.
- Collision simulation extends suffix.
- `path_segment_slugs` and `path_tags` are both populated by upload indexing.

---

## Stage 6 — Manifest and caption rendering

### Goal

Make media captions safe for Telegram.

### Tasks

1. Create `internal/manifest`.
2. Implement `UTF16Units(s string) int`.
3. Implement `FitsTelegramCaption` and `FitsTelegramText`.
4. Implement `td:v1` compact metadata renderer.
5. Implement `td-manifest:v1` full reply renderer.
6. Implement parser for both formats.
7. Implement caption rendering order:
   - display name
   - human parent path
   - compact metadata
   - shallow-to-deep hashtag chain until budget remains
8. Implement manifest fallback.
9. Implement `ERR_CAPTION_TOO_LONG` when even minimal caption exceeds budget.
10. Make examples use full hashes in production output. Ellipses are documentation-only.

### Acceptance

- UTF-16 tests cover ASCII, Chinese, BMP symbols, supplementary emoji.
- Deep path triggers manifest reply.
- Very long filename triggers `ERR_CAPTION_TOO_LONG`.
- Truncated hashtags preserve shallow tags first.
- Parser ignores unknown keys and rejects missing `p` or `n`.

---

## Stage 7 — Telegram adapter boundary and fake client

### Goal

Keep Telegram behavior testable without network access.

### Tasks

1. Create `internal/telegram` interfaces:
   - `Client`
   - `AuthClient`
   - `ChannelClient`
   - `MediaClient`
   - `HistoryClient`
2. Create `internal/telegram/fake` for integration tests.
3. Define typed errors:
   - flood wait
   - permission denied
   - message not editable
   - message not found
   - file too large
   - auth required
4. No app logic imports `gotd/td` directly except the real adapter.
5. Real adapter lives under `internal/mtproto`.

### Acceptance

- Fake adapter supports upload, edit caption, delete, reply manifest, history pagination.
- Integration tests can run without Telegram credentials.

---

## Stage 8 — Auth commands

### Goal

Implement MTProto login/session management.

### Tasks

1. Implement `td auth login`.
2. Implement `td auth status`.
3. Implement `td auth logout`.
4. Read `TD_API_ID`, `TD_API_HASH`, `TD_PHONE`.
5. Prompt for missing values.
6. Support login code and 2FA password.
7. Persist session to configured session path.
8. Redact sensitive values everywhere.

### Acceptance

- Fake auth tests cover success, phone code, 2FA, wrong code, missing config.
- Real smoke test instructions are in `docs/manual-smoke-tests.md`.
- `td auth status --json` returns authenticated user info with phone redacted.

---

## Stage 9 — Channel create, bind, share, capability doctor

### Goal

Support channel setup and runtime capability detection.

### Tasks

1. Implement `td init <local-root>`.
2. Support flags:
   - `--channel <title-or-id>`
   - `--create-channel`
   - `--bind-channel <title-or-id>`
3. Implement `td share [remote-path]`.
4. For subtree share, output invite link plus best available shallow/deep hashtag.
5. Implement `td doctor` checks:
   - auth
   - DB
   - channel accessible
   - upload
   - delete
   - invite link
   - old caption edit capability
   - file size limit
   - flood wait handling smoke test when safe
6. Cache capability results with timestamp.

### Acceptance

- Fake adapter tests cover create/bind/share.
- `td share /Pictures/2024 --json` returns `invite_link`, `path`, and `hashtag`.
- `td doctor --json` reports capability states as `pass`, `warn`, `fail`, or `unknown`.

---

## Stage 10 — Single file upload

### Goal

Upload one file safely and index it.

### Tasks

1. Implement `td cp <local> <remote-path>` for files.
2. Acquire locks:
   - `path:<channel_id>:<destination>`
   - all ancestor paths during conflict check when needed
3. Insert pending file row before Telegram upload.
4. Check file size before upload.
5. Compute MIME and BLAKE3 hash.
6. Generate slug chain.
7. Render caption/manifest.
8. Upload media.
9. If manifest reply is required, send reply.
10. If manifest reply fails, delete uploaded media when possible and mark pending as invalid/orphaned.
11. Commit active file row and tags in one DB transaction.
12. Release locks by owner token.

### Flags

- `--replace`
- `--skip-existing`
- `--auto-rename`
- `--no-hash`

### Acceptance

- Single upload works with fake adapter.
- Duplicate path fails by default.
- `--replace`, `--skip-existing`, `--auto-rename` behave as documented.
- Manifest reply failure rolls back or records repairable orphan.
- Concurrent upload to same path produces one active file.

---

## Stage 11 — ls/tree/status

### Goal

Expose the local virtual file tree.

### Tasks

1. Implement `td ls [path]` from SQLite.
2. Implement `td tree [path]` from SQLite.
3. Implement `td status`.
4. Human output uses stable columns.
5. JSON output uses contracts in `docs/contracts/json-contract.md`.

### Acceptance

- `ls` sorts dirs before files, then lexicographic name.
- `tree --depth` obeys max depth.
- Empty local-only dirs are marked `ephemeral` if shown before full scan.
- JSON snapshots pass.

---

## Stage 12 — Full scan rebuild

### Goal

Rebuild DB from Telegram messages.

### Tasks

1. Implement `td scan --full`.
2. Page through channel history.
3. Parse `td:v1` captions.
4. Fetch manifest replies when needed.
5. Upsert directories/files/tags.
6. Record invalid managed messages in `scan_errors` with dedupe key.
7. Resolve scan errors when message becomes valid or disappears.
8. Mark absent previously-active files as `missing`.
9. Rebuild slug tables.
10. Run directory GC.

### Acceptance

- Empty DB rebuild matches expected tree.
- Invalid messages do not stop default scan.
- `--strict` exits 5 on invalid managed messages.
- Repeated scans do not duplicate `scan_errors`.
- Full scan removes empty local-only directories.

---

## Stage 13 — Incremental scan

### Goal

Fast update path for new messages.

### Tasks

1. Implement default `td scan` using `scan_state`.
2. Fetch messages after last scanned marker.
3. Do not claim to detect old manual edits/deletes.
4. Add warning in human output when last full scan is old.
5. Add `td scan --full` recommendation when drift is suspected.

### Acceptance

- Incremental scan imports new messages.
- Manual old deletion is detected only after full scan.
- JSON output includes `mode: incremental`.

---

## Stage 14 — Download

### Goal

Download files and directories with local conflict policy.

### Tasks

1. Implement `td get <remote-path> <local-dest>`.
2. Implement recursive directory download with `--recursive`.
3. Add local conflict flags:
   - `--replace`
   - `--skip-existing`
   - `--auto-rename`
4. Use temp files and atomic rename.
5. Verify size and hash when available.
6. Support `--continue-on-error` for recursive download.

### Acceptance

- Existing local file fails by default.
- Replace/skip/auto-rename are tested.
- Hash mismatch deletes temp file and returns error.
- Recursive download preserves tree.

---

## Stage 15 — File move/rename

### Goal

Move or rename files without directory-level operations.

### Tasks

1. Implement `td mv <remote-from> <remote-to>`.
2. Reject directory source with `ERR_DIRECTORY_MOVE_UNSUPPORTED`.
3. Resolve destination directory semantics.
4. Acquire locks for source and destination using `path:<channel_id>:<path>`.
5. Render new metadata/caption/tags.
6. If Telegram says message is editable, edit caption and manifest reply.
7. If not editable:
   - upload/copy new indexed message when local file source is unavailable? No. V1 cannot recreate media from Telegram without download/reupload. Return `ERR_MESSAGE_NOT_EDITABLE` with repair guidance.
   - optional future path is download/reupload behind explicit `--reupload` flag; not V1.
8. Update DB in one transaction.
9. Clear old `node_id` and run directory GC.

### Acceptance

- File rename works in fake adapter.
- Destination active directory moves file into directory.
- Non-editable message returns `ERR_MESSAGE_NOT_EDITABLE`.
- Directory source returns `ERR_DIRECTORY_MOVE_UNSUPPORTED`.

---

## Stage 16 — Delete and tombstone

### Goal

Remove files according to configured delete policy.

### Tasks

1. Implement `td rm <remote-path>`.
2. Reject directories unless `--recursive` is added in a later version. V1 returns `ERR_DIRECTORY_DELETE_UNSUPPORTED`.
3. In `delete` mode, delete media message when allowed.
4. In `tombstone` mode, edit media caption and manifest reply to remove detailed metadata.
5. If tombstone edit is not possible, return `ERR_MESSAGE_NOT_EDITABLE`.
6. Clear `node_id`, update status, run directory GC.

### Acceptance

- Delete mode removes message in fake adapter.
- Tombstone mode removes manifest details from both media caption and manifest reply.
- Directory delete returns unsupported error.

---

## Stage 17 — Repair

### Goal

Recover from pending/orphaned/invalid states.

### Commands

```sh
td repair [path]
td repair --pending
td repair --orphaned
td repair --scan-errors
```

### Tasks

1. Implement pending repair:
   - pending DB rows with no Telegram message: mark invalid or retry if local file exists
   - pending DB rows with Telegram message but no manifest: attempt manifest/caption repair
2. Implement orphan repair:
   - Telegram managed media not in DB: index it or mark scan error
3. Implement scan error repair:
   - retry parsing after manual user fix
   - resolve stale errors
4. Never silently delete Telegram messages during repair unless `--delete-orphaned` is passed.

### Acceptance

- `td repair --pending` is tested.
- `td repair --orphaned` is tested.
- `td repair --scan-errors` is tested.
- Repair output is deterministic in JSON.

---

## Stage 18 — Recursive upload and crash recovery

### Goal

Support folders without a full job queue.

### Tasks

1. Implement `td cp --recursive <local-dir> <remote-dir>`.
2. Upload files in stable lexical order.
3. Apply same conflict policy per file.
4. Support `--continue-on-error`.
5. Record operation progress in DB using pending rows and operation locks.
6. On process restart, `td repair --pending` must recover or mark leftovers.
7. Empty directories are local-only and are not persisted to Telegram.

### Acceptance

- Recursive upload preserves files.
- Empty directory disappears after full scan and docs mention it.
- Crash after N files leaves repairable state.
- `td repair --pending` handles partial recursive uploads.

---

## Stage 19 — Release readiness

### Goal

Ship a production-ready binary.

### Tasks

1. Complete README quickstart.
2. Complete `docs/manual-smoke-tests.md`.
3. Complete shell completions.
4. Validate GoReleaser config:

```sh
make goreleaser-check
make snapshot
```

5. Run real Telegram smoke test with a private channel.
6. Run install script smoke test on macOS/Linux.
7. Run PowerShell installer syntax check.
8. Merge release-please PR.
9. Push tag.
10. Confirm GitHub Release assets:
   - darwin universal tar.gz
   - linux amd64 tar.gz
   - linux arm64 tar.gz
   - windows amd64 zip
   - windows arm64 zip
   - deb/rpm/apk
   - checksums.txt
   - checksum signature bundle
   - SBOM files

### Acceptance

- `td doctor` passes against a real test account/channel.
- Release workflow publishes assets.
- Install scripts install `td` and `td version` works.
- README install commands are correct.
