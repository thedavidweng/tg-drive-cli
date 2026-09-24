package telegramgotd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoginStateFilePermissions(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "cfg", "session.json")
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: time.Now().UTC()})
	info, err := os.Stat(loginStatePath(sessionPath))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestLoginStateRejectsOtherPhone(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: time.Now().UTC()})
	if _, ok := loadLoginState(sessionPath, "+2000", time.Now().UTC()); ok {
		t.Fatal("state for another phone must not be reused")
	}
}

func TestLoginStateExpires(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	sent := time.Now().UTC().Add(-loginStateTTL - time.Minute)
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: sent})
	if _, ok := loadLoginState(sessionPath, "+1000", time.Now().UTC()); ok {
		t.Fatal("expired state must not be reused")
	}
}

func TestLoginStateMissingFile(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if _, ok := loadLoginState(sessionPath, "+1000", time.Now().UTC()); ok {
		t.Fatal("missing file must not produce state")
	}
}

func TestClearLoginState(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: time.Now().UTC()})
	clearLoginState(sessionPath)
	if _, err := os.Stat(loginStatePath(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("state file should be removed, stat err = %v", err)
	}
}

func TestLoginStateCorruptFile(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(loginStatePath(sessionPath), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadLoginState(sessionPath, "+1000", time.Now().UTC()); ok {
		t.Fatal("corrupt file must not produce state")
	}
}
