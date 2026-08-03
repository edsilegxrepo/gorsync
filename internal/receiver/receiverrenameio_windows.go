//go:build windows

package receiver

import (
	"os"
	"path/filepath"
)

type nativePendingFile struct {
	fn string
	f  *os.File
}

func newNativePendingFile(root *os.Root, fn string, tempDir string) (*nativePendingFile, error) {
	abs := filepath.Join(root.Name(), fn)
	dir := tempDir
	if dir == "" {
		dir = filepath.Dir(abs)
	}
	f, err := os.CreateTemp(dir, "temp-rsync-*")
	if err != nil {
		return nil, err
	}
	return &nativePendingFile{
		fn: abs,
		f:  f,
	}, nil
}

func (p *nativePendingFile) Name() string {
	return p.fn
}

func (p *nativePendingFile) Write(buf []byte) (n int, _ error) {
	return p.f.Write(buf)
}

func (p *nativePendingFile) CloseAtomicallyReplace() error {
	tmpName := p.f.Name()
	if err := p.f.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, p.fn); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (p *nativePendingFile) Cleanup() {
	if p.f != nil {
		tmpName := p.f.Name()
		_ = p.f.Close()
		_ = os.Remove(tmpName)
	}
}
