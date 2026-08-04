// Tool gorsync is the main rsync client/server CLI executable.
//
// OBJECTIVE:
// Provide the primary Command Line Interface (CLI) entry point for the native Go rsync executable.
//
// CORE COMPONENTS & DATA FLOW:
// 1. Signal Handler: Captures OS interrupt signals (SIGINT, SIGTERM) into a cancellable Go context.
// 2. Command Adapter: Constructs an rsynccmd.Cmd instance populated with standard OS stream handles (Stdin, Stdout, Stderr).
// 3. Execution Engine: Calls cmd.Run(ctx) which dispatches to maincmd.Main.
// 4. Exit Code Mapper: Evaluates returned errors against maincmd.ExitError to exit with standard rsync(1) numeric exit codes.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/edsilegxrepo/gorsync/internal/maincmd"
	"github.com/edsilegxrepo/gorsync/rsynccmd"
)

func main() {
	// 1. Context Signal Management: Cancel context gracefully on SIGINT / SIGTERM.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 2. Command Initialization: Delegate CLI argument parsing and stream binding.
	cmd := rsynccmd.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 3. Execution & Exit Code Mapping:
	// Execute transfer pipeline and map error types to precise rsync(1) exit codes.
	if _, err := cmd.Run(ctx); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			os.Exit(maincmd.ExitCodeSignal)
		}
		var exitErr *maincmd.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.Err != nil && exitErr.Code != maincmd.ExitCodeOK {
				fmt.Fprintf(os.Stderr, "rsync error: %v (code %d)\n", exitErr.Err, exitErr.Code)
			}
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "rsync error: %v\n", err)
		os.Exit(1)
	}
}
