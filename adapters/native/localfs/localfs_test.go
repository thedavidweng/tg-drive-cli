package localfs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
)

func TestLocalFSWriteRename(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	fs := localfs.FS{}
	tmp, f, err := fs.CreateTemp(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rename(context.Background(), tmp, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Fatalf("data = %q", data)
	}
}
