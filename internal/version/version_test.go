package version_test

import (
	"strings"
	"testing"

	"github.com/gokrazy/rsync/internal/version"
)

func TestReadVersion(t *testing.T) {
	v := version.Read()
	if !strings.HasPrefix(v, "gokrazy/rsync ") {
		t.Fatalf("Unexpected version output: %q", v)
	}
}
