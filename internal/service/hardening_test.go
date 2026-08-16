package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// scanErrCount reads pending scan errors matching code.
func scanErrCount(t *testing.T, app *App, code string) int {
	t.Helper()
	var n int
	if code == "" {
		_ = app.DB.Raw().QueryRow(`select count(*) from scan_errors where status='pending'`).Scan(&n)
	} else {
		_ = app.DB.Raw().QueryRow(`select count(*) from scan_errors where status='pending' and error_code=?`, code).Scan(&n)
	}
	return n
}

// TestTombstoneWinsOverStaleManifestReply covers the tombstone resurrection
// bug: a tombstoned media caption must win over a manifest reply that could
// not be redacted, so the file cannot reappear after a full scan.
func TestTombstoneWinsOverStaleManifestReply(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "payload")
	remote := deepPath("resurrect.bin")
	if _, err := app.UploadFile(ctx, local, remote, ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	// Tombstone delete whose manifest redaction fails: the reply stays live.
	tg.SetFailEditText(true)
	if _, err := app.DeleteFile(ctx, remote, DeleteOptions{Tombstone: true}); err == nil {
		t.Fatal("expected stale-manifest error")
	}
	tg.SetFailEditText(false)
	if got := fileStatus(t, app, remote); got != "deleted" {
		t.Fatalf("status after delete = %q, want deleted", got)
	}
	// The next full scan must not resurrect the file from the stale reply.
	res, err := app.Scan(ctx, ScanOptions{Full: true, IncludeDeleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != 0 {
		t.Fatalf("tombstoned file resurrected: active = %v", res["active"])
	}
	if got := fileStatus(t, app, remote); got != "deleted" {
		t.Fatalf("status after scan = %q, want deleted", got)
	}
}

// addAlbumMember injects a grouped media message with a human-only caption.
func addAlbumMember(t *testing.T, tg interface {
	AddMessage(int64, telegram.Message) telegram.Message
}, tgChID int64, grouped int64, name string) telegram.Message {
	t.Helper()
	return tg.AddMessage(tgChID, telegram.Message{
		FileName: name, FileSize: 3, MIME: "image/jpeg",
		Kind: telegram.KindPhoto, Caption: "album human caption", GroupedID: grouped,
		Data: []byte("abc"),
	})
}

// TestAlbumInventoryMissingScanError covers a whole album silently vanishing:
// grouped media with no td-album:v1 inventory records a scan error instead.
func TestAlbumInventoryMissingScanError(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)

	addAlbumMember(t, tg, tgChID, 7001, "a.jpg")
	addAlbumMember(t, tg, tgChID, 7001, "b.jpg")

	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := scanErrCount(t, app, "ERR_ALBUM_INVENTORY_INVALID"); n != 1 {
		t.Fatalf("album-inventory scan errors = %d, want 1", n)
	}
	// Members are not silently indexed from nothing.
	if res["active"].(int) != 0 {
		t.Fatalf("members indexed without inventory: active = %v", res["active"])
	}
	// Strict mode fails on it.
	if _, err := app.Scan(ctx, ScanOptions{Full: true, Strict: true}); err == nil {
		t.Fatal("strict scan should fail with a missing inventory")
	}
}

// TestAlbumInventoryCorruptScanError covers an unparseable inventory.
func TestAlbumInventoryCorruptScanError(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)

	m1 := addAlbumMember(t, tg, tgChID, 7002, "c.jpg")
	addAlbumMember(t, tg, tgChID, 7002, "d.jpg")
	rt := m1.ID
	tg.AddMessage(tgChID, telegram.Message{Text: "td-album:v1\ng=notanumber", ReplyTo: &rt})

	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	if n := scanErrCount(t, app, "ERR_ALBUM_INVENTORY_INVALID"); n != 1 {
		t.Fatalf("album-inventory scan errors = %d, want 1", n)
	}
}

