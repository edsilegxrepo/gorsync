package maincmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/gokrazy/rsync/internal/rsyncopts"
	"github.com/mmcloughlin/md4"
)

func extractUserPass(path string) (user, pass string) {
	s := path
	if strings.HasPrefix(s, "rsync://") {
		s = strings.TrimPrefix(s, "rsync://")
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

func getPassword(opts *rsyncopts.Options, urlPass string) (string, error) {
	if urlPass != "" {
		return urlPass, nil
	}
	if f := opts.PasswordFile(); f != "" {
		data, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			return "", fmt.Errorf("reading password file: %v", err)
		}
		lines := strings.SplitN(string(data), "\n", 2)
		return strings.TrimSpace(lines[0]), nil
	}
	if p := os.Getenv("RSYNC_PASSWORD"); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no password supplied (set RSYNC_PASSWORD or use --password-file)")
}

func generateAuthHash(password, challenge string) string {
	h := md4.New()
	h.Write([]byte{0, 0, 0, 0})
	h.Write([]byte(password))
	h.Write([]byte(challenge))
	digest := h.Sum(nil)
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(digest)
}
