package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/core/drive"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
)

func testApp(t *testing.T) (*App, *fake.Client) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	database, err := sqlitestore.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	tg := fake.New()
	tg.SetCredentials("12345", "")
	app := &App{
		Cfg:     cfg,
		DB:      database,
		TG:      tg,
		Runtime: drive.NewRuntime(database, localfs.FS{}, tg),
	}
	return app, tg
}

func loginAndInit(t *testing.T, app *App, tg *fake.Client) {
	t.Helper()
	ctx := context.Background()
	_, err := tg.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.InitRoot(ctx, t.TempDir(), "Test Channel", "Test Channel", "")
	if err != nil {
		t.Fatal(err)
	}
}

// machineRecords returns every message that may carry a machine manifest
// record: the drive channel's messages plus the linked discussion group's
// (ADR 0018 comment carrier).
func machineRecords(t *testing.T, app *App, ctx context.Context) []telegram.Message {
	t.Helper()
	rowID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	discID, _, _, err := app.DB.DiscussionGroup(ctx, rowID)
	if err != nil {
		t.Fatal(err)
	}
	tgChID, _ := app.tgChannelID(ctx)
	out := append([]telegram.Message(nil), app.TG.(*fake.Client).Messages(tgChID)...)
	if discID != "" {
		var gid int64
		_, _ = fmt.Sscanf(discID, "%d", &gid)
		out = append(out, app.TG.(*fake.Client).Messages(gid)...)
	}
	return out
}

