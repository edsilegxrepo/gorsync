package rsyncsec

import (
	"testing"

	"github.com/edsilegxrepo/secretprotector/pkg/libsecsecrets"
)

func TestProtectedSecret(t *testing.T) {
	t.Parallel()

	rawPass := "MySuperSecretPassword123!"
	sec, err := NewProtectedSecret(rawPass)
	if err != nil {
		t.Fatalf("NewProtectedSecret failed: %v", err)
	}

	revealed, err := sec.Reveal()
	if err != nil {
		t.Fatalf("Reveal failed: %v", err)
	}
	defer libsecsecrets.ZeroBuffer(revealed)

	if string(revealed) != rawPass {
		t.Errorf("revealed secret mismatch: got %q, want %q", string(revealed), rawPass)
	}

	// Destroy handle and verify subsequent Reveal returns error
	sec.Destroy()
	if _, err := sec.Reveal(); err == nil {
		t.Errorf("expected error revealing destroyed secret, got nil")
	}
}
