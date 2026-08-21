package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// e2eEnvelope mirrors docs/contracts/json-contract.md.
type e2eEnvelope struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// runE2EJSON runs td --json and returns the success data payload.
func runE2EJSON(t *testing.T, bin, cfgPath, dbPath, statePath string, args ...string) map[string]any {
	t.Helper()
	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", prependJSON(args...)...)
	if err != nil {
		t.Fatalf("%v: stdout=%s stderr=%s", args, stdout, stderr)
	}
	var env e2eEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("%v: bad JSON %q: %v", args, stdout, err)
	}
	if !env.OK || env.Error != nil {
		t.Fatalf("%v: expected success envelope, got %s", args, stdout)
	}
	return env.Data
}

// runE2EExpectError asserts the exit code and error code from the CLI
// contract's exit-code mapping.
func runE2EExpectError(t *testing.T, bin, cfgPath, dbPath, statePath string, wantExit int, wantCode string, args ...string) {
	t.Helper()
	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", prependJSON(args...)...)
	if err == nil {
		t.Fatalf("%v: expected failure, stdout=%s", args, stdout)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("%v: err = %v", args, err)
	}
	if exit.ExitCode() != wantExit {
		t.Fatalf("%v: exit = %d, want %d (stdout=%s stderr=%s)", args, exit.ExitCode(), wantExit, stdout, stderr)
	}
	var env e2eEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("%v: bad JSON %q: %v", args, stdout, err)
	}
	if env.OK || env.Error == nil || env.Error.Code != wantCode {
		t.Fatalf("%v: want error %s, got %s", args, wantCode, stdout)
	}
}

func prependJSON(args ...string) []string {
	return append([]string{"--json"}, args...)
}

func e2eLogin(t *testing.T, bin, cfgPath, dbPath, statePath string) {
	t.Helper()
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "12345\n", "auth", "login"); err != nil {
		t.Fatalf("login: %v", err)
	}
}

func e2eSetup(t *testing.T, dir string) (bin, cfgPath, dbPath, statePath, root string) {
	t.Helper()
	bin = buildBinary(t)
	cfgPath = filepath.Join(dir, "config.toml")
	dbPath = filepath.Join(dir, "db.sqlite")
	statePath = filepath.Join(dir, "fake-state.json")
	root = filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `
[telegram]
api_id = 12345
api_hash = "deadbeef"
phone = "+15551234567"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return bin, cfgPath, dbPath, statePath, root
}

func e2eLocalFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestE2ELifecycle walks the full user journey through the real binary:
// login, init, upload, browse, download, move, delete, share, scan,
// config, self-test, logout.
func TestE2ELifecycle(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)

	e2eLogin(t, bin, cfgPath, dbPath, statePath)

	status := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "auth", "status")
	if status["authenticated"] != true {
		t.Fatalf("auth status = %v, want authenticated", status)
	}

	initData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	if initData["channel_id"] == nil || initData["channel_title"] != "Drive" {
		t.Fatalf("init = %v", initData)
	}

	chans := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "channels", "list")
	list, _ := chans["channels"].([]any)
	if len(list) < 1 {
		t.Fatalf("channels list = %v", chans)
	}

	aLocal := e2eLocalFile(t, dir, "a.txt", "hello from a\n")
	bLocal := e2eLocalFile(t, dir, "b.txt", strings.Repeat("b", 4096))

	upA := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", aLocal, "/docs/a.txt")
	if upA["path"] != "/docs/a.txt" {
		t.Fatalf("cp = %v", upA)
	}
	if id, _ := upA["message_id"].(float64); id <= 0 {
		t.Fatalf("cp message_id = %v", upA["message_id"])
	}
	if size, _ := upA["size"].(float64); size != float64(len("hello from a\n")) {
		t.Fatalf("cp size = %v", upA["size"])
	}

	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", bLocal, "/docs/b.txt")

	lsData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/docs")
	entries, _ := lsData["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("ls /docs = %v", lsData)
	}

	treeData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "tree", "/")
	docs := findTreeNode(treeData["tree"], "docs")
	if docs == nil {
		t.Fatalf("tree missing docs: %v", treeData)
	}
	if kids, _ := docs["children"].([]any); len(kids) != 2 {
		t.Fatalf("tree docs children = %v", docs)
	}

	restore := filepath.Join(dir, "restore-single", "a-copy.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/docs/a.txt", restore)
	got, err := os.ReadFile(restore)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello from a\n" {
		t.Fatalf("downloaded content = %q", got)
	}

	restoreDir := filepath.Join(dir, "restore-dir")
	recData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "--recursive", "/docs", restoreDir)
	if n, _ := recData["downloaded"].(float64); n != 2 {
		t.Fatalf("recursive get = %v", recData)
	}

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 10, "ERR_CONFIRMATION_REQUIRED",
		"mv", "/docs/b.txt", "/docs/renamed.txt")
	mvData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "mv", "--confirm", "/docs/b.txt", "/docs/renamed.txt")
	if mvData["from"] != "/docs/b.txt" || mvData["to"] != "/docs/renamed.txt" {
		t.Fatalf("mv = %v", mvData)
	}

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 10, "ERR_CONFIRMATION_REQUIRED",
		"rm", "/docs/renamed.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "rm", "--confirm", "/docs/renamed.txt")

	lsAfter := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/docs")
	entriesAfter, _ := lsAfter["entries"].([]any)
	if len(entriesAfter) != 1 {
		t.Fatalf("ls after rm = %v", lsAfter)
	}

	shareData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "share", "/")
	if link, _ := shareData["invite_link"].(string); link == "" {
		t.Fatalf("share = %v", shareData)
	}

	scanData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan")
	if n, _ := scanData["active"].(float64); n != 1 {
		t.Fatalf("scan = %v, want 1 active", scanData)
	}

	statusData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "status")
	files, _ := statusData["files"].(map[string]any)
	if active, _ := files["active"].(float64); active != 1 {
		t.Fatalf("status = %v, want 1 active", statusData)
	}

	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "hash.enabled", "false")
	cfgGet := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "get", "hash.enabled")
	if cfgGet["hash.enabled"] != false {
		t.Fatalf("config get = %v", cfgGet)
	}

	pcData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "doctor", "path-codec")
	if pcData["fixed_vectors"] != "pass" {
		t.Fatalf("path-codec = %v", pcData)
	}

	logoutData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "auth", "logout")
	if logoutData["status"] != "logged_out" {
		t.Fatalf("logout = %v", logoutData)
	}
	statusAfter := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "auth", "status")
	if statusAfter["authenticated"] == true {
		t.Fatalf("auth status after logout = %v", statusAfter)
	}
}

// TestE2ERemoteNotFoundExitCode verifies the contract exit-code mapping for a
// missing remote path through the real binary.
func TestE2ERemoteNotFoundExitCode(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_REMOTE_NOT_FOUND",
		"get", "/nope.txt", filepath.Join(dir, "out.bin"))
}

// findTreeNode finds a node by name in the tree payload.
func findTreeNode(nodes any, name string) map[string]any {
	list, ok := nodes.([]any)
	if !ok {
		return nil
	}
	for _, n := range list {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == name {
			return m
		}
	}
	return nil
}
