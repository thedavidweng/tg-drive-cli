package fakefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/ports"
)

// FS is an in-memory FileSystem adapter for tests and browser-like targets.
type FS struct {
	mu   sync.RWMutex
	data map[string][]byte
	dirs map[string]bool
}

// New creates an empty in-memory filesystem.
func New() *FS {
	return &FS{
		data: map[string][]byte{},
		dirs: map[string]bool{},
	}
}

// WriteFile stores path -> content in memory.
func (f *FS) WriteFile(path string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[path] = content
	for dir := filepath.Dir(path); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		f.dirs[dir] = true
	}
}

// Stat returns metadata for an in-memory path.
func (f *FS) Stat(ctx context.Context, path string) (ports.FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if data, ok := f.data[path]; ok {
		return ports.FileInfo{Size: int64(len(data)), IsDir: false, ModTime: time.Now().Unix()}, nil
	}
	if f.dirs[path] {
		return ports.FileInfo{Size: 0, IsDir: true, ModTime: time.Now().Unix()}, nil
	}
	return ports.FileInfo{}, fmt.Errorf("%w: %s", fs.ErrNotExist, path)
}

// Open returns a ReadCloser for an in-memory file.
func (f *FS) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	data, ok := f.data[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s", fs.ErrNotExist, path)
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}

// MkdirAll records a directory in memory.
func (f *FS) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs[path] = true
	return nil
}

// CreateTemp is not supported by the fake.
func (f *FS) CreateTemp(ctx context.Context, dest string) (string, io.WriteCloser, error) {
	return "", nil, errors.New("CreateTemp not supported in fake fs")
}

// Rename moves an in-memory file.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.data[oldPath]
	if !ok {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, oldPath)
	}
	f.data[newPath] = data
	delete(f.data, oldPath)
	return nil
}

// Remove deletes an in-memory path.
func (f *FS) Remove(ctx context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, path)
	return nil
}

// Walk walks the in-memory tree.
func (f *FS) Walk(ctx context.Context, root string, fn ports.WalkFunc) error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for path, data := range f.data {
		if !strings.HasPrefix(path, root) {
			continue
		}
		if err := fn(path, ports.FileInfo{Size: int64(len(data)), IsDir: false, ModTime: time.Now().Unix()}, nil); err != nil {
			return err
		}
	}
	return nil
}
