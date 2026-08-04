package rsyncwire_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/edsilegxrepo/gorsync"
	"github.com/edsilegxrepo/gorsync/internal/rsyncwire"
)

// TestProtocol27Features tests Protocol 27 legacy wire encoding mechanics.
func TestProtocol27Features(t *testing.T) {
	var buf bytes.Buffer
	conn := &rsyncwire.Conn{
		Writer: &buf,
		Reader: &buf,
	}

	// Protocol 27 uses 32-bit fixed integer serialization
	if err := conn.WriteInt32(27); err != nil {
		t.Fatalf("WriteInt32 failed: %v", err)
	}

	readVal, err := conn.ReadInt32()
	if err != nil {
		t.Fatalf("ReadInt32 failed: %v", err)
	}
	if readVal != 27 {
		t.Fatalf("Protocol 27 Int32 mismatch: got %d, want 27", readVal)
	}
}

// TestProtocol30_31Features tests Protocol 30/31 VarInt and Checksum Header mechanics.
func TestProtocol30_31Features(t *testing.T) {
	// Test VarInt 64-bit encoding
	testCases := []int64{0, 1, 127, 128, 255, 65535, 1 << 30, 1 << 40}
	for _, val := range testCases {
		var buf bytes.Buffer
		if err := rsyncwire.WriteVarInt(&buf, val); err != nil {
			t.Fatalf("WriteVarInt(%d) failed: %v", val, err)
		}
		got, err := rsyncwire.ReadVarInt(&buf)
		if err != nil {
			t.Fatalf("ReadVarInt failed for %d: %v", val, err)
		}
		if got != val {
			t.Fatalf("VarInt mismatch: got %d, want %d", got, val)
		}
	}

	// Test SumHead reading up to 32-byte SHA256/xxHash checksum length
	var shBuf bytes.Buffer
	conn := &rsyncwire.Conn{
		Writer: &shBuf,
		Reader: &shBuf,
	}
	// Write ChecksumCount=10, BlockLength=1024, ChecksumLength=32 (SHA256), RemainderLength=512
	conn.WriteInt32(10)
	conn.WriteInt32(1024)
	conn.WriteInt32(32) // 32-byte checksum length (Protocol 30+)
	conn.WriteInt32(512)

	var sh rsync.SumHead
	if err := sh.ReadFrom(conn); err != nil {
		t.Fatalf("SumHead.ReadFrom failed for 32-byte checksum: %v", err)
	}
	if sh.ChecksumLength != 32 {
		t.Fatalf("ChecksumLength mismatch: got %d, want 32", sh.ChecksumLength)
	}
}

// TestProtocol32Features tests Protocol 32 VarInt32 and Time64 nanosecond precision mechanics.
func TestProtocol32Features(t *testing.T) {
	// Test VarInt32 round trip
	testCases32 := []int32{0, 42, 127, 1000, 65535, 1 << 28}
	for _, val := range testCases32 {
		var buf bytes.Buffer
		if err := rsyncwire.WriteVarInt32(&buf, val); err != nil {
			t.Fatalf("WriteVarInt32(%d) failed: %v", val, err)
		}
		got, err := rsyncwire.ReadVarInt32(&buf)
		if err != nil {
			t.Fatalf("ReadVarInt32 failed for %d: %v", val, err)
		}
		if got != val {
			t.Fatalf("VarInt32 mismatch: got %d, want %d", got, val)
		}
	}

	// Test Time64 nanosecond timestamp precision
	now := time.Now().Truncate(time.Nanosecond)
	var timeBuf bytes.Buffer
	if err := rsyncwire.WriteTime64(&timeBuf, now); err != nil {
		t.Fatalf("WriteTime64 failed: %v", err)
	}

	gotTime, err := rsyncwire.ReadTime64(&timeBuf)
	if err != nil {
		t.Fatalf("ReadTime64 failed: %v", err)
	}

	if now.Unix() != gotTime.Unix() || now.Nanosecond() != gotTime.Nanosecond() {
		t.Fatalf("Time64 precision mismatch: got %v, want %v", gotTime, now)
	}

	if rsync.MaxProtocolVersion != 32 {
		t.Fatalf("MaxProtocolVersion mismatch: got %d, want 32", rsync.MaxProtocolVersion)
	}
}
