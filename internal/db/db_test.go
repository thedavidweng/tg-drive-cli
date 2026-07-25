package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationCreatesFreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	var version int
	if err := d.Raw().QueryRow(`select version from schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d", version)
	}
}

func TestWALMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	mode, err := d.JournalMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q", mode)
	}
}

func TestPartialUniqueActivePending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.Raw().Exec(`insert into accounts(id,tg_user_id,created_at,updated_at) values(1,'u1',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Raw().Exec(`insert into channels(id,account_id,tg_channel_id,title,root_local_path,created_at,updated_at) values(1,1,'c1','t','/tmp',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,status,updated_at) values(1,'/a.txt','a','active',?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,status,updated_at) values(1,'/a.txt','a','active',?)`, now)
	if err == nil {
		t.Fatal("expected duplicate active path error")
	}
	_, err = d.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,status,updated_at) values(1,'/a.txt','a','deleted',?)`, now)
	if err != nil {
		t.Fatalf("deleted duplicate should be allowed: %v", err)
	}
}

func TestForeignKeyNodeIDClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fk.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.Raw().Exec(`insert into accounts(id,tg_user_id,created_at,updated_at) values(1,'u1',?,?)`, now, now)
	_, _ = d.Raw().Exec(`insert into channels(id,account_id,tg_channel_id,title,root_local_path,created_at,updated_at) values(1,1,'c1','t','/tmp',?,?)`, now, now)
	res, _ := d.Raw().Exec(`insert into nodes(channel_id,canonical_path,display_name,type,created_at,updated_at) values(1,'/a.txt','a','file',?,?)`, now, now)
	nodeID, _ := res.LastInsertId()
	res, _ = d.Raw().Exec(`insert into files(channel_id,node_id,canonical_path,display_name,status,updated_at) values(1,?,'/a.txt','a','active',?)`, nodeID, now)
	fileID, _ := res.LastInsertId()
	err = d.WithTx(ctx, func(tx *sql.Tx) error {
		return d.ClearNodeID(ctx, tx, fileID)
	})
	if err != nil {
		t.Fatal(err)
	}
	var cleared sql.NullInt64
	_ = d.Raw().QueryRow(`select node_id from files where id=?`, fileID).Scan(&cleared)
	if cleared.Valid {
		t.Fatal("node_id should be null")
	}
}

func TestLockAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	key := LockKey(1, "/test")
	if err := d.AcquireLock(ctx, key, "owner1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := d.AcquireLock(ctx, key, "owner2", time.Minute); err == nil {
		t.Fatal("expected lock conflict")
	}
	if err := d.ReleaseLock(ctx, key, "owner1"); err != nil {
		t.Fatal(err)
	}
}

func TestStaleLockTakeover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	key := LockKey(1, "/stale")
	// Acquire with a TTL already in the past.
	if err := d.AcquireLock(ctx, key, "dead-owner", -time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := d.AcquireLock(ctx, key, "new-owner", time.Minute); err != nil {
		t.Fatalf("stale lock not stolen: %v", err)
	}
	var owner string
	if err := d.Raw().QueryRow(`select owner_token from operation_locks where key=?`, key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "new-owner" {
		t.Fatalf("owner = %q, want new-owner", owner)
	}
}

func TestReleaseLockOwnerTokenGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	key := LockKey(1, "/guarded")
	if err := d.AcquireLock(ctx, key, "owner1", time.Minute); err != nil {
		t.Fatal(err)
	}
	// Wrong token must not release the lock.
	if err := d.ReleaseLock(ctx, key, "intruder"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.Raw().QueryRow(`select count(*) from operation_locks where key=?`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("lock released by non-owner")
	}
}
