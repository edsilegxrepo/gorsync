// Package rsyncd implements an rsync server (only), but note that gokrazy/rsync
// contains a native Go rsync implementation that supports sending and receiving
// files as client or server, compatible with the original tridge rsync (from
// the samba project) or openrsync (used on OpenBSD and macOS 15+).
package rsyncd

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/edsilegxrepo/rsync"
	"github.com/edsilegxrepo/rsync/internal/log"
	"github.com/edsilegxrepo/rsync/internal/parallel"
	"github.com/edsilegxrepo/rsync/internal/progress"
	"github.com/edsilegxrepo/rsync/internal/receiver"
	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncos"
	"github.com/edsilegxrepo/rsync/internal/rsyncsec"
	"github.com/edsilegxrepo/rsync/internal/rsyncwire"
	"github.com/edsilegxrepo/rsync/internal/sender"
	"github.com/edsilegxrepo/secretprotector/pkg/libsecsecrets"
	"github.com/mmcloughlin/md4"
)

type Module struct {
	Name          string           `toml:"name"`
	Comment       string           `toml:"comment"` // Description presented during module listing
	Path          string           `toml:"path"`    // If empty, FS or WritableFS must be non-nil
	FS            fs.FS            `toml:"-"`       // If set, serve read-only from this instead of Path
	WritableFS    rsync.WritableFS `toml:"-"`       // If set, serve read/write from this virtual FS
	ACL           []string         `toml:"acl"`
	Writable      bool             `toml:"writable"`
	AuthUsers     []string         `toml:"auth_users"`      // Usernames allowed to connect; empty means no auth
	SecretsFile   string           `toml:"secrets_file"`    // Path to file with user:password lines
	TLSCert       string           `toml:"tls_cert"`        // Path to TLS certificate PEM file
	TLSKey        string           `toml:"tls_key"`         // Path to TLS private key PEM file
	TLSAllowedCNs []string         `toml:"tls_allowed_cns"` // Allowed X.509 Common Names for mTLS RBAC
}

// Option specifies the server options.
type Option interface {
	applyServer(*Server)
}

type serverOptionFunc func(server *Server)

func (f serverOptionFunc) applyServer(s *Server) {
	f(s)
}

// WithLogger specifies the logger to use for the server.
// It also sets the global logger used by the rsync package.
func WithLogger(logger log.Logger) Option {
	return serverOptionFunc(func(s *Server) {
		s.logger = logger
	})
}

func WithStderr(stderr io.Writer) Option {
	return serverOptionFunc(func(s *Server) {
		s.stderr = stderr
	})
}

func DontRestrict() Option {
	return serverOptionFunc(func(s *Server) {
		s.dontRestrict = true
	})
}

// WithTLSConfig sets the TLS configuration for the server listener.
func WithTLSConfig(cfg *tls.Config) Option {
	return serverOptionFunc(func(s *Server) {
		s.tlsConfig = cfg
	})
}

// WithTLSCertKeyPair sets the TLS certificate and key files for the server.
func WithTLSCertKeyPair(certFile, keyFile string) Option {
	return serverOptionFunc(func(s *Server) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			s.tlsErr = fmt.Errorf("failed to load server TLS cert/key: %v", err)
			return
		}
		s.tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	})
}

