package rsyncwire_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/edsilegxrepo/gorsync/internal/rsyncos"
	"github.com/edsilegxrepo/gorsync/internal/rsyncwire"
)

func TestConnAndBuffer(t *testing.T) {
	var buf bytes.Buffer
	c := &rsyncwire.Conn{
		Writer: &buf,
		Reader: &buf,
	}

	if err := c.WriteByte(0x42); err != nil {
		t.Fatalf("WriteByte: %v", err)
	}
	if err := c.WriteInt32(123456); err != nil {
		t.Fatalf("WriteInt32: %v", err)
	}
	if err := c.WriteInt64(7890123456789); err != nil {
		t.Fatalf("WriteInt64 large: %v", err)
	}
	if err := c.WriteInt64(100); err != nil {
		t.Fatalf("WriteInt64 small: %v", err)
	}
	if err := c.WriteString("hello rsync"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	b, err := c.ReadByte()
	if err != nil || b != 0x42 {
		t.Fatalf("ReadByte = %x, %v; want 0x42", b, err)
	}

	i32, err := c.ReadInt32()
	if err != nil || i32 != 123456 {
		t.Fatalf("ReadInt32 = %d, %v; want 123456", i32, err)
	}

	i64Large, err := c.ReadInt64()
	if err != nil || i64Large != 7890123456789 {
		t.Fatalf("ReadInt64 large = %d, %v; want 7890123456789", i64Large, err)
	}

	i64Small, err := c.ReadInt64()
	if err != nil || i64Small != 100 {
		t.Fatalf("ReadInt64 small = %d, %v; want 100", i64Small, err)
	}

	strBuf := make([]byte, 11)
	if _, err := io.ReadFull(c.Reader, strBuf); err != nil || string(strBuf) != "hello rsync" {
		t.Fatalf("Read string = %q, %v", string(strBuf), err)
	}
}

func TestWireBuffer(t *testing.T) {
	var wb rsyncwire.Buffer
	wb.WriteByte(0x12)
	wb.WriteInt32(99)
	wb.WriteInt64(500)
	wb.WriteInt64(0x100000000)
	wb.WriteString("test")

	if wb.String() == "" {
		t.Fatalf("Buffer.String() empty")
	}

	wb.Reset()
	if wb.String() != "" {
		t.Fatalf("Buffer.Reset() failed to clear buffer")
	}
}

func TestMultiplexWriterReader(t *testing.T) {
	pr, pw := net.Pipe()
	defer pr.Close()
	defer pw.Close()

	mw := &rsyncwire.MultiplexWriter{Writer: pw}
	mr := &rsyncwire.MultiplexReader{
		Env:    &rsyncos.Env{Stderr: io.Discard},
		Reader: pr,
	}

	go func() {
		_, _ = mw.WriteMsg(rsyncwire.MsgData, []byte("data payload"))
		_, _ = mw.WriteMsg(rsyncwire.MsgInfo, []byte("info payload"))
		_, _ = mw.WriteMsg(rsyncwire.MsgErrorXfer, []byte("error payload"))
		_, _ = mw.Write([]byte("raw write"))
	}()

	tag, p, err := mr.ReadMsg()
	if err != nil || tag != rsyncwire.MsgData || string(p) != "data payload" {
		t.Fatalf("ReadMsg Data = tag %d, payload %q, err %v", tag, p, err)
	}

	outBuf := make([]byte, 9)
	if _, err := io.ReadFull(mr, outBuf); err != nil {
		t.Fatalf("io.ReadFull error: %v", err)
	}
	if string(outBuf) != "raw write" {
		t.Fatalf("Read = %q, want 'raw write'", string(outBuf))
	}
}

func TestMultiplexReaderSocketError(t *testing.T) {
	pr, pw := net.Pipe()
	defer pr.Close()
	defer pw.Close()

	mw := &rsyncwire.MultiplexWriter{Writer: pw}
	mr := &rsyncwire.MultiplexReader{
		Env:    &rsyncos.Env{Stderr: io.Discard},
		Reader: pr,
	}

	go func() {
		_, _ = mw.WriteMsg(rsyncwire.MsgErrorSocket, []byte("fatal socket error"))
	}()

	outBuf := make([]byte, 10)
	_, err := mr.Read(outBuf)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("socket error")) {
		t.Fatalf("Expected socket error, got %v", err)
	}
}

func TestMultiplexReaderOversizedMessage(t *testing.T) {
	var buf bytes.Buffer
	// Header with length 0x01000001 (exceeds maxMessageSize)
	header := uint32(7+0)<<24 | uint32(0x01000001)
	_ = binary.Write(&buf, binary.LittleEndian, header)

	mr := &rsyncwire.MultiplexReader{
		Env:    &rsyncos.Env{Stderr: io.Discard},
		Reader: &buf,
	}

	_, _, err := mr.ReadMsg()
	if err == nil {
		t.Fatalf("Expected error for oversized message, got nil")
	}
}

func TestCountingReaderWriter(t *testing.T) {
	var buf bytes.Buffer
	crd, cwr := rsyncwire.CounterPair(&buf, &buf)

	n, err := cwr.Write([]byte("12345"))
	if err != nil || n != 5 || cwr.BytesWritten != 5 {
		t.Fatalf("cwr.Write = %d, %v; BytesWritten=%d", n, err, cwr.BytesWritten)
	}

	readBuf := make([]byte, 5)
	n, err = crd.Read(readBuf)
	if err != nil || n != 5 || crd.BytesRead != 5 {
		t.Fatalf("crd.Read = %d, %v; BytesRead=%d", n, err, crd.BytesRead)
	}
}

func TestRateLimiter(t *testing.T) {
	rl := rsyncwire.NewRateLimiter(1024) // 1MB/s
	if rl == nil {
		t.Fatalf("NewRateLimiter returned nil")
	}

	start := time.Now()
	ctx := context.Background()
	rl.Wait(ctx, 100)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("RateLimiter waited too long")
	}

	rlZero := rsyncwire.NewRateLimiter(0)
	rlZero.Wait(ctx, 1000) // zero limit should return immediately
}
