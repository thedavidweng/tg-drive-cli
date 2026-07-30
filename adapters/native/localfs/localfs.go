package localfs

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thedavidweng/tg-drive-cli/core/ports"
)

// FS implements ports.FileSystem using the host OS filesystem.
type FS struct{}

var _ ports.FileSystem = (*FS)(nil)

// Stat returns metadata for a local path.
func (FS) Stat(ctx context.Context, path string) (ports.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ports.FileInfo{}, err
	}
	return ports.FileInfo{
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime().Unix(),
	}, nil
}

// Open opens a local file for reading.
func (FS) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// MkdirAll creates a directory tree.
func (FS) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

// CreateTemp creates a sibling .tmp file next to dest for atomic writes.
func (FS) CreateTemp(ctx context.Context, dest string) (string, io.WriteCloser, error) {
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", nil, err
	}
	return tmp, f, nil
}

// Rename atomically moves oldPath to newPath.
func (FS) Rename(ctx context.Context, oldPath, newPath string) error {
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}

// Remove deletes a path.
func (FS) Remove(ctx context.Context, path string) error {
	return os.Remove(path)
}

// Walk walks the file tree rooted at root.
func (FS) Walk(ctx context.Context, root string, fn ports.WalkFunc) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fn(path, ports.FileInfo{}, err)
		}
		return fn(path, ports.FileInfo{
			Size:    info.Size(),
			IsDir:   info.IsDir(),
			ModTime: info.ModTime().Unix(),
		}, nil)
	})
}
