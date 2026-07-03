<!--
Canonical product/specification document for tg-drive-cli.
Former working filename: PLAN_FIXED_V2.md.
Renamed to PRODUCT_SPEC.md because this file defines the product and storage contract, not an implementation plan.
-->

# tg-drive-cli Product Specification

`tg-drive-cli` 是一个 Telegram-backed 文件树 CLI。它把本地目录映射为 Telegram 频道中的媒体消息，让 Telegram 原生客户端可以通过 hashtag 做路径筛选，让 CLI 可以通过 SQLite cache 提供稳定、可重建、可脚本化的文件树视图。

默认命令名：`td`

---

## Reference / Donor

- https://github.com/caamer20/Telegram-Drive
- Telegram API docs: https://core.telegram.org/api
- Telegram Bot API docs: https://core.telegram.org/bots/api

`caamer20/Telegram-Drive` 可作为 MTProto 接入、文件上传下载、频道管理的基础设施参考。

本项目的核心差异：

- 纯 CLI
- 单频道优先
- 文件夹层级通过 `td:v1` manifest + path hashtag chain 表达
- 原生 Telegram 客户端可用 hashtag 辅助筛选
- SQLite 是本地 cache，可从 Telegram 消息重建

---

## Product Goal

Telegram 的原生信息结构是：

```text
channel/group -> messages
```

本项目提供一层可恢复的虚拟文件系统：

```text
Telegram channel -> Telegram media messages -> td:v1 metadata -> SQLite index -> CLI file tree
```

用户目标：

- 本地文件夹可以上传到 Telegram
- 远程文件可以按目录结构列出
- 本地数据库丢失后可以扫描 Telegram 频道重建
- 分享一个频道即可分享整棵子树
- 分享一个子树时可以输出频道邀请链接 + 对应 hashtag
- 接收者使用任意 Telegram 客户端也能通过 hashtag 做路径筛选
- CLI 可用于备份、恢复、脚本化上传下载

---

## V1 Scope

V1 做稳定的文件级映射。

包含：

- 单频道模式
- MTProto 用户账号登录
- 频道创建、绑定、分享链接
- 单文件上传
- 递归上传
- 文件列表
- 文件树
- 单文件下载
- 递归下载
- 文件级 rename/move within same channel
- 删除或 tombstone
- full scan rebuild
- incremental scan
- native hashtag navigation helper
- SQLite WAL
- 并发写入保护
- caption budget / manifest fallback
- 超大文件前置校验
- Telegram capability doctor

---

## Future Scope

后续版本再做：

- 多频道策略
- forum topic 视觉增强
- 目录级 rename/move
- watch 本地目录变更自动上传
- 分享导入向导
- WebDAV/FUSE 层
- 去重存储
- 端到端加密层
- 多账号切换
- 可恢复批处理 job queue

目录级 rename/move 进入 V2。它需要批量编辑整棵子树的所有后代文件，涉及限流、部分成功、断点恢复和 repair job，V1 保持文件级操作。

---

## Technical Stack

Recommended implementation:

- Language: Go
- MTProto client: `gotd/td`
- Database: SQLite
- SQLite driver: pure Go driver preferred for simple cross-platform builds
- CLI framework: `cobra` or equivalent
- Config format: TOML
- Log format: human by default, JSON under `--json`
- Build target: single binary

Repository shape:

```text
tg-drive-cli/
├── cmd/td/
│   └── main.go
├── internal/
│   ├── app/
│   ├── auth/
│   ├── config/
│   ├── db/
│   ├── fsmodel/
│   ├── localfs/
│   ├── mtproto/
│   ├── pathcodec/
│   ├── manifest/
│   ├── syncscan/
│   ├── tgdrive/
│   └── workerlock/
├── docs/
│   ├── storage-contract.md
│   ├── cli-contract.md
│   ├── telegram-behavior.md
│   └── troubleshooting.md
├── testdata/
│   └── paths/
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Telegram Platform Constraints

Implementation must treat Telegram behavior as a runtime capability surface.

Known stable constraints:

- Telegram MTProto requires `api_id` and `api_hash` for third-party client applications.
- Telegram Bot API media captions use a 1024-character documented budget after entity parsing. This project uses 1024 as the conservative media caption budget even though implementation uses MTProto.
- Telegram entity offsets and lengths use UTF-16 code units. The caption renderer must count UTF-16 code units, not bytes and not Go runes.
- Telegram free users can upload files up to 2 GB. Telegram Premium users can upload files up to 4 GB.
- Hashtag taps can include global public results in modern Telegram clients. Public chat-specific hashtags can use `#tag@username` when the chat has a public username.

Capability checks required before relying on platform behavior:

- old media caption edit capability
- delete capability
- channel creation capability
- invite link creation capability
- upload size limit for current account
- flood wait handling

`td doctor` must expose capability checks instead of assuming all Telegram accounts and channel types behave the same.

---

## Telegram Storage Model

A remote file is represented by:

```text
Telegram media message
+ td:v1 machine metadata in caption when it fits
+ native hashtag chain in caption when it fits
+ optional td-manifest:v1 reply text message
```

Primary identity:

```text
channel_id
message_id
canonical_path
```

Secondary identity:

```text
content_size
content_hash
mime_type
display_name
manifest_message_id
```

Telegram messages are the recoverable source. SQLite is the local query cache and operation index.

---

## Caption Budget

Use conservative budgets:

```text
safe media caption budget: 1024 UTF-16 code units
safe text message budget: 4096 UTF-16 code units
```

Counting rule:

- Count UTF-16 code units.
- Go `len(string)` is invalid for this budget.
- Go `utf8.RuneCountInString` is invalid for this budget.
- BMP code points count as 1 code unit.
- Supplementary plane emoji count as 2 code units.
- Rendered caption must leave a small margin, default 16 code units.

Implementation API:

```go
func UTF16Units(s string) int
func FitsTelegramCaption(s string, limit int, margin int) bool
```

Caption rendering order:

1. filename
2. human-readable parent path
3. compact `td:v1` header
4. shallow-to-deep hashtag chain while budget remains

If generated content exceeds budget:

