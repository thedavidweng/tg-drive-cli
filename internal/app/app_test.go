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
	cmd.Env = append(os.Environ(), "TD_FAKE_TELEGRAM=1")
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
	// go run does not propagate the child's exit code, so build a real binary.
	bin := filepath.Join(t.TempDir(), "td")
	build := exec.Command("go", "build", "-o", bin, filepath.Join("..", "..", "cmd", "td"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "no-such-command")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error")
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || !exit.Exited() {
		t.Fatalf("err = %v", err)
	}
	if exit.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", exit.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDoctorJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "db.sqlite")
	_ = os.WriteFile(cfgPath, []byte(""), 0o600)
	cmd := exec.Command("go", "run", filepath.Join("..", "..", "cmd", "td"),
		"--config", cfgPath, "--db", dbPath, "doctor", "--json")
	cmd.Env = append(os.Environ(), "TD_FAKE_TELEGRAM=1")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"ok":true`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRepairDeleteOrphanedRequiresConfirm(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "--json", "repair", "--orphaned", "--delete-orphaned")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected confirmation error")
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	if exit.ExitCode() != 10 {
		t.Fatalf("exit = %d, want 10 (stdout=%s stderr=%s)", exit.ExitCode(), stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "ERR_CONFIRMATION_REQUIRED") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

// TestRepairDeleteOrphanedConfirm runs the destructive repair end to end:
// --confirm must clear the confirmation gate and reach the repair itself.
func TestRepairDeleteOrphanedConfirm(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)

	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	data := runE2EJSON(t, bin, cfgPath, dbPath, statePath,
		"repair", "--orphaned", "--delete-orphaned", "--confirm")
	if repaired, _ := data["repaired"].(float64); repaired != 0 {
		t.Fatalf("repaired = %v, want 0 on a clean channel", data)
	}
	if deleted, _ := data["deleted"].(float64); deleted != 0 {
		t.Fatalf("deleted = %v, want 0 on a clean channel", data)
	}
}
