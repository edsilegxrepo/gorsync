package sender_test

import (
	"io"
	"log"
	"net"
	"testing"
	"testing/fstest"

	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncos"
	"github.com/edsilegxrepo/rsync/internal/rsyncwire"
	"github.com/edsilegxrepo/rsync/internal/sender"
)

func TestSenderDoWithMapFS(t *testing.T) {
	mockFS := fstest.MapFS{
		"file1.txt":    &fstest.MapFile{Data: []byte("file1 content")},
		"subdir/file2": &fstest.MapFile{Data: []byte("file2 content")},
	}

	src := sender.NewFSSource(mockFS)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	opts := rsyncopts.NewOptions(&rsyncos.Env{Stdout: io.Discard, Stderr: io.Discard})

	go func() {
		clientConn, err := ln.Accept()
		if err != nil {
			return
		}
		defer clientConn.Close()

		crd, cwr := rsyncwire.CounterPair(clientConn, clientConn)
		cServer := &rsyncwire.Conn{Writer: clientConn, Reader: clientConn}

		st := &sender.Transfer{
			Logger: log.Default(),
			Conn:   cServer,
			Opts:   opts,
			Env:    &rsyncos.Env{Stdout: io.Discard, Stderr: io.Discard},
			Source: src,
		}
		_, _ = st.Do(crd, cwr, ".", []string{"."}, nil)
	}()

	srvConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer srvConn.Close()

	cClient := &rsyncwire.Conn{Writer: srvConn, Reader: srvConn}
	var readCount int
	for {
		b, err := cClient.ReadByte()
		if err != nil {
			break
		}
		readCount++
		if b == 0 {
			// End of file list -> send NDX_DONE (-1) to finish sender transfer
			_ = cClient.WriteInt32(-1)
			break
		}
	}
	if readCount == 0 {
		t.Fatalf("Expected wire output from sender")
	}
}
