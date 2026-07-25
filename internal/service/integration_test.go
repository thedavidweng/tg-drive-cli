package service

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

func b64url(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func uploadReq(channelID int64, name, caption string, r io.Reader) telegram.UploadRequest {
	return telegram.UploadRequest{
		ChannelID: channelID,
		Caption:   caption,
		FileName:  name,
		MIME:      "application/octet-stream",
		Size:      1,
		Reader:    r,
	}
}

func writeLocal(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func appErrCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := apperr.As(err)
	if !ok {
		t.Fatalf("not an AppError: %v", err)
	}
	return ae.Code
}

func fileStatus(t *testing.T, app *App, path string) string {
	t.Helper()
	var status string
	err := app.DB.Raw().QueryRow(`select status from files where canonical_path=? order by id desc limit 1`, path).Scan(&status)
	if err != nil {
		return ""
	}
	return status
}

// deepPath builds a path deep enough that the full caption (with the base64
// td:v1 line) overflows the 1024-unit budget while the minimal manifest=reply
// caption still fits.
func deepPath(name string) string {
	parts := make([]string, 30)
	for i := range parts {
		parts[i] = "level-" + strings.Repeat("x", 14) + string(rune('a'+i%26))
	}
	return "/" + strings.Join(parts, "/") + "/" + name
}

func TestTwoFilesSameDirectory(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "hello")
	if _, err := app.UploadFile(ctx, local, "/Pictures/a.jpg", ConflictFail, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := app.UploadFile(ctx, local, "/Pictures/b.jpg", ConflictFail, false); err != nil {
		t.Fatalf("second upload into same dir: %v", err)
	}
	if _, err := app.UploadFile(ctx, local, "/Pictures/sub/c.jpg", ConflictFail, false); err != nil {
		t.Fatalf("nested upload: %v", err)
	}
}

func TestReplaceFailureKeepsOldActive(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "v1")
	if _, err := app.UploadFile(ctx, local, "/keep.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	tg.SetFailUpload(true)
	if _, err := app.UploadFile(ctx, local, "/keep.txt", ConflictReplace, false); err == nil {
		t.Fatal("expected replace upload to fail")
	}
	tg.SetFailUpload(false)
	if got := fileStatus(t, app, "/keep.txt"); got != "active" {
		t.Fatalf("old row status = %q, want active", got)
	}
	dest := filepath.Join(t.TempDir(), "out.txt")
	if err := app.DownloadFile(ctx, "/keep.txt", dest, ConflictFail); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "v1" {
		t.Fatalf("content = %q", data)
	}
}

func TestReplaceSupersedesOldRow(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "v1")
	if _, err := app.UploadFile(ctx, local, "/sup.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(local, []byte("v2"), 0o644)
	if _, err := app.UploadFile(ctx, local, "/sup.txt", ConflictReplace, false); err != nil {
		t.Fatal(err)
	}
	var superseded, active int
	_ = app.DB.Raw().QueryRow(`select count(*) from files where canonical_path='/sup.txt' and status='superseded'`).Scan(&superseded)
	_ = app.DB.Raw().QueryRow(`select count(*) from files where canonical_path='/sup.txt' and status='active'`).Scan(&active)
	if superseded != 1 || active != 1 {
		t.Fatalf("superseded=%d active=%d, want 1/1", superseded, active)
	}
}

func TestManifestReplyFailureRollsBack(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "payload")
	remote := deepPath("f.bin")
	tg.SetFailReply(true)
	if _, err := app.UploadFile(ctx, local, remote, ConflictFail, false); err == nil {
		t.Fatal("expected upload to fail")
	}
	if got := fileStatus(t, app, remote); got != "" {
		t.Fatalf("pending row still present with status %q", got)
	}
	tgChID, _ := app.tgChannelID(ctx)
	if n := len(tg.Messages(tgChID)); n != 0 {
		t.Fatalf("media message not rolled back, %d messages remain", n)
	}
}

