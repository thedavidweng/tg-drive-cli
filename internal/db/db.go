package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
	"github.com/thedavidweng/tg-drive-cli/internal/fsmodel"
	_ "modernc.org/sqlite"
)

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

create unique index if not exists idx_files_active_path
  on files(channel_id, canonical_path)
  where status = 'active';

create unique index if not exists idx_files_pending_path
  on files(channel_id, canonical_path)
  where status = 'pending';

create unique index if not exists idx_files_channel_message
  on files(channel_id, message_id)
  where message_id is not null;

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
`

// DB wraps SQLite with migrations and helpers.
type DB struct {
	sql *sql.DB
}

// Open opens or creates the database with required pragmas.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "open database", err)
	}
	d := &DB{sql: sqlDB}
	if err := d.init(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) init() error {
	pragmas := []string{
		"pragma foreign_keys = ON",
		"pragma journal_mode = WAL",
		"pragma busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := d.sql.Exec(p); err != nil {
			return apperr.Wrap(apperr.ErrDB, "pragma", err)
		}
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "begin migration", err)
	}
	if _, err := tx.Exec(schemaSQL); err != nil {
		_ = tx.Rollback()
		return apperr.Wrap(apperr.ErrDB, "apply schema", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`insert into schema_version(version, applied_at) select 1, ? where not exists(select 1 from schema_version where version=1)`, now); err != nil {
		_ = tx.Rollback()
		return apperr.Wrap(apperr.ErrDB, "record schema version", err)
	}
	return tx.Commit()
}

// Close closes the database.
func (d *DB) Close() error {
	return d.sql.Close()
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

// AcquireLock acquires or takes over a stale lock.
func (d *DB) AcquireLock(ctx context.Context, key, owner string, ttl time.Duration) error {
	now := time.Now().UTC()
	expires := now.Add(ttl)
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		var existingOwner, expiresAt string
		err := tx.QueryRowContext(ctx, `select owner_token, expires_at from operation_locks where key=?`, key).Scan(&existingOwner, &expiresAt)
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, `insert into operation_locks(key, owner_token, acquired_at, expires_at) values(?,?,?,?)`,
				key, owner, now.Format(time.RFC3339), expires.Format(time.RFC3339))
			return err
		}
		if err != nil {
			return apperr.Wrap(apperr.ErrDB, "read lock", err)
		}
		t, parseErr := time.Parse(time.RFC3339, expiresAt)
		if parseErr != nil || t.After(now) {
			if existingOwner != owner {
				return apperr.New(apperr.ErrOperationLocked, "path is locked by another operation")
			}
			return nil
		}
		_, err = tx.ExecContext(ctx, `update operation_locks set owner_token=?, acquired_at=?, expires_at=? where key=?`,
			owner, now.Format(time.RFC3339), expires.Format(time.RFC3339), key)
		return err
	})
}

// ReleaseLock releases locks owned by owner.
func (d *DB) ReleaseLock(ctx context.Context, key, owner string) error {
	_, err := d.sql.ExecContext(ctx, `delete from operation_locks where key=? and owner_token=?`, key, owner)
	return err
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
func (d *DB) LoadSlugMap(ctx context.Context, channelID int64) (map[string]string, error) {
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

// RunDirectoryGC removes derived directory nodes without active descendants.
func (d *DB) RunDirectoryGC(ctx context.Context, channelID int64) error {
	dirRows, err := d.sql.QueryContext(ctx, `
		select canonical_path from nodes
		where channel_id=? and type='dir' and derived=1 and ephemeral=0`, channelID)
	if err != nil {
		return err
	}
	defer func() { _ = dirRows.Close() }()
	var dirs []string
	for dirRows.Next() {
		var p string
		if err := dirRows.Scan(&p); err != nil {
			return err
		}
		dirs = append(dirs, p)
	}
	if err := dirRows.Err(); err != nil {
		return err
	}
	fileRows, err := d.sql.QueryContext(ctx, `
		select canonical_path from files where channel_id=? and status='active'`, channelID)
	if err != nil {
		return err
	}
	defer func() { _ = fileRows.Close() }()
	var files []string
	for fileRows.Next() {
		var p string
		if err := fileRows.Scan(&p); err != nil {
			return err
		}
		files = append(files, p)
	}
	if err := fileRows.Err(); err != nil {
		return err
	}
	for _, p := range fsmodel.GCDirectories(dirs, files) {
		if _, err := d.sql.ExecContext(ctx, `
			delete from nodes where channel_id=? and canonical_path=? and derived=1`, channelID, p); err != nil {
			return err
		}
	}
	return nil
}
