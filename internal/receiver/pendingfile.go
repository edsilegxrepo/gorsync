package receiver

import (
	"io"
	"os"

	"github.com/gokrazy/rsync"
)

type pendingFile interface {
	io.Writer
	Name() string
	Cleanup()
	CloseAtomicallyReplace() error
}

type virtualPendingFile struct {
	f       rsync.WritableFile
	wfs     rsync.WritableFS
	target  string
	tmpName string
}

func (v *virtualPendingFile) Write(p []byte) (int, error) {
	return v.f.Write(p)
}

func (v *virtualPendingFile) Name() string {
	return v.target
}

func (v *virtualPendingFile) Cleanup() {
	if v.f != nil {
		_ = v.f.Close()
		_ = v.wfs.Remove(v.tmpName)
	}
}

func (v *virtualPendingFile) CloseAtomicallyReplace() error {
	if err := v.f.Close(); err != nil {
		_ = v.wfs.Remove(v.tmpName)
		return err
	}
	err := v.wfs.Rename(v.tmpName, v.target)
	v.f = nil
	return err
}

func newPendingFile(rt *Transfer, fn string) (pendingFile, error) {
	if rt.Opts != nil && rt.Opts.WritableFS != nil {
		wf, err := rt.Opts.WritableFS.CreateTemp(rt.Opts.TempDir, "temp-rsync-*")
		if err != nil {
			return nil, err
		}
		return &virtualPendingFile{
			f:       wf,
			wfs:     rt.Opts.WritableFS,
			target:  fn,
			tmpName: wf.Name(),
		}, nil
	}
	var root *os.Root
	tempDir := ""
	if rt != nil {
		root = rt.DestRoot
		if rt.Opts != nil {
			tempDir = rt.Opts.TempDir
		}
	}
	pf, err := newNativePendingFile(root, fn, tempDir)
	if err != nil {
		return nil, err
	}
	return pf, nil
}
