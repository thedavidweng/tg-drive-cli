package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	_ "modernc.org/sqlite"
)

var _ ports.Store = (*DB)(nil)

const schemaSQL = `
create table if not exists schema_version (
  version integer primary key,
  applied_at text not null
);

create table if not exists accounts (
  id integer primary key,
  tg_user_id text not null unique,
  phone text,
  display_name text,
  created_at text not null,
  updated_at text not null
);

create table if not exists channels (
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
  discussion_tg_channel_id text not null default '',
  discussion_access_hash text not null default '',
  discussion_title text not null default '',
  created_at text not null,
  updated_at text not null,
  unique(account_id, tg_channel_id)
);

create table if not exists nodes (
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

create table if not exists files (
  id integer primary key,
  channel_id integer not null references channels(id),
  node_id integer references nodes(id) on delete set null,
  message_id integer,
  manifest_message_id integer,
  manifest_chat_tg_id text not null default '',
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

create table if not exists path_segment_slugs (
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

create table if not exists path_tags (
  id integer primary key,
  file_id integer not null references files(id) on delete cascade,
  tag text not null,
  depth integer not null,
  unique(file_id, tag)
);

create table if not exists operation_locks (
  key text primary key,
  owner_token text not null,
  acquired_at text not null,
  expires_at text not null
);

create table if not exists scan_state (
  id integer primary key,
  channel_id integer not null references channels(id),
  last_scanned_message_id integer,
  last_full_scan_at text,
  checkpoint_message_id integer,
  full_scan_started_at text,
  discussion_last_scanned_message_id integer,
  updated_at text not null,
  unique(channel_id)
);

create table if not exists scan_errors (
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

create table if not exists upload_progress (
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

create unique index if not exists idx_files_active_path
  on files(channel_id, canonical_path)
  where status = 'active';

create unique index if not exists idx_files_pending_path
  on files(channel_id, canonical_path)
  where status = 'pending';

create unique index if not exists idx_files_channel_message
  on files(channel_id, message_id)
  where message_id is not null;

create index if not exists idx_nodes_channel_parent on nodes(channel_id, parent_path);
create index if not exists idx_nodes_channel_type on nodes(channel_id, type);
create index if not exists idx_files_channel_status on files(channel_id, status);
create index if not exists idx_files_channel_path on files(channel_id, canonical_path);
create index if not exists idx_path_tags_tag on path_tags(tag);
create index if not exists idx_scan_errors_status on scan_errors(channel_id, status);
`

// DB wraps SQLite with migrations and helpers.
type DB struct {
	sql *sql.DB
}

// dsn builds the connection string. Pragmas ride in the DSN so every pooled
// connection enforces them; executing them once on one connection (the old
// behavior) left foreign_keys off on every other connection the pool opened.
// _txlock=immediate makes write transactions take the write lock up front, so
// concurrent processes queue on busy_timeout instead of failing mid-transaction
// on a deferred-to-write upgrade.
func dsn(path string) string {
	return path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
}

// maxPoolConns caps the connection pool. A CLI rarely needs more than a
// handful of concurrent statements, and WAL allows exactly one writer anyway.
const maxPoolConns = 4

// Open opens or creates the database with required pragmas.
func Open(path string) (*DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, apperr.New(apperr.ErrConfigInvalid,
			"database path must not contain '?' or '#' (reserved by the connection DSN): "+path)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "create database directory", err)
		}
	}
	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "open database", err)
	}
	sqlDB.SetMaxOpenConns(maxPoolConns)
	sqlDB.SetMaxIdleConns(maxPoolConns)
	d := &DB{sql: sqlDB}
	if err := d.init(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) init() error {
	return d.WithTx(context.Background(), func(tx *sql.Tx) error {
		return applyMigrations(context.Background(), tx)
	})
}

// migration is one versioned schema step. Each migration runs in a single
// transaction together with its schema_version row, so a database can never
// be left half-migrated.
type migration struct {
	version int
	// stmts are executed in order. The baseline is idempotent (create if not
	// exists) so fresh databases converge through the same steps.
	stmts []string
}

// Pre-release the schema is squashed instead of accumulated: schema changes
// rewrite the baseline in place, and local databases that predate the squash
// are discarded (delete the file; `td scan --full` rebuilds it from
// Telegram, which is the recoverable source). Version numbering restarts at
// each squash. Versioned migrations resume when the schema freezes for
// release.
var migrations = []migration{
	{version: 1, stmts: []string{schemaSQL}},
}

// currentVersion reports the highest applied schema version, 0 for a fresh
// database. Runs inside the migration transaction.
func currentVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	var hasTable int
	if err := tx.QueryRowContext(ctx, `
		select count(*) from sqlite_master where type='table' and name='schema_version'`).Scan(&hasTable); err != nil {
		return 0, err
	}
	if hasTable == 0 {
		return 0, nil
	}
	var v int
	err := tx.QueryRowContext(ctx, `select coalesce(max(version),0) from schema_version`).Scan(&v)
	return v, err
}

