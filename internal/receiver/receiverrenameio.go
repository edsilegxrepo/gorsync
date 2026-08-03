//go:build linux || darwin

package receiver

import (
	"os"

	"github.com/google/renameio/v2"
)

type nativePendingFile struct {
	*renameio.PendingFile
}

func newNativePendingFile(root *os.Root, fn string, tempDir string) (*nativePendingFile, error) {
	opts := []renameio.Option{renameio.WithRoot(root)}
	if tempDir != "" {
		opts = append(opts, renameio.WithTempDir(tempDir))
	}
	pf, err := renameio.NewPendingFile(fn, opts...)
	if err != nil {
		return nil, err
	}
	return &nativePendingFile{PendingFile: pf}, nil
}
