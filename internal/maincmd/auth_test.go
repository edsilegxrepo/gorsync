package maincmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncos"
	"github.com/edsilegxrepo/rsync/internal/rsyncsec"
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
	sec, err := rsyncsec.NewProtectedSecret(rawPass)
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

func TestGetPasswordSecret(t *testing.T) {
	// 1. From URL pass
	sec1, err := getPasswordSecret(&rsyncopts.Options{}, "urlpass123")
	if err != nil {
		t.Fatalf("getPasswordSecret from urlPass failed: %v", err)
	}
	rev1, _ := sec1.Reveal()
	if string(rev1) != "urlpass123" {
		t.Errorf("expected 'urlpass123', got %q", string(rev1))
	}
	sec1.Destroy()

	// 2. From password file
	tmpDir := t.TempDir()
	passPath := filepath.Join(tmpDir, "pass.txt")
	if err := os.WriteFile(passPath, []byte("filepass456\nsecondline"), 0o600); err != nil {
		t.Fatal(err)
	}

	osenv := &rsyncos.Env{}
	pc := rsyncopts.NewContext(rsyncopts.NewOptionsWithDefaults(osenv))
	if err := pc.ParseArguments(osenv, []string{"--password-file=" + passPath}); err != nil {
		t.Fatalf("ParseArguments failed: %v", err)
	}
	opts := pc.Options
	sec2, err := getPasswordSecret(opts, "")
	if err != nil {
		t.Fatalf("getPasswordSecret from file failed: %v", err)
	}
	rev2, _ := sec2.Reveal()
	if string(rev2) != "filepass456" {
		t.Errorf("expected 'filepass456', got %q", string(rev2))
	}
	sec2.Destroy()

	// 3. From environment variable
	t.Setenv("RSYNC_PASSWORD", "envpass789")
	sec3, err := getPasswordSecret(&rsyncopts.Options{}, "")
	if err != nil {
		t.Fatalf("getPasswordSecret from env failed: %v", err)
	}
	rev3, _ := sec3.Reveal()
	if string(rev3) != "envpass789" {
		t.Errorf("expected 'envpass789', got %q", string(rev3))
	}
	sec3.Destroy()
}

func TestResolveUsername(t *testing.T) {
	if u := resolveUsername("custom_alice"); u != "custom_alice" {
		t.Errorf("expected 'custom_alice', got %q", u)
	}

	t.Setenv("RSYNC_USERNAME", "env_bob")
	if u := resolveUsername(""); u != "env_bob" {
		t.Errorf("expected 'env_bob', got %q", u)
	}
}
