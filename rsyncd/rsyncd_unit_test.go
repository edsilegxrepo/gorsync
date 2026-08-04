package rsyncd_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"testing/fstest"

	"github.com/edsilegxrepo/rsync/rsyncd"
)

func TestNewServerValidation(t *testing.T) {
	// Module without Name
	_, err := rsyncd.NewServer([]rsyncd.Module{{Path: "/tmp"}})
	if err == nil {
		t.Fatalf("Expected error for missing module name, got nil")
	}

	// Module without Path or FS or WritableFS
	_, err = rsyncd.NewServer([]rsyncd.Module{{Name: "test"}})
	if err == nil {
		t.Fatalf("Expected error for module without backing store, got nil")
	}

	// Valid module with FS
	mem := fstest.MapFS{"test.txt": &fstest.MapFile{Data: []byte("hello")}}
	srv, err := rsyncd.NewServer([]rsyncd.Module{
		{Name: "valid", FS: mem},
	})
	if err != nil || srv == nil {
		t.Fatalf("NewServer with FS failed: %v", err)
	}
}

func TestServerModuleListing(t *testing.T) {
	mem := fstest.MapFS{"data.txt": &fstest.MapFile{Data: []byte("content")}}
	srv, err := rsyncd.NewServer([]rsyncd.Module{
		{Name: "mod1", FS: mem},
		{Name: "mod2", FS: mem},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Serve(ctx, ln)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("@RSYNCD: 31.0\n#list\n"))

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read: %v", err)
	}

	out := string(buf[:n])
	if !bytes.Contains([]byte(out), []byte("@RSYNCD: ")) {
		t.Fatalf("Expected daemon greeting in output, got %q", out)
	}
}