// WithTLSClientCA enables mTLS and sets the Client CA certificate pool for verifying client certs.
func WithTLSClientCA(caFile string, requireClientCert bool) Option {
	return serverOptionFunc(func(s *Server) {
		caPEM, err := os.ReadFile(caFile) // #nosec G304 -- TLS CA certificate file loaded from explicit configured path
		if err != nil {
			s.tlsErr = fmt.Errorf("failed to read Client CA file: %v", err)
			return
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			s.tlsErr = fmt.Errorf("failed to parse Client CA cert from %s", caFile)
			return
		}
		if s.tlsConfig == nil {
			s.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		s.tlsConfig.ClientCAs = pool
		if requireClientCert {
			s.tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			s.tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
	})
}

func NewServer(modules []Module, opts ...Option) (*Server, error) {
	for _, mod := range modules {
		if err := validateModule(mod); err != nil {
			return nil, err
		}
	}

	server := &Server{
		modules: modules,
	}

	for _, opt := range opts {
		opt.applyServer(server)
	}

	if server.tlsErr != nil {
		return nil, server.tlsErr
	}

	// Default to os.Stderr if no stderr was specified.
	// Explicitly use io.Discard if you do not want stderr.
	if server.stderr == nil {
		server.stderr = os.Stderr
	}

	if server.logger == nil {
		// TODO: use the logger in a *rsyncos.Env instead
		server.logger = log.New(server.stderr)
	}

	// An empty module list means this server is a sender
	// (e.g. started in command mode with --server --sender),
	// in which case restrict.MaybeFileSystem() will be called
	// by the caller of NewServer().
	if !server.dontRestrict && len(server.modules) > 0 {
		if err := restrictToModules(server.modules); err != nil {
			return nil, err
		}
	}

	return server, nil
}

type Server struct {
	stderr       io.Writer
	logger       log.Logger
	dontRestrict bool
	tlsConfig    *tls.Config
	tlsErr       error

	modules []Module
}

func (s *Server) getModule(requestedModule string) (Module, error) {
	for _, mod := range s.modules {
		if mod.Name == requestedModule {
			return mod, nil
		}
	}

	return Module{}, fmt.Errorf("no such module: %s", requestedModule)
}

func (s *Server) formatModuleList() string {
	if len(s.modules) == 0 {
		return ""
	}
	var list strings.Builder
	for _, mod := range s.modules {
		comment := mod.Comment
		if comment == "" {
			comment = mod.Name
		}
		fmt.Fprintf(&list, "%s\t%s\n",
			mod.Name,
			comment)
	}
	return list.String()
}

func checkACL(acls []string, remoteAddr string) error {
	if len(acls) == 0 {
		return nil
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return fmt.Errorf("BUG: invalid remote address %q", remoteAddr)
	}
	remoteIP := net.ParseIP(host)
	if remoteIP == nil {
		return fmt.Errorf("BUG: invalid remote host %q", host)
	}
	for _, acl := range acls {
		// TODO(performance): move ACL parsing to config-time to make ACL checks
		// less expensive
		i := strings.Index(acl, " ")
		if i < 0 {
			return fmt.Errorf("invalid acl: %q (no space found)", acl)
		}
		action, who := acl[:i], acl[i+len(" "):]
		if action != "allow" && action != "deny" {
			return fmt.Errorf("invalid acl: %q (syntax: allow|deny <all|ipnet>)", acl)
		}
		if who == "all" {
			// The all keyword matches any remote IP address
		} else {
			_, net, err := net.ParseCIDR(who)
			if err != nil {
				return fmt.Errorf("invalid acl: %q (syntax: allow|deny <all|ipnet>)", acl)
			}
			if !net.Contains(remoteIP) {
				// Skip this instruction, the remote IP does not match
				continue
			}
		}
		switch action {
		case "allow":
			return nil
		case "deny":
			return fmt.Errorf("access denied (acl %q)", acl)
		default:
			return fmt.Errorf("invalid acl: %q (syntax: allow|deny <all|ipnet>)", acl)
		}
	}
	return nil
}

// FIXME: context cancellation not yet implemented
func (s *Server) HandleDaemonConn(ctx context.Context, conn *Conn) (err error) {
	_ = ctx // not implemented. what would be the best thing to do? wrap conn's reader part with cancelable reader?

	const terminationCommand = "@RSYNCD: OK\n"
	cwr := conn.cwr
	rd := conn.rd
	// send server greeting

	fmt.Fprintf(cwr, "@RSYNCD: 31.0\n")

	// read client greeting
	clientGreeting, err := rd.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(clientGreeting, "@RSYNCD: ") {
		return fmt.Errorf("invalid client greeting: got %q", clientGreeting)
	}

	var remoteProtocol int
	_, _ = fmt.Sscanf(clientGreeting, "@RSYNCD: %d", &remoteProtocol)
	negotiatedProtocol := remoteProtocol
	if negotiatedProtocol > rsync.MaxProtocolVersion {
		negotiatedProtocol = rsync.MaxProtocolVersion
	}
	if negotiatedProtocol < 20 {
		negotiatedProtocol = 20
	}
	s.logger.Printf("negotiated protocol version: %d (remote: %d)", negotiatedProtocol, remoteProtocol)

	// read requested module(s), if any
	requestedModule, err := rd.ReadString('\n')
	if err != nil {
		return err
	}
	requestedModule = strings.TrimSpace(requestedModule)
	if requestedModule == "" || requestedModule == "#list" {
		s.logger.Printf("client %v requested rsync module listing", conn.name)
		_, _ = io.WriteString(cwr, s.formatModuleList())
		_, _ = io.WriteString(cwr, "@RSYNCD: EXIT\n")
		return nil
	}
	s.logger.Printf("client %v requested rsync module %q", conn.name, requestedModule)
	module, err := s.getModule(requestedModule)
	if err != nil {
		fmt.Fprintf(cwr, "@ERROR: Unknown module %q\n", requestedModule)
		return err
	}

	if err := checkACL(module.ACL, conn.name); err != nil {
		fmt.Fprintf(cwr, "@ERROR: %v\n", err)
		return err
	}

	if len(module.TLSAllowedCNs) > 0 {
		var clientCN string
		if conn.raw != nil {
			if tc, ok := conn.raw.(*tls.Conn); ok {
				state := tc.ConnectionState()
				if len(state.PeerCertificates) > 0 {
					clientCN = state.PeerCertificates[0].Subject.CommonName
				}
			}
		}
		allowed := false
		for _, cn := range module.TLSAllowedCNs {
			if cn == clientCN || cn == "*" {
				allowed = true
				break
			}
		}
		if !allowed {
			err := fmt.Errorf("access denied (client CN %q not allowed for module %q)", clientCN, module.Name)
			fmt.Fprintf(cwr, "@ERROR: %v\n", err)
			return err
		}
	}

	if len(module.AuthUsers) > 0 {
		if err := s.authServer(rd, cwr, &module, conn.name); err != nil {
			fmt.Fprintf(cwr, "@ERROR: auth failed on module %s\n", module.Name)
			return err
		}
	}

	_, _ = io.WriteString(cwr, terminationCommand)

	// read requested flags
	var flags []string
	for {
		flag, err := rd.ReadString('\n')
		if err != nil {
			return err
		}
		flag = strings.TrimSpace(flag)
		s.logger.Printf("client sent: %q", flag)
		if flag == "" {
			break
		}
		flags = append(flags, flag)
	}

	s.logger.Printf("flags: %+v", flags)
	osenv := &rsyncos.Env{Stderr: s.stderr}
	pc := rsyncopts.NewContext(rsyncopts.NewOptionsWithDefaults(osenv))
	if err := pc.ParseArguments(osenv, flags); err != nil {
		err = fmt.Errorf("parsing server args: %v", err)

		// terminate connection with an error about which flag is not supported
		c := &rsyncwire.Conn{
			Reader: rd,
			Writer: cwr,
		}

		const errorSeed = 0xee
		if err := c.WriteInt32(errorSeed); err != nil {
			return err
		}

		// Switch to multiplexing protocol, but only for server-side transmissions.
		// Transmissions received from the client are not multiplexed.
		mpx := &rsyncwire.MultiplexWriter{Writer: c.Writer}
		_, _ = mpx.WriteMsg(rsyncwire.MsgErrorXfer, fmt.Appendf(nil, "gorsync [sender]: %v\n", err))

		return err
	}
	remaining := pc.RemainingArgs
	s.logger.Printf("remaining: %q", remaining)
	// remaining[0] is always "."
	// remaining[1] is the first directory
	if len(remaining) < 2 {
		return fmt.Errorf("invalid args: at least one directory required")
	}
	if got, want := remaining[0], "."; got != want {
		return fmt.Errorf("protocol error: got %q, expected %q", got, want)
	}
	paths := remaining[1:]
	s.logger.Printf("paths: %q", paths)

	// Strip the module_name/ prefix out of the paths,
	// see rsync/io.c:read_args, glob_expand_module().
	for idx, path := range pc.RemainingArgs {
		if idx == 0 {
			// skip pc.RemainingArgs[0], only strip RemainingArgs[1:]
			continue
		}
		p := path
		if at := strings.IndexByte(p, '@'); at > -1 {
			p = p[at+1:]
		}
		trimmed := strings.TrimPrefix(p, module.Name)
		if trimmed == "" {
			trimmed = "."
		}
		pc.RemainingArgs[idx] = trimmed
	}

	s.logger.Printf("trimmed paths: %q", pc.RemainingArgs[1:])

	return s.handleConn(ctx, conn, &module, pc, false)
}

func sanitizePath(p string) string {
	p = strings.TrimPrefix(p, "/")
	p = path.Clean(p)
	if p == "" || p == "." || p == ".." {
		return "."
	}
	return p
}

type Conn struct {
	name string
	crd  *rsyncwire.CountingReader
	cwr  *rsyncwire.CountingWriter
	rd   *bufio.Reader
	raw  net.Conn
}

func NewConnection(r io.Reader, w io.Writer, name string) *Conn {
	crd, cwr := rsyncwire.CounterPair(r, w)
	rd := bufio.NewReader(crd)
	var raw net.Conn
	if nc, ok := r.(net.Conn); ok {
		raw = nc
	}
	return &Conn{
		name: name,
		crd:  crd,
		cwr:  cwr,
		rd:   rd,
		raw:  raw,
	}
}

// This method is only exported until we refactor; use HandleConnArgs() instead
func (s *Server) InternalHandleConn(ctx context.Context, conn *Conn, module *Module, pc *rsyncopts.Context) error {
	return s.handleConn(ctx, conn, module, pc, true /* negotiate */)
}

func (s *Server) HandleConnArgs(ctx context.Context, conn *Conn, module *Module, args []string) error {
	osenv := &rsyncos.Env{Stderr: s.stderr}
	pc := rsyncopts.NewContext(rsyncopts.NewOptionsWithDefaults(osenv))
	if err := pc.ParseArguments(osenv, args); err != nil {
		return fmt.Errorf("parsing server args: %v", err)
	}
	return s.handleConn(ctx, conn, module, pc, true /* negotiate */)
}

// handleConn is equivalent to rsync/main.c:start_server
func (s *Server) handleConn(ctx context.Context, conn *Conn, module *Module, pc *rsyncopts.Context, negotiate bool) (err error) {
	rd := conn.rd
	crd := conn.crd
	cwr := conn.cwr
	opts := pc.Options
	paths := pc.RemainingArgs[1:]

	// “SHOULD be unique to each connection” as per
	// https://github.com/JohannesBuchner/Jarsync/blob/master/jarsync/rsync.txt
	//
	// Computed the same way that tridge rsync does it, but the details do not
	// matter. The goal is to have a checksum seed each time.
	sessionChecksumSeed := int32(time.Now().Unix()) ^ (int32(os.Getpid()) << 6) // #nosec G115 -- checksum seed generation int32 conversion

	c := &rsyncwire.Conn{
		Reader: rd,
		Writer: cwr,
	}

	if negotiate {
		remoteProtocol, err := c.ReadInt32()
		if err != nil {
			return err
		}
		if opts.DebugGTE(rsyncopts.DEBUG_PROTO, 1) {
			s.logger.Printf("remote protocol: %d", remoteProtocol)
		}
		if err := c.WriteInt32(rsync.ProtocolVersion); err != nil {
			return err
		}
	}

	if err := c.WriteInt32(sessionChecksumSeed); err != nil {
		return err
	}

	// Switch to multiplexing protocol, but only for server-side transmissions.
	// Transmissions received from the client are not multiplexed.
	mpx := &rsyncwire.MultiplexWriter{Writer: c.Writer}
	// Update cwr to track the multiplexed writer,
	// but copy the number of bytes written.
	cwr = &rsyncwire.CountingWriter{
		W:            mpx,
		BytesWritten: cwr.BytesWritten,
	}
	c.Writer = cwr

	if opts.Sender() {
		// If returning an error, send the error to the client for display, too:
		defer func() {
			if err != nil {
				_, _ = mpx.WriteMsg(rsyncwire.MsgErrorXfer, fmt.Appendf(nil, "gorsync [sender]: %v\n", err))
			}
		}()

		return s.handleConnSender(module, crd, cwr, paths, opts, false, c, sessionChecksumSeed)
	}

	// If returning an error, send the error to the client for display, too:
	defer func() {
		if err != nil {
			_, _ = mpx.WriteMsg(rsyncwire.MsgErrorXfer, fmt.Appendf(nil, "gorsync [receiver]: %v\n", err))
		}
	}()
	return s.handleConnReceiver(module, crd, cwr, paths, opts, false, c, sessionChecksumSeed)
}

// handleConnReceiver is equivalent to rsync/main.c:do_server_recv
func (s *Server) handleConnReceiver(module *Module, crd *rsyncwire.CountingReader, cwr *rsyncwire.CountingWriter, paths []string, opts *rsyncopts.Options, negotiate bool, c *rsyncwire.Conn, sessionChecksumSeed int32) (err error) {
	var destPath string
	implicitModule := module == nil
	if implicitModule {
		if len(paths) != 1 {
			return fmt.Errorf("precisely one destination path required, got %q", paths)
		}
		module = &Module{
			Name:     "implicit",
			Path:     paths[0],
			Writable: true,
		}
		destPath = module.Path
	}
	if opts.Verbose() {
		s.logger.Printf("handleConnReceiver(module=%+v, destPath=%q)", module, destPath)
	}

	if !module.Writable {
		return fmt.Errorf("ERROR: module is read only")
	}

	rt := &receiver.Transfer{
		Logger: s.logger,
		Opts: &receiver.TransferOpts{
			DryRun:   opts.DryRun(),
			Server:   opts.Server(),
			Verbose:  opts.Verbose(),
			Progress: opts.Progress(),

			DeleteMode:        opts.DeleteMode(),
			PreserveGid:       opts.PreserveGid(),
			PreserveUid:       opts.PreserveUid(),
			PreserveLinks:     opts.PreserveLinks(),
			PreservePerms:     opts.PreservePerms(),
			PreserveDevices:   opts.PreserveDevices(),
			PreserveSpecials:  opts.PreserveSpecials(),
			PreserveTimes:     opts.PreserveMTimes(),
			PreserveHardlinks: opts.PreserveHardLinks(),
			IgnoreTimes:       opts.IgnoreTimes(),
			SizeOnly:          opts.SizeOnly(),
			TempDir:           opts.TempDir(),
			AlwaysChecksum:    opts.AlwaysChecksum(),
			WholeFile:         opts.WholeFile(),
			WritableFS:        module.WritableFS,

			InfoGTE:  opts.InfoGTE,
			DebugGTE: opts.DebugGTE,
		},
		Dest: module.Path,
		Env: &rsyncos.Env{
			Stderr: s.stderr,
		},
		Conn:     c,
		Seed:     sessionChecksumSeed,
		Progress: progress.NewPrinter(io.Discard, time.Now),
	}
	if err := os.MkdirAll(rt.Dest, 0o755); err != nil { // #nosec G301 -- rsync default destination directory permission (0755)
		return fmt.Errorf("MkdirAll(dest=%s): %v", rt.Dest, err)
	}
	rt.DestRoot, err = os.OpenRoot(rt.Dest)
	if err != nil {
		return fmt.Errorf("OpenRoot(dest=%s): %v", rt.Dest, err)
	}
	defer rt.DestRoot.Close()

	if !implicitModule {
		if len(paths) > 1 {
			return fmt.Errorf("module is available, and at most one destination path is allowed, got %q", paths)
		}
		// Descend into subdirectory (if requested),
		// using the os.OpenRoot traversal-safe API.
		if len(paths) == 1 && paths[0] != "/" {
			subdir := strings.Trim(paths[0], "/")
			subdir = strings.TrimSuffix(filepath.Clean(subdir), string(filepath.Separator))
			if subdir != "" && subdir != "." {
				subRoot, err := rt.DestRoot.OpenRoot(subdir)
				if err != nil {
					if os.IsNotExist(err) {
						if err := rt.DestRoot.MkdirAll(subdir, 0o755); err != nil {
							return fmt.Errorf("MkdirAll(%s): %v", subdir, err)
						}
						subRoot, err = rt.DestRoot.OpenRoot(subdir)
					}
					if err != nil {
						return fmt.Errorf("OpenRoot(%s): %v", subdir, err)
					}
				}
				if name := subRoot.Name(); filepath.IsAbs(name) {
					rt.Dest = name
				} else {
					// Go changed behavior: In Go 1.25, subRoot.Name()
					// did not return an absolute path:
					// https://go.googlesource.com/go/+/ed7f804
					rt.Dest = filepath.Join(rt.Dest, name)
				}
				rt.DestRoot = subRoot
				if opts.Verbose() {
					s.logger.Printf("opened subdirectory %q", rt.Dest)
				}
			}
		}
	}

	if opts.PreserveHardLinks() {
		return fmt.Errorf("support for hard links not yet implemented")
	}

	if opts.DeleteMode() {
		// receive the exclusion list (openrsync’s is always empty)
		exclusionList, err := sender.RecvFilterList(c)
		if err != nil {
			return err
		}
		s.logger.Printf("exclusion list read (entries: %d)", len(exclusionList.Filters))
	}

	// receive file list
	if opts.InfoGTE(rsyncopts.INFO_FLIST, 1) {
		s.logger.Printf("receiving file list")
	}
	fileList, err := rt.ReceiveFileList()
	if err != nil {
		return err
	}
	if opts.InfoGTE(rsyncopts.INFO_FLIST, 1) {
		s.logger.Printf("received %d names", len(fileList))
	}
	stats, err := rt.Do(c, fileList, true)
	if err != nil {
		return err
	}
	if opts.InfoGTE(rsyncopts.INFO_STATS, 1) {
		s.logger.Printf("stats: %+v", stats)
	}
	return nil
}

// handleConnSender is equivalent to rsync/main.c:do_server_sender
func (s *Server) handleConnSender(module *Module, crd *rsyncwire.CountingReader, cwr *rsyncwire.CountingWriter, paths []string, opts *rsyncopts.Options, negotiate bool, c *rsyncwire.Conn, sessionChecksumSeed int32) (err error) {
	if module == nil {
		module = &Module{
			Name: "implicit",
			Path: rsync.FileSystemRoot,
		}
	}

	st := &sender.Transfer{
		Logger: s.logger,
		Opts:   opts,
		Conn:   c,
		Seed:   sessionChecksumSeed,
		Env: &rsyncos.Env{
			Stderr: s.stderr,
		},
		Progress: progress.NewPrinter(io.Discard, time.Now),
	}
	// receive the exclusion list (openrsync’s is always empty)

	if module.FS != nil {
		st.Source = sender.NewFSSource(module.FS)
	}

	exclusionList, err := sender.RecvFilterList(st.Conn)
	if err != nil {
		return err
	}
	st.Logger.Printf("exclusion list read (entries: %d)", len(exclusionList.Filters))

	stats, err := st.Do(crd, cwr, module.Path, paths, exclusionList)
	if err != nil {
		return err
	}

	s.logger.Printf("handleConnSender done. stats: %+v", stats)

	return nil
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.tlsConfig != nil {
		if s.tlsConfig.MinVersion == 0 {
			s.tlsConfig.MinVersion = tls.VersionTLS12
		}
		ln = tls.NewListener(ln, s.tlsConfig)
	}
	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			_ = ln.Close() // unblocks Accept()
		}()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil // ignore expected 'use of closed network connection' error on context cancel
			default:
				return err
			}
		}
		remoteAddr := conn.RemoteAddr()
		parallel.TuneSocketBuffers(conn)
		s.logger.Printf("remote connection from %s", remoteAddr)
		go func() {
			defer conn.Close()
			c := NewConnection(conn, conn, remoteAddr.String())
			if err := s.HandleDaemonConn(ctx, c); err != nil {
				s.logger.Printf("[%s] handle: %v", remoteAddr, err)
			}
		}()
	}
}

