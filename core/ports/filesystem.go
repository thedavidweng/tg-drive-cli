package ports

import (
	"context"
	"io"
	"os"
)

// FileInfo describes a local file for upload/download use cases.
type FileInfo struct {
	Size    int64
	IsDir   bool
	ModTime int64
}

// FileSystem is the local filesystem port for native and browser adapters.
type FileSystem interface {
	Stat(ctx context.Context, path string) (FileInfo, error)
	Open(ctx context.Context, path string) (io.ReadCloser, error)
	MkdirAll(ctx context.Context, path string, perm os.FileMode) error
	CreateTemp(ctx context.Context, dest string) (path string, file io.WriteCloser, err error)
	Rename(ctx context.Context, oldPath, newPath string) error
	Remove(ctx context.Context, path string) error
}