func TestManifestReplyFailureRecordsOrphan(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "payload")
	remote := deepPath("g.bin")
	tg.SetFailReply(true)
	tg.SetFailDelete(true)
	_, err := app.UploadFile(ctx, local, remote, ConflictFail, false)
	if code := appErrCode(t, err); code != apperr.ErrOrphanedUpload {
		t.Fatalf("code = %s, want ERR_ORPHANED_UPLOAD", code)
	}
	if got := fileStatus(t, app, remote); got != "orphaned" {
		t.Fatalf("status = %q, want orphaned", got)
	}
}

func TestRepairOrphanedCompletesUpload(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "payload")
	remote := deepPath("h.bin")
	tg.SetFailReply(true)
	tg.SetFailDelete(true)
	_, _ = app.UploadFile(ctx, local, remote, ConflictFail, false)
	tg.SetFailReply(false)
	tg.SetFailDelete(false)
	res, err := app.RepairOrphaned(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if res["repaired"] != 1 {
		t.Fatalf("res = %v", res)
	}
	if got := fileStatus(t, app, remote); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
	var manifestID int
	_ = app.DB.Raw().QueryRow(`select coalesce(manifest_message_id,0) from files where canonical_path=? and status='active'`, remote).Scan(&manifestID)
	if manifestID == 0 {
		t.Fatal("manifest reply not recorded after repair")
	}
}

func TestRepairOrphanedDeleteOrphans(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "payload")
	remote := deepPath("i.bin")
	tg.SetFailReply(true)
	tg.SetFailDelete(true)
	_, _ = app.UploadFile(ctx, local, remote, ConflictFail, false)
	tg.SetFailReply(false)
	tg.SetFailDelete(false)
	res, err := app.RepairOrphaned(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if res["deleted"] != 1 {
		t.Fatalf("res = %v", res)
	}
	tgChID, _ := app.tgChannelID(ctx)
	if n := len(tg.Messages(tgChID)); n != 0 {
		t.Fatalf("orphaned message not deleted, %d remain", n)
	}
}