func validateModule(mod Module) error {
	if mod.Name == "" {
		return errors.New("module has no name")
	}
	if mod.FS != nil {
		if mod.Writable {
			return fmt.Errorf("module %q: FS modules cannot be writable", mod.Name)
		}
		if mod.Path != "" {
			return fmt.Errorf("module %q: cannot specify both Path and FS", mod.Name)
		}
	} else if mod.WritableFS != nil {
		if mod.Path != "" {
			return fmt.Errorf("module %q: cannot specify both Path and WritableFS", mod.Name)
		}
	} else {
		if mod.Path == "" {
			return fmt.Errorf("module %q has empty path", mod.Name)
		}
	}

	return nil
}

func (s *Server) authServer(rd *bufio.Reader, cwr io.Writer, module *Module, remoteAddr string) error {
	challenge := genChallenge()
	fmt.Fprintf(cwr, "@RSYNCD: AUTHREQD %s\n", challenge)

	line, err := rd.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading auth response: %v", err)
	}
	line = strings.TrimSpace(line)
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		s.logger.Printf("auth failed on module %s from %s: invalid response", module.Name, remoteAddr)
		return fmt.Errorf("invalid auth response")
	}
	user, response := line[:sp], line[sp+1:]
	s.logger.Printf("authServer got user=%q, response=%q", user, response)

	matched := false
	for _, allowed := range module.AuthUsers {
		if allowed == user {
			matched = true
			break
		}
	}
	if !matched {
		s.logger.Printf("auth failed on module %s from %s for %q: unknown user (allowed: %v)", module.Name, remoteAddr, user, module.AuthUsers)
		return fmt.Errorf("auth failed")
	}

	secret, err := lookupSecret(module.SecretsFile, user)
	if err != nil {
		s.logger.Printf("auth failed on module %s from %s for %s: %v (secretsFile: %s)", module.Name, remoteAddr, user, err, module.SecretsFile)
		return fmt.Errorf("auth failed")
	}
	defer secret.Destroy()

	expected, err := authHashSecret(secret, challenge)
	if err != nil {
		s.logger.Printf("auth failed on module %s from %s for %s: %v", module.Name, remoteAddr, user, err)
		return fmt.Errorf("auth failed")
	}
	if response != expected {
		s.logger.Printf("auth failed on module %s from %s for %s: password mismatch", module.Name, remoteAddr, user)
		return fmt.Errorf("auth failed")
	}

	s.logger.Printf("auth ok on module %s from %s for %s", module.Name, remoteAddr, user)
	return nil
}

