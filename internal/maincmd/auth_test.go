package maincmd

import (
	"testing"
)

func TestExtractUserPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		wantUser string
		wantPass string
	}{
		{"alice:secret@host/module", "alice", "secret"},
		{"bob@host/module", "bob", ""},
		{"host/module", "", ""},
	}

	for _, tt := range tests {
		u, p := extractUserPass(tt.input)
		if u != tt.wantUser || p != tt.wantPass {
			t.Errorf("extractUserPass(%q) = (%q, %q), want (%q, %q)", tt.input, u, p, tt.wantUser, tt.wantPass)
		}
	}
}

func TestGenerateAuthHash(t *testing.T) {
	t.Parallel()

	// Verify MD4 challenge-response hash generation
	pw := "secretpass"
	challenge := "abc123xyz"
	hash1 := generateAuthHash(pw, challenge)
	hash2 := generateAuthHash(pw, challenge)

	if hash1 == "" {
		t.Fatalf("expected non-empty hash")
	}
	if hash1 != hash2 {
		t.Errorf("expected deterministic auth hash output, got %q vs %q", hash1, hash2)
	}
}

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
	if string(revealed) != rawPass {
		t.Errorf("revealed secret mismatch: got %q, want %q", string(revealed), rawPass)
	}

	hash, err := generateAuthHashSecret(sec, "challenge123")
	if err != nil {
		t.Fatalf("generateAuthHashSecret failed: %v", err)
	}
	if hash == "" {
		t.Fatalf("expected non-empty hash string")
	}

	// Destroy handle and verify subsequent Reveal returns error
	sec.Destroy()
	if _, err := sec.Reveal(); err == nil {
		t.Errorf("expected error revealing destroyed secret, got nil")
	}
}

