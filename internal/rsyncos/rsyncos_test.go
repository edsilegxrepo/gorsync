package rsyncos_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/edsilegxrepo/gorsync/internal/rsyncos"
)

func TestResolveSSH(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ResolveSSH custom path logic is Windows specific")
	}

	// Case 1: Custom OPENSSH_HOME
	tmpDir := t.TempDir()
	dummySSH := filepath.Join(tmpDir, "ssh.exe")
	if err := os.WriteFile(dummySSH, []byte("dummy ssh"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OPENSSH_HOME", tmpDir)
	resolved := rsyncos.ResolveSSH("ssh")
	if resolved != dummySSH {
		t.Fatalf("expected OPENSSH_HOME resolution %q, got %q", dummySSH, resolved)
	}

	// Case 2: Fallback D:\inetd\sshd\ssh.exe when OPENSSH_HOME is unset
	t.Setenv("OPENSSH_HOME", "")
	resolvedFallback := rsyncos.ResolveSSH("ssh")
	if _, err := os.Stat(`d:\inetd\sshd\ssh.exe`); err == nil {
		expected := `d:\inetd\sshd\ssh.exe`
		if resolvedFallback != expected {
			t.Fatalf("expected fallback %q, got %q", expected, resolvedFallback)
		}
	} else {
		if resolvedFallback != "ssh" {
			t.Fatalf("expected default %q, got %q", "ssh", resolvedFallback)
		}
	}
}
