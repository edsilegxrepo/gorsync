package flist_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/edsilegxrepo/rsync/internal/rsynctest"
	"github.com/edsilegxrepo/rsync/rsyncd"
)

// rsynctest.go:282: length 280063 exceeds max message size (262144)
func TestLargeFileList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy 5000-file flist test in short mode")
	}
	t.Parallel()

	tmp := t.TempDir()

	source := filepath.Join(tmp, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 5000 {
		fn := fmt.Sprintf("file_with_long_name_number_%04d", i)
		if err := os.WriteFile(filepath.Join(source, fn), []byte("dummy"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(tmp, "dest")

	// start a server to sync from
	srv := rsynctest.NewInMemory(t, rsyncd.Module{
		Name: "interop",
		Path: source,
	})
	srv.RunClient(t, []string{"-aH"}, []string{dest})
}
