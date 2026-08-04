package maincmd_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/edsilegxrepo/gorsync/internal/maincmd"
)

func TestExtractUserPass(t *testing.T) {
	u, p := maincmd.ExportExtractUserPass("alice:secret@example.com/mod")
	if u != "alice" || p != "secret" {
		t.Fatalf("extractUserPass = %q, %q; want alice, secret", u, p)
	}

	u2, p2 := maincmd.ExportExtractUserPass("example.com/mod")
	if u2 != "" || p2 != "" {
		t.Fatalf("extractUserPass without userpass = %q, %q; want empty", u2, p2)
	}
}

func TestWriteTest(t *testing.T) {
	tmpDir := t.TempDir()
	err := maincmd.ExportCanUnexpectedlyWriteTo(tmpDir)
	if err == nil {
		t.Fatalf("Expected error for writable directory in canUnexpectedlyWriteTo, got nil")
	}

	nonExistent := filepath.Join(tmpDir, "nonexistent_sub_dir")
	err = maincmd.ExportCanUnexpectedlyWriteTo(nonExistent)
	if err != nil {
		t.Fatalf("canUnexpectedlyWriteTo on non-existent dir: %v", err)
	}
}

func TestExitError(t *testing.T) {
	baseErr := errors.New("test error")
	err := &maincmd.ExitError{
		Code: maincmd.ExitCodeSyntax,
		Err:  baseErr,
	}
	if err.Code != 1 {
		t.Fatalf("ExitCode mismatch: got %d, want 1", err.Code)
	}
	if err.Unwrap() != baseErr {
		t.Fatalf("Unwrap mismatch: got %v", err.Unwrap())
	}
}