- render a minimal media caption using display name + `td:v1 manifest=reply`
- keep shallow hashtags first only when they fit after the minimal marker
- send full metadata in `td-manifest:v1` reply text message
- record `manifest_message_id`

If the minimal media caption still exceeds the safe 1024 UTF-16 code unit budget, return `ERR_CAPTION_TOO_LONG`. This can happen with extremely long filenames or clients whose effective caption limit is lower than the configured safe budget.

Hashtag truncation direction:

- Keep shallow tags first.
- Drop deepest tags first.
- Reason: root and top-level folder filtering remain useful in native Telegram.

Machine recovery must never depend only on hashtags.

---

## Storage Contract

### Canonical Path

A canonical remote path is:

```text
/Pictures/2024/06/15/Vacation/beach.jpg
```

Rules:

- UTF-8
- NFC normalized
- POSIX separator `/`
- absolute within the virtual root
- root path is `/`
- no empty path segments
- no `.` segment
- no `..` segment
- case-sensitive by default
- leading slash required in DB and internals
- command input may omit leading slash and will be normalized

Examples:

```text
td cp ./beach.jpg /Pictures/2024/beach.jpg
td cp ./beach.jpg Pictures/2024/beach.jpg
```

Both normalize to:

```text
/Pictures/2024/beach.jpg
```

### File vs Directory Conflicts

The virtual filesystem must prevent file/directory ambiguity.

Invalid states:

```text
/a is an active file
/a/b.txt is uploaded
```

```text
/a/b.txt exists
/a is uploaded as a file
```

Upload validation:

1. Normalize destination path.
2. Check whether destination is an active directory node.
3. Check every ancestor path.
4. Reject if any ancestor is an active file.
5. Reject if destination path already has active file unless conflict flag changes behavior.
6. Reject if destination path is an active directory and command expects a final file path.

Error codes:

```text
ERR_PATH_EXISTS
ERR_PATH_IS_DIRECTORY
ERR_PATH_ANCESTOR_IS_FILE
ERR_PATH_CONFLICT
```

### Display Name

`display_name` is the final path segment after NFC normalization.

Original local filename is preserved in metadata when available.

### Directory Nodes

Directories are derived from active file paths.

V1 behavior:

- Directory rows exist for fast `ls/tree`.
- Directory rows are regenerated from active file rows after scan and after write operations.
- Empty directories are not recoverable from Telegram after DB loss.
- Empty local directories are skipped by default during recursive upload.
- `td cp --recursive --include-empty-dirs` returns `ERR_EMPTY_DIRS_UNSUPPORTED` in V1.

After a file is deleted or moved:

- run directory garbage collection inside the same DB transaction
- remove derived directory nodes that have no active descendants
- keep root `/`

This keeps the DB consistent with the rule: Telegram stores files, and directories are derived.

---

## Path Hashtag Chain

### Purpose

Hashtags provide native Telegram navigation.

They are not the authoritative path encoding.

Example source:

```text
~/Pictures/2024/06/15/Vacation/beach.jpg
```

Caption excerpt:

```text
beach.jpg
Pictures/2024/06/15/Vacation/

td:v1 p=...
#td_Pictures_n4j7x
#td_Pictures_n4j7x_2024_l9p2q
#td_Pictures_n4j7x_2024_l9p2q_06_z1k8c
#td_Pictures_n4j7x_2024_l9p2q_06_z1k8c_15_t6w9m
#td_Pictures_n4j7x_2024_l9p2q_06_z1k8c_15_t6w9m_Vacation_a3d7r
```

### Hashtag Compatibility

Generated hashtag characters:

```text
A-Z
a-z
0-9
_
```

Generated hashtags must:

- start with `#td_`
- contain only safe characters after `#`
- avoid raw `/`
- avoid spaces
- avoid emoji
- avoid punctuation

### Segment Slug Codec

Path segments can contain spaces, Unicode, underscores, emoji, punctuation, and mixed case.

The hashtag segment codec must prevent practical collision and keep common non-English names recognizable.

Algorithm:

```text
input segment:
  NFC normalize
  preserve original in manifest

readable prefix:
  if Chinese Han characters are present, generate pinyin initials + pinyin syllables when library support is available
  transliterate other scripts to ASCII when library support is available
  replace remaining non [A-Za-z0-9] with _
  collapse repeated _ to single _
  trim leading/trailing _
  if empty, use x
  limit to 32 chars

hash suffix:
  base32(blake3(nfc_segment))[0:8] by default
  lowercase

slug:
  readablePrefix + "_" + hashSuffix
```

Examples:

```text
My Photos      -> My_Photos_q8k3x2ab
a_b            -> a_b_91kfz7pq
a/b            -> a_b_4p7za2nm
空 白           -> kong_bai_r8m2qa1z
文件            -> wen_jian_n7x4tp9k
📷              -> x_m2x9kp3d
06             -> 06_z1k8ca4b
```

Pinyin support is required for V1 when the chosen Go dependency is acceptable. If pinyin dependency is rejected, README must state that Chinese-only path segments use hash-heavy fallback tags.

### Collision Handling

The default suffix is 40 bits (`8 base32 chars`).

Runtime behavior:

- During `td cp`, compute slug for all path segments in the target channel.
- If a slug collision is detected for different original segments, retry with 13 base32 chars.
- If collision still exists, retry with full 26-char base32 truncated BLAKE3 digest.
- If full digest collides, return `ERR_SLUG_COLLISION`.

Persist final slug mapping:

```sql
create table path_segment_slugs (
  id integer primary key,
  channel_id integer not null references channels(id),
  original_segment text not null,
  slug text not null,
  hash_bits integer not null,
  created_at text not null,
  unique(channel_id, original_segment),
  unique(channel_id, slug)
);
```

`td doctor path-codec` runs codec tests against both fixed vectors and current DB values.

### Chain Generation

For canonical path:

```text
/Pictures/2024/06/15/Vacation/beach.jpg
```

Directory segments:

```text
Pictures
2024
06
15
Vacation
```

Generated chain:

