package version_test

import (
	"strings"
	"testing"

	"github.com/edsilegxrepo/rsync/internal/version"
)

func TestReadVersion(t *testing.T) {
	v := version.Read()
	if !strings.HasPrefix(v, "gorsync ") {
		t.Fatalf("Unexpected version output: %q", v)
	}
}