func TestTombstoneModeRedactsBoth(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "payload")
	remote := deepPath("t.bin")
	if _, err := app.UploadFile(ctx, local, remote, ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	res, err := app.DeleteFile(ctx, remote, DeleteOptions{Tombstone: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["mode"] != "tombstone" {
		t.Fatalf("res = %v", res)
	}
	tgChID, _ := app.tgChannelID(ctx)
	var sawTombCaption, sawTombManifest bool
	for _, m := range tg.Messages(tgChID) {
		if strings.Contains(m.Caption, "td:v1 deleted=true p=") {
			sawTombCaption = true
		}
		if strings.HasPrefix(m.Text, "td-manifest:v1\ndeleted=true") {
			sawTombManifest = true
		}
	}
	if !sawTombCaption || !sawTombManifest {
		t.Fatalf("tombstone redaction incomplete: caption=%v manifest=%v", sawTombCaption, sawTombManifest)
	}
	// Default rescan must not resurrect or error on the tombstone.
	scanRes, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if scanRes["active"].(int) != 0 || scanRes["invalid"].(int) != 0 {
		t.Fatalf("scan after tombstone = %v", scanRes)
	}
}

func TestDeletePermissionDenied(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/perm.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	tg.SetDenyPermissions(true)
	_, err := app.DeleteFile(ctx, "/perm.txt", DeleteOptions{})
	if code := appErrCode(t, err); code != apperr.ErrChannelPermission {
		t.Fatalf("code = %s, want ERR_CHANNEL_PERMISSION", code)
	}
	tg.SetDenyPermissions(false)
	if got := fileStatus(t, app, "/perm.txt"); got != "active" {
		t.Fatalf("status = %q, want active (no silent downgrade)", got)
	}
}

func TestMoveNotEditable(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/ne.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	var msgID int
	_ = app.DB.Raw().QueryRow(`select message_id from files where canonical_path='/ne.txt' and status='active'`).Scan(&msgID)
	tgChID, _ := app.tgChannelID(ctx)
	tg.SetNotEditable(tgChID, msgID, true)
	err := app.MoveFile(ctx, "/ne.txt", "/ne2.txt")
	if code := appErrCode(t, err); code != apperr.ErrMessageNotEditable {
		t.Fatalf("code = %s, want ERR_MESSAGE_NOT_EDITABLE", code)
	}
	if got := fileStatus(t, app, "/ne.txt"); got != "active" {
		t.Fatalf("source status = %q, want active", got)
	}
}

func TestMoveIntoExistingDirectory(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	_, _ = app.UploadFile(ctx, local, "/Archive/existing.txt", ConflictFail, false)
	_, _ = app.UploadFile(ctx, local, "/a.jpg", ConflictFail, false)
	if err := app.MoveFile(ctx, "/a.jpg", "/Archive"); err != nil {
		t.Fatal(err)
	}
	if got := fileStatus(t, app, "/Archive/a.jpg"); got != "active" {
		t.Fatalf("moved status = %q, want active", got)
	}
}

func TestMoveDestinationExists(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	_, _ = app.UploadFile(ctx, local, "/m1.txt", ConflictFail, false)
	_, _ = app.UploadFile(ctx, local, "/m2.txt", ConflictFail, false)
	err := app.MoveFile(ctx, "/m1.txt", "/m2.txt")
	if code := appErrCode(t, err); code != apperr.ErrPathExists {
		t.Fatalf("code = %s, want ERR_PATH_EXISTS", code)
	}
}

func TestMoveDeepPathCreatesManifestReply(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/shallow.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	deep := deepPath("moved.txt")
	if err := app.MoveFile(ctx, "/shallow.txt", deep); err != nil {
		t.Fatal(err)
	}
	var manifestID int
	_ = app.DB.Raw().QueryRow(`select coalesce(manifest_message_id,0) from files where canonical_path=? and status='active'`, deep).Scan(&manifestID)
	if manifestID == 0 {
		t.Fatal("manifest reply not created on deep move")
	}
}

func TestFileTooLarge(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	app.cachedLimit = 3
	local := writeLocal(t, "way too big")
	_, err := app.UploadFile(ctx, local, "/big.bin", ConflictFail, false)
	if code := appErrCode(t, err); code != apperr.ErrFileTooLarge {
		t.Fatalf("code = %s, want ERR_FILE_TOO_LARGE", code)
	}
}

func TestConcurrentSamePathUpload(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = app.UploadFile(ctx, local, "/race.txt", ConflictFail, false)
		}(i)
	}
	wg.Wait()
	okCount := 0
	for _, err := range errs {
		if err == nil {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("expected exactly one success, got %d (errs: %v)", okCount, errs)
	}
	var active int
	_ = app.DB.Raw().QueryRow(`select count(*) from files where canonical_path='/race.txt' and status='active'`).Scan(&active)
	if active != 1 {
		t.Fatalf("active rows = %d", active)
	}
}

func TestScanRebuildFromEmptyDB(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "content")
	paths := []string{
		"/Pictures/beach.jpg",
		"/Pictures/2024/06/beach.jpg",
		"/空 白/文件.txt",
		"/emoji/📷.jpg",
		"/a_b/c.txt",
	}
	for _, p := range paths {
		if _, err := app.UploadFile(ctx, local, p, ConflictFail, false); err != nil {
			t.Fatalf("upload %s: %v", p, err)
		}
	}
	channelID, _, _ := app.channelID(ctx)
	for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
		if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
			t.Fatalf("wipe %s: %v", table, err)
		}
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != len(paths) {
		t.Fatalf("active = %v, want %d", res["active"], len(paths))
	}
	for _, p := range paths {
		if got := fileStatus(t, app, p); got != "active" {
			t.Fatalf("%s status = %q after rebuild", p, got)
		}
	}
	var tagCount int
	_ = app.DB.Raw().QueryRow(`select count(*) from path_tags`).Scan(&tagCount)
	if tagCount == 0 {
		t.Fatal("path_tags empty after rebuild")
	}
	_ = channelID
}

