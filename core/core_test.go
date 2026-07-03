package core

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Core packages must stay free of platform-specific imports so they compile to WASM.
var forbiddenImports = []string{
	"os",
	"path/filepath",
	"database/sql",
	"net/http",
	"github.com/gotd/td",
	"modernc.org/sqlite",
	"github.com/spf13/cobra",
	"github.com/thedavidweng/tg-drive-cli/internal/db",
	"github.com/thedavidweng/tg-drive-cli/internal/mtproto",
}

func TestCorePackagesAvoidForbiddenImports(t *testing.T) {
	root := "."
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "testdata" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbiddenImports {
				if p == bad {
					t.Errorf("%s imports forbidden package %s", path, bad)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
