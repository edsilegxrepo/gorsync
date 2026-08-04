// Package rsynccmd provides a command-like interface to the rsync engine, which
// contains a native Go rsync implementation that supports sending and receiving
// files as client or server, compatible with the original tridge rsync (from
// the samba project) or openrsync (used on OpenBSD and macOS 15+).
//
// This interface allows you to replace calls to an external rsync program, like:
//
//	rsync -a rsync://share01/dataset /tmp/dataset
//
// …with calls into Go code running in the same process (no dependency on an
// external rsync program!):
//
//	cmd := rsynccmd.Command("rsync", "-a", "rsync://share01/dataset", "/tmp/dataset")
//	cmd.Stdout = os.Stdout
//	cmd.Stderr = os.Stderr
//	if _, err := cmd.Run(context.Background()); err != nil {
//	  return fmt.Errorf("%v: %v", cmd.Args, err)
//	}
//
// OBJECTIVE:
// Expose a public, os/exec.Command-like API for programmatically embedding rsync transfers inside Go applications.
//
// CORE COMPONENTS & DATA FLOW:
// 1. Cmd Struct: Holds execution options, stream handles (Stdin/Stdout/Stderr), and optional custom dialers.
// 2. rsyncos.Env Adapter: Translates Cmd stream bindings and isolation settings into internal OS environment handles.
// 3. Engine Dispatcher: Invokes maincmd.Main to execute transfer pipeline and return transfer statistics.
package rsynccmd

import (
	"context"
	"io"
	"net"

	"github.com/edsilegxrepo/gorsync/internal/maincmd"
	"github.com/edsilegxrepo/gorsync/internal/rsyncos"
	"github.com/edsilegxrepo/gorsync/internal/rsyncstats"
)

// Cmd represents an rsync invocation being prepared or run.
type Cmd struct {
	Args         []string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	DontRestrict bool

	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Command returns the [Cmd] struct to execute rsync with the given arguments.
//
// The name parameter is ignored and only specified for symmetry with
// os/exec.Command.
func Command(name string, arg ...string) *Cmd {
	return &Cmd{
		Args: append([]string{name}, arg...),
	}
}

// Result contains information about the transfer.
type Result struct {
	Stats *rsyncstats.TransferStats
}

// Run starts the specified rsync invocation.
func (c *Cmd) Run(ctx context.Context) (*Result, error) {
	// 1. Construct OS Environment Adapter: Bind streams and custom net.Dialer overrides.
	osenv := &rsyncos.Env{
		Stdin:        c.Stdin,
		Stdout:       c.Stdout,
		Stderr:       c.Stderr,
		DontRestrict: c.DontRestrict,
		DialContext:  c.DialContext,
	}

	// 2. Execute Transfer Pipeline: Dispatch options parsing, handshake, and payload transfer.
	stats, err := maincmd.Main(ctx, osenv, c.Args, nil)
	if err != nil {
		return nil, err
	}
	return &Result{Stats: stats}, nil
}
