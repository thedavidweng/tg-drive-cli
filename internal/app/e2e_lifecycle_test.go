package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"lukechampine.com/blake3"
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
	// ls --json contract (docs/contracts/json-contract.md): file entries
	// always carry the stored blake3 content hash.
	for name, want := range map[string]string{
		"a.txt": blake3Hex([]byte("hello from a\n")),
		"b.txt": blake3Hex([]byte(strings.Repeat("b", 4096))),
	} {
		entry := findEntry(entries, name)
		if entry == nil {
			t.Fatalf("ls /docs missing %s: %v", name, lsData)
		}
		if entry["hash"] != want {
			t.Fatalf("ls %s hash = %v, want %s", name, entry["hash"], want)
		}
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

// findEntry finds a listing entry by name in an ls --json payload.
func findEntry(entries any, name string) map[string]any {
	list, ok := entries.([]any)
	if !ok {
		return nil
	}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == name {
			return m
		}
	}
	return nil
}

// TestE2ETypedLifecycle publishes typed content through the real binary,
// verifies byte identity and NDJSON parity, then wipes the database and
// reconstructs purely from the channel with scan --full. Covers the typed
// photo and video kinds; offline the fake models photo read-back verbatim,
// while real Telegram serves its recompressed largest representation
// (docs/integration-notes.md).
func TestE2ETypedLifecycle(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)

	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	content := bytes.Repeat([]byte("scene-frame-"), 2048)
	videoLocal := filepath.Join(dir, "scene.mp4")
	if err := os.WriteFile(videoLocal, content, 0o644); err != nil {
		t.Fatal(err)
	}
	poster := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x00}, 32)...)
	posterPath := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(posterPath, poster, 0o644); err != nil {
		t.Fatal(err)
	}
	pixels := bytes.Repeat([]byte("photo-pixels-"), 512)
	photoLocal := filepath.Join(dir, "beach.jpg")
	if err := os.WriteFile(photoLocal, pixels, 0o644); err != nil {
		t.Fatal(err)
	}

	upTyped := runE2EJSON(t, bin, cfgPath, dbPath, statePath,
		"cp", videoLocal, "/media/scene.mp4",
		"--as", "video",
		"--duration", "97.5",
		"--width", "1280",
		"--height", "720",
		"--streaming",
		"--thumb", posterPath,
	)
	wantHash := blake3Hex(content)
	if upTyped["hash"] != wantHash {
		t.Fatalf("typed cp hash = %v, want %s", upTyped["hash"], wantHash)
	}
	if size, _ := upTyped["size"].(float64); size != float64(len(content)) {
		t.Fatalf("typed cp size = %v", upTyped["size"])
	}

	upPhoto := runE2EJSON(t, bin, cfgPath, dbPath, statePath,
		"cp", photoLocal, "/pics/beach.jpg", "--as", "photo")
	wantPhotoHash := blake3Hex(pixels)
	if upPhoto["hash"] != wantPhotoHash {
		t.Fatalf("photo cp hash = %v, want %s", upPhoto["hash"], wantPhotoHash)
	}

	// Byte fidelity: the attributed video downloads back bit-identical.
	restore := filepath.Join(dir, "restored.mp4")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/media/scene.mp4", restore)
	got, err := os.ReadFile(restore)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("restored bytes differ from source")
	}
	photoRestore := filepath.Join(dir, "restored.jpg")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/pics/beach.jpg", photoRestore)
	photoGot, err := os.ReadFile(photoRestore)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(photoGot, pixels) {
		t.Fatal("photo bytes differ from source")
	}

	// NDJSON parity: typed uploads emit the same event shapes as plain ones.
	plainLocal := e2eLocalFile(t, dir, "plain.bin", "plain payload")
	plainEvents := runE2EEvents(t, bin, cfgPath, dbPath, statePath, "cp", plainLocal, "/docs/plain.bin", "--events")
	typedEvents := runE2EEvents(t, bin, cfgPath, dbPath, statePath,
		"cp", videoLocal, "/media/again.mp4", "--as", "video", "--duration", "1", "--width", "2", "--height", "2", "--events")
	photoEvents := runE2EEvents(t, bin, cfgPath, dbPath, statePath,
		"cp", photoLocal, "/pics/again.jpg", "--as", "photo", "--events")
	for name, events := range map[string][]map[string]any{"plain": plainEvents, "video": typedEvents, "photo": photoEvents} {
		if len(events) != 1 {
			t.Fatalf("%s event count = %d, want 1", name, len(events))
		}
		if cmd(events[0]) != "cp" {
			t.Fatalf("%s event command = %q, want cp", name, cmd(events[0]))
		}
		if !equalKeys(plainEvents[0]["data"], events[0]["data"]) {
			t.Fatalf("%s cp event shape differs: plain=%v typed=%v", name, keys(plainEvents[0]["data"]), keys(events[0]["data"]))
		}
	}

	// Reconstruction: wipe the local cache entirely, rebind, scan --full.
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=Drive")
	scanData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan", "--full")
	if n, _ := scanData["active"].(float64); n != 5 {
		t.Fatalf("scan = %v, want 5 active", scanData)
	}
	lsData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/media")
	entries, _ := lsData["entries"].([]any)
	var entry map[string]any
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m["name"] == "scene.mp4" {
			entry = m
		}
	}
	if entry == nil {
		t.Fatalf("ls /media missing scene.mp4: %v", lsData)
	}
	if size, _ := entry["size"].(float64); size != float64(len(content)) {
		t.Fatalf("reconstructed size = %v", entry["size"])
	}
	// The manifest-restored hash must surface through ls --json after the
	// wipe + scan --full rebuild.
	if entry["hash"] != wantHash {
		t.Fatalf("reconstructed hash = %v, want %s", entry["hash"], wantHash)
	}
	picsData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/pics")
	picEntries, _ := picsData["entries"].([]any)
	var photoEntry map[string]any
	for _, e := range picEntries {
		m, _ := e.(map[string]any)
		if m["name"] == "beach.jpg" {
			photoEntry = m
		}
	}
	if photoEntry == nil {
		t.Fatalf("ls /pics missing beach.jpg: %v", picsData)
	}
	if size, _ := photoEntry["size"].(float64); size != float64(len(pixels)) {
		t.Fatalf("reconstructed photo size = %v", photoEntry["size"])
	}
	if photoEntry["hash"] != wantPhotoHash {
		t.Fatalf("reconstructed photo hash = %v, want %s", photoEntry["hash"], wantPhotoHash)
	}
	rebuilt := filepath.Join(dir, "rebuilt.mp4")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/media/scene.mp4", rebuilt)
	reGot, err := os.ReadFile(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reGot, content) || blake3Hex(reGot) != wantHash {
		t.Fatal("reconstructed content or hash mismatch")
	}
	rebuiltPhoto := filepath.Join(dir, "rebuilt.jpg")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/pics/beach.jpg", rebuiltPhoto)
	rePhotoGot, err := os.ReadFile(rebuiltPhoto)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rePhotoGot, pixels) {
		t.Fatal("reconstructed photo content mismatch")
	}
}