```text
#td_<PicturesSlug>
#td_<PicturesSlug>_<2024Slug>
#td_<PicturesSlug>_<2024Slug>_<06Slug>
#td_<PicturesSlug>_<2024Slug>_<06Slug>_<15Slug>
#td_<PicturesSlug>_<2024Slug>_<06Slug>_<15Slug>_<VacationSlug>
```

File name is not included in path hashtags by default.

### Chat-Specific Hashtags

For public channels with usernames, output helper tags can include Telegram chat-specific hashtag form:

```text
#td_Pictures_n4j7x@channelusername
```

Private channels cannot rely on `@username` scoped tags. README must tell users to select current-chat/current-channel results in Telegram clients.

---

## Message Caption Format

### Normal Caption

```text
{display_name}
{human_relative_parent_path}/

td:v1 p={base64url(canonical_path)} n={base64url(display_name)} s={size} h={content_hash_or_dash} m={mime_or_dash}

{hashtag_chain}
```

Example:

```text
beach.jpg
Pictures/2024/06/15/Vacation/

td:v1 p=L1BpY3R1cmVzLzIwMjQvMDYvMTUvVmFjYXRpb24vYmVhY2guanBn n=YmVhY2guanBn s=2482911 h=blake3:6bb8f35e8a... m=image/jpeg

#td_Pictures_n4j7x2ab #td_Pictures_n4j7x2ab_2024_l9p2q8dd
```

Documentation examples may use `...` only as prose abbreviation. Production metadata must store full configured hashes.

### Manifest Reply

If media caption exceeds the safe budget, upload media with a minimal caption and reply with text manifest.

Media caption:

```text
beach.jpg
Pictures/2024/06/15/Vacation/

td:v1 manifest=reply

#td_Pictures_n4j7x2ab #td_Pictures_n4j7x2ab_2024_l9p2q8dd
```

Reply text:

```text
td-manifest:v1
p=L1BpY3R1cmVzLzIwMjQvMDYvMTUvVmFjYXRpb24vYmVhY2guanBn
n=YmVhY2guanBn
parent=L1BpY3R1cmVzLzIwMjQvMDYvMTUvVmFjYXRpb24
size=2482911
hash=blake3:6bb8f35e8a...
mime=image/jpeg
created=2026-07-02T10:15:00Z
tags=#td_Pictures_n4j7x2ab #td_Pictures_n4j7x2ab_2024_l9p2q8dd ...
```

### Manifest Parsing Rules

Parser accepts:

```text
td:v1 ...
td-manifest:v1
```

Parser ignores unknown keys.

Parser requires:

```text
p
n
```

Parser treats these as optional:

```text
size
hash
mime
created
parent
tags
```

Invalid manifests are recorded in `scan_errors`.

`td scan --strict` exits non-zero when invalid managed messages are found.

Default scan records errors and completes.

---

## Channel Strategy

### V1: Single Channel

Each initialized root maps to one Telegram channel.

Example:

```text
local root: ~/Pictures
channel: Pictures [TD]
```

All files under `~/Pictures` upload into this channel.

Path hierarchy is represented by:

```text
td:v1 metadata
SQLite nodes/files tables
#td_ hashtag chain
```

### V2: Multi-Channel

A root can map first-level subdirectories to separate channels.

Example:

```text
~/Media/Pictures -> Pictures [TD]
~/Media/Videos   -> Videos [TD]
~/Media/Music    -> Music [TD]
```

### V2: Forum Topic Enhancement

For Telegram forum-capable chats, first-level directories may map to topics.

Example:

```text
Pictures [TD]
├── Topic: 2024
├── Topic: 2025
└── General: root-level files
```

Hashtag chain and manifest remain authoritative.

Topics are visual grouping only.

---

## Auth Model

The CLI uses MTProto user login.

Required user inputs:

```text
api_id
api_hash
phone_number
login_code
2fa_password when enabled
```

Session storage:

```text
~/.config/tg-drive-cli/session.json
```

Config storage:

```text
~/.config/tg-drive-cli/config.toml
```

Database path:

```text
~/.local/share/tg-drive-cli/local_cache.db
```

Path overrides:

```text
TD_CONFIG
TD_SESSION
TD_DB
```

Auth env vars:

```text
TD_API_ID
TD_API_HASH
TD_PHONE
```

No password env var.

Auth commands:

```text
td auth login
td auth status
td auth logout
```

`td auth login` flow:

1. Read `api_id/api_hash` from flags, env, or config.
2. Prompt for missing values.
3. Connect MTProto client.
4. Send phone code.
5. Prompt for code.
6. Handle 2FA password when required.
7. Persist session.
8. Print authenticated user summary.

---

## SQLite Schema

SQLite is a local cache and operation index.

Source-of-recovery is Telegram messages.

SQLite settings:

```sql
pragma journal_mode = WAL;
pragma foreign_keys = ON;
pragma busy_timeout = 5000;
```

Schema:

```sql
create table schema_version (
  version integer primary key,
  applied_at text not null
);

create table accounts (
  id integer primary key,
  tg_user_id text not null unique,
  phone text,
  display_name text,
  created_at text not null,
  updated_at text not null
);

create table channels (
  id integer primary key,
  account_id integer not null references accounts(id),
  tg_channel_id text not null,
  access_hash text,
  title text not null,
  username text,
  invite_link text,
  root_local_path text not null,
  root_remote_path text not null default '/',
  strategy text not null default 'single',
  capabilities_json text,
  created_at text not null,
  updated_at text not null,
  unique(account_id, tg_channel_id)
);

create table nodes (
  id integer primary key,
  channel_id integer not null references channels(id),
  canonical_path text not null,
  parent_path text,
  display_name text not null,
  type text not null check(type in ('dir', 'file')),
  derived_from_active_files integer not null default 1,
  created_at text not null,
  updated_at text not null,
  unique(channel_id, canonical_path)
);

create table files (
  id integer primary key,
  channel_id integer not null references channels(id),
  node_id integer references nodes(id) on delete set null,
  message_id integer,
  manifest_message_id integer,
  canonical_path text not null,
  display_name text not null,
  original_local_path text,
  size integer,
  content_hash text,
  mime text,
  caption_version integer not null default 1,
  status text not null check(status in ('pending', 'active', 'deleted', 'superseded', 'missing', 'invalid', 'orphaned')),
  error_code text,
  uploaded_at text,
  updated_at text not null,
  unique(channel_id, message_id)
);

create unique index idx_files_active_path
  on files(channel_id, canonical_path)
  where status = 'active';

create unique index idx_files_pending_path
  on files(channel_id, canonical_path)
  where status = 'pending';

create table path_tags (
  id integer primary key,
  file_id integer not null references files(id) on delete cascade,
  tag text not null,
  depth integer not null,
  unique(file_id, tag)
);

-- path_tags stores the final generated tag chain per file for reverse lookup,
-- scan/debug output, and optional native-share helpers.
-- path_segment_slugs stores reusable segment-to-slug mappings and does not replace path_tags.

create table path_segment_slugs (
  id integer primary key,
  channel_id integer not null references channels(id),
  original_segment text not null,
  slug text not null,
  hash_bits integer not null,
  created_at text not null,
  unique(channel_id, original_segment),
  unique(channel_id, slug)
);

create table scan_state (
  id integer primary key,
  channel_id integer not null references channels(id),
  last_scanned_message_id integer,
  last_full_scan_at text,
  updated_at text not null,
  unique(channel_id)
);

create table scan_errors (
  id integer primary key,
  channel_id integer not null references channels(id),
  message_id integer,
  error_code text not null,
  error_message text not null,
  raw_excerpt text,
  status text not null check(status in ('pending', 'resolved', 'ignored')) default 'pending',
  first_seen_at text not null,
  last_seen_at text not null,
  resolved_at text,
  unique(channel_id, message_id, error_code)
);

create table operation_locks (
  id integer primary key,
  lock_key text not null unique,
  owner_pid integer,
  owner_token text not null,
  created_at text not null,
  expires_at text not null
);
```

Indexes:

```sql
create index idx_nodes_channel_parent on nodes(channel_id, parent_path);
create index idx_nodes_channel_type on nodes(channel_id, type);
create index idx_files_channel_status on files(channel_id, status);
create index idx_files_channel_path on files(channel_id, canonical_path);
create index idx_path_tags_tag on path_tags(tag);
create index idx_scan_errors_status on scan_errors(channel_id, status);
```

### Node/File Synchronization

Write contract:

- `files.canonical_path` is the authoritative indexed file path.
- `nodes` is regenerated from active `files` rows when needed.
- File write operations update `files`, then regenerate affected `nodes` inside one transaction.
- `node_id` is nullable and uses `ON DELETE SET NULL`. Directory GC may remove derived nodes even when old non-active file history still exists.
- When a file leaves `active`, the same transaction should set `files.node_id = null` where practical before directory GC runs. The foreign key is a safety fallback.
- Full scan truncates and rebuilds `nodes` from active files.

This avoids long-term divergence between duplicated path fields and prevents non-active file history from blocking directory garbage collection.

---

## Conflict Policy

Default behavior:

```text
td cp local remote
```

fails if `remote` already exists.

Supported flags:

```text
--replace
--skip-existing
--auto-rename
```

Rules:

- `--replace`: upload new message, mark old active file `superseded`, promote new file to `active`, optionally delete old Telegram message under config.
- `--skip-existing`: leave existing remote file unchanged.
- `--auto-rename`: keep existing file and choose a generated destination name for the new upload.

Generated conflict names:

```text
beach.jpg
beach (1).jpg
beach (2).jpg
archive.tar.gz
archive (1).tar.gz
```

Compound extension rule:

- Known compound extensions stay together.
- Default list: `.tar.gz`, `.tar.bz2`, `.tar.xz`, `.tar.zst`, `.user.js`, `.min.js`, `.d.ts`.
- Other names use the final extension.

Conflict comparison uses canonical path.

Content hash can detect equal content, but path conflict remains path conflict.

---

## Delete Policy

Default V1 behavior:

```text
td rm /path/file
```

deletes the Telegram media message when the account has permission.

Configurable mode:

```toml
[delete]
mode = "delete" # delete | tombstone
```

`delete` mode:

- delete Telegram media message
- delete manifest reply when present
- mark DB row `deleted`

`tombstone` mode:

- edit media caption to include `td:deleted` when edit is supported
- edit manifest reply to redacted tombstone when present
- mark DB row `deleted`
- if edit is not supported, return `ERR_MESSAGE_NOT_EDITABLE`

Tombstone media caption:

```text
{display_name}

td:v1 deleted=true p={base64url(canonical_path)}
```

Tombstone manifest reply:

```text
td-manifest:v1
deleted=true
p={base64url(canonical_path)}
```

Delete fallback:

- If delete permission is missing and mode is `delete`, return `ERR_CHANNEL_PERMISSION`.
- The CLI must not silently downgrade to tombstone.
- User may rerun with `--tombstone`.

`td scan` ignores deleted/tombstoned files by default.

`td scan --include-deleted` records tombstones.

---

## Move / Rename Policy

V1 supports file-level same-channel move/rename.

Command:

```text
td mv <remote-from> <remote-to>
```

### File-to-File Move

```text
td mv /Pictures/a.jpg /Pictures/b.jpg
```

Behavior:

1. Load active file by canonical source path.
2. Validate destination path.
3. Check file-vs-directory conflicts.
4. Check conflict policy.
5. Generate new manifest/caption.
6. If message is editable, edit media caption and manifest reply.
7. If message is not editable and delete/copy fallback is enabled, upload replacement message and mark old row `superseded`.
8. Update SQLite rows in one transaction.

### File-to-Directory Move

If destination is an existing active directory:

```text
td mv /Pictures/a.jpg /Archive
```

V1 treats it as:

```text
/Archive/a.jpg
```

If destination directory does not exist, `to` is treated as final path.

### Directory Move

V1 returns:

```text
ERR_DIRECTORY_MOVE_UNSUPPORTED
```

Example:

```text
td mv /Pictures/2024 /Pictures/2024-archived
```

V2 will implement directory move as resumable batch operation.

### Edit Capability

