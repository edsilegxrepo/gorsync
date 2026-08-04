package rsyncos

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/edsilegxrepo/gorsync/internal/log"
)

type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	DontRestrict bool

	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	logger log.Logger
}

func (s *Env) initLogger() {
	if s.logger == nil {
		s.logger = log.New(s.Stderr)
	}
}

func (s *Env) Logger() log.Logger {
	s.initLogger()
	return s.logger
}

func (s *Env) Logf(format string, v ...any) {
	s.initLogger()
	s.logger.Printf(format, v...)
}

func (s *Env) Restrict() bool { return !s.DontRestrict }

// ResolveSSH resolves the path to an OpenSSH executable (e.g. "ssh", "sshd", "ssh-keygen").
// On Windows, it checks in order:
//  1. OPENSSH_HOME environment variable (%OPENSSH_HOME%\<binaryName>.exe)
//  2. Default path D:\inetd\sshd\<binaryName>.exe
//  3. System PATH / default binary name
func ResolveSSH(binaryName string) string {
	if runtime.GOOS == "windows" {
		exeName := binaryName
		if !strings.HasSuffix(strings.ToLower(exeName), ".exe") {
			exeName += ".exe"
		}
		if home := os.Getenv("OPENSSH_HOME"); home != "" {
			candidate := filepath.Clean(filepath.Join(home, exeName))
			if _, err := os.Stat(candidate); err == nil { // #nosec G703 -- cleaned candidate path stat check for SSH binary resolution
				return candidate
			}
		}
		candidate := filepath.Clean(filepath.Join(`d:\inetd\sshd`, exeName))
		if _, err := os.Stat(candidate); err == nil { // #nosec G703 -- cleaned system path stat check for SSH binary resolution
			return candidate
		}
	}
	return binaryName
}
