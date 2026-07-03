package fsmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadPathCases(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "paths", "simple.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

func TestNormalizeSimplePaths(t *testing.T) {
	for _, p := range loadPathCases(t) {
		got, err := NormalizeCanonicalPath(p)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got != p {
			t.Fatalf("%q => %q", p, got)
		}
	}
}

func TestNormalizeRejectsDotDot(t *testing.T) {
	if _, err := NormalizeCanonicalPath("/a/../b"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeRejectsEmptySegment(t *testing.T) {
	if _, err := NormalizeCanonicalPath("/a//b"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFileDirCollision(t *testing.T) {
	active := []ActivePath{{Canonical: "/a", IsDir: false}}
	if err := CheckUploadConflict("/a/b.txt", active); err == nil {
		t.Fatal("expected conflict")
	}
}

func TestDirBlocksUpload(t *testing.T) {
	active := []ActivePath{{Canonical: "/a", IsDir: true}}
	dest, err := MoveDestination("/x/y.txt", "/a", active)
	if err != nil {
		t.Fatal(err)
	}
	if dest != "/a/y.txt" {
		t.Fatalf("dest = %q", dest)
	}
}

func TestDirectoryMoveUnsupported(t *testing.T) {
	active := []ActivePath{{Canonical: "/dir", IsDir: true}}
	if !IsDirectorySource("/dir", active) {
		t.Fatal("expected directory source")
	}
}

func TestGCDirectories(t *testing.T) {
	remove := GCDirectories([]string{"/a", "/a/b"}, []string{"/a/b/file.txt"})
	if len(remove) != 0 {
		t.Fatalf("unexpected remove: %v", remove)
	}
	remove = GCDirectories([]string{"/orphan"}, []string{"/other/file.txt"})
	if len(remove) != 1 {
		t.Fatalf("remove = %v", remove)
	}
}