`td mv` must not assume old messages can be edited forever.

Capability-based behavior:

- If edit succeeds, message ID remains stable.
- If edit fails with an edit-window or not-editable error, return `ERR_MESSAGE_NOT_EDITABLE` unless fallback is explicitly enabled.
- If fallback is enabled, upload a new media message under destination path, then delete or supersede the old message according to config.

Error codes:

```text
ERR_MESSAGE_NOT_EDITABLE
ERR_MESSAGE_EDIT_EXPIRED
ERR_CROSS_CHANNEL_MOVE
ERR_DIRECTORY_MOVE_UNSUPPORTED
```

---

## Upload Algorithm

Command:

```text
td cp <local> <remote-path>
```

Steps:

1. Resolve local path.
2. Stat file.
3. Reject directories unless `--recursive`.
4. Detect current account upload limit.
5. Return `ERR_FILE_TOO_LARGE` when file exceeds current account limit.
6. Normalize remote path.
7. Validate canonical path.
8. Check file-vs-directory conflicts.
9. Acquire DB operation lock for `(channel_id, canonical_path)`.
10. Insert `pending` file row inside transaction.
11. Compute MIME.
12. Compute content hash when enabled.
13. Generate manifest.
14. Generate hashtag chain.
15. Render caption using UTF-16 code unit budget.
16. Upload file to Telegram channel.
17. If needed, send manifest reply.
18. Promote DB row from `pending` to `active` inside transaction.
19. Insert generated per-file hashtag chain into `path_tags`.
20. Regenerate affected directory nodes.
21. Print result.

Failure handling:

- If Telegram upload fails before media message exists, delete pending row.
- If media upload succeeds and manifest reply fails, attempt to delete media message.
- If rollback delete succeeds, delete pending row and return original error.
- If rollback delete fails, mark row `orphaned` with message_id and return `ERR_ORPHANED_UPLOAD`.
- `td repair --orphaned` handles orphaned uploads.

Default output:

```text
uploaded /Pictures/2024/beach.jpg
```

JSON output:

```json
{
  "ok": true,
  "path": "/Pictures/2024/beach.jpg",
  "channel_id": "123456789",
  "message_id": 8821,
  "manifest_message_id": null,
  "size": 2482911,
  "hash": "blake3:6bb8f35e8a..."
}
```

Recursive upload:

```text
td cp --recursive ./Pictures /Pictures
```

Rules:

- preserve relative subtree
- upload files in stable lexical order
- skip empty directories in V1
- support `--continue-on-error`
- summarize failures at end

---

## Download Algorithm

Command:

```text
td get <remote-path> <local-dest>
```

Steps:

1. Normalize remote path.
2. Load file from SQLite.
3. Resolve Telegram channel/message.
4. Check local destination conflict.
5. Download media to temp path.
6. Verify size/hash when available.
7. Move temp path into destination.
8. Print result.

Local conflict flags:

```text
--replace
--skip-existing
--auto-rename
```

Default behavior fails with:

```text
ERR_LOCAL_PATH_EXISTS
```

Recursive directory download:

```text
td get --recursive /Pictures ./restore
```

Rules:

- query SQLite for descendants
- preserve relative paths
- create directories locally
- support `--continue-on-error`

---

## Scan Algorithm

Command:

```text
td scan [remote-root]
```

Purpose:

- rebuild SQLite from Telegram messages
- recover after DB loss
- detect drift
- repair index

Full scan:

```text
td scan --full
```

Incremental scan:

```text
td scan
```

Full scan behavior:

1. Resolve configured channel.
2. Iterate message history with MTProto pagination.
3. For each message:
   - inspect media message
   - parse caption
   - detect `td:v1`
   - fetch manifest reply when needed and possible
   - parse canonical path
   - validate path
   - parse hashtags
   - upsert slug mappings
   - upsert file row
   - upsert or refresh `scan_errors`
4. Mark previously indexed active files as `missing` when absent from full scan.
5. Rebuild `nodes` from active files.
6. Mark resolved scan errors when the message now parses cleanly.
7. Update `scan_state`.

Incremental scan behavior:

- reads messages newer than `last_scanned_message_id`
- catches new uploads and new managed messages
- does not detect old messages manually edited or deleted in Telegram
- user must run `td scan --full` to detect old-message drift

Strict mode:

```text
td scan --strict
```

Strict mode exits non-zero when invalid managed messages are found.

Repair mode:

```text
td scan --repair
```

Repair mode can:

- regenerate missing local nodes from active files
- clear resolved scan errors
- repair DB-only inconsistencies

Repair mode cannot automatically repair a Telegram message whose canonical path cannot be parsed. The user must manually delete or reupload that message.

Scan output:

```text
scanned channel Pictures [TD]
files: 1240 active, 0 deleted, 3 invalid, 0 missing
```

JSON output:

```json
{
  "ok": true,
  "channel": "Pictures [TD]",
  "active": 1240,
  "deleted": 0,
  "invalid": 3,
  "missing": 0
}
```

---

## Repair Command

Command:

```text
td repair [path]
td repair --orphaned
td repair --pending
td repair --scan-errors
```

Purpose:

- fix orphaned uploads after partial Telegram success
- inspect, clear, or retry stale pending rows
- regenerate captions/manifests for known paths
- clean resolved scan errors
- report unrepairable invalid messages

Rules:

- `td repair <path>` requires a parsed canonical path.
- Messages without parseable `td:v1` path are reported with Telegram message ID and manual action.
- Repair actions that edit old messages must pass edit capability checks.
- Repair never deletes Telegram messages unless `--delete-orphans` is passed.

---

## List and Tree

`td ls` and `td tree` read SQLite by default.

### `td ls`

```text
td ls /
td ls /Pictures/2024
td ls --json /Pictures/2024
```

Human output:

```text
DIR  2024/
DIR  2025/
FILE beach.jpg  2.4 MB
```

JSON output:

```json
{
  "path": "/Pictures/2024",
  "entries": [
    {
      "type": "dir",
      "name": "06",
      "path": "/Pictures/2024/06"
    },
    {
      "type": "file",
      "name": "beach.jpg",
      "path": "/Pictures/2024/beach.jpg",
      "size": 2482911
    }
  ]
}
```

