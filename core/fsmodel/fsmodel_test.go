package fsmodel

import (
	"testing"
)

func TestFileDirCollision(t *testing.T) {
	active := []ActivePath{{Canonical: "/a", IsDir: false}}
	if err := CheckUploadConflict("/a/b.txt", active); err == nil {
		t.Fatal("expected conflict")
	}
}

func TestDirAllowsDescendantUpload(t *testing.T) {
	active := []ActivePath{{Canonical: "/a", IsDir: true}}
	if err := CheckUploadConflict("/a/b.txt", active); err != nil {
		t.Fatalf("unexpected conflict: %v", err)
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
