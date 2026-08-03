package sender_test

import (
	"bytes"
	"testing"

	"github.com/edsilegxrepo/rsync/internal/rsyncwire"
	"github.com/edsilegxrepo/rsync/internal/sender"
)

func TestParseFilterRulesAndWildmatch(t *testing.T) {
	rules, err := sender.ParseFilterRules([]string{
		"- *.bak",
		"+ important.bak",
		"- /docs/",
		"- [0-9].txt",
		"- [!a-z].log",
		"- **/*.tmp",
	})
	if err != nil {
		t.Fatalf("ParseFilterRules: %v", err)
	}

	if rules == nil || len(rules.Filters) != 6 {
		t.Fatalf("Expected 6 filter rules, got %v", rules)
	}
}

func TestRecvFilterList(t *testing.T) {
	var buf bytes.Buffer
	cWrite := &rsyncwire.Conn{Writer: &buf}

	// Write 2 filter rules + 0 terminator
	_ = cWrite.WriteInt32(6) // len("- *.go")
	_ = cWrite.WriteString("- *.go")
	_ = cWrite.WriteInt32(9) // len("+ main.go")
	_ = cWrite.WriteString("+ main.go")
	_ = cWrite.WriteInt32(0) // terminator

	cRead := &rsyncwire.Conn{Reader: bytes.NewReader(buf.Bytes())}
	fl, err := sender.RecvFilterList(cRead)
	if err != nil {
		t.Fatalf("RecvFilterList: %v", err)
	}
	if len(fl.Filters) != 2 {
		t.Fatalf("Expected 2 filters, got %d", len(fl.Filters))
	}
}