func TestScanErrorDedupeAndResolve(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	// Inject an invalid managed message directly into the fake channel.
	local := writeLocal(t, "x")
	f, _ := os.Open(local)
	defer func() { _ = f.Close() }()
	up, err := tg.UploadMedia(ctx, uploadReq(tgChID, "bad.bin", "bad.bin\n\ntd:v1 p=%%% n=%%%", f))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	var errCount int
	_ = app.DB.Raw().QueryRow(`select count(*) from scan_errors where status='pending'`).Scan(&errCount)
	if errCount != 1 {
		t.Fatalf("pending scan errors = %d, want 1 (dedupe)", errCount)
	}
	// Strict mode must exit non-zero.
	if _, err := app.Scan(ctx, ScanOptions{Full: true, Strict: true}); err == nil {
		t.Fatal("strict scan should fail with invalid message present")
	}
	// Fix the message; rescan resolves the error.
	validCaption := "bad.bin\n\ntd:v1 p=" + b64url("/fixed.bin") + " n=" + b64url("bad.bin") + " s=1 h=- m=-"
	if err := tg.EditCaption(ctx, tgChID, up.MessageID, validCaption); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	_ = app.DB.Raw().QueryRow(`select count(*) from scan_errors where status='pending'`).Scan(&errCount)
	if errCount != 0 {
		t.Fatalf("pending scan errors = %d after fix, want 0", errCount)
	}
}

