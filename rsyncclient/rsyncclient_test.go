package rsyncclient_test

import (
	"bytes"
	"context"
	"net"
	"testing"

	"github.com/edsilegxrepo/rsync/rsyncclient"
)

func TestClientOptionsAndInit(t *testing.T) {
	var stdoutBuf, stderrBuf bytes.Buffer

	client, err := rsyncclient.New(
		[]string{"--archive", "--verbose"},
		rsyncclient.WithStdout(&stdoutBuf),
		rsyncclient.WithStderr(&stderrBuf),
		rsyncclient.WithSender(),
		rsyncclient.WithoutNegotiate(),
	)
	if err != nil {
		t.Fatalf("rsyncclient.New: %v", err)
	}

	if client == nil {
		t.Fatalf("Client is nil")
	}
}

func TestClientListFilesMock(t *testing.T) {
	var stdoutBuf, stderrBuf bytes.Buffer
	client, err := rsyncclient.New(
		[]string{"--archive"},
		rsyncclient.WithStdout(&stdoutBuf),
		rsyncclient.WithStderr(&stderrBuf),
		rsyncclient.WithoutNegotiate(),
	)
	if err != nil {
		t.Fatalf("rsyncclient.New: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("@RSYNCD: 31.0\n@RSYNCD: OK\n"))
		_, _ = conn.Read(buf)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	files, err := client.ListFiles(ctx, conn, "testmod/subdir")
	t.Logf("ListFiles result: files=%d, err=%v", len(files), err)
}
