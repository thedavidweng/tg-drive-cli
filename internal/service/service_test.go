package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/core/drive"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
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
