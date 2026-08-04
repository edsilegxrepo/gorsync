// Package maincmd implements a subset of the '$ rsync' CLI surface, namely that it can:
//   - serve as a server daemon over TCP or SSH (via SSH session stdin/stdout)
//   - act as "client" CLI for connecting to the server
//   - Not yet implemented: both "client" and "server" can act as the sender and the receiver
package maincmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/edsilegxrepo/rsync/internal/anonssh"
	"github.com/edsilegxrepo/rsync/internal/restrict"
	"github.com/edsilegxrepo/rsync/internal/rsyncdconfig"
	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncos"
	"github.com/edsilegxrepo/rsync/internal/rsyncstats"
	"github.com/edsilegxrepo/rsync/rsyncd"

	// For profiling and debugging
	_ "net/http/pprof"
)

// Standard rsync(1) exit codes for granular diagnostic reporting
const (
	ExitCodeOK          = 0  // Success
	ExitCodeSyntax      = 1  // Syntax or usage error
	ExitCodeProtocol    = 2  // Protocol incompatibility
	ExitCodeFileSelect  = 3  // Errors selecting input/output files
	ExitCodeStartClient = 5  // Error starting client-server protocol
	ExitCodeSocketIO    = 10 // Error in socket I/O
	ExitCodeFileIO      = 11 // Error in file I/O
	ExitCodeSignal      = 20 // Received SIGUSR1 or SIGINT
)

// ExitError wraps errors with a granular rsync exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d: %v", e.Code, e.Err)
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func version(osenv *rsyncos.Env) {
	osenv.Logf("gokrazy rsync, pid %d", os.Getpid())
}

type readWriter struct {
	r io.Reader
	w io.Writer
}

func (r *readWriter) Read(p []byte) (n int, err error)  { return r.r.Read(p) }
func (r *readWriter) Write(p []byte) (n int, err error) { return r.w.Write(p) }