### `td tree`

```text
td tree /
td tree /Pictures --depth 3
```

Human output:

```text
/
└── Pictures
    └── 2024
        └── 06
            └── beach.jpg
```

JSON output:

```json
{
  "path": "/",
  "children": [
    {
      "type": "dir",
      "name": "Pictures",
      "path": "/Pictures"
    }
  ]
}
```

---

## Share UX

Command:

```text
td share [remote-path]
```

Whole channel:

```text
td share
```

Subtree helper:

```text
td share /Pictures/2024
```

Output includes:

- channel invite link
- recommended hashtag for the subtree
- chat-specific hashtag when public username is available
- instruction to choose current chat/channel search results in Telegram clients

Example:

```text
Channel: Pictures [TD]
Invite: https://t.me/+...
Filter: #td_Pictures_n4j7x2ab_2024_l9p2q8dd

Open the channel, then search or tap the filter tag. In clients that show global hashtag results, choose the current channel/chat tab.
```

---

## Native Telegram UX

Each uploaded file appears as a normal Telegram media message.

Caption shows:

```text
filename
human-readable folder
td:v1 metadata
#td_ path tags
```

User can:

- browse messages chronologically
- search generated hashtags
- click hashtags in Telegram clients
- download files directly
- share the channel invite link

Accuracy statement:

- CLI provides exact tree behavior.
- Telegram native clients provide hashtag filtering.
- Modern Telegram clients can show global public hashtag results.
- Users should select the current-chat/current-channel result tab when available.
- Public channels with usernames may use `#tag@username` scoped tags.

---

## CLI Contract

Global flags:

```text
--config <path>
--db <path>
--session <path>
--json
--quiet
--verbose
--channel <name-or-id>
--wait
--no-wait
```

Commands:

```text
td auth login
td auth status
td auth logout

td init <local-root>
td status
td scan
td repair [path]
td ls [remote-path]
td tree [remote-path]
td cp <local> <remote-path>
td get <remote-path> <local-dest>
td mv <remote-from> <remote-to>
td rm <remote-path>
td share [remote-path]
td config get [key]
td config set <key> <value>
td doctor
```

### `td init`

```text
td init ~/Pictures
td init ~/Pictures --channel "Pictures [TD]"
td init ~/Pictures --create-channel
td init ~/Pictures --bind-channel "Pictures [TD]"
```

Behavior:

- create config if missing
- create or bind Telegram channel
- insert channel mapping
- run initial scan
- print local root and channel summary

### `td status`

Prints:

- auth status
- configured roots
- channels
- DB path
- last scan time
- pending scan errors count
- orphaned upload count
- current account upload size limit if known

### `td doctor`

Checks:

- config file
- session file
- DB connectivity
- SQLite WAL
- Telegram auth
- channel accessibility
- upload permission
- delete permission
- current-account upload size limit
- current-channel invite link ability
- current-channel old caption edit behavior
- caption UTF-16 counter
- path codec collision tests
- scan pagination smoke test

`td doctor edit-window` must create a small test message, edit it immediately, and report immediate edit capability. Testing multi-day edit windows requires user-provided existing message or manual test mode:

```text
td doctor edit-window --message-id <id>
```

---

## Error Codes

All JSON errors use this shape:

```json
{
  "ok": false,
  "error": {
    "code": "ERR_PATH_EXISTS",
    "message": "remote path already exists",
    "details": {
      "path": "/Pictures/beach.jpg"
    }
  }
}
```

Required error codes:

```text
ERR_AUTH_REQUIRED
ERR_CONFIG_MISSING
ERR_CHANNEL_NOT_FOUND
ERR_CHANNEL_PERMISSION
ERR_PATH_INVALID
ERR_PATH_EXISTS
ERR_PATH_IS_DIRECTORY
ERR_PATH_ANCESTOR_IS_FILE
ERR_PATH_CONFLICT
ERR_LOCAL_NOT_FOUND
ERR_LOCAL_PATH_EXISTS
ERR_REMOTE_NOT_FOUND
ERR_CAPTION_TOO_LONG
ERR_MANIFEST_INVALID
ERR_FILE_TOO_LARGE
ERR_MESSAGE_NOT_EDITABLE
ERR_MESSAGE_EDIT_EXPIRED
ERR_CROSS_CHANNEL_MOVE
ERR_DIRECTORY_MOVE_UNSUPPORTED
ERR_SCAN_FAILED
ERR_TELEGRAM_RATE_LIMITED
ERR_TELEGRAM_RPC
ERR_DB
ERR_SLUG_COLLISION
ERR_ORPHANED_UPLOAD
ERR_EMPTY_DIRS_UNSUPPORTED
ERR_OPERATION_LOCKED
```

Exit code mapping:

```text
0 success
1 general error
2 usage error
3 auth/config error
4 remote Telegram error
5 scan/index error
6 local filesystem error
7 conflict error
```

Error-to-exit mapping:

```text
ERR_AUTH_REQUIRED             -> 3
ERR_CONFIG_MISSING            -> 3
ERR_CHANNEL_NOT_FOUND         -> 4
ERR_CHANNEL_PERMISSION        -> 4
ERR_PATH_INVALID              -> 2
ERR_PATH_EXISTS               -> 7
ERR_PATH_IS_DIRECTORY         -> 7
ERR_PATH_ANCESTOR_IS_FILE     -> 7
ERR_PATH_CONFLICT             -> 7
ERR_LOCAL_NOT_FOUND           -> 6
ERR_LOCAL_PATH_EXISTS         -> 7
ERR_REMOTE_NOT_FOUND          -> 5
ERR_CAPTION_TOO_LONG          -> 2
ERR_MANIFEST_INVALID          -> 5
ERR_FILE_TOO_LARGE            -> 2
ERR_MESSAGE_NOT_EDITABLE      -> 4
ERR_MESSAGE_EDIT_EXPIRED      -> 4
ERR_CROSS_CHANNEL_MOVE        -> 2
ERR_DIRECTORY_MOVE_UNSUPPORTED -> 2
ERR_SCAN_FAILED               -> 5
ERR_TELEGRAM_RATE_LIMITED     -> 4
ERR_TELEGRAM_RPC              -> 4
ERR_DB                        -> 5
ERR_SLUG_COLLISION            -> 5
ERR_ORPHANED_UPLOAD           -> 5
ERR_EMPTY_DIRS_UNSUPPORTED    -> 2
ERR_OPERATION_LOCKED          -> 7
```

