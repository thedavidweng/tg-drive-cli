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

// TestImportReservedForExternalIngest pins the adopt/import vocabulary split
// (ADR 0019): old in-place claim forms fail with pointers naming the split.
func TestImportReservedForExternalIngest(t *testing.T) {
	bin := buildBinary(t)
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"bare import", []string{"import"}, "ERR_USAGE"},
		{"old unmanaged form", []string{"import", "--unmanaged"}, "ERR_USAGE"},
		{"old rewrite-captions form", []string{"import", "--rewrite-captions"}, "ERR_USAGE"},
		{"old positional form", []string{"import", "123", "/videos/x.mp4"}, "ERR_USAGE"},
		{"old flag on new form", []string{"import", "saved", "--unmanaged"}, "ERR_FLAG_CONFLICT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, append([]string{"--json"}, tc.args...)...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			exit, ok := err.(*exec.ExitError)
			if err == nil || !ok || !exit.Exited() {
				t.Fatalf("err = %v", err)
			}
			if exit.ExitCode() != 2 {
				t.Fatalf("exit = %d, want 2 (stdout=%s)", exit.ExitCode(), stdout.String())
			}
			out := stdout.String()
			if !strings.Contains(out, tc.code) {
				t.Fatalf("%s missing from %s", tc.code, out)
			}
			if !strings.Contains(out, "td adopt") || !strings.Contains(out, "td import saved") {
				t.Fatalf("split pointer missing: %s", out)
			}
		})
	}
}

// TestImportSourceValidation checks the first external-chat source (`saved`)
// and keeps unknown source names rejected.
func TestImportSourceValidation(t *testing.T) {
	bin := buildBinary(t)
	for src, want := range map[string]string{"random": "unknown import source"} {
		cmd := exec.Command(bin, "--json", "import", src)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err == nil {
			t.Fatalf("expected error for source %q", src)
		}
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("source %q: %s", src, stdout.String())
		}
	}

	cmd := exec.Command(bin, "--json", "import", "saved")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if err == nil || !ok || !exit.Exited() {
		t.Fatalf("expected confirmation error, err = %v", err)
	}
	if exit.ExitCode() != 10 {
		t.Fatalf("exit = %d, want 10 (stdout=%s)", exit.ExitCode(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "ERR_CONFIRMATION_REQUIRED") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

// TestE2EImportSavedSurface pins the binary-level safety, dry-run, event, and
// JSON contracts without needing a real saved message. The fake account starts
// with an empty Saved Messages chat, which is enough to exercise the command
// lifecycle and its stable result envelope.
func TestE2EImportSavedSurface(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 10, "ERR_CONFIRMATION_REQUIRED",
		"import", "saved")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"import", "saved", "--confirm", "--photos-as", "archive")

	plan := runE2EJSON(t, bin, cfgPath, dbPath, statePath,
		"import", "saved", "--dry-run", "--photos-as", "document")
	if plan["source"] != "saved" || plan["into"] != "/saved" || plan["dry_run"] != true {
		t.Fatalf("import plan = %v", plan)
	}
	if plan["history_complete"] != true {
		t.Fatalf("empty saved history should be complete: %v", plan)
	}

	events := runE2EEvents(t, bin, cfgPath, dbPath, statePath,
		"import", "saved", "--dry-run", "--photos-as", "document", "--events")
	if len(events) != 1 || cmd(events[0]) != "import" {
		t.Fatalf("import events = %v", events)
	}
	if data, ok := events[0]["data"].(map[string]any); !ok || data["source"] != "saved" {
		t.Fatalf("final import event = %v", events[0])
	}
}

// TestAdoptRequiresConfirm gates the renamed claim command.
func TestAdoptRequiresConfirm(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "--json", "adopt", "--unmanaged")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if err == nil || !ok || !exit.Exited() {
		t.Fatalf("err = %v", err)
	}
	if exit.ExitCode() != 10 {
		t.Fatalf("exit = %d, want 10 (stdout=%s)", exit.ExitCode(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "ERR_CONFIRMATION_REQUIRED") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}
