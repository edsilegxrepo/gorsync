// Tool rsync is the main rsync client/server executable.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/edsilegxrepo/rsync/rsynccmd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cmd := rsynccmd.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if _, err := cmd.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
