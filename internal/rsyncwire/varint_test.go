package rsyncwire_test

import (
	"bytes"
	"testing"

	"github.com/edsilegxrepo/rsync/internal/rsyncwire"
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
