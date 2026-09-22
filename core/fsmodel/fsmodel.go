package fsmodel

import (
	"path"
	"strconv"
	"strings"
	"unicode"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"golang.org/x/text/unicode/norm"
)

// NormalizeCanonicalPath normalizes a remote path per storage contract.
func NormalizeCanonicalPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "/" {
		return "/", nil
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	s = norm.NFC.String(s)
	parts := strings.Split(s, "/")
	if parts[0] != "" {
		return "", apperr.New(apperr.ErrPathInvalid, "path must start with /")
	}
	for i := 1; i < len(parts); i++ {
		seg := parts[i]
		if seg == "" {
			return "", apperr.New(apperr.ErrPathInvalid, "empty path segment")
		}
		if seg == "." || seg == ".." {
			return "", apperr.New(apperr.ErrPathInvalid, "path segment cannot be . or ..")
		}
		for _, r := range seg {
			if unicode.IsControl(r) {
				return "", apperr.New(apperr.ErrPathInvalid, "control characters not allowed")
			}
		}
	}
	s = "/" + strings.Join(parts[1:], "/")
	return s, nil
}

// BaseName returns the last segment of a canonical path.
func BaseName(canonical string) string {
	if canonical == "/" {
		return ""
	}
	return path.Base(canonical)
}

// ParentPath returns parent canonical path.
func ParentPath(canonical string) string {
	if canonical == "/" {
		return ""
	}
	p := path.Dir(canonical)
	if p == "." {
		return "/"
	}
	return p
}

// HumanParent returns parent path without leading slash for captions.
func HumanParent(canonical string) string {
	p := ParentPath(canonical)
	if p == "/" || p == "" {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}

// AncestorPaths returns all ancestor paths from root to parent.
func AncestorPaths(canonical string) []string {
	if canonical == "/" {
		return nil
	}
	var out []string
	cur := ParentPath(canonical)
	for cur != "" && cur != "/" {
		out = append([]string{cur}, out...)
		cur = ParentPath(cur)
	}
	return out
}

// Segments returns path segments after leading slash.
func Segments(canonical string) []string {
	if canonical == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(canonical, "/"), "/")
}

// ActivePath describes an indexed path in the tree.
type ActivePath struct {
	Canonical string
	IsDir     bool
}

// CheckUploadConflict rejects file/dir collisions for a destination file path.
// Uploading beneath an existing directory is allowed; conflicts are:
// destination already exists, an ancestor of the destination is a file, or the
// destination sits at a directory position (has active descendants).
func CheckUploadConflict(dest string, active []ActivePath) error {
	dest = strings.TrimSuffix(dest, "/")
	for _, a := range active {
		p := strings.TrimSuffix(a.Canonical, "/")
		if p == dest {
			if a.IsDir {
				return apperr.New(apperr.ErrPathIsDirectory, "destination is a directory: "+dest)
			}
			return apperr.New(apperr.ErrPathExists, "file exists at destination: "+dest)
		}
		if !a.IsDir && strings.HasPrefix(dest+"/", p+"/") {
			return apperr.New(apperr.ErrPathAncestorIsFile, "ancestor path is a file: "+p)
		}
		if strings.HasPrefix(p+"/", dest+"/") {
			return apperr.New(apperr.ErrPathIsDirectory, "destination has active descendants: "+dest)
		}
	}
	return nil
}

// MoveDestination resolves mv destination semantics.
func MoveDestination(from, to string, active []ActivePath) (string, error) {
	to, err := NormalizeCanonicalPath(to)
	if err != nil {
		return "", err
	}
	from, err = NormalizeCanonicalPath(from)
	if err != nil {
		return "", err
	}
	for _, a := range active {
		if a.IsDir && a.Canonical == to {
			return path.Join(to, BaseName(from)), nil
		}
	}
	return to, nil
}

// IsDirectorySource checks if from refers to an active directory.
func IsDirectorySource(from string, active []ActivePath) bool {
	for _, a := range active {
		if a.IsDir && a.Canonical == from {
			return true
		}
	}
	return false
}

// compoundExtensions are known multi-part extensions kept together when
// generating conflict rename candidates.
var compoundExtensions = []string{
	".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".user.js", ".min.js", ".d.ts",
}

// SplitExtension splits a file name into stem and extension, keeping known
// compound extensions (e.g. archive.tar.gz -> "archive", ".tar.gz") together.
func SplitExtension(name string) (stem, ext string) {
	lower := strings.ToLower(name)
	for _, ce := range compoundExtensions {
		if strings.HasSuffix(lower, ce) && len(name) > len(ce) {
			return name[:len(name)-len(ce)], name[len(name)-len(ce):]
		}
	}
	ext = path.Ext(name)
	return strings.TrimSuffix(name, ext), ext
}

// ConflictRenameCandidate returns the nth rename candidate for a name:
// beach.jpg -> "beach (1).jpg", archive.tar.gz -> "archive (1).tar.gz".
func ConflictRenameCandidate(name string, n int) string {
	stem, ext := SplitExtension(name)
	return stem + " (" + strconv.Itoa(n) + ")" + ext
}

// DeriveDirectoryNodes returns directory nodes implied by file paths.
func DeriveDirectoryNodes(filePaths []string) map[string]string {
	nodes := make(map[string]string)
	for _, fp := range filePaths {
		for _, anc := range AncestorPaths(fp) {
			nodes[anc] = BaseName(anc)
		}
	}
	return nodes
}

// GCDirectories returns directory paths with no active descendants.
func GCDirectories(dirs, activeFiles []string) []string {
	activeSet := make(map[string]bool, len(activeFiles))
	for _, f := range activeFiles {
		for _, anc := range AncestorPaths(f) {
			activeSet[anc] = true
		}
	}
	var remove []string
	for _, d := range dirs {
		if !activeSet[d] {
			remove = append(remove, d)
		}
	}
	return remove
}
