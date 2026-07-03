package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePathsUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TD_CONFIG", "")
	t.Setenv("TD_SESSION", "")
	t.Setenv("TD_DB", "")

	cfgPath, sessPath, dbPath := ResolvePaths(Overrides{})
	wantCfg := filepath.Join(home, ".config", "tg-drive-cli", "config.toml")
	wantSess := filepath.Join(home, ".config", "tg-drive-cli", "session.json")
	wantDB := filepath.Join(home, ".local", "share", "tg-drive-cli", "local_cache.db")
	if cfgPath != wantCfg || sessPath != wantSess || dbPath != wantDB {
		t.Fatalf("paths = %q %q %q, want %q %q %q", cfgPath, sessPath, dbPath, wantCfg, wantSess, wantDB)
	}
}

func TestResolvePathsEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TD_DB", filepath.Join(tmp, "custom.db"))
	_, _, dbPath := ResolvePaths(Overrides{})
	if dbPath != filepath.Join(tmp, "custom.db") {
		t.Fatalf("db = %q", dbPath)
	}
}

func TestRedactSecrets(t *testing.T) {
	cfg := Defaults()
	cfg.Telegram.APIHash = "secret123"
	cfg.Telegram.Phone = "+1234567890"
	m := RedactConfigMap(cfg, false)
	if m["telegram.api_hash"] != "redacted" {
		t.Fatalf("api_hash = %v", m["telegram.api_hash"])
	}
	phone := m["telegram.phone"].(string)
	if phone == "+1234567890" {
		t.Fatal("phone not redacted")
	}
}

func TestRedactShowSecrets(t *testing.T) {
	cfg := Defaults()
	cfg.Telegram.APIHash = "secret123"
	m := RedactConfigMap(cfg, true)
	if m["telegram.api_hash"] != "secret123" {
		t.Fatalf("api_hash = %v", m["telegram.api_hash"])
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := Defaults()
	cfg.Telegram.APIID = 42
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Telegram.APIID != 42 {
		t.Fatalf("api_id = %d", loaded.Telegram.APIID)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", info.Mode().Perm())
	}
}
