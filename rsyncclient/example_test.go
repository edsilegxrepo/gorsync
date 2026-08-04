package rsyncclient_test

import (
	"context"
	"fmt"
	"net"

	"github.com/edsilegxrepo/gorsync/rsyncclient"
)

// ExampleNew demonstrates using rsyncclient as a Go library to perform an rsync transfer over a net.Conn.
func ExampleNew() {
	// 1. Connect to remote rsync daemon or server socket
	conn, err := net.Dial("tcp", "localhost:8730")
	if err != nil {
		fmt.Printf("dial error: %v\n", err)
		return
	}
	defer func() { _ = conn.Close() }()

	// 2. Initialize rsync client with library options
	client, err := rsyncclient.New([]string{"--archive", "--verbose"})
	if err != nil {
		fmt.Printf("client init error: %v\n", err)
		return
	}

	// 3. Run daemon inband exchange and transfer files
	ctx := context.Background()
	res, err := client.RunDaemon(ctx, conn, "module_name", []string{"file.txt"})
	if err != nil {
		fmt.Printf("transfer error: %v\n", err)
		return
	}

	_ = res
}