// TestE2ETypedFlagUsage pins the typed-upload flag failures to the contract
// exit-code mapping (usage errors: exit 2).
func TestE2ETypedFlagUsage(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	local := e2eLocalFile(t, dir, "v.mp4", "x")

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", local, "/v.mp4", "--as", "hologram")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", local, "/v.mp4", "--width", "100")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", local, "/v.mp4", "--duration", "-1", "--as", "video")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", local, "/v.mp4", "--as", "photo", "--thumb", "poster.jpg")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", "--recursive", local, "/media", "--as", "video")

	// A valid photo upload still succeeds end to end.
	up := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", local, "/pics/v.mp4", "--as", "photo")
	if up["path"] != "/pics/v.mp4" {
		t.Fatalf("photo cp = %v", up)
	}
}

// runE2EEvents runs td --events and returns every emitted envelope.
func runE2EEvents(t *testing.T, bin, cfgPath, dbPath, statePath string, args ...string) []map[string]any {
	t.Helper()
	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", prependJSON(args...)...)
	if err != nil {
		t.Fatalf("%v: stdout=%s stderr=%s", args, stdout, stderr)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("bad event line %q: %v", line, err)
		}
		out = append(out, env)
	}
	return out
}

func cmd(env map[string]any) string {
	meta, _ := env["meta"].(map[string]any)
	c, _ := meta["command"].(string)
	return c
}

func keys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalKeys(a, b any) bool {
	ka, kb := keys(a), keys(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// blake3Hex mirrors the service's content-hash format.
func blake3Hex(b []byte) string {
	h := blake3.New(32, nil)
	_, _ = h.Write(b)
	return "blake3:" + hex.EncodeToString(h.Sum(nil))
}
