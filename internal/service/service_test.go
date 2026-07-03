package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/db"
	"github.com/thedavidweng/tg-drive-cli/internal/telegram/fake"
)

func testApp(t *testing.T) (*App, *fake.Client) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	database, err := db.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	tg := fake.New()
	tg.SetCredentials("12345", "")
	app := &App{Cfg: cfg, DB: database, TG: tg}
	return app, tg
}

func loginAndInit(t *testing.T, app *App, tg *fake.Client) {
	t.Helper()
	ctx := context.Background()
	_, err := tg.Login(ctx, 1, "hash", "+1000", func() (string, error) { return "12345", nil }, func() (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.InitRoot(ctx, t.TempDir(), "Test Channel", "Test Channel", "")
	if err != nil {
		t.Fatal(err)
	}
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
	if err := app.DeleteFile(ctx, "/del.txt"); err != nil {
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
	if err := app.DownloadFile(ctx, "/get.txt", dest, ConflictFail); err != nil {
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
	_, _ = tg.Login(ctx, 1, "hash", "+1000", func() (string, error) { return "12345", nil }, func() (string, error) { return "", nil })
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
