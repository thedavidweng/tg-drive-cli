package fake

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

func TestPersistentStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "fake.json")
	ctx := context.Background()

	c1 := NewPersistent(path)
	_, err := c1.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c1.CreateChannel(ctx, "Persist Test")
	if err != nil {
		t.Fatal(err)
	}
	up, err := c1.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: ch.ID, FileName: "a.txt", Size: 5, Reader: strings.NewReader("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// A fresh client from the same path sees everything.
	c2 := NewPersistent(path)
	if _, ok, _ := c2.Status(ctx); !ok {
		t.Fatal("login should persist")
	}
	got, err := c2.ResolveChannel(ctx, "Persist Test")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != ch.ID {
		t.Fatalf("channel ID = %d, want %d", got.ID, ch.ID)
	}
	var buf bytes.Buffer
	if err := c2.DownloadMedia(ctx, ch.ID, up.MessageID, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Fatalf("data = %q", buf.String())
	}

	// Logout persists too.
	if err := c2.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	c3 := NewPersistent(path)
	if _, ok, _ := c3.Status(ctx); ok {
		t.Fatal("logout should persist")
	}
}

func TestNonPersistentUnaffected(t *testing.T) {
	c := New()
	ctx := context.Background()
	_, err := c.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// save() with empty statePath must be a no-op (no panic, no file).
	if _, ok, _ := c.Status(ctx); !ok {
		t.Fatal("expected logged in")
	}
}
