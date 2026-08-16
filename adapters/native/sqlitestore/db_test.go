package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	_ "modernc.org/sqlite"
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

func TestOpenCreatesMissingParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "data", "cache.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var version int
	if err := d.Raw().QueryRow(`select version from schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
}

func TestPragmasOnEveryPooledConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pragmas.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()

	// Pin several distinct pooled connections by holding transactions open
	// while other goroutines query, then verify each connection's pragmas.
	const conns = 4
	var wg sync.WaitGroup
	errs := make([]error, conns)
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = d.WithTx(ctx, func(tx *sql.Tx) error {
				var fk, busy int
				var mode string
				if err := tx.QueryRowContext(ctx, `pragma foreign_keys`).Scan(&fk); err != nil {
					return err
				}
				if err := tx.QueryRowContext(ctx, `pragma busy_timeout`).Scan(&busy); err != nil {
					return err
				}
				if err := tx.QueryRowContext(ctx, `pragma journal_mode`).Scan(&mode); err != nil {
					return err
				}
				if fk != 1 {
					t.Errorf("connection %d: foreign_keys = %d, want 1", i, fk)
				}
				if busy != 5000 {
					t.Errorf("connection %d: busy_timeout = %d, want 5000", i, busy)
				}
				if mode != "wal" {
					t.Errorf("connection %d: journal_mode = %q, want wal", i, mode)
				}
				time.Sleep(50 * time.Millisecond)
				return nil
			})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestForeignKeyEnforcedOnFreshConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fk-pool.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.Raw().Exec(`insert into accounts(id,tg_user_id,created_at,updated_at) values(1,'u1',?,?)`, now, now)
	// A path_tags row referencing a nonexistent file must fail no matter
	// which pooled connection serves the statement.
	for i := 0; i < 6; i++ {
		if _, err := d.Raw().Exec(`insert into path_tags(file_id,tag,depth) values(999,'t',0)`); err == nil {
			t.Fatalf("iteration %d: foreign key not enforced", i)
		}
	}
}

// buildV1Database creates a database in the pre-v2 state: v1 schema applied
// and version row set, without the v2 migration.
func buildV1Database(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`insert into schema_version(version, applied_at) values(1, ?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMigrationV2UpgradesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	raw := buildV1Database(t, path)
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = raw.Exec(`insert into accounts(id,tg_user_id,created_at,updated_at) values(1,'u1',?,?)`, now, now)
	_, _ = raw.Exec(`insert into channels(id,account_id,tg_channel_id,title,root_local_path,created_at,updated_at) values(1,1,'c1','t','/tmp',?,?)`, now, now)
	// One live file row and one state row referencing it, written with v1
	// semantics: the key carries the row id, file_id holds Telegram's random
	// big-file id. Plus an orphaned state row whose key names no file row.
	res, _ := raw.Exec(`insert into files(channel_id,canonical_path,display_name,status,updated_at) values(1,'/a.bin','a','pending',?)`, now)
	fileID, _ := res.LastInsertId()
	_, _ = raw.Exec(`insert into upload_progress(key,file_id,content_hash,part_size,total_parts,total_bytes,confirmed_parts,confirmed_bytes,updated_at) values(?,?,NULL,512,4,4096,'0,1',1024,?)`,
		fmt.Sprintf("file:%d", fileID), int64(987654321), now)
	_, _ = raw.Exec(`insert into upload_progress(key,file_id,content_hash,part_size,total_parts,total_bytes,confirmed_parts,confirmed_bytes,updated_at) values('file:424242',424242,NULL,1,1,10,'0',0,?)`, now)
	_, _ = raw.Exec(`insert into scan_state(channel_id,last_scanned_message_id,updated_at) values(1,5,?)`, now)
	_ = raw.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade existing database: %v", err)
	}
	defer func() { _ = d.Close() }()

	var version int
	if err := d.Raw().QueryRow(`select max(version) from schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema version = %d, want 2", version)
	}
	var states int
	if err := d.Raw().QueryRow(`select count(*) from upload_progress`).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if states != 1 {
		t.Fatalf("upload states = %d, want 1 (orphan dropped)", states)
	}
	var confirmed string
	var telegramFileID int64
	if err := d.Raw().QueryRow(`select confirmed_parts, telegram_file_id from upload_progress where file_id=?`, fileID).Scan(&confirmed, &telegramFileID); err != nil {
		t.Fatalf("surviving state row: %v", err)
	}
	if confirmed != "0,1" {
		t.Fatalf("confirmed_parts = %q, want preserved %q", confirmed, "0,1")
	}
	if telegramFileID != 987654321 {
		t.Fatalf("telegram_file_id = %d, want the v1 file_id value preserved", telegramFileID)
	}
	var newCols int
	if err := d.Raw().QueryRow(`select count(*) from pragma_table_info('scan_state') where name in ('checkpoint_message_id','full_scan_started_at')`).Scan(&newCols); err != nil {
		t.Fatal(err)
	}
	if newCols != 2 {
		t.Fatalf("scan_state gained %d new columns, want 2", newCols)
	}
}

func TestUploadStateForeignKeyCascade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cascade.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.Raw().Exec(`insert into accounts(id,tg_user_id,created_at,updated_at) values(1,'u1',?,?)`, now, now)
	_, _ = d.Raw().Exec(`insert into channels(id,account_id,tg_channel_id,title,root_local_path,created_at,updated_at) values(1,1,'c1','t','/tmp',?,?)`, now, now)
	res, _ := d.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,status,updated_at) values(1,'/a.bin','a','pending',?)`, now)
	fileID, _ := res.LastInsertId()
	if err := d.SaveUploadState(ctx, fmt.Sprintf("file:%d", fileID), &telegram.UploadState{
		FileID: 77, PartSize: 128, TotalParts: 1, TotalBytes: 10, ContentHash: "blake3:x",
	}); err != nil {
		t.Fatal(err)
	}
	// Deleting the file row must take the upload state with it.
	if _, err := d.Raw().Exec(`delete from files where id=?`, fileID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.Raw().QueryRow(`select count(*) from upload_progress`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("upload states after file delete = %d, want 0", n)
	}
}

func TestLockAcquisitionRaceYieldsTypedError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	key := LockKey(1, "/race")

	const racers = 8
	winners := make(chan string, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			owner := fmt.Sprintf("owner-%d", i)
			if err := d.AcquireLock(ctx, key, owner, time.Minute); err == nil {
				winners <- owner
			} else if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrOperationLocked {
				t.Errorf("racer %d: err = %v, want typed ERR_OPERATION_LOCKED", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(winners)
	if n := len(winners); n != 1 {
		t.Fatalf("lock winners = %d, want exactly 1", n)
	}
}

func TestRenewLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renew.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	key := LockKey(1, "/renew")
	if err := d.AcquireLock(ctx, key, "owner", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := d.RenewLock(ctx, key, "owner", 2*time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}
	var expires string
	if err := d.Raw().QueryRow(`select expires_at from operation_locks where key=?`, key).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	_ = expires
	// Wrong owner cannot renew.
	if err := d.RenewLock(ctx, key, "intruder", time.Minute); err == nil {
		t.Fatal("intruder renewed lock")
	} else if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrOperationLocked {
		t.Fatalf("renew err = %v, want ERR_OPERATION_LOCKED", err)
	}
	// After takeover, the old owner's renewal fails.
	_, _ = d.Raw().Exec(`update operation_locks set expires_at=? where key=?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), key)
	if err := d.AcquireLock(ctx, key, "new-owner", time.Minute); err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	if err := d.RenewLock(ctx, key, "owner", time.Minute); err == nil {
		t.Fatal("renewal after lock loss should fail")
	}
}