func TestUploadAndList(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := app.UploadFile(ctx, local, "/a.txt", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	if data["path"] != "/a.txt" {
		t.Fatalf("path = %v", data["path"])
	}
	entries, err := app.ListDir(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries")
	}
}

func TestDuplicateUploadFails(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/dup.txt", ConflictFail, false)
	_, err := app.UploadFile(ctx, local, "/dup.txt", ConflictFail, false)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestSkipExisting(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/skip.txt", ConflictFail, false)
	data, err := app.UploadFile(ctx, local, "/skip.txt", ConflictSkip, false)
	if err != nil {
		t.Fatal(err)
	}
	if data["skipped"] != true {
		t.Fatalf("data = %v", data)
	}
}

func TestMoveFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/from.txt", ConflictFail, false)
	if err := app.MoveFile(ctx, "/from.txt", "/to.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/del.txt", ConflictFail, false)
	if _, err := app.DeleteFile(ctx, "/del.txt", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestScanIncremental(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/scan.txt", ConflictFail, false)
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["mode"] != "full" {
		t.Fatalf("mode = %v", res["mode"])
	}
}

func TestDownloadFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/get.txt", ConflictFail, false)
	dest := filepath.Join(t.TempDir(), "out.txt")
	if _, err := app.DownloadFile(ctx, "/get.txt", dest, ConflictFail); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "hello" {
		t.Fatalf("data = %q", data)
	}
}

func TestAuthStatus(t *testing.T) {
	app, tg := testApp(t)
	ctx := context.Background()
	status, err := app.AuthStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != false {
		t.Fatalf("status = %v", status)
	}
	_, _ = tg.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{})
	status, err = app.AuthStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status["authenticated"] != true {
		t.Fatalf("status = %v", status)
	}
}

func TestDoctor(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	res, err := app.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checks := res["checks"].(map[string]string)
	if checks["auth"] != "pass" {
		t.Fatalf("checks = %v", checks)
	}
	if checks["saved_history"] != "pass" || checks["saved_delete"] != "pass" {
		t.Fatalf("saved checks = %v", checks)
	}
}

func TestShare(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/share.txt", ConflictFail, false)
	res, err := app.Share(ctx, "/share.txt")
	if err != nil {
		t.Fatal(err)
	}
	if res["invite_link"] == "" {
		t.Fatal("missing invite link")
	}
}

func TestRepairPending(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	res, err := app.RepairPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res["repaired"] == nil {
		t.Fatalf("res = %v", res)
	}
}

func TestRepairPath(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	if _, err := app.UploadFile(ctx, local, "/repair-path.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	res, err := app.RepairPath(ctx, "/repair-path.txt")
	if err != nil {
		t.Fatal(err)
	}
	if res["repaired"] != "/repair-path.txt" {
		t.Fatalf("res = %v", res)
	}
}

func TestRepairScanErrors(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	if _, err := app.UploadFile(ctx, local, "/scan-err.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := "2020-01-01T00:00:00Z"
	if _, err := app.DB.Raw().ExecContext(ctx, `insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at) values(?,?,?,?,?,?,?,?)`,
		channelID, 999999, "ERR_MANIFEST_INVALID", "missing manifest", "", "pending", now, now); err != nil {
		t.Fatal(err)
	}
	res, err := app.RepairScanErrors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res["resolved"] != 1 {
		t.Fatalf("resolved = %v", res["resolved"])
	}
	if res["pending"] != 0 {
		t.Fatalf("pending = %v", res["pending"])
	}
}

func TestReplaceUpload(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("v1"), 0o644)
	_, err := app.UploadFile(ctx, local, "/replace.txt", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(local, []byte("v2-longer"), 0o644)
	_, err = app.UploadFile(ctx, local, "/replace.txt", ConflictReplace, false)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out.txt")
	if _, err := app.DownloadFile(ctx, "/replace.txt", dest, ConflictFail); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "v2-longer" {
		t.Fatalf("data = %q", data)
	}
}

func TestManifestReplyScanRebuild(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	parts := make([]string, 35)
	for i := range parts {
		parts[i] = fmt.Sprintf("level-%02d-unique", i)
	}
	remote := "/" + strings.Join(parts, "/") + "/manifest-reply.bin"
	local := filepath.Join(t.TempDir(), "manifest-reply.bin")
	if err := os.WriteFile(local, []byte("manifest-reply-payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, local, remote, ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().ExecContext(ctx, `delete from path_tags`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().ExecContext(ctx, `delete from files where channel_id=?`, channelID); err != nil {
		t.Fatal(err)
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != 1 {
		t.Fatalf("active = %v", res["active"])
	}
	var messageID int
	if err := app.DB.Raw().QueryRowContext(ctx, `select message_id from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, remote).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	tgChID, _ := app.tgChannelID(ctx)
	// ADR 0018: the indexed media message carries a human-only caption and
	// its machine record is a comment on the discussion thread.
	var chat string
	if err := app.DB.Raw().QueryRowContext(ctx,
		`select manifest_chat_tg_id from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, remote).Scan(&chat); err != nil || chat == "" {
		t.Fatalf("rebuilt row missing comment carrier: chat=%q err=%v", chat, err)
	}
	for _, m := range tg.Messages(tgChID) {
		if m.ID == messageID {
			if strings.Contains(m.Caption, "td:v1") {
				t.Fatalf("caption carries machine text: %q", m.Caption)
			}
			return
		}
	}
	t.Fatalf("indexed message_id %d not found on channel (msgs=%d)", messageID, len(tg.Messages(tgChID)))
}

func TestReplaceCreatesSupersededRow(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("v1"), 0o644)
	data, err := app.UploadFile(ctx, local, "/replace.txt", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	oldID := int(data["message_id"].(int))
	_ = os.WriteFile(local, []byte("v2"), 0o644)
	_, err = app.UploadFile(ctx, local, "/replace.txt", ConflictReplace, false)
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var superseded int
	if err := app.DB.Raw().QueryRowContext(ctx, `
		select count(*) from files where channel_id=? and canonical_path='/replace.txt' and status='superseded'`,
		channelID).Scan(&superseded); err != nil {
		t.Fatal(err)
	}
	if superseded != 1 {
		t.Fatalf("superseded count = %d", superseded)
	}
	var active int
	if err := app.DB.Raw().QueryRowContext(ctx, `
		select count(*) from files where channel_id=? and canonical_path='/replace.txt' and status='active'`,
		channelID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active count = %d", active)
	}
	tgChID, _ := app.tgChannelID(ctx)
	for _, m := range tg.Messages(tgChID) {
		if m.ID == oldID {
			t.Fatalf("old message %d should be deleted after replace", oldID)
		}
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != 1 {
		t.Fatalf("active after full scan = %v", res["active"])
	}
}

func TestScanRejectsFileDirConflict(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "blocker")
	_ = os.WriteFile(local, []byte("blocks"), 0o644)
	_, err := app.UploadFile(ctx, local, "/blocker", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChID, _ := app.tgChannelID(ctx)
	conflictPath := "/blocker/nested.txt"
	caption := manifest.RenderCompact(manifest.FileMeta{
		CanonicalPath: conflictPath,
		DisplayName:   "nested.txt",
		Size:          4,
		MIME:          "text/plain",
	})
	if _, err := tg.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: tgChID,
		Caption:   caption,
		FileName:  "nested.txt",
		MIME:      "text/plain",
		Size:      4,
		Reader:    strings.NewReader("evil"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	var scanErrors int
	if err := app.DB.Raw().QueryRowContext(ctx, `
		select count(*) from scan_errors where channel_id=? and status='pending' and error_code=?`,
		channelID, apperr.ErrPathAncestorIsFile).Scan(&scanErrors); err != nil {
		t.Fatal(err)
	}
	if scanErrors == 0 {
		t.Fatal("expected scan error for file/dir conflict")
	}
	var indexed int
	if err := app.DB.Raw().QueryRowContext(ctx, `
		select count(*) from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, conflictPath).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 0 {
		t.Fatalf("conflicting path indexed as active")
	}
}

func TestStatusIncludesUploadLimit(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	res, err := app.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res["upload_limit_bytes"]; !ok {
		t.Fatalf("status = %v", res)
	}
	if res["authenticated"] != true {
		t.Fatalf("status = %v", res)
	}
	_ = fmt.Sprint(res["upload_limit_bytes"])
}

func TestMapTGErrFloodWaitDetails(t *testing.T) {
	err := telegram.MapError(&telegram.FloodWaitError{Seconds: 85286})
	ae, ok := apperr.As(err)
	if !ok {
		t.Fatalf("not an AppError: %v", err)
	}
	if ae.Code != apperr.ErrTelegramRateLimited {
		t.Fatalf("code = %s", ae.Code)
	}
	if !strings.Contains(ae.Message, "23h41m26s") {
		t.Fatalf("message should contain human duration, got %q", ae.Message)
	}
	if got := ae.Details["retry_after_seconds"]; got != 85286 {
		t.Fatalf("retry_after_seconds = %v", got)
	}
	if _, ok := ae.Details["retry_at"]; !ok {
		t.Fatal("retry_at missing from details")
	}
}

func TestAuthLoginReportsAlreadyAuthenticated(t *testing.T) {
	app, _ := testApp(t)
	app.Cfg.Telegram.APIID = 1
	app.Cfg.Telegram.APIHash = "hash"
	app.Cfg.Telegram.Phone = "+1000"
	ctx := context.Background()
	code := func(telegram.CodePrompt) (string, error) { return "12345", nil }
	pw := func() (string, error) { return "", nil }

	data, err := app.AuthLogin(ctx, code, pw, telegram.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data["already_authenticated"] != false {
		t.Fatalf("first login already_authenticated = %v", data["already_authenticated"])
	}
	data, err = app.AuthLogin(ctx, code, pw, telegram.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data["already_authenticated"] != true {
		t.Fatalf("second login already_authenticated = %v", data["already_authenticated"])
	}
}

func TestMapTGErrLoginErrorCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&telegram.CodeInvalidError{Attempts: 3}, apperr.ErrAuthFailed},
		{&telegram.PasswordInvalidError{}, apperr.ErrAuthFailed},
		{&telegram.CodeExpiredError{}, apperr.ErrAuthFailed},
		{&telegram.PhoneInvalidError{}, apperr.ErrConfigInvalid},
	}
	for _, tc := range cases {
		ae, ok := apperr.As(telegram.MapError(tc.err))
		if !ok {
			t.Fatalf("%T: not an AppError", tc.err)
		}
		if ae.Code != tc.code {
			t.Fatalf("%T: code = %s, want %s", tc.err, ae.Code, tc.code)
		}
	}
}

func TestInitRootIdempotentCreate(t *testing.T) {
	app, tg := testApp(t)
	ctx := context.Background()
	_, _ = tg.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{})
	root := t.TempDir()
	first, err := app.InitRoot(ctx, root, "", "My Drive", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.InitRoot(ctx, root, "", "My Drive", "")
	if err != nil {
		t.Fatal(err)
	}
	if second["already_initialized"] != true {
		t.Fatalf("second init should be a no-op, got %v", second)
	}
	if first["channel_id"] != second["channel_id"] {
		t.Fatalf("channel changed across re-init: %v -> %v", first["channel_id"], second["channel_id"])
	}
}

func TestUploadToRootKeepsBasename(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "readme.txt")
	_ = os.WriteFile(local, []byte("hi"), 0o644)
	data, err := app.UploadFile(ctx, local, "/", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	if data["path"] != "/readme.txt" {
		t.Fatalf("path = %v, want /readme.txt", data["path"])
	}
}

func TestDownloadIntoDirectory(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/a.txt", ConflictFail, false)
	dir := t.TempDir()
	res, err := app.DownloadFile(ctx, "/a.txt", dir, ConflictFail)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dest != filepath.Join(dir, "a.txt") {
		t.Fatalf("dest = %q", res.Dest)
	}
	data, _ := os.ReadFile(res.Dest)
	if string(data) != "hello" {
		t.Fatalf("data = %q", data)
	}
}

func TestDownloadSkipExistingReported(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/a.txt", ConflictFail, false)
	dest := filepath.Join(t.TempDir(), "out.txt")
	_ = os.WriteFile(dest, []byte("LOCAL"), 0o644)
	res, err := app.DownloadFile(ctx, "/a.txt", dest, ConflictSkip)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatal("expected Skipped=true")
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "LOCAL" {
		t.Fatalf("local file overwritten: %q", data)
	}
}

func TestListDirNotFoundErrors(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	_, err := app.ListDir(ctx, "/does-not-exist")
	if code := appErrCode(t, err); code != apperr.ErrRemoteNotFound {
		t.Fatalf("code = %s, want ERR_REMOTE_NOT_FOUND", code)
	}
}

func TestListDirOfFileListsFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(local, []byte("hello"), 0o644)
	_, _ = app.UploadFile(ctx, local, "/a.txt", ConflictFail, false)
	entries, err := app.ListDir(ctx, "/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != "file" || entries[0].Name != "a.txt" {
		t.Fatalf("entries = %+v", entries)
	}
}
