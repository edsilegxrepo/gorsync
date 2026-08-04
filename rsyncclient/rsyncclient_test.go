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
		rsyncclient.DontRestrict(),
	)
	if err != nil {
		t.Fatalf("rsyncclient.New: %v", err)
	}

	if client == nil {
		t.Fatalf("Client is nil")
	}

	opts := client.ServerCommandOptions("src", "dest")
	if len(opts) == 0 {
		t.Fatalf("ServerCommandOptions returned empty slice")
	}
}

func TestClientNewErrorPaths(t *testing.T) {
	// Invalid CLI flag
	_, err := rsyncclient.New([]string{"--invalid-flag-does-not-exist"})
	if err == nil {
		t.Fatalf("Expected error for invalid flag, got nil")
	}

	// Remaining positional args not permitted in New()
	_, err = rsyncclient.New([]string{"--archive", "extra_arg_1", "extra_arg_2"})
	if err == nil {
		t.Fatalf("Expected error for remaining args, got nil")
	}
}

func TestClientRunDaemonExit(t *testing.T) {
	var stdoutBuf, stderrBuf bytes.Buffer
	client, err := rsyncclient.New(
		[]string{"--archive"},
		rsyncclient.WithStdout(&stdoutBuf),
		rsyncclient.WithStderr(&stderrBuf),
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
		_, _ = conn.Write([]byte("@RSYNCD: 31.0\n@RSYNCD: EXIT\n"))
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	res, err := client.RunDaemon(ctx, conn, "testmod", []string{"."})
	if err != nil {
		t.Fatalf("RunDaemon returned error: %v", err)
	}
	if res == nil || res.Stats == nil {
		t.Fatalf("Expected non-nil Result and Stats")
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