func TestScanIncludeDeletedRecordsTombstone(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/tomb.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteFile(ctx, "/tomb.txt", DeleteOptions{Tombstone: true}); err != nil {
		t.Fatal(err)
	}
	// Simulate DB loss, then rescan with tombstones included.
	_, _ = app.DB.Raw().Exec(`update files set status='active' where canonical_path='/tomb.txt'`)
	res, err := app.Scan(ctx, ScanOptions{Full: true, IncludeDeleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["tombstones"].(int) != 1 {
		t.Fatalf("tombstones = %v", res["tombstones"])
	}
	if got := fileStatus(t, app, "/tomb.txt"); got != "deleted" {
		t.Fatalf("status = %q, want deleted", got)
	}
}

func TestDownloadConflictFlags(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "remote-content")
	if _, err := app.UploadFile(ctx, local, "/dl.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "dl.txt")
	if err := os.WriteFile(dest, []byte("local-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Default fails.
	err := app.DownloadFile(ctx, "/dl.txt", dest, ConflictFail)
	if code := appErrCode(t, err); code != apperr.ErrLocalPathExists {
		t.Fatalf("code = %s, want ERR_LOCAL_PATH_EXISTS", code)
	}
	// Skip keeps local content.
	if err := app.DownloadFile(ctx, "/dl.txt", dest, ConflictSkip); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "local-content" {
		t.Fatalf("skip overwrote local file: %q", data)
	}
	// Auto-rename writes " (1)".
	if err := app.DownloadFile(ctx, "/dl.txt", dest, ConflictRename); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(destDir, "dl (1).txt")
	if data, _ := os.ReadFile(renamed); string(data) != "remote-content" {
		t.Fatalf("auto-rename content = %q", data)
	}
	// Replace overwrites.
	if err := app.DownloadFile(ctx, "/dl.txt", dest, ConflictReplace); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "remote-content" {
		t.Fatalf("replace content = %q", data)
	}
}

func TestUploadPermissionDeniedCleansPending(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	tg.SetDenyPermissions(true)
	_, err := app.UploadFile(ctx, local, "/denied.txt", ConflictFail, false)
	if code := appErrCode(t, err); code != apperr.ErrChannelPermission {
		t.Fatalf("code = %s, want ERR_CHANNEL_PERMISSION", code)
	}
	if got := fileStatus(t, app, "/denied.txt"); got != "" {
		t.Fatalf("pending row left behind with status %q", got)
	}
}

func TestRepairPendingRetriesUpload(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	channelID, _, _ := app.channelID(ctx)
	// Simulate a crash: pending row with no Telegram message.
	_, err := app.DB.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,original_local_path,status,updated_at) values(?,?,?,?,'pending','2020-01-01T00:00:00Z')`,
		channelID, "/crashed.txt", "crashed.txt", local)
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.RepairPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res["repaired"] != 1 {
		t.Fatalf("res = %v", res)
	}
	if got := fileStatus(t, app, "/crashed.txt"); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
}

func TestRecursiveUploadAndDownload(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	src := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("a.txt", "A")
	mustWrite("sub/b.txt", "B")
	mustWrite("sub/deep/c.txt", "C")
	res, err := app.UploadRecursive(ctx, src, "/backup", ConflictFail, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res["uploaded"] != 3 {
		t.Fatalf("res = %v", res)
	}
	dest := t.TempDir()
	if err := app.DownloadRecursive(ctx, "/backup", dest, ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"a.txt": "A", "sub/b.txt": "B", "sub/deep/c.txt": "C"} {
		data, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q", rel, data)
		}
	}
}

func TestIncludeEmptyDirsUnsupported(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	_, err := app.UploadRecursive(context.Background(), t.TempDir(), "/x", ConflictFail, false, false, true)
	if code := appErrCode(t, err); code != apperr.ErrEmptyDirsUnsupported {
		t.Fatalf("code = %s, want ERR_EMPTY_DIRS_UNSUPPORTED", code)
	}
}

func TestAutoRenameCompoundExtension(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/archive.tar.gz", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, local, "/archive.tar.gz", ConflictRename, false); err != nil {
		t.Fatal(err)
	}
	if got := fileStatus(t, app, "/archive (1).tar.gz"); got != "active" {
		t.Fatalf("compound rename missing, status = %q", got)
	}
}

func TestDirectoryDeleteUnsupported(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	_, _ = app.UploadFile(ctx, local, "/dir/f.txt", ConflictFail, false)
	_, err := app.DeleteFile(ctx, "/dir", DeleteOptions{})
	if code := appErrCode(t, err); code != apperr.ErrDirectoryDeleteUnsupported {
		t.Fatalf("code = %s, want ERR_DIRECTORY_DELETE_UNSUPPORTED", code)
	}
}

func TestDirectoryMoveUnsupportedIntegration(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	_, _ = app.UploadFile(ctx, local, "/dirmv/f.txt", ConflictFail, false)
	err := app.MoveFile(ctx, "/dirmv", "/dirmv2")
	if code := appErrCode(t, err); code != apperr.ErrDirectoryMoveUnsupported {
		t.Fatalf("code = %s, want ERR_DIRECTORY_MOVE_UNSUPPORTED", code)
	}
}

func TestFullScanAfterReplace(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "v1")
	if _, err := app.UploadFile(ctx, local, "/rescan.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(local, []byte("v2"), 0o644)
	if _, err := app.UploadFile(ctx, local, "/rescan.txt", ConflictReplace, false); err != nil {
		t.Fatal(err)
	}
	// Full scan must survive the lingering superseded row.
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatalf("full scan after replace: %v", err)
	}
	if res["active"].(int) != 1 {
		t.Fatalf("active = %v", res["active"])
	}
}

func TestFullScanAfterDeleteAndReupload(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "v1")
	if _, err := app.UploadFile(ctx, local, "/cycle.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteFile(ctx, "/cycle.txt", DeleteOptions{Tombstone: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, local, "/cycle.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatalf("full scan after delete+reupload: %v", err)
	}
	if got := fileStatus(t, app, "/cycle.txt"); got != "active" {
		t.Fatalf("status = %q", got)
	}
}

func TestReplaceTombstoneModeRedactsOldMessage(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	app.Cfg.Delete.Mode = "tombstone"
	ctx := context.Background()
	local := writeLocal(t, "SECRET-ORIGINAL")
	if _, err := app.UploadFile(ctx, local, "/leak.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(local, []byte("NEW"), 0o644)
	if _, err := app.UploadFile(ctx, local, "/leak.txt", ConflictReplace, false); err != nil {
		t.Fatal(err)
	}
	tgChID, _ := app.tgChannelID(ctx)
	liveClaims := 0
	for _, m := range tg.Messages(tgChID) {
		if m.Caption == "" {
			continue
		}
		if strings.Contains(m.Caption, "deleted=true") {
			continue
		}
		liveClaims++
	}
	if liveClaims != 1 {
		t.Fatalf("expected exactly one live (non-tombstoned) media message, got %d", liveClaims)
	}
	// And a rescan stays consistent.
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatalf("scan after tombstone replace: %v", err)
	}
}

func TestReplaceOntoDirectoryRejected(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/repdir/f.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	_, err := app.UploadFile(ctx, local, "/repdir", ConflictReplace, false)
	if code := appErrCode(t, err); code != apperr.ErrPathIsDirectory {
		t.Fatalf("code = %s, want ERR_PATH_IS_DIRECTORY", code)
	}
}

func TestListDirEscapesLikeWildcards(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	// /a_b must not match /aXb children via the LIKE '_' wildcard.
	if _, err := app.UploadFile(ctx, local, "/a_b/inside.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, local, "/aXb/other.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	entries, err := app.ListDir(ctx, "/a_b")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, "/aXb") {
			t.Fatalf("wildcard leak: %v", entries)
		}
	}
	if len(entries) != 1 || entries[0].Name != "inside.txt" {
		t.Fatalf("entries = %v", entries)
	}
}

func TestMoveCaptionFailureRestoresManifest(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	src := deepPath("orig.bin")
	if _, err := app.UploadFile(ctx, local, src, ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	var msgID, manifestID int
	_ = app.DB.Raw().QueryRow(`select message_id, coalesce(manifest_message_id,0) from files where canonical_path=? and status='active'`, src).Scan(&msgID, &manifestID)
	if manifestID == 0 {
		t.Fatal("test setup: expected manifest reply")
	}
	tgChID, _ := app.tgChannelID(ctx)
	tg.SetNotEditable(tgChID, msgID, true)
	dst := deepPath("moved.bin")
	if err := app.MoveFile(ctx, src, dst); err == nil {
		t.Fatal("expected move to fail")
	}
	// The manifest reply must still encode the OLD path so a rescan does not
	// silently complete the move.
	for _, m := range tg.Messages(tgChID) {
		if m.ID == manifestID {
			if !strings.Contains(m.Text, b64url(src)) {
				t.Fatalf("manifest reply not restored to source path: %q", m.Text)
			}
			return
		}
	}
	t.Fatal("manifest reply message not found")
}

func TestDirectoryGCAfterDelete(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	_, _ = app.UploadFile(ctx, local, "/gc/only.txt", ConflictFail, false)
	if _, err := app.DeleteFile(ctx, "/gc/only.txt", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	var nodes int
	_ = app.DB.Raw().QueryRow(`select count(*) from nodes where canonical_path='/gc'`).Scan(&nodes)
	if nodes != 0 {
		t.Fatal("empty derived directory not garbage collected")
	}
}

func TestFullScanIgnoresStaleIndexForConflicts(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	channelID, _, _ := app.channelID(ctx)
	tgChID, _ := app.tgChannelID(ctx)
	// Stale index state: an active FILE at /notes whose message no longer
	// exists on Telegram; the real channel now holds files UNDER /notes/.
	_, err := app.DB.Raw().Exec(`insert into files(channel_id,message_id,canonical_path,display_name,status,updated_at)
		values(?,9999,'/notes','notes','active','2020-01-01T00:00:00Z')`, channelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		caption := name + "\nnotes/\n\ntd:v1 p=" + b64url("/notes/"+name) + " n=" + b64url(name) + " s=1 h=- m=-"
		if _, err := tg.UploadMedia(ctx, uploadReq(tgChID, name, caption, strings.NewReader("x"))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/notes/a.txt", "/notes/b.txt"} {
		if got := fileStatus(t, app, p); got != "active" {
			t.Fatalf("%s status = %q, want active", p, got)
		}
	}
	var staleStatus string
	_ = app.DB.Raw().QueryRow(`select status from files where canonical_path='/notes' and message_id=9999`).Scan(&staleStatus)
	if staleStatus != "missing" {
		t.Fatalf("stale /notes status = %q, want missing", staleStatus)
	}
	var bogus int
	_ = app.DB.Raw().QueryRow(`select count(*) from scan_errors where status='pending'`).Scan(&bogus)
	if bogus != 0 {
		t.Fatalf("bogus scan errors = %d", bogus)
	}
}
