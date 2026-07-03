package fsmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadPathFile(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "contracts", "paths", name))
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

func TestPathFilesNormalize(t *testing.T) {
	files := []string{"simple.txt", "unicode.txt", "deep-path.txt", "underscore-collision.txt"}
	for _, f := range files {
		for _, p := range loadPathFile(t, f) {
			got, err := NormalizeCanonicalPath(p)
			if err != nil {
				t.Fatalf("%s %q: %v", f, p, err)
			}
			if got != p {
				t.Fatalf("%s %q => %q", f, p, got)
			}
		}
	}
}