func genChallenge() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fallback value if system entropy is unavailable
		for i := range buf {
			buf[i] = byte(i)
		}
	}
	h := md4.New()
	h.Write([]byte{0, 0, 0, 0})
	h.Write(buf[:])
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(h.Sum(nil))
}

func authHashSecret(sec *rsyncsec.ProtectedSecret, challenge string) (string, error) {
	revealed, err := sec.Reveal()
	if err != nil {
		return "", err
	}
	defer libsecsecrets.ZeroBuffer(revealed)

	h := md4.New()
	h.Write([]byte{0, 0, 0, 0})
	h.Write(revealed)
	h.Write([]byte(challenge))
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(h.Sum(nil)), nil
}

func lookupSecret(path, user string) (*rsyncsec.ProtectedSecret, error) {
	if path == "" {
		return nil, fmt.Errorf("no secrets file configured")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("reading secrets file: %v", err)
	}
	defer func() {
		libsecsecrets.ZeroBuffer(data)
	}()

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == user {
			pass := strings.TrimSpace(parts[1])
			sec, err := rsyncsec.NewProtectedSecret(pass)
			if err != nil {
				return nil, fmt.Errorf("creating secret handle: %v", err)
			}
			return sec, nil
		}
	}
	return nil, fmt.Errorf("user not found in secrets file")
}
