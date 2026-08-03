package rsync

import (
	"io"
	"io/fs"
	"time"
)

// WritableFile represents an open writable file in a [WritableFS].
type WritableFile interface {
	io.ReadWriteCloser
	Name() string
}

// WritableFS extends [fs.FS] with file creation, deletion, directory creation,
// and modification capabilities required by the rsync receiver.
type WritableFS interface {
	fs.FS

	CreateTemp(dir, pattern string) (WritableFile, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
	MkdirAll(path string, perm fs.FileMode) error
	Chtimes(name string, atime, mtime time.Time) error
}