func applyMigrations(ctx context.Context, tx *sql.Tx) error {
	current, err := currentVersion(ctx, tx)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "read schema version", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		for _, stmt := range m.stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return apperr.Wrap(apperr.ErrDB, fmt.Sprintf("apply migration %d", m.version), err)
			}
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_version(version, applied_at) values(?,?)`, m.version, now); err != nil {
			return apperr.Wrap(apperr.ErrDB, "record schema version", err)
		}
	}
	return nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.sql.Close()
}

var _ telegram.ResumableStore = (*DB)(nil)

func joinInts(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	s := make([]string, len(nums))
	for i, n := range nums {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

func splitInts(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// fileRowIDFromKey extracts the files.id an upload-state key refers to. Keys
// are "file:<row id>"; the file_id column is the foreign key to that row.
func fileRowIDFromKey(key string) (int64, error) {
	rest, ok := strings.CutPrefix(key, "file:")
	if !ok {
		return 0, apperr.Wrap(apperr.ErrDB, "save upload state", fmt.Errorf("upload state key %q is not file-anchored", key))
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.Wrap(apperr.ErrDB, "save upload state", fmt.Errorf("upload state key %q has no valid file row id", key))
	}
	return id, nil
}

// LoadUploadState loads resumable upload state.
func (d *DB) LoadUploadState(ctx context.Context, key string) (*telegram.UploadState, error) {
	var st telegram.UploadState
	var confirmed string
	err := d.sql.QueryRowContext(ctx, `
		select telegram_file_id, content_hash, part_size, total_parts, total_bytes, confirmed_parts, confirmed_bytes, updated_at
		from upload_progress where key=?`, key).Scan(
		&st.FileID, &st.ContentHash, &st.PartSize, &st.TotalParts, &st.TotalBytes, &confirmed, &st.ConfirmedBytes, new(string))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.ConfirmedParts = splitInts(confirmed)
	return &st, nil
}

// SaveUploadState persists resumable upload state.
func (d *DB) SaveUploadState(ctx context.Context, key string, st *telegram.UploadState) error {
	fileRowID, err := fileRowIDFromKey(key)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.sql.ExecContext(ctx, `
		insert into upload_progress(key,file_id,telegram_file_id,content_hash,part_size,total_parts,total_bytes,confirmed_parts,confirmed_bytes,updated_at)
		values(?,?,?,?,?,?,?,?,?,?)
		on conflict(key) do update set file_id=excluded.file_id, telegram_file_id=excluded.telegram_file_id, content_hash=excluded.content_hash,
			part_size=excluded.part_size, total_parts=excluded.total_parts, total_bytes=excluded.total_bytes,
			confirmed_parts=excluded.confirmed_parts, confirmed_bytes=excluded.confirmed_bytes, updated_at=excluded.updated_at`,
		key, fileRowID, st.FileID, st.ContentHash, st.PartSize, st.TotalParts, st.TotalBytes, joinInts(st.ConfirmedParts), st.ConfirmedBytes, now)
	return err
}

// DeleteUploadState removes resumable upload state.
func (d *DB) DeleteUploadState(ctx context.Context, key string) error {
	_, err := d.sql.ExecContext(ctx, `delete from upload_progress where key=?`, key)
	return err
}

// Raw returns underlying sql.DB.
func (d *DB) Raw() *sql.DB { return d.sql }

// JournalMode returns current journal_mode.
func (d *DB) JournalMode(ctx context.Context) (string, error) {
	var mode string
	err := d.sql.QueryRowContext(ctx, "pragma journal_mode").Scan(&mode)
	return mode, err
}

// WithTx runs fn in a transaction.
func (d *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "begin tx", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// LockKey builds an operation lock key.
func LockKey(channelID int64, canonicalPath string) string {
	return fmt.Sprintf("path:%d:%s", channelID, canonicalPath)
}

// AcquireLock acquires the lock, re-entrant for the same owner, or takes over
// a stale (expired) lock. Lock timestamps are RFC3339 UTC strings written by
// this package; the comparisons below rely on that lexicographic ordering.
// a stale (expired) lock. It is a single conditional write so two racing
// processes cannot both observe a free lock: the loser gets the typed
// operation-locked error rather than a raw database-busy failure.
func (d *DB) AcquireLock(ctx context.Context, key, owner string, ttl time.Duration) error {
	now := time.Now().UTC()
	expires := now.Add(ttl)
	res, err := d.sql.ExecContext(ctx, `
		insert into operation_locks(key, owner_token, acquired_at, expires_at) values(?,?,?,?)
		on conflict(key) do update set owner_token=excluded.owner_token, acquired_at=excluded.acquired_at, expires_at=excluded.expires_at
		where operation_locks.owner_token = excluded.owner_token
		   or operation_locks.expires_at < ?`,
		key, owner, now.Format(time.RFC3339), expires.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "acquire lock", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.New(apperr.ErrOperationLocked, "path is locked by another operation")
	}
	return nil
}

// RenewLock extends a held lock. It fails with the typed locked error when the
// lock was lost (taken over after expiry), so long operations abort instead of
// running unprotected.
func (d *DB) RenewLock(ctx context.Context, key, owner string, ttl time.Duration) error {
	expires := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	res, err := d.sql.ExecContext(ctx, `update operation_locks set expires_at=? where key=? and owner_token=?`,
		expires, key, owner)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "renew lock", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.New(apperr.ErrOperationLocked, "operation lock was lost; aborting")
	}
	return nil
}

// ReleaseLock releases locks owned by owner.
func (d *DB) ReleaseLock(ctx context.Context, key, owner string) error {
	_, err := d.sql.ExecContext(ctx, `delete from operation_locks where key=? and owner_token=?`, key, owner)
	return err
}

// LockHeld reports whether a live (unexpired) lock exists for key.
func (d *DB) LockHeld(ctx context.Context, key string) (bool, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `select count(*) from operation_locks where key=? and expires_at > ?`,
		key, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ClearNodeID clears node_id for a file leaving active status.
func (d *DB) ClearNodeID(ctx context.Context, tx *sql.Tx, fileID int64) error {
	_, err := tx.ExecContext(ctx, `update files set node_id=null where id=?`, fileID)
	return err
}

// ActivePaths returns active and pending paths for conflict checks.
func (d *DB) ActivePaths(ctx context.Context, channelID int64) ([]struct {
	Path  string
	IsDir bool
}, error) {
	rows, err := d.sql.QueryContext(ctx, `
		select canonical_path, 0 from files where channel_id=? and status in ('active','pending')
		union
		select canonical_path, 1 from nodes where channel_id=? and type='dir'`, channelID, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []struct {
		Path  string
		IsDir bool
	}
	for rows.Next() {
		var p string
		var isDir int
		if err := rows.Scan(&p, &isDir); err != nil {
			return nil, err
		}
		out = append(out, struct {
			Path  string
			IsDir bool
		}{p, isDir == 1})
	}
	return out, rows.Err()
}

// LoadSlugMap returns parent|segment -> slug mappings for a channel.
func (d *DB) LoadSlugMap(ctx context.Context, channelID model.ChannelID) (map[string]string, error) {
	rows, err := d.sql.QueryContext(ctx, `
		select parent_canonical_path, segment, slug
		from path_segment_slugs where channel_id=?`, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string)
	for rows.Next() {
		var parent, segment, slug string
		if err := rows.Scan(&parent, &segment, &slug); err != nil {
			return nil, err
		}
		out[parent+"|"+segment] = slug
	}
	return out, rows.Err()
}

// DeleteUploadStateByFile removes resumable state belonging to one file row
// (GC for superseded or abandoned uploads).
func (d *DB) DeleteUploadStateByFile(ctx context.Context, fileID int64) error {
	_, err := d.sql.ExecContext(ctx, `delete from upload_progress where file_id=?`, fileID)
	return err
}

// CountUploadStates reports how many resumable-upload states are persisted.
func (d *DB) CountUploadStates(ctx context.Context) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `select count(*) from upload_progress`).Scan(&n)
	return n, err
}

// RunDirectoryGC removes derived directory nodes without active descendants.
// It runs as a single transaction: a GC interrupted halfway cannot leave the
// node table diverging from the active file set.
func (d *DB) RunDirectoryGC(ctx context.Context, channelID int64) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		dirRows, err := tx.QueryContext(ctx, `
			select canonical_path from nodes
			where channel_id=? and type='dir' and derived=1 and ephemeral=0`, channelID)
		if err != nil {
			return err
		}
		var dirs []string
		for dirRows.Next() {
			var p string
			if err := dirRows.Scan(&p); err != nil {
				_ = dirRows.Close()
				return err
			}
			dirs = append(dirs, p)
		}
		if err := dirRows.Err(); err != nil {
			_ = dirRows.Close()
			return err
		}
		_ = dirRows.Close()
		fileRows, err := tx.QueryContext(ctx, `
			select canonical_path from files where channel_id=? and status='active'`, channelID)
		if err != nil {
			return err
		}
		var files []string
		for fileRows.Next() {
			var p string
			if err := fileRows.Scan(&p); err != nil {
				_ = fileRows.Close()
				return err
			}
			files = append(files, p)
		}
		if err := fileRows.Err(); err != nil {
			_ = fileRows.Close()
			return err
		}
		_ = fileRows.Close()
		for _, p := range fsmodel.GCDirectories(dirs, files) {
			if _, err := tx.ExecContext(ctx, `
				delete from nodes where channel_id=? and canonical_path=? and derived=1`, channelID, p); err != nil {
				return err
			}
		}
		return nil
	})
}