// TestAlbumScanRebuildIndexesMembers verifies healthy albums still rebuild.
func TestAlbumScanRebuildIndexesMembers(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)

	m1 := addAlbumMember(t, tg, tgChID, 7003, "e.jpg")
	m2 := addAlbumMember(t, tg, tgChID, 7003, "f.jpg")
	rt := m1.ID
	inv := manifest.AlbumMeta{GroupedID: 7003, Files: []manifest.AlbumFile{
		{MessageID: m1.ID, CanonicalPath: "/photos/e.jpg", DisplayName: "e.jpg", Size: 3, MIME: "image/jpeg"},
		{MessageID: m2.ID, CanonicalPath: "/photos/f.jpg", DisplayName: "f.jpg", Size: 3, MIME: "image/jpeg"},
	}}
	tg.AddMessage(tgChID, telegram.Message{Text: manifest.RenderAlbumReply(inv), ReplyTo: &rt})

	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != 2 {
		t.Fatalf("active = %v, want 2", res["active"])
	}
	for _, p := range []string{"/photos/e.jpg", "/photos/f.jpg"} {
		if got := fileStatus(t, app, p); got != "active" {
			t.Fatalf("%s = %q, want active", p, got)
		}
	}
	if n := scanErrCount(t, app, ""); n != 0 {
		t.Fatalf("scan errors = %d, want 0", n)
	}
}

// TestTruncatedHistoryAbortsScan covers the Telegram pagination quirk: a read
// that cannot prove completion aborts with a typed error and marks nothing
// missing.
func TestTruncatedHistoryAbortsScan(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	for _, p := range []string{"/one.txt", "/two.txt", "/three.txt"} {
		if _, err := app.UploadFile(ctx, local, p, ConflictFail, false); err != nil {
			t.Fatal(err)
		}
	}
	// A full scan first, so last_full_scan_at is set and rows exist.
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	tg.SetTruncateHistory(1)
	_, err := app.Scan(ctx, ScanOptions{Full: true})
	if code := appErrCode(t, err); code != "ERR_SCAN_INCOMPLETE" {
		t.Fatalf("code = %s, want ERR_SCAN_INCOMPLETE (err: %v)", code, err)
	}
	tg.SetTruncateHistory(0)
	// No row was marked missing by the aborted scan.
	for _, p := range []string{"/one.txt", "/two.txt", "/three.txt"} {
		if got := fileStatus(t, app, p); got != "active" {
			t.Fatalf("%s = %q after aborted scan, want active", p, got)
		}
	}
	var missing int
	_ = app.DB.Raw().QueryRow(`select count(*) from files where status='missing'`).Scan(&missing)
	if missing != 0 {
		t.Fatalf("missing rows = %d after aborted scan, want 0", missing)
	}
}

