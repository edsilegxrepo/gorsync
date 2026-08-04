package receiver_test

import (
	"bytes"
	"io"
	"io/fs"
	"net"
	"testing"
	"time"

	"github.com/edsilegxrepo/gorsync"
	golog "github.com/edsilegxrepo/gorsync/internal/log"
	"github.com/edsilegxrepo/gorsync/internal/receiver"
	"github.com/edsilegxrepo/gorsync/internal/rsyncopts"
	"github.com/edsilegxrepo/gorsync/internal/rsyncos"
	"github.com/edsilegxrepo/gorsync/internal/rsyncwire"
)

// memFS implements rsync.WritableFS for in-memory mock testing
type memFS struct {
	files map[string]*memFile
}

func newMemFS() *memFS {
	return &memFS{files: make(map[string]*memFile)}
}

func (m *memFS) Open(name string) (fs.File, error) {
	f, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &memFileReader{f: f, pos: 0}, nil
}

func (m *memFS) CreateTemp(dir, pattern string) (rsync.WritableFile, error) {
	name := dir + "/temp_" + pattern
	f := &memFile{name: name}
	m.files[name] = f
	return f, nil
}

func (m *memFS) Rename(oldpath, newpath string) error {
	f, ok := m.files[oldpath]
	if !ok {
		return fs.ErrNotExist
	}
	delete(m.files, oldpath)
	f.name = newpath
	m.files[newpath] = f
	return nil
}

func (m *memFS) Remove(name string) error {
	delete(m.files, name)
	return nil
}

func (m *memFS) MkdirAll(path string, perm fs.FileMode) error {
	return nil
}

func (m *memFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return nil
}

type memFile struct {
	name string
	buf  bytes.Buffer
}

func (f *memFile) Name() string { return f.name }

func (f *memFile) Write(p []byte) (n int, err error) {
	return f.buf.Write(p)
}

func (f *memFile) Read(p []byte) (n int, err error) {
	return f.buf.Read(p)
}

func (f *memFile) Close() error {
	return nil
}

type memFileReader struct {
	f   *memFile
	pos int64
}

func (r *memFileReader) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: r.f.name, size: int64(r.f.buf.Len())}, nil
}

func (r *memFileReader) Read(p []byte) (n int, err error) {
	if r.pos >= int64(r.f.buf.Len()) {
		return 0, io.EOF
	}
	n = copy(p, r.f.buf.Bytes()[r.pos:])
	r.pos += int64(n)
	return n, nil
}

func (r *memFileReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.pos = offset
	case io.SeekCurrent:
		r.pos += offset
	case io.SeekEnd:
		r.pos = int64(r.f.buf.Len()) + offset
	}
	return r.pos, nil
}

func (r *memFileReader) Close() error { return nil }

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

func TestTransferWithWritableFS(t *testing.T) {
	m := newMemFS()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		clientConn, err := ln.Accept()
		if err != nil {
			return
		}
		defer clientConn.Close()

		cClient := &rsyncwire.Conn{Writer: clientConn, Reader: clientConn}
		_ = cClient.WriteByte(0)  // End of file list
		_ = cClient.WriteInt32(0) // ioErrors
	}()

	srvConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer srvConn.Close()

	conn := &rsyncwire.Conn{
		Writer: srvConn,
		Reader: srvConn,
	}

	env := &rsyncos.Env{
		Stderr: io.Discard,
		Stdout: io.Discard,
	}
	opts := rsyncopts.NewOptions(env)
	topts := &receiver.TransferOpts{
		WritableFS: m,
		Verbose:    true,
		DebugGTE:   opts.DebugGTE,
		InfoGTE:    opts.InfoGTE,
	}

	rt := &receiver.Transfer{
		Logger: golog.New(io.Discard),
		Conn:   conn,
		Opts:   topts,
		Env:    env,
	}

	files, err := rt.ReceiveFileList()
	if err != nil {
		t.Fatalf("ReceiveFileList: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("Expected 0 files, got %d", len(files))
	}
}

func TestVirtualPendingFile(t *testing.T) {
	m := newMemFS()
	rt := &receiver.Transfer{
		Opts: &receiver.TransferOpts{
			WritableFS: m,
		},
	}

	pf, err := receiver.ExportNewPendingFile(rt, "dest.txt")
	if err != nil {
		t.Fatalf("ExportNewPendingFile: %v", err)
	}

	if pf.Name() != "dest.txt" {
		t.Fatalf("pf.Name() = %q, want 'dest.txt'", pf.Name())
	}

	n, err := pf.Write([]byte("hello world"))
	if err != nil || n != 11 {
		t.Fatalf("pf.Write = %d, %v; want 11", n, err)
	}

	if err := pf.CloseAtomicallyReplace(); err != nil {
		t.Fatalf("CloseAtomicallyReplace: %v", err)
	}

	// Test cleanup on second instance
	pf2, err := receiver.ExportNewPendingFile(rt, "dest2.txt")
	if err != nil {
		t.Fatalf("ExportNewPendingFile 2: %v", err)
	}
	pf2.Cleanup()
}

func TestReceiver_PreserveHardlinksOption(t *testing.T) {
	opts := &receiver.TransferOpts{
		PreserveHardlinks: true,
	}

	rt := &receiver.Transfer{
		Opts: opts,
	}

	if !rt.Opts.PreserveHardlinks {
		t.Fatalf("Expected rt.Opts.PreserveHardlinks to be true")
	}

	optsDisabled := &receiver.TransferOpts{
		PreserveHardlinks: false,
	}

	rtDisabled := &receiver.Transfer{
		Opts: optsDisabled,
	}

	if rtDisabled.Opts.PreserveHardlinks {
		t.Fatalf("Expected rtDisabled.Opts.PreserveHardlinks to be false")
	}
}
