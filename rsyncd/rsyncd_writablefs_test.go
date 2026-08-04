package rsyncd_test

import (
	"bytes"
	"io"
	"io/fs"
	"sync"
	"testing"
	"time"

	"github.com/edsilegxrepo/gorsync"
	"github.com/edsilegxrepo/gorsync/rsyncd"
)

type memWritableFile struct {
	name string
	buf  bytes.Buffer
	mfs  *memFS
}

func (m *memWritableFile) Write(p []byte) (int, error) {
	return m.buf.Write(p)
}

func (m *memWritableFile) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (m *memWritableFile) Close() error {
	m.mfs.mu.Lock()
	defer m.mfs.mu.Unlock()
	m.mfs.files[m.name] = m.buf.Bytes()
	return nil
}

func (m *memWritableFile) Name() string {
	return m.name
}

type memFS struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
	}
}

func (m *memFS) Open(name string) (fs.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if data, ok := m.files[name]; ok {
		return &memReadFSFile{name: name, data: data}, nil
	}
	return nil, fs.ErrNotExist
}

func (m *memFS) CreateTemp(dir, pattern string) (rsync.WritableFile, error) {
	return &memWritableFile{name: pattern, mfs: m}, nil
}

func (m *memFS) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if data, ok := m.files[oldpath]; ok {
		m.files[newpath] = data
		delete(m.files, oldpath)
	}
	return nil
}

func (m *memFS) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, name)
	return nil
}

func (m *memFS) MkdirAll(path string, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[path] = true
	return nil
}

func (m *memFS) Chtimes(name string, atime, mtime time.Time) error {
	return nil
}

type memReadFSFile struct {
	name string
	data []byte
	off  int64
}

func (f *memReadFSFile) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: f.name, size: int64(len(f.data))}, nil
}

func (f *memReadFSFile) Read(p []byte) (int, error) {
	if f.off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += int64(n)
	return n, nil
}

func (f *memReadFSFile) Close() error { return nil }

type memFileInfo struct {
	name string
	size int64
}

func (m *memFileInfo) Name() string       { return m.name }
func (m *memFileInfo) Size() int64        { return m.size }
func (m *memFileInfo) Mode() fs.FileMode  { return 0o644 }
func (m *memFileInfo) ModTime() time.Time { return time.Now() }
func (m *memFileInfo) IsDir() bool        { return false }
func (m *memFileInfo) Sys() any           { return nil }

func TestWritableFSModule(t *testing.T) {
	mfs := newMemFS()
	srv, err := rsyncd.NewServer([]rsyncd.Module{
		{
			Name:       "virtual_upload",
			WritableFS: mfs,
			Writable:   true,
		},
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	_ = srv
}
