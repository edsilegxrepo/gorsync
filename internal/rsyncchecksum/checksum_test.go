package rsyncchecksum_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gokrazy/rsync/internal/rsyncchecksum"
)

func constructLargeDataFile(headPattern, bodyPattern, endPattern []byte) []byte {
	// create large data file in source directory to be copied
	head := bytes.Repeat(headPattern, 1*1024*1024)
	body := bytes.Repeat(bodyPattern, 1*1024*1024)
	end := bytes.Repeat(endPattern, 1*1024*1024)
	return append(append(head, body...), end...)
}

func writeLargeDataFile(t *testing.T, source string, headPattern, bodyPattern, endPattern []byte) {
	// create large data file in source directory to be copied
	content := constructLargeDataFile(headPattern, bodyPattern, endPattern)
	large := filepath.Join(source, "large-data-file")
	if err := os.MkdirAll(filepath.Dir(large), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(large, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncExtended(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source")

	writeLargeDataFile(t, source, []byte{0x11}, []byte{0xbb}, []byte{0xee})

	// These values are taken from the rsync debug output:
	const k = 1768
	want := make([]uint32, 1780)
	for i := 0; i <= 592; i++ {
		want[i] = 0xa5d47568
	}
	want[593] = 0x23645688
	for i := 594; i <= 1185; i++ {
		want[i] = 0x8c1c2378
	}
	want[1186] = 0x12504720
	for i := 1187; i <= 1778; i++ {
		want[i] = 0x7d9883b0
	}
	want[1779] = 0x61b8dff0

	sourceLarge := filepath.Join(source, "large-data-file")
	f, err := os.Open(sourceLarge)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, k)
	for idx, wantChecksum := range want {
		n, err := f.Read(buf)
		if err != nil {
			t.Fatal(err)
		}

		chunk := buf[:n]
		sum := rsyncchecksum.Checksum1(chunk)
		if sum != wantChecksum {
			t.Fatalf("checksum calculation error: got %08x, want %08x (idx %d), chunk: %#v", sum, wantChecksum, idx, chunk)
		}
	}
}

func TestChecksumHelpers(t *testing.T) {
	tag := rsyncchecksum.Tag(0x12345678)
	if tag == 0 {
		t.Fatalf("Tag returned 0")
	}

	se := rsyncchecksum.SignExtend(0x80)
	if se == 0 {
		t.Fatalf("SignExtend returned 0")
	}

	c2 := rsyncchecksum.Checksum2(1234, []byte("data"))
	if len(c2) == 0 {
		t.Fatalf("Checksum2 empty")
	}

	rSum, err := rsyncchecksum.ReaderChecksum(bytes.NewReader([]byte("test")))
	if err != nil || len(rSum) == 0 {
		t.Fatalf("ReaderChecksum: %v", err)
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer root.Close()

	rootSum, err := rsyncchecksum.RootChecksum(root, "file.txt")
	if err != nil || len(rootSum) == 0 {
		t.Fatalf("RootChecksum: %v", err)
	}
}
