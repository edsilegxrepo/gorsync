package rsyncwire_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/edsilegxrepo/gorsync/internal/rsyncwire"
)

func TestVarIntRoundTrip(t *testing.T) {
	testValues := []int64{
		0,
		1,
		127,
		128,
		255,
		16383,
		16384,
		2097151,
		2097152,
		268435455,
		1073741824,
		9223372036854775807 / 2,
	}

	for _, val := range testValues {
		var buf bytes.Buffer
		if err := rsyncwire.WriteVarInt(&buf, val); err != nil {
			t.Fatalf("WriteVarInt failed for %d: %v", val, err)
		}

		got, err := rsyncwire.ReadVarInt(&buf)
		if err != nil {
			t.Fatalf("ReadVarInt failed for %d: %v", val, err)
		}

		if got != val {
			t.Errorf("varint mismatch: expected %d, got %d", val, got)
		}
	}
}

func TestTime64RoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Nanosecond)
	var buf bytes.Buffer
	if err := rsyncwire.WriteTime64(&buf, now); err != nil {
		t.Fatalf("WriteTime64 failed: %v", err)
	}

	got, err := rsyncwire.ReadTime64(&buf)
	if err != nil {
		t.Fatalf("ReadTime64 failed: %v", err)
	}

	if got.Unix() != now.Unix() || got.Nanosecond() != now.Nanosecond() {
		t.Errorf("timestamp mismatch: expected %v, got %v", now, got)
	}
}
