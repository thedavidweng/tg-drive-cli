package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EAlbumLifecycle publishes a multi-file album through the real binary,
// verifies the split into consecutive groups, then wipes the database and
// reconstructs purely from the channel with scan --full (issue #26).
func TestE2EAlbumLifecycle(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)

	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	// Twelve members force the 10+2 split at the binary level.
	const total = 12
	var locals []string
	want := map[string]string{}
	for i := 0; i < total; i++ {
		name := "scene" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + ".bin"
		content := bytes.Repeat([]byte{byte('A' + i)}, i+1)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
		locals = append(locals, p)
		want["/albums/"+name] = string(content)
	}

	args := append([]string{"cp"}, locals...)
	args = append(args, "/albums/")
	up := runE2EJSON(t, bin, cfgPath, dbPath, statePath, args...)
	if n, _ := up["uploaded"].(float64); n != total {
		t.Fatalf("uploaded = %v, want %d", up["uploaded"], total)
	}
	rawGroups, _ := up["albums"].([]any)
	if len(rawGroups) != 2 {
		t.Fatalf("albums = %v, want 2 groups", up["albums"])
	}
	gids := map[float64]bool{}
	for i, g := range rawGroups {
		m, _ := g.(map[string]any)
		gid, _ := m["grouped_id"].(float64)
		if gid == 0 || gids[gid] {
			t.Fatalf("group %d has bad grouped id %v", i, m["grouped_id"])
		}
		gids[gid] = true
		if id, _ := m["reply_message_id"].(float64); id <= 0 {
			t.Fatalf("group %d missing reply_message_id: %v", i, m)
		}
		wantN := 10
		if i == 1 {
			wantN = 2
		}
		if paths, _ := m["paths"].([]any); len(paths) != wantN {
			t.Fatalf("group %d holds %v paths, want %d", i, m["paths"], wantN)
		}
	}

	// ls sees the album members as ordinary files with hashes.
	lsData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/albums")
	entries, _ := lsData["entries"].([]any)
	if len(entries) != total {
		t.Fatalf("ls /albums = %d entries, want %d", len(entries), total)
	}

	// NDJSON: the final cp event carries the album payload.
	events := runE2EEvents(t, bin, cfgPath, dbPath, statePath, append([]string{"cp"}, append(locals[:2], "/events/", "--events")...)...)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if cmd(events[0]) != "cp" {
		t.Fatalf("event command = %q, want cp", cmd(events[0]))
	}
	if _, ok := events[0]["data"].(map[string]any)["albums"]; !ok {
		t.Fatalf("cp event data missing albums key: %v", events[0])
	}

	// Usage contract: multi-file destinations must be directory-shaped, and
	// --replace is refused (exit 2, ERR_USAGE).
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", locals[0], locals[1], "/no-slash-dir")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"cp", "--confirm", "--replace", locals[0], locals[1], "/albums/")

	// Reconstruction: wipe the local cache entirely, rebind, scan --full.
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=Drive")
	scanData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan", "--full")
	// total album members plus the two files the NDJSON check uploaded to
	// /events/.
	if n, _ := scanData["active"].(float64); n != total+2 {
		t.Fatalf("scan = %v, want %d active", scanData, total+2)
	}
	for name, content := range want {
		if !strings.HasSuffix(name, "3.bin") && !strings.HasSuffix(name, "0.bin") {
			continue
		}
		dest := filepath.Join(dir, "rebuilt-"+filepath.Base(name))
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", name, dest)
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Fatalf("rebuilt %s content mismatch", name)
		}
	}
}

// TestE2EAlbumRecursiveFolder uploads a local tree through the real binary
// and verifies the per-directory album grouping survives a full rebuild.
func TestE2EAlbumRecursiveFolder(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)

	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	tree := filepath.Join(dir, "photos")
	if err := os.MkdirAll(filepath.Join(tree, "raw"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(tree, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jpg", "alpha")
	write("b.jpg", "bravo")
	write(filepath.Join("raw", "c.jpg"), "charlie")

	rec := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", "--recursive", tree, "/gallery")
	if n, _ := rec["uploaded"].(float64); n != 3 {
		t.Fatalf("recursive cp = %v, want 3 uploaded", rec)
	}
	rawGroups, _ := rec["albums"].([]any)
	if len(rawGroups) != 1 {
		t.Fatalf("recursive albums = %v, want one group (the lone raw/ file stays ungrouped)", rec["albums"])
	}

	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=Drive")
	scanData := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan", "--full")
	if n, _ := scanData["active"].(float64); n != 3 {
		t.Fatalf("scan = %v, want 3 active", scanData)
	}
	restored := filepath.Join(dir, "restored", "c.jpg")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/gallery/raw/c.jpg", restored)
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "charlie" {
		t.Fatalf("restored raw/c.jpg = %q", got)
	}
}