---

## Config Contract

Default config path:

```text
~/.config/tg-drive-cli/config.toml
```

Example:

```toml
[telegram]
api_id = 123456
api_hash = "..."

[storage]
db_path = "~/.local/share/tg-drive-cli/local_cache.db"
session_path = "~/.config/tg-drive-cli/session.json"

[caption]
safe_media_caption_utf16_units = 1024
safe_text_message_utf16_units = 4096
caption_margin_utf16_units = 16
max_hashtags_in_caption = 32

[hash]
enabled = true
algorithm = "blake3"
stored_hash = "full"

[slug]
default_hash_base32_chars = 8
collision_hash_base32_chars = 13
transliterate_cjk = true

[delete]
mode = "delete"

[rate_limit]
default_wait = false
max_wait_seconds = 300

[concurrency]
sqlite_busy_timeout_ms = 5000
operation_lock_ttl_seconds = 600
pending_row_ttl_seconds = 3600

[[roots]]
local_path = "~/Pictures"
remote_path = "/"
channel_title = "Pictures [TD]"
strategy = "single"
```

Environment override precedence:

```text
CLI flags > TD_* environment variables > config file > defaults
```

Environment variables:

```text
TD_CONFIG
TD_SESSION
TD_DB
TD_API_ID
TD_API_HASH
TD_PHONE
TD_JSON
TD_VERBOSE
TD_WAIT
```

---

## Security and Privacy

Sensitive values:

- session authorization data
- `api_hash`
- phone number
- local paths
- invite links
- content hashes

Redaction is global across:

- `td status`
- `td config get`
- logs
- JSON errors
- verbose output unless `--show-secrets` is explicitly passed

`--show-secrets` must require interactive confirmation unless `--yes` is passed.

File permissions:

- POSIX: config/session/db files use user-only permissions where possible.
- Windows: use user-scoped application data directory and Windows ACLs where possible.
- DB file gets same privacy treatment as session-adjacent metadata because it stores local paths and file structure.

Telegram storage warning:

- Uploaded files are stored in Telegram cloud.
- Channel members can view and download files.
- Hashtags and captions expose path names unless users choose encrypted/archive workflows in future versions.

Tombstone privacy:

- Tombstone must redact both media caption and manifest reply.
- If manifest reply cannot be edited or deleted, return a visible warning and non-zero exit unless user passes `--allow-stale-manifest`.

---

## Rate Limits and Reliability

Telegram RPCs can rate limit.

Implementation must:

- detect flood wait errors
- respect `--wait` when safe
- fail with `ERR_TELEGRAM_RATE_LIMITED` when wait is disabled
- retry transient network errors with bounded exponential backoff
- use temp files for downloads
- write SQLite changes in transactions
- commit DB active state after Telegram upload and manifest handling succeed
- preserve orphaned upload records when rollback cannot delete uploaded media

`--wait` behavior:

```text
--wait     sleep through Telegram flood wait up to configured max
--no-wait  fail immediately on flood wait
```

Default controlled by config:

```toml
[rate_limit]
default_wait = false
max_wait_seconds = 300
```

---

## Concurrency and Atomicity

Multiple `td` processes may run against the same DB.

Required behavior:

- SQLite WAL enabled.
- `busy_timeout` configured.
- Writes run in short transactions.
- Upload/move/delete acquire operation locks by canonical path, using one namespace for every operation type.
- Pending row prevents duplicate concurrent uploads to the same path.
- Stale pending rows can be repaired or cleared by `td repair --pending`.

Canonical path lock key:

```text
path:<channel_id>:<canonical_path>
```

Examples:

```text
td cp X /target.jpg        -> path:<channel_id>:/target.jpg
td mv /a.jpg /target.jpg   -> path:<channel_id>:/a.jpg and path:<channel_id>:/target.jpg
td rm /target.jpg          -> path:<channel_id>:/target.jpg
```

Lock ordering:

- compute all touched canonical path locks before Telegram-side mutation
- sort lock keys lexicographically
- acquire in sorted order
- keep locks until DB state has been committed after Telegram mutation
- release locks only when the local indexed state matches the intended Telegram state

Stale lock acquisition:

- Each process generates an `owner_token` for the operation.
- To acquire a lock, insert `(lock_key, owner_pid, owner_token, created_at, expires_at)`.
- If insertion conflicts and `expires_at` is in the future, fail with a lock-busy error.
- If insertion conflicts and `expires_at` is in the past, steal the lock by deleting or replacing it in the same immediate transaction.
- A process may only release a lock when both `lock_key` and `owner_token` match.
- Lock TTL must exceed the expected local DB transaction window. Long Telegram uploads should refresh the lock before expiry.

Crash recovery:

- pending rows older than configured TTL are reported by `td status`
- stale operation locks are reported by `td status`
- `td repair --pending` inspects and resolves stale pending rows
- `td repair --pending` may clear expired operation locks with no matching live process

---

## Testing Contract

Unit tests:

- path normalization
- file-vs-directory conflict detection
- path slug codec
- Chinese pinyin slug generation
- hashtag chain generation
- slug collision extension
- UTF-16 caption counting
- caption rendering under budget
- manifest fallback
- manifest parser
- SQLite upsert
- `files.node_id on delete set null` behavior under directory GC
- partial unique active path constraint
- scan_errors upsert/resolution
- path_tags insertion and reverse lookup
- operation lock acquisition, stale-lock steal, and owner-token release guard
- conflict policy
- local download conflict policy
- JSON error shape
- exit code mapping

Golden tests:

