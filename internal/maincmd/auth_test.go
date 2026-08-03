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
