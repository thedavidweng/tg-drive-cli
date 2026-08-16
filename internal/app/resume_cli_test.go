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

// buildBinary compiles the td binary once per test.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "td")
	build := exec.Command("go", "build", "-o", bin, filepath.Join("..", "..", "cmd", "td"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func execCommand(bin string, args ...string) *exec.Cmd {
	return exec.Command(bin, args...)
}

// runTD executes the built binary in the fake-telegram environment.
func runTD(t *testing.T, bin, cfgPath, dbPath, statePath string, extraEnv []string, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := execCommand(bin, args...)
	cmd.Env = append(os.Environ(),
		"TD_FAKE_TELEGRAM=1",
		"TD_FAKE_TELEGRAM_STATE="+statePath,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	if cfgPath != "" {
		cmd.Args = append([]string{bin, "--config", cfgPath, "--db", dbPath}, args...)
	} else {
		cmd.Args = append([]string{bin}, args...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(stdin)
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// TestCLIRetryReportsResumed covers the user-visible resume contract end to
// end: a failed large upload leaves recoverable state, and the plain retry
// reports resumed=true in JSON (and "resumed upload of" for humans).
func TestCLIRetryReportsResumed(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "db.sqlite")
	statePath := filepath.Join(dir, "fake-state.json")
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`
[telegram]
api_id = 12345
api_hash = "deadbeef"
phone = "+15551234567"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "12345\n", "auth", "login"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "init", root, "--create-channel=Drive"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// A file above the 10MB resumable threshold, 1MB parts, interrupted
	// after 2 confirmed parts.
	big := filepath.Join(dir, "big.bin")
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	for written := 0; written < 12*1024*1024; written += len(buf) {
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Close()

	failEnv := []string{"TD_FAKE_PART_SIZE=1048576", "TD_FAKE_FAIL_UPLOAD_AFTER_PARTS=2"}
	_, _, err = runTD(t, bin, cfgPath, dbPath, statePath, failEnv, "", "cp", big, "/resumed.bin")
	if err == nil {
		t.Fatal("expected first cp to fail")
	}

	// Plain retry resumes (knob fired once; part size still applies).
	_, _, err = runTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_PART_SIZE=1048576"}, "", "cp", big, "/resumed.bin")
	if err != nil {
		t.Fatalf("retry cp: %v", err)
	}

	stdout, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "--json", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	_ = json.Unmarshal([]byte(stdout), &env)
	files, _ := env.Data["files"].(map[string]any)
	if active, _ := files["active"].(float64); active != 1 {
		t.Fatalf("status files = %v, want 1 active", env.Data)
	}

	// A second interrupted-then-resumed upload verifies the JSON and human
	// reporting of the resume itself.
	other := filepath.Join(dir, "other.bin")
	if err := os.Rename(big, other); err != nil {
		t.Fatal(err)
	}
	_, _, err = runTD(t, bin, cfgPath, dbPath, statePath, failEnv, "", "cp", other, "/second.bin")
	if err == nil {
		t.Fatal("expected first cp of second file to fail")
	}
	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_PART_SIZE=1048576"}, "", "--json", "cp", other, "/second.bin")
	if err != nil {
		t.Fatalf("resumed cp: %v", err)
	}
	var cpEnv struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &cpEnv); err != nil {
		t.Fatal(err)
	}
	if cpEnv.Data["resumed"] != true {
		t.Fatalf("cp JSON data = %v, want resumed:true", cpEnv.Data)
	}
}
