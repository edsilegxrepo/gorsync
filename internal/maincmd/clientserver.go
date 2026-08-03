package maincmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/edsilegxrepo/rsync"
	"github.com/edsilegxrepo/rsync/internal/restrict"
	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncos"
	"github.com/edsilegxrepo/rsync/internal/rsyncstats"
)

// rsync/clientserver.c:start_socket_client
func socketClient(ctx context.Context, osenv *rsyncos.Env, opts *rsyncopts.Options, host string, remotePath string, port int, paths []string, roDirs, rwDirs []string) (*rsyncstats.TransferStats, error) {
	dialHost := host
	if idx := strings.IndexByte(dialHost, '@'); idx > -1 {
		dialHost = dialHost[idx+1:]
	}
	if port < 0 {
		if port := opts.RsyncPort(); port > 0 {
			dialHost += ":" + strconv.Itoa(port)
		} else {
			dialHost += ":873" // rsync daemon port
		}
	} else {
		dialHost += ":" + strconv.Itoa(port)
	}
	dialer := net.Dialer{
		// Prefer the Go resolver: We know which files it uses (which makes life
		// easier for the restrict package), whereas the C resolver can be
		// extended by host-specific plugins.
		Resolver: &net.Resolver{
			PreferGo: true,
		},
	}
	timeoutStr := ""
	if timeout := opts.ConnectTimeoutSeconds(); timeout > 0 {
		dialer.Timeout = time.Duration(timeout) * time.Second
		timeoutStr = fmt.Sprintf(" (timeout: %d seconds)", timeout)
	}
	dialFn := dialer.DialContext
	if osenv.DialContext != nil {
		dialFn = osenv.DialContext
		osenv.Logf("Opening TCP connection to %s%s (via custom DialContext)", dialHost, timeoutStr)
	} else {
		osenv.Logf("Opening TCP connection to %s%s", dialHost, timeoutStr)
	}
	conn, err := dialFn(ctx, "tcp", dialHost)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var ioConn io.ReadWriter = conn
	if timeout := opts.IOTimeoutSeconds(); timeout > 0 {
		ioConn = &TimeoutConn{
			Conn:    conn,
			Timeout: time.Duration(timeout) * time.Second,
		}
	}

	if osenv.Restrict() {
		if err := restrict.MaybeFileSystem(roDirs, rwDirs); err != nil {
			return nil, err
		}
	}
	fullPath := remotePath
	if idx := strings.IndexByte(host, '@'); idx > -1 {
		fullPath = host[:idx] + "@" + remotePath
	}
	done, err := StartInbandExchange(osenv, opts, ioConn, fullPath)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, nil
	}
	stats, err := ClientRun(osenv, opts, ioConn, paths, false)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// rsync/clientserver.c:start_inband_exchange
func StartInbandExchange(osenv *rsyncos.Env, opts *rsyncopts.Options, conn io.ReadWriter, remotePath string) (done bool, _ error) {
	urlUser, urlPass := extractUserPass(remotePath)
	module := remotePath
	if idx := strings.IndexByte(module, '@'); idx > -1 {
		module = module[idx+1:]
	}
	if idx := strings.Index(module, "://"); idx > -1 {
		module = module[idx+3:]
		if at := strings.IndexByte(module, '/'); at > -1 {
			module = module[at+1:]
		}
	}
	if idx := strings.IndexByte(module, '/'); idx > -1 {
		module = module[:idx]
	}
	osenv.Logf("rsync module %q, path %q", module, remotePath)

	rd := bufio.NewReader(conn)

	// send client greeting
	fmt.Fprintf(conn, "@RSYNCD: %d\n", rsync.ProtocolVersion)

	// read server greeting
	serverGreeting, err := rd.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("ReadString: %v", err)
	}
	serverGreeting = strings.TrimSpace(serverGreeting)
	const serverGreetingPrefix = "@RSYNCD: "
	if !strings.HasPrefix(serverGreeting, serverGreetingPrefix) {
		return false, fmt.Errorf("invalid server greeting: got %q", serverGreeting)
	}
	// protocol negotiation: require at least version 27
	serverGreeting = strings.TrimPrefix(serverGreeting, serverGreetingPrefix)
	var remoteProtocol, remoteSub int32
	if _, err := fmt.Sscanf(serverGreeting, "%d.%d", &remoteProtocol, &remoteSub); err != nil {
		if _, err := fmt.Sscanf(serverGreeting, "%d", &remoteProtocol); err != nil {
			return false, fmt.Errorf("reading server greeting: %v", err)
		}
	}
	if remoteProtocol < 27 {
		return false, fmt.Errorf("server version %d too old", remoteProtocol)
	}

	if opts.Verbose() {
		osenv.Logf("(Client) Protocol versions: remote=%d, negotiated=%d", remoteProtocol, rsync.ProtocolVersion)
		osenv.Logf("Client checksum: md4")
	}

	// send module name
	fmt.Fprintf(conn, "%s\n", module)
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return false, fmt.Errorf("did not get server startup line: %v", err)
		}
		line = strings.TrimSpace(line)
		if opts.DebugGTE(rsyncopts.DEBUG_PROTO, 1) {
			osenv.Logf("read line: %q", line)
		}

		if strings.HasPrefix(line, "@RSYNCD: AUTHREQD ") {
			challenge := strings.TrimPrefix(line, "@RSYNCD: AUTHREQD ")
			user := resolveUsername(urlUser)
			passSec, err := getPasswordSecret(opts, urlPass)
			if err != nil {
				return false, fmt.Errorf("auth error: %v", err)
			}
			hash, err := generateAuthHashSecret(passSec, challenge)
			passSec.Destroy()
			if err != nil {
				return false, fmt.Errorf("auth hash error: %v", err)
			}
			fmt.Fprintf(conn, "%s %s\n", user, hash)
			continue
		}

		if line == "@RSYNCD: OK" {
			break
		}

		if line == "@RSYNCD: EXIT" {
			return true, nil
		}

		if strings.HasPrefix(line, "@ERROR") {
			fmt.Fprintf(osenv.Stderr, "%s\n", line)
			return false, fmt.Errorf("abort (rsync fatal error)")
		}

		if opts.OutputMOTD() {
			// print rsync server message of the day (MOTD)
			fmt.Fprintf(osenv.Stdout, "%s\n", line)
		}
	}

	sargv := opts.ServerOptions()
	sargv = append(sargv, ".")
	sargv = append(sargv, remotePath)
	if opts.Verbose() {
		osenv.Logf("sending daemon args: %s", sargv)
	}
	for _, argv := range sargv {
		fmt.Fprintf(conn, "%s\n", argv)
	}
	fmt.Fprintf(conn, "\n")

	return false, nil
}

type TimeoutConn struct {
	net.Conn
	Timeout time.Duration
}

func (c *TimeoutConn) Read(b []byte) (int, error) {
	if c.Timeout > 0 {
		_ = c.SetReadDeadline(time.Now().Add(c.Timeout))
	}
	return c.Conn.Read(b)
}

func (c *TimeoutConn) Write(b []byte) (int, error) {
	if c.Timeout > 0 {
		_ = c.SetWriteDeadline(time.Now().Add(c.Timeout))
	}
	return c.Conn.Write(b)
}