func Main(ctx context.Context, osenv *rsyncos.Env, args []string, cfg *rsyncdconfig.Config) (*rsyncstats.TransferStats, error) {
	osenv.Logf("Main(osenv=%v, args=%q)", osenv, args)
	pc := rsyncopts.NewContext(rsyncopts.NewOptionsWithDefaults(osenv))
	if err := pc.ParseArguments(osenv, args[1:]); err != nil {
		if pe, ok := err.(*rsyncopts.PoptError); ok &&
			pe.Errno == rsyncopts.POPT_ERROR_BADOPT &&
			strings.HasPrefix(pe.Error(), "--gokr.") {
			return nil, fmt.Errorf("%v (you need to specify --daemon before flags starting with --gokr are available)", pe)
		}
		return nil, err
	}
	opts := pc.Options
	remaining := pc.RemainingArgs
	// osenv.Logf("remaining: %v", remaining)

	// calling convention: daemon mode over remote shell (also builtin SSH)
	// Example: --server --daemon .
	if opts.Daemon() && opts.Server() {
		// start_daemon()
		if cfg == nil {
			var err error
			cfg, _, err = rsyncdconfig.FromDefaultFiles()
			if err != nil {
				return nil, err
			}
		}
		rsyncdOpts := []rsyncd.Option{
			rsyncd.WithStderr(osenv.Stderr),
		}
		if osenv.DontRestrict {
			rsyncdOpts = append(rsyncdOpts, rsyncd.DontRestrict())
		}
		srv, err := rsyncd.NewServer(cfg.Modules, rsyncdOpts...)
		if err != nil {
			return nil, err
		}
		conn := rsyncd.NewConnection(osenv.Stdin, osenv.Stdout, "<remote-shell-daemon>")
		return nil, srv.HandleDaemonConn(ctx, conn)
	}

	// calling convention: command mode (over remote shell or locally)
	// Example: --server --sender -vvvvlogDtpre.iLsfxCIvu . .
	if opts.Server() {
		// start_server()
		srv, err := rsyncd.NewServer(nil, rsyncd.WithStderr(osenv.Stderr))
		if err != nil {
			return nil, err
		}

		// TODO: remove duplication with handleDaemonConn
		if len(remaining) < 2 {
			return nil, fmt.Errorf("invalid args: at least one directory required")
		}
		if got, want := remaining[0], "."; got != want {
			return nil, fmt.Errorf("protocol error: got %q, expected %q", got, want)
		}
		paths := remaining[1:]
		if opts.Verbose() {
			osenv.Logf("paths: %q", paths)
		}
		var roDirs, rwDirs []string
		if opts.Sender() {
			roDirs = append(roDirs, paths...)
		} else {
			for _, path := range paths {
				if err := os.MkdirAll(path, 0o755); err != nil {
					return nil, err
				}
			}
			rwDirs = append(rwDirs, paths...)
		}
		if osenv.Restrict() {
			if err := restrict.MaybeFileSystem(roDirs, rwDirs); err != nil {
				return nil, err
			}
		}
		conn := rsyncd.NewConnection(osenv.Stdin, osenv.Stdout, "<remote-shell>")
		return nil, srv.InternalHandleConn(ctx, conn, nil, pc)
	}

	if !opts.Daemon() {
		if !osenv.DontRestrict {
			osenv.DontRestrict = opts.CustomClient.DontRestrict == 1 || opts.GokrazyClient.DontRestrict == 1
		}
		return clientMain(ctx, osenv, opts, remaining)
	}

	// daemon_main()

	// calling convention: start a daemon in TCP listening mode (or with systemd
	// socket activation)

	var cfgfn string
	var cfgErr error
	if cfg == nil {
		daemonOpts := opts.CustomDaemon
		if daemonOpts.Config == "" {
			daemonOpts = opts.GokrazyDaemon
		}
		if daemonOpts.Config != "" {
			cfgfn = daemonOpts.Config
			cfg, cfgErr = rsyncdconfig.FromFile(cfgfn)
		} else {
			cfg, cfgfn, cfgErr = rsyncdconfig.FromDefaultFiles()
		}
		if cfgErr != nil {
			if os.IsNotExist(cfgErr) {
				osenv.Logf("config file not found, relying on flags")
				// a non-existant config file is not an error: users can start
				// gokr-rsyncd with e.g. the -gokr.listen and -gokr.modulemap flags.
				listen := daemonOpts.Listen
				anonSSH := daemonOpts.AnonSSHListen
				cfg = &rsyncdconfig.Config{
					Listeners: []rsyncdconfig.Listener{
						{
							Rsyncd:  listen,
							AnonSSH: anonSSH,
						},
					},
					Modules: []rsyncd.Module{},
				}
			} else {
				return nil, cfgErr
			}
		} else {
			osenv.Logf("config file %s loaded", cfgfn)
		}
	}

	daemonOpts := opts.CustomDaemon
	if daemonOpts.Listen == "" && daemonOpts.AnonSSHListen == "" && daemonOpts.ModuleMap == "" && daemonOpts.MonitoringListen == "" {
		daemonOpts = opts.GokrazyDaemon
	}

	if os.IsNotExist(cfgErr) {
		if daemonOpts.Listen == "" &&
			daemonOpts.AnonSSHListen == "" {
			return nil, fmt.Errorf("neither -gokr.listen nor -gokr.anonssh_listen specified, and config file not found: %v", cfgErr)
		}
		// If no config file was found, and the user did not specify a
		// -gokr.modulemap flag, use a default value to force the user to
		// configure a module map.
		if daemonOpts.ModuleMap == "" {
			daemonOpts.ModuleMap = "nonex=/nonexistant/path"
		}
	} else {
		if len(cfg.Listeners) == 0 ||
			(cfg.Listeners[0].Rsyncd == "" &&
				cfg.Listeners[0].AnonSSH == "" &&
				cfg.Listeners[0].AuthorizedSSH.Address == "") {
			return nil, fmt.Errorf("no rsyncd listeners configured, add a [[listener]] to %s", cfgfn)
		}
	}
	// TODO: loosen this restriction, create multiple listeners

	if len(cfg.Listeners) != 1 ||
		(cfg.Listeners[0].Rsyncd == "" &&
			cfg.Listeners[0].AnonSSH == "" &&
			cfg.Listeners[0].AuthorizedSSH.Address == "") {
		return nil, fmt.Errorf("not precisely 1 rsyncd listener specified")
	}

	var sshListener *anonssh.Listener
	listenAddr := cfg.Listeners[0].Rsyncd
	if listenAddr == "" {
		listenAddr = cfg.Listeners[0].AnonSSH
		if listenAddr == "" {
			listenAddr = cfg.Listeners[0].AuthorizedSSH.Address
			var err error
			sshListener, err = anonssh.ListenerFromConfig(osenv, cfg.Listeners[0])
			if err != nil {
				return nil, err
			}
		} else {
			var err error
			sshListener, err = anonssh.ListenerFromConfig(osenv, cfg.Listeners[0])
			if err != nil {
				return nil, err
			}
		}
	}

	if moduleMap := daemonOpts.ModuleMap; moduleMap != "" {
		parts := strings.Split(moduleMap, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed -gokr.modulemap parameter %q, expected <modulename>=<path>", moduleMap)
		}
		module := rsyncd.Module{
			Name: parts[0],
			Path: parts[1],
		}
		cfg.Modules = append(cfg.Modules, module)
	}
	if cfg.DontNamespace {
		if cfg.Listeners[0].Rsyncd != "" ||
			cfg.Listeners[0].AnonSSH != "" {
			return nil, fmt.Errorf("dont_namespace must be used with authorized_ssh listeners only")
		}
		version(osenv)
		osenv.Logf("environment: not namespace due to dont_namespace option")
	} else {
		if err := namespace(osenv, cfg.Modules, listenAddr); err == errIsParent {
			return nil, nil
		} else if err != nil {
			return nil, fmt.Errorf("namespace: %v", err)
		}
	}
	osenv.Logf("%d rsync modules configured in total", len(cfg.Modules))
	for _, mod := range cfg.Modules {
		if !cfg.DontNamespace && !mod.Writable {
			if err := canUnexpectedlyWriteTo(mod.Path); err != nil {
				return nil, err
			}
		}

		osenv.Logf("rsync module %q with path %s configured", mod.Name, mod.Path)
	}

	if monitoringListen := daemonOpts.MonitoringListen; monitoringListen != "" {
		go func() {
			osenv.Logf("HTTP server for monitoring listening on http://%s/debug/pprof", monitoringListen)
			// nosemgrep: go.lang.security.audit.net.use-tls.use-tls, go.lang.security.audit.net.pprof.pprof-debug-exposure
			if err := http.ListenAndServe(monitoringListen, nil); err != nil {
				osenv.Logf("-monitoring_listen: %v", err)
			}
		}()
	}

	var serverOpts []rsyncd.Option
	serverOpts = append(serverOpts, rsyncd.WithStderr(osenv.Stderr))
	cert := cfg.TLSCert
	key := cfg.TLSKey
	auth := cfg.TLSAuth
	clientCA := cfg.TLSClientCA
	if cert == "" && len(cfg.Listeners) > 0 {
		cert = cfg.Listeners[0].TLSCert
		key = cfg.Listeners[0].TLSKey
		if auth == "" {
			auth = cfg.Listeners[0].TLSAuth
		}
		if clientCA == "" {
			clientCA = cfg.Listeners[0].TLSClientCA
		}
	}
	if cert != "" && key != "" {
		serverOpts = append(serverOpts, rsyncd.WithTLSCertKeyPair(cert, key))
	}
	if clientCA != "" || auth != "" {
		requireCert := auth == "require" || auth == "RequireAndVerifyClientCert"
		serverOpts = append(serverOpts, rsyncd.WithTLSClientCA(clientCA, requireCert))
	}

	srv, err := rsyncd.NewServer(cfg.Modules, serverOpts...)
	if err != nil {
		return nil, err
	}
	var ln net.Listener
	listeners, err := systemdListeners()
	if err != nil {
		return nil, err
	}
	if len(listeners) > 0 {
		ln = listeners[0]
	} else {
		osenv.Logf("not using systemd socket activation, creating listener")
		ln, err = net.Listen("tcp", listenAddr)
		if err != nil {
			return nil, err
		}
	}

	if cfg.Listeners[0].AuthorizedSSH.Address != "" {
		if cfg.Listeners[0].AuthorizedSSH.AuthorizedKeys == "" {
			return nil, fmt.Errorf("misconfiguration: authorized_keys must not be empty when using an authorized_ssh listener")
		}
		osenv.Logf("rsync daemon listening (authorized SSH) on %s", ln.Addr())
		return nil, anonssh.Serve(ctx, osenv, ln, sshListener, cfg, func(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			osenv := &rsyncos.Env{
				Stdin:  stdin,
				Stdout: stdout,
				Stderr: stderr,
				// This process is already restricted since to the
				// rsyncd.NewServer call above. Do not add more rulesets to stay
				// under the limit of policy layers per process.
				DontRestrict: true,
			}
			_, err := Main(ctx, osenv, args, cfg)
			return err
		})
	}

	if cfg.Listeners[0].AnonSSH != "" {
		osenv.Logf("rsync daemon listening (anon SSH) on %s", ln.Addr())
		return nil, anonssh.Serve(ctx, osenv, ln, sshListener, cfg, func(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			osenv := &rsyncos.Env{
				Stdin:  stdin,
				Stdout: stdout,
				Stderr: stderr,
				// This process is already restricted since to the
				// rsyncd.NewServer call above. Do not add more rulesets to stay
				// under the limit of policy layers per process.
				DontRestrict: true,
			}
			_, err := Main(ctx, osenv, args, cfg)
			return err
		})
	}

	osenv.Logf("rsync daemon listening on rsync://%s", ln.Addr())
	return nil, srv.Serve(ctx, ln)
}
