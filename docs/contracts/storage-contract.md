# Storage contract

SQLite is a cache and operation index. Telegram messages are the recoverable source.

## Required pragmas

```sql
pragma foreign_keys = ON;
pragma journal_mode = WAL;
pragma busy_timeout = 5000;
```

The pragmas ride in the connection DSN so **every pooled connection** enforces
them (executing them once on a single connection leaves foreign keys off on
the rest of the pool). Write transactions begin `IMMEDIATE`, so concurrent
processes queue on the busy timeout instead of failing mid-transaction on a
deferred-to-write upgrade. The connection pool is capped (4 connections) — a
CLI needs a handful of concurrent statements and WAL allows one writer.

## Versioned migrations

`schema_version` records every applied migration; each migration runs in one
transaction together with its version row, so a database can never be left
half-migrated.

- Version 1: base schema below.
- Version 2: rebuilds `upload_progress` with a foreign key
  (`file_id references files(id) on delete cascade`, plus a separate
  `telegram_file_id` column for Telegram's client-chosen big-file id), drops
  state rows that no longer reference a file row, and adds
  `scan_state.checkpoint_message_id` / `scan_state.full_scan_started_at`.

Existing databases upgrade in place on open. Fresh databases apply all
versions in order.

## Schema

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
  derived integer not null default 1,
  ephemeral integer not null default 0,
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
  uploaded_at text,
  updated_at text not null
);

create unique index idx_files_active_path
  on files(channel_id, canonical_path)
  where status = 'active';

create unique index idx_files_pending_path
  on files(channel_id, canonical_path)
  where status = 'pending';

create unique index idx_files_channel_message
  on files(channel_id, message_id)
  where message_id is not null;

create index idx_nodes_channel_parent on nodes(channel_id, parent_path);
create index idx_nodes_channel_type on nodes(channel_id, type);
create index idx_files_channel_status on files(channel_id, status);
create index idx_files_channel_path on files(channel_id, canonical_path);
create index idx_path_tags_tag on path_tags(tag);
create index idx_scan_errors_status on scan_errors(channel_id, status);

create table path_segment_slugs (
  id integer primary key,
  channel_id integer not null references channels(id),
  parent_canonical_path text not null,
  segment text not null,
  slug text not null,
  hash_len integer not null,
  created_at text not null,
  unique(channel_id, parent_canonical_path, segment),
  unique(channel_id, parent_canonical_path, slug)
);

create table path_tags (
  id integer primary key,
  file_id integer not null references files(id) on delete cascade,
  tag text not null,
  depth integer not null,
  unique(file_id, tag)
);

create table operation_locks (
  key text primary key,
  owner_token text not null,
  acquired_at text not null,
  expires_at text not null
);

create table scan_state (
  id integer primary key,
  channel_id integer not null references channels(id),
  last_scanned_message_id integer,
  last_full_scan_at text,
  checkpoint_message_id integer,
  full_scan_started_at text,
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
  status text not null check(status in ('pending', 'resolved')),
  first_seen_at text not null,
  last_seen_at text not null,
  resolved_at text,
  unique(channel_id, message_id, error_code)
);

create table upload_progress (
  key text primary key,
  file_id integer not null references files(id) on delete cascade,
  telegram_file_id integer not null default 0,
  content_hash text,
  part_size integer not null,
  total_parts integer not null,
  total_bytes integer not null,
  confirmed_parts text not null,
  confirmed_bytes integer not null default 0,
  updated_at text not null
);
```

## Operation locks

Use one namespace for all path-touching operations:

```text
path:<channel_id>:<canonical_path>
```

Acquisition is a single atomic conditional write: the winner inserts or takes
over a stale row (`expires_at < now`) with its own `owner_token`; the loser
receives the typed `ERR_OPERATION_LOCKED` error, never a raw database-busy
failure. A process may release only locks with its own `owner_token`.

Long operations (uploads, moves, deletes, imports, album rewrites) hold their
locks with **heartbeat renewal**: the holder renews at one third of the TTL
for the duration of the work, so an operation legitimately longer than the
TTL still excludes concurrent mutators. If renewal discovers the lock was
taken over, the operation aborts instead of continuing unprotected. Locks
are released on a background context, so cancelling a command (Ctrl-C) cannot
strand a path for the remaining TTL.

## Directory GC

When a file leaves `active`, clear `files.node_id` in the same transaction. Then remove derived directory nodes that have no active descendants. Directory GC runs as a single transaction.

## Reconciliation precedence

Telegram is the source of truth, and contradictory Telegram state resolves in
a fixed order during `td scan --full`:

1. A media caption that carries machine metadata (including a tombstone
   `td:v1 deleted=true`) always wins over the manifest reply, whatever the
   reply still claims. A tombstoned file therefore stays deleted even when its
   manifest reply could not be redacted.
2. The manifest reply (`td-manifest:v1`) wins only when the media caption
   carries no machine metadata of its own.
3. A missing or corrupt `td-album:v1` inventory is a scan error
   (`ERR_ALBUM_INVENTORY_INVALID`), parity with per-file manifests: the whole
   album can never silently vanish from the index.
4. Duplicate claims on one path resolve newest-message-wins; the older
   duplicate is recorded as a scan error (also within one commit chunk).

Slug assignment during scans is deterministic (message-id order — the same
chronological order uploads are assigned in), so a rebuilt index reproduces
the original tag chains and previously shared hashtag links keep working.

The full-scan finalizer (marking unseen rows `missing`) only runs when the
history read provably reached the channel's oldest message — cross-checked
against the total count Telegram reports. A suspicious early termination
aborts the scan with `ERR_SCAN_INCOMPLETE` and leaves the index untouched.

## Full-scan checkpoints

A full scan records its progress in `scan_state.checkpoint_message_id` and
`scan_state.full_scan_started_at`. Chunks of scanned files commit in batched
transactions; the checkpoint advances only after its chunk commits. A
restarted full scan resumes: rows the interrupted run committed are
identified by `updated_at >= full_scan_started_at` and are neither redone nor
marked missing. The checkpoint is cleared when the scan completes.

## Upload-state lifecycle

For files above 10 MB the resumable path persists part state in
`upload_progress` keyed `file:<file-row-id>`:

- `file_id` is the foreign key to the pending file row (parsed from the key);
  deleting the file row cascades the state away.
- `telegram_file_id` is Telegram's client-chosen big-file id — it must survive
  process crashes, because resumed parts have to be sent under the same id.
- A retry that finds a pending row at the destination with matching identity
  (size, content hash) adopts the row — reusing its id and therefore its
  state key — and sends only unconfirmed parts. Mismatched identity blocks
  with `ERR_PATH_EXISTS`; `--replace` supersedes the pending row and deletes
  its state.
- Hashing is mandatory on the resumable path (`--no-hash` applies only to
  small files), so resume identity never rests on an empty hash.
- Stale state is garbage-collected: `td repair --pending` clears state for
  rows it resolves, supersedes/deletes cascade their state, and a completed
  upload deletes its state.
