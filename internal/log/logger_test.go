package log_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/edsilegxrepo/rsync/internal/log"
)

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	l := log.New(&buf)
	l.Printf("hello %s", "world")

	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Fatalf("Expected output containing 'hello world', got %q", out)
	}

	_ = l.Output(2, "direct log message")
	if !strings.Contains(buf.String(), "direct log message") {
		t.Fatalf("Expected direct log message in output")
	}
}
