package fsmodel

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func loadGoldenLines(t *testing.T, name string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "paths", name))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line))
	}
	return out
}

func TestEmojiPathsNormalize(t *testing.T) {
	for _, p := range loadPathFile(t, "emoji.txt") {
		got, err := NormalizeCanonicalPath(p)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got != p {
			t.Fatalf("%q => %q", p, got)
		}
	}
}

func TestFileDirConflictGolden(t *testing.T) {
	for _, f := range loadGoldenLines(t, "file-dir-conflict.txt") {
		if len(f) != 4 {
			t.Fatalf("bad golden line: %v", f)
		}
		existing, typ, dest, expect := f[0], f[1], f[2], f[3]
		active := []ActivePath{{Canonical: existing, IsDir: typ == "dir"}}
		err := CheckUploadConflict(dest, active)
		if expect == "conflict" && err == nil {
			t.Fatalf("%v: expected conflict, got nil", f)
		}
		if expect == "ok" && err != nil {
			t.Fatalf("%v: expected ok, got %v", f, err)
		}
	}
}

func TestCompoundExtensionsGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "paths", "compound-extensions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// name may not contain spaces in these vectors; expected may.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("bad golden line: %q", line)
		}
		name := fields[0]
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("bad n in %q", line)
		}
		expected := strings.Join(fields[2:], " ")
		if got := ConflictRenameCandidate(name, n); got != expected {
			t.Fatalf("ConflictRenameCandidate(%q, %d) = %q, want %q", name, n, got, expected)
		}
	}
}
