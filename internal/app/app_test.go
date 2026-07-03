package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	cmd := exec.Command("go", "run", filepath.Join("..", "..", "cmd", "td"), "version", "--json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env["ok"] != true {
		t.Fatalf("ok = %v", env["ok"])
	}
}

func TestInvalidCommandExit2(t *testing.T) {
	cmd := exec.Command("go", "run", filepath.Join("..", "..", "cmd", "td"), "no-such-command")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error")
	}
	if exit, ok := err.(*exec.ExitError); !ok || !exit.Exited() {
		t.Fatalf("err = %v", err)
	}
}

func TestDoctorJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "db.sqlite")
	_ = os.WriteFile(cfgPath, []byte(""), 0o600)
	cmd := exec.Command("go", "run", filepath.Join("..", "..", "cmd", "td"),
		"--config", cfgPath, "--db", dbPath, "doctor", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"ok":true`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}
