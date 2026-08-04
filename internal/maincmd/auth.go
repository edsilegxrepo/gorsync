package maincmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncsec"
	"github.com/edsilegxrepo/secretprotector/pkg/libsecsecrets"
	"github.com/mmcloughlin/md4"
)

func extractUserPass(path string) (user, pass string) {
	s := path
	if strings.HasPrefix(s, "rsync://") {
		s = strings.TrimPrefix(s, "rsync://")
	} else if strings.HasPrefix(s, "rsyncts://") {
		s = strings.TrimPrefix(s, "rsyncts://")
	}
	if idx := strings.IndexByte(s, '@'); idx > -1 {
		userInfo := s[:idx]
		if passIdx := strings.IndexByte(userInfo, ':'); passIdx > -1 {
			return userInfo[:passIdx], userInfo[passIdx+1:]
		}
		return userInfo, ""
	}
	return "", ""
}

func resolveUsername(urlUser string) string {
	if urlUser != "" {
		return urlUser
	}
	if u := os.Getenv("RSYNC_USERNAME"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "nobody"
}

func getPasswordSecret(opts *rsyncopts.Options, urlPass string) (*rsyncsec.ProtectedSecret, error) {
	var rawPass string
	if urlPass != "" {
		rawPass = urlPass
	} else if f := opts.PasswordFile(); f != "" {
		data, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			return nil, fmt.Errorf("reading password file: %v", err)
		}
		defer func() {
			libsecsecrets.ZeroBuffer(data)
		}()
		lines := strings.SplitN(string(data), "\n", 2)
		rawPass = strings.TrimSpace(lines[0])
	} else if p := os.Getenv("RSYNC_PASSWORD"); p != "" {
		rawPass = p
	} else {
		return nil, fmt.Errorf("no password supplied (set RSYNC_PASSWORD or use --password-file)")
	}

	sec, err := rsyncsec.NewProtectedSecret(rawPass)
	if err != nil {
		return nil, fmt.Errorf("creating protected secret handle: %v", err)
	}
	return sec, nil
}

func generateAuthHashSecret(sec *rsyncsec.ProtectedSecret, challenge string) (string, error) {
	revealed, err := sec.Reveal()
	if err != nil {
		return "", err
	}
	defer libsecsecrets.ZeroBuffer(revealed)

	h := md4.New()
	h.Write([]byte{0, 0, 0, 0})
	h.Write(revealed)
	h.Write([]byte(challenge))
	digest := h.Sum(nil)
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(digest), nil
}

// Deprecated fallback for backward compatibility
func generateAuthHash(password, challenge string) string {
	sec, err := rsyncsec.NewProtectedSecret(password)
	if err != nil {
		return ""
	}
	defer sec.Destroy()
	hash, err := generateAuthHashSecret(sec, challenge)
	if err != nil {
		return ""
	}
	return hash
}
