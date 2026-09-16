package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// marshalShape renders one entry exactly as td ls --json emits it and
// decodes it back into a key/value map.
func marshalShape(t *testing.T, e LSEntry) map[string]any {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func shapeKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestLSJSONHashFieldContract locks the ls --json entry shapes documented in
// docs/contracts/json-contract.md: every file entry always carries hash —
// the stored blake3 content hash, "" when unknown (rows adopted without
// --hash) — while directory entries keep their historical key set without a
// hash field.
func TestLSJSONHashFieldContract(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	content := []byte("audited payload")
	local := filepath.Join(t.TempDir(), "audited.bin")
	if err := os.WriteFile(local, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, local, "/docs/audited.bin", ConflictFail, false); err != nil {
		t.Fatal(err)
	}

	// A nested upload gives /docs a subdirectory entry and exercises the
	// deep-path collapse in ListDir.
	nested := filepath.Join(t.TempDir(), "deep.bin")
	if err := os.WriteFile(nested, []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, nested, "/docs/sub/deep.bin", ConflictFail, false); err != nil {
		t.Fatal(err)
	}

	// Adopt a message without --hash so its stored content hash stays empty.
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 90, Kind: telegram.KindDocument, MIME: "text/plain",
		FileName: "adopted.txt", FileSize: 7, Data: []byte("adopted"),
	})
	if _, err := app.Adopt(ctx, AdoptOptions{MessageID: 90, Dest: "/docs/adopted.txt", NoHash: true}); err != nil {
		t.Fatal(err)
	}

	entries, err := app.ListDir(ctx, "/docs")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]LSEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	wantFileKeys := []string{"hash", "name", "path", "size", "status", "type"}

	uploaded := marshalShape(t, byName["audited.bin"])
	if got := shapeKeys(uploaded); !reflect.DeepEqual(got, wantFileKeys) {
		t.Fatalf("uploaded file keys = %v, want %v", got, wantFileKeys)
	}
	wantHash, err := computeHash(strings.NewReader(string(content)), true)
	if err != nil {
		t.Fatal(err)
	}
	if uploaded["hash"] != wantHash {
		t.Fatalf("uploaded hash = %v, want %s", uploaded["hash"], wantHash)
	}

	adopted := marshalShape(t, byName["adopted.txt"])
	if got := shapeKeys(adopted); !reflect.DeepEqual(got, wantFileKeys) {
		t.Fatalf("adopted file keys = %v, want %v", got, wantFileKeys)
	}
	if adopted["hash"] != "" {
		t.Fatalf("adopted hash = %v, want empty string", adopted["hash"])
	}

	sub := marshalShape(t, byName["sub"])
	// A dir collapsed from deeper file rows inherits their size/status
	// (pre-existing behavior) but must not grow a hash field even though
	// those rows have one.
	wantCollapsedKeys := []string{"name", "path", "size", "status", "type"}
	if got := shapeKeys(sub); !reflect.DeepEqual(got, wantCollapsedKeys) {
		t.Fatalf("collapsed dir keys = %v, want %v (must not grow hash)", got, wantCollapsedKeys)
	}

	rootEntries, err := app.ListDir(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	var docsDir LSEntry
	for _, e := range rootEntries {
		if e.Name == "docs" {
			docsDir = e
		}
	}
	// Dirs collapsed from direct file rows inherit their size/status
	// (pre-existing behavior); either way they must not grow a hash field.
	wantDocsKeys := []string{"name", "path", "size", "status", "type"}
	if got := shapeKeys(marshalShape(t, docsDir)); !reflect.DeepEqual(got, wantDocsKeys) {
		t.Fatalf("root dir keys = %v, want %v (directory entries must not grow hash)", got, wantDocsKeys)
	}

	// Listing a file path directly carries the hash too.
	single, err := app.ListDir(ctx, "/docs/audited.bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 {
		t.Fatalf("ls of a file = %v", single)
	}
	if got := marshalShape(t, single[0])["hash"]; got != wantHash {
		t.Fatalf("single-file listing hash = %v, want %s", got, wantHash)
	}
}