```text
testdata/paths/simple.txt
testdata/paths/unicode.txt
testdata/paths/emoji.txt
testdata/paths/underscore-collision.txt
testdata/paths/deep-path.txt
testdata/paths/file-dir-conflict.txt
testdata/paths/compound-extensions.txt
```

Required path cases:

```text
/Pictures/beach.jpg
/Pictures/2024/06/beach.jpg
/a_b/c.txt
/a/b_c.txt
/a/b/c.txt
/空 白/文件.txt
/emoji/📷.jpg
/dots/file.name.with.dots.tar.gz
/deep/01/02/03/04/05/06/07/08/09/10/file.txt
```

Integration tests with fake MTProto adapter:

- upload single file
- upload deep path requiring manifest reply
- manifest reply failure rollback
- orphaned upload recording
- scan rebuilds DB from fake history
- scan error upsert avoids duplicates
- scan resolves fixed errors
- move editable message
- move non-editable message returns correct error
- delete mode
- tombstone mode with manifest reply redaction
- file-too-large rejection
- concurrent uploads to same path
- upload and move racing for the same destination path
- stale operation lock takeover
- local download conflict flags
- permission denial

Crash-recovery tests:

- recursive upload killed after some files commit and some remain pending
- recursive upload killed during Telegram upload before DB promotion
- recursive download killed with temp files present
- scan killed mid-run and rerun with consistent final index
- repair resolves stale pending rows without losing active files

Manual smoke tests:

```text
td auth login
td init ./testdata/local --create-channel
td doctor
td cp ./testdata/local/a.txt /a.txt
td ls /
td tree /
td get /a.txt ./restore/a.txt
td mv /a.txt /b.txt
td rm /b.txt
td scan --full
td repair --scan-errors
```

Telegram behavior manual tests:

```text
td doctor edit-window
td doctor edit-window --message-id <old_channel_message_id>
td doctor upload-limit
td doctor hashtag-scope
```

---

## V1 Acceptance

The project is ready when these pass:

- `td auth login` completes MTProto login with phone code and 2FA.
- `td init <path>` creates or binds one Telegram channel.
- `td cp <local> <remote-path>` uploads files and writes `td:v1` metadata.
- Caption renderer counts UTF-16 code units.
- Deep paths that exceed media caption budget round-trip through manifest reply.
- Manifest reply failure rolls back or records orphaned state.
- `td scan --full` rebuilds SQLite from an empty local database.
- Incremental scan documents and preserves its old-message drift limitation.
- `td ls` and `td tree` read SQLite and match uploaded paths exactly.
- Paths containing spaces, Unicode, Chinese, `_`, `-`, `.`, emoji, and similar slug shapes round-trip without collision.
- File/directory path conflicts are rejected.
- Directory GC does not fail because of non-active file history.
- `path_tags` contains generated per-file hashtags after upload and scan.
- Concurrent upload/move/delete operations on the same path use the same `path:` lock namespace.
- `td get` downloads a file and verifies size/hash when available.
- Local download conflicts use `--replace`, `--skip-existing`, or `--auto-rename`.
- `td mv` supports file-level same-channel moves.
- `td mv` treats existing directory destination as move-into-directory.
- `td mv` rejects directory moves with `ERR_DIRECTORY_MOVE_UNSUPPORTED`.
- `td mv` rejects cross-channel moves with `ERR_CROSS_CHANNEL_MOVE`.
- `td rm` applies configured delete policy and handles manifest replies.
- `td cp` rejects files above current account upload limit.
- Native Telegram clients can search generated `#td_` tags for folder-level filtering.
- README explains global hashtag results and current-channel filtering.
- `td share /subtree` prints invite link + subtree hashtag.
- `td doctor` reports auth, DB, channel, upload, edit, delete, caption, file-size, and scan status.
- All JSON outputs are stable and documented.
- All listed error codes map to documented exit codes.

---

## Build Order

Implement in this order:

1. CLI shell, config loading, global redaction, JSON error format.
2. SQLite schema, WAL setup, migrations.
3. Path normalization and file-vs-directory conflict model.
4. UTF-16 code unit counter.
5. Segment slug codec with Chinese transliteration and collision extension.
6. Manifest renderer/parser.
7. Caption renderer with 1024 UTF-16 unit budget and reply fallback.
8. MTProto auth login/status/logout.
9. Channel create/bind/share.
10. Doctor skeleton and capability checks.
11. Upload single file with pending row and lock.
12. Manifest reply failure rollback/orphan handling.
13. SQLite indexing and derived directory regeneration.
14. `ls` and `tree`.
15. Full scan rebuild.
16. Incremental scan.
17. Download single file.
18. File-level move/rename by edit with capability-based failure.
19. Delete/tombstone with manifest handling.
20. Recursive upload/download with `--continue-on-error`.
21. Repair command.
22. Integration tests with fake MTProto adapter.
23. Manual Telegram smoke tests.
24. README and docs finalization.

---

## README Requirements

README must include:

- what the tool does
- Telegram account/API requirements
- install/build instructions
- auth setup
- quickstart
- command examples
- storage model
- caption/manifest model
- native Telegram hashtag navigation example
- global hashtag search caveat
- file size limits
- old-message edit capability caveat
- empty directory limitation
- privacy warning
- troubleshooting
- V1 limitations

Quickstart example:

```text
td auth login
td init ~/Pictures --create-channel
td cp ~/Pictures/beach.jpg /2024/beach.jpg
td ls /
td tree /
td share /2024
```

---

## Current Limitations

V1 uses single-channel storage.

V1 supports file-level move/rename only.

Empty directories are not recoverable from Telegram after DB loss because Telegram stores file messages and optional manifest replies, not standalone directory objects.

Telegram native clients show files as messages, with hashtag-based filtering. Exact folder tree behavior is provided by CLI through SQLite.

Modern Telegram clients may show global hashtag results. Users should select current-chat/current-channel results when filtering a tg-drive channel.

Old Telegram messages may fail caption edit depending on account, chat type, permissions, and platform behavior. `td doctor` must test relevant capability, and `td mv` must handle edit failure explicitly.

Forum topics, multi-channel storage, watch mode, directory-level moves, advanced sync, and E2EE archive mode are future work.
