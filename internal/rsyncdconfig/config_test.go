package rsyncdconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/edsilegxrepo/gorsync/internal/rsyncdconfig"
)

func TestFromString(t *testing.T) {
	tomlInput := `
dont_namespace = true

[[listener]]
rsyncd = "127.0.0.1:8730"

[[module]]
name = "testmod"
path = "/tmp/test"
`
	cfg, err := rsyncdconfig.FromString(tomlInput)
	if err != nil {
		t.Fatalf("FromString: %v", err)
	}
	if !cfg.DontNamespace {
		t.Fatalf("Expected DontNamespace=true")
	}
	if len(cfg.Modules) != 1 || cfg.Modules[0].Name != "testmod" {
		t.Fatalf("Expected module 'testmod', got %v", cfg.Modules)
	}
}

func TestFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	content := `
[[module]]
name = "filemod"
path = "/var/data"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := rsyncdconfig.FromFile(cfgPath)
	if err != nil {
		t.Fatalf("FromFile: %v", err)
	}
	if len(cfg.Modules) != 1 || cfg.Modules[0].Name != "filemod" {
		t.Fatalf("Unexpected config contents: %v", cfg)
	}
}

func TestFromDefaultFiles(t *testing.T) {
	// FromDefaultFiles searches user config dir
	_, _, _ = rsyncdconfig.FromDefaultFiles()
}