// writeBigLocal writes an n-byte deterministic file above the resumable
// big-file threshold.
func writeBigLocal(t *testing.T, n int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "big.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	written := 0
	for written < n {
		chunk := buf
		if n-written < len(chunk) {
			chunk = buf[:n-written]
		}
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
		written += len(chunk)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestUploadResumeAdoptsPendingRow covers the wired resumable upload: a plain
// retry adopts the pending row, sends only unconfirmed parts, and reports
// that it resumed.
func TestUploadResumeAdoptsPendingRow(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.SetPartSize(1024 * 1024)

	local := writeBigLocal(t, 12*1024*1024)
	content, _ := os.ReadFile(local)

	tg.SetFailUploadAfterParts(4)
	_, err := app.UploadFile(ctx, local, "/resume.bin", ConflictFail, true)
	if err == nil {
		t.Fatal("expected first upload to fail")
	}
	if got := fileStatus(t, app, "/resume.bin"); got != "pending" {
		t.Fatalf("status after failure = %q, want pending", got)
	}
	var states int
	_ = app.DB.Raw().QueryRow(`select count(*) from upload_progress`).Scan(&states)
	if states != 1 {
		t.Fatalf("upload states = %d, want 1", states)
	}
	var confirmedBytes int64
	_ = app.DB.Raw().QueryRow(`select confirmed_bytes from upload_progress`).Scan(&confirmedBytes)
	if confirmedBytes == 0 {
		t.Fatal("no parts were confirmed before the interruption")
	}

	// Plain retry: only unconfirmed parts are re-sent.
	tg.ResetPartSubmissions()
	data, err := app.UploadFile(ctx, local, "/resume.bin", ConflictFail, true)
	if err != nil {
		t.Fatal(err)
	}
	if data["resumed"] != true {
		t.Fatalf("retry did not report resumed: %v", data)
	}
	if got := fileStatus(t, app, "/resume.bin"); got != "active" {
		t.Fatalf("status after retry = %q, want active", got)
	}
	// 12 parts of 1MB, 4 confirmed before the failure: the retry submits
	// only the remaining 8.
	if n := tg.PartSubmissions(); n != 8 {
		t.Fatalf("part submissions during retry = %d, want 8", n)
	}
	// The completed upload cleans its state.
	_ = app.DB.Raw().QueryRow(`select count(*) from upload_progress`).Scan(&states)
	if states != 0 {
		t.Fatalf("upload states after completion = %d, want 0", states)
	}
	// Content round-trips.
	dest := filepath.Join(t.TempDir(), "out.bin")
	if _, err := app.DownloadFile(ctx, "/resume.bin", dest, ConflictFail); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Fatal("resumed upload content mismatch")
	}
}

// TestUploadResumeIdentityMismatchBlocks covers the safety gate: a different
// file at the same destination cannot splice onto the pending upload.
func TestUploadResumeIdentityMismatchBlocks(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.SetPartSize(1024 * 1024)

	local := writeBigLocal(t, 12*1024*1024)
	tg.SetFailUploadAfterParts(4)
	_, _ = app.UploadFile(ctx, local, "/mismatch.bin", ConflictFail, true)

	// Same size, different bytes.
	other := append([]byte(nil), localMustRead(t, local)...)
	other[len(other)-1] ^= 0xff
	otherPath := filepath.Join(t.TempDir(), "second.bin")
	if err := os.WriteFile(otherPath, other, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := app.UploadFile(ctx, otherPath, "/mismatch.bin", ConflictFail, true)
	if code := appErrCode(t, err); code != "ERR_PATH_EXISTS" {
		t.Fatalf("code = %s, want ERR_PATH_EXISTS", code)
	}
	// --replace supersedes the pending row and completes.
	if _, err := app.UploadFile(ctx, otherPath, "/mismatch.bin", ConflictReplace, true); err != nil {
		t.Fatalf("replace after mismatch: %v", err)
	}
	if got := fileStatus(t, app, "/mismatch.bin"); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
	var states int
	_ = app.DB.Raw().QueryRow(`select count(*) from upload_progress`).Scan(&states)
	if states != 0 {
		t.Fatalf("superseded upload state left behind: %d", states)
	}
}

func localMustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRepairPendingLeavesInFlightUploadUntouched covers the repair-vs-upload
// race: a fresh pending row is never stale enough to touch, and a stale row
// whose path is locked is skipped.
func TestRepairPendingLeavesInFlightUploadUntouched(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	channelID, _, _ := app.channelID(ctx)
	res, err := app.DB.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,original_local_path,status,updated_at) values(?,?,?,?,'pending',?)`,
		channelID, "/inflight.txt", "inflight.txt", local, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	freshID, _ := res.LastInsertId()

	// A second row that IS stale by timestamp but whose path lock is held by
	// a live operation.
	res, err = app.DB.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,original_local_path,status,updated_at) values(?,?,?,?,'pending',?)`,
		channelID, "/locked.txt", "locked.txt", local, "2020-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	lockedID, _ := res.LastInsertId()
	lockErr := make(chan error, 1)
	go func() {
		lockErr <- app.withLocks(ctx, []string{sqlitestore.LockKey(channelID, "/locked.txt")}, func(ctx context.Context) error {
			time.Sleep(700 * time.Millisecond)
			return nil
		})
	}()
	// Wait for the lock to actually be held before repairing.
	waitLockHeld(t, app, sqlitestore.LockKey(channelID, "/locked.txt"))

	out, err := app.RepairPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out["repaired"].(int) != 0 {
		t.Fatalf("repair = %v, want no repairs", out)
	}
	var status string
	_ = app.DB.Raw().QueryRow(`select status from files where id=?`, freshID).Scan(&status)
	if status != "pending" {
		t.Fatalf("fresh in-flight row status = %q, want pending", status)
	}
	_ = app.DB.Raw().QueryRow(`select status from files where id=?`, lockedID).Scan(&status)
	if status != "pending" {
		t.Fatalf("locked row status = %q, want pending", status)
	}
	if err := <-lockErr; err != nil {
		t.Fatal(err)
	}
}

// TestScanDuplicatePathClaimsNewestWins covers duplicate claims landing in
// the same commit chunk: the newest message wins, the older duplicate records
// a scan error, and the scan must not abort on the active-path unique index.
func TestScanDuplicatePathClaimsNewestWins(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	older := "old.bin\n\ntd:v1 p=" + b64url("/dup.txt") + " n=" + b64url("old.bin") + " s=1 h=- m=-"
	newer := "new.bin\n\ntd:v1 p=" + b64url("/dup.txt") + " n=" + b64url("new.bin") + " s=1 h=- m=-"
	if _, err := tg.UploadMedia(ctx, uploadReq(tgChID, "old.bin", older, strings.NewReader("a"))); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.UploadMedia(ctx, uploadReq(tgChID, "new.bin", newer, strings.NewReader("b"))); err != nil {
		t.Fatal(err)
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatalf("scan aborted on duplicate claims: %v", err)
	}
	if res["active"].(int) != 1 {
		t.Fatalf("active = %v, want 1", res["active"])
	}
	if n := scanErrCount(t, app, "ERR_PATH_CONFLICT"); n != 1 {
		t.Fatalf("path-conflict scan errors = %d, want 1", n)
	}
	var name string
	_ = app.DB.Raw().QueryRow(`select display_name from files where canonical_path='/dup.txt' and status='active'`).Scan(&name)
	if name != "new.bin" {
		t.Fatalf("winner = %q, want new.bin (newest message)", name)
	}
}

// TestScanDeterministicSlugRebuild covers hashtag survival across DB loss:
// colliding sibling segments get the same slug chains after a rebuild.
func TestScanDeterministicSlugRebuild(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	// "My Photos" and "my photos" transliterate to the same slug, so the
	// second segment gets the longer collision-fallback hash. Upload in
	// reverse lexicographic order so a path-ordered rebuild would swap the
	// chains — proving the rebuild follows upload (chronological) order.
	if _, err := app.UploadFile(ctx, local, "/my photos/b.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, local, "/My Photos/a.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	tagsBefore := slugChains(t, app)

	for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
		if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	tagsAfter := slugChains(t, app)
	if len(tagsBefore) == 0 || len(tagsAfter) == 0 {
		t.Fatal("no tag chains recorded")
	}
	if fmt.Sprint(tagsBefore) != fmt.Sprint(tagsAfter) {
		t.Fatalf("tag chains changed across rebuild:\nbefore=%v\nafter=%v", tagsBefore, tagsAfter)
	}
}

func slugChains(t *testing.T, app *App) []string {
	t.Helper()
	rows, err := app.DB.Raw().Query(`
		select f.canonical_path, group_concat(pt.tag, ' ') as chain
		from files f join path_tags pt on pt.file_id = f.id
		where f.status='active'
		group by f.id order by f.canonical_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var path, chain string
		if err := rows.Scan(&path, &chain); err != nil {
			t.Fatal(err)
		}
		out = append(out, path+" => "+chain)
	}
	return out
}

// batchFailIndex delegates to the real index but fails the Nth batch, leaving
// earlier chunks committed (an interrupted full scan).
type batchFailIndex struct {
	inner   ports.FileIndex
	failOn  int
	failErr error
	calls   int
}

func (b *batchFailIndex) Index(ctx context.Context, req ports.FileIndexRequest) (int64, error) {
	return b.inner.Index(ctx, req)
}

func (b *batchFailIndex) IndexBatch(ctx context.Context, reqs []ports.FileIndexRequest) error {
	b.calls++
	if b.calls == b.failOn {
		return b.failErr
	}
	return b.inner.IndexBatch(ctx, reqs)
}

// TestFullScanResumesFromCheckpoint covers an interrupted full scan: committed
// chunks are kept, and the rerun finishes without redoing them.
func TestFullScanResumesFromCheckpoint(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	const total = scanIndexChunk + 5
	for i := 0; i < total; i++ {
		dest := fmt.Sprintf("/bulk/f%03d.txt", i)
		if _, err := app.UploadFile(ctx, local, dest, ConflictFail, false); err != nil {
			t.Fatalf("upload %s: %v", dest, err)
		}
	}
	// Wipe the index: the scan must rebuild it from Telegram.
	for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
		if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
			t.Fatal(err)
		}
	}
	app.Index = &batchFailIndex{inner: app.DB, failOn: 2, failErr: errors.New("crash mid scan")}
	_, err := app.Scan(ctx, ScanOptions{Full: true})
	if code := appErrCode(t, err); code != "ERR_DB" {
		t.Fatalf("interrupted scan code = %s, want ERR_DB", code)
	}
	var checkpoint sql.NullInt64
	_ = app.DB.Raw().QueryRow(`select checkpoint_message_id from scan_state`).Scan(&checkpoint)
	if !checkpoint.Valid {
		t.Fatal("interrupted scan left no checkpoint")
	}
	// The first chunk committed before the crash.
	var active int
	_ = app.DB.Raw().QueryRow(`select count(*) from files where status='active'`).Scan(&active)
	if active != scanIndexChunk {
		t.Fatalf("active rows after interrupted scan = %d, want %d", active, scanIndexChunk)
	}

	// Rerun to completion: it resumes and finishes the rest.
	app.Index = nil
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != total {
		t.Fatalf("active after resume = %v, want %d", res["active"], total)
	}
	_ = app.DB.Raw().QueryRow(`select checkpoint_message_id from scan_state`).Scan(&checkpoint)
	if checkpoint.Valid {
		t.Fatal("checkpoint not cleared after completed scan")
	}
}

// TestIncrementalScanPicksUpNewMessage pins the documented incremental
// behavior: new managed messages are adopted; edits of old ones are not seen.
func TestIncrementalScanPicksUpNewMessage(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/base.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	// A new managed message arrives (e.g. posted by another client).
	caption := "late.txt\n\ntd:v1 p=" + b64url("/late.txt") + " n=" + b64url("late.txt") + " s=1 h=- m=-"
	if _, err := tg.UploadMedia(ctx, uploadReq(tgChID, "late.txt", caption, strings.NewReader("x"))); err != nil {
		t.Fatal(err)
	}
	res, err := app.Scan(ctx, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res["mode"] != "incremental" {
		t.Fatalf("mode = %v", res["mode"])
	}
	if got := fileStatus(t, app, "/late.txt"); got != "active" {
		t.Fatalf("incremental scan missed the new message: %q", got)
	}
}

// TestHeartbeatKeepsLockDuringLongOp covers lock renewal: an operation longer
// than the TTL still excludes concurrent lockers, and releases afterwards.
func TestHeartbeatKeepsLockDuringLongOp(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	_ = tg
	app.Cfg.Locks.TTLSeconds = 1 // aggressive: renewal at ~333ms
	key := "path:1:/slow.txt"
	done := make(chan error, 1)
	go func() {
		done <- app.withLocks(ctx, []string{key}, func(ctx context.Context) error {
			select {
			case <-time.After(2 * time.Second): // twice the TTL
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
	}()
	// Wait until the lock row appears (avoid racing the operation for the
	// initial acquisition), then keep trying to steal it well past the
	// original expiry: renewal must keep it alive.
	var held bool
	deadline := time.Now().Add(3 * time.Second)
	for !held && time.Now().Before(deadline) {
		var expires string
		if err := app.DB.Raw().QueryRow(`select expires_at from operation_locks where key=?`, key).Scan(&expires); err == nil {
			if exp, perr := time.Parse(time.RFC3339, expires); perr == nil && exp.After(time.Now()) {
				held = true
			}
		}
		if !held {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !held {
		t.Fatal("lock never became held")
	}
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("long op aborted: %v", err)
			}
			done = nil
		default:
		}
		if done == nil {
			break
		}
		if err := app.DB.AcquireLock(ctx, key, "thief", time.Minute); err == nil {
			t.Fatal("lock stolen while operation still running")
		}
		time.Sleep(150 * time.Millisecond)
	}
	if done != nil {
		if err := <-done; err != nil {
			t.Fatalf("long op aborted: %v", err)
		}
	}
	// Released after completion: another owner can take it.
	if err := app.DB.AcquireLock(ctx, key, "thief", time.Minute); err != nil {
		t.Fatalf("lock not released after op: %v", err)
	}
}

// TestScanScaleSmoke keeps full scans practical on large channels: tens of
// thousands of messages must scan in seconds, not hours.
func TestScanScaleSmoke(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)

	n := 20000
	timeBound := 60 * time.Second
	if raceEnabled {
		n = 5000
		timeBound = 120 * time.Second
	}
	for i := 0; i < n; i += 1000 {
		end := i + 1000
		if end > n {
			end = n
		}
		// The fake is the source; inject messages directly.
		for j := i; j < end; j++ {
			path := fmt.Sprintf("/scale/dir%02d/file%05d.txt", j%50, j)
			name := fmt.Sprintf("file%05d.txt", j)
			caption := name + "\n\ntd:v1 p=" + b64url(path) + " n=" + b64url(name) + " s=1 h=- m=-"
			tg.AddMessage(tgChID, telegram.Message{
				Kind: telegram.KindDocument, Caption: caption,
				FileName: name, FileSize: 1, MIME: "text/plain", Data: []byte("x"),
			})
		}
	}
	start := time.Now()
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != n {
		t.Fatalf("active = %v, want %d", res["active"], n)
	}
	if elapsed > timeBound {
		t.Fatalf("scan of %d messages took %s", n, elapsed)
	}
	t.Logf("scanned %d messages in %s", n, elapsed)
}

// TestHasMachineMetaLineAnchored pins the stricter detection: human captions
// merely mentioning the marker token are not machine metadata.
func TestHasMachineMetaLineAnchored(t *testing.T) {
	if manifest.HasMachineMeta("I really love td:v1 so much") {
		t.Fatal("human mention misclassified as machine metadata")
	}
	if manifest.HasMachineMeta("check out td-manifest:v1 sometime") {
		t.Fatal("human mention misclassified as machine metadata")
	}
	if !manifest.HasMachineMeta("name\n\ntd:v1 p=AA n=BB") {
		t.Fatal("real compact line not detected")
	}
	if !manifest.HasMachineMeta("td-manifest:v1\np=AA") {
		t.Fatal("manifest reply not detected")
	}
	if !manifest.HasMachineMeta("td-album:v1\ng=1") {
		t.Fatal("album inventory not detected")
	}
}

// waitLockHeld polls until the given operation lock row exists unexpired.
func waitLockHeld(t *testing.T, app *App, key string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		_ = app.DB.Raw().QueryRow(`select count(*) from operation_locks where key=? and expires_at > ?`, key, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("lock never became held")
}
