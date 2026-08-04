package rsync

import (
	"io"
	"io/fs"
	"time"
)

// WritableFile represents an open writable file in a [WritableFS], supporting
// concurrent reads, writes, handles, and closing operations.
type WritableFile interface {
	io.ReadWriteCloser
	Name() string
}

// WritableFS extends [fs.FS] with file creation, deletion, directory creation,
// timestamp updates, and atomic rename capabilities required by the rsync receiver engine.
// Implementations of WritableFS allow target storage engines (e.g. S3, database, or
// pure in-memory filesystems) to be plugged seamlessly into the rsync receiver.
type WritableFS interface {
	fs.FS

	CreateTemp(dir, pattern string) (WritableFile, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
	MkdirAll(path string, perm fs.FileMode) error
	Chtimes(name string, atime, mtime time.Time) error
}
