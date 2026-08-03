package maincmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/edsilegxrepo/secretprotector/pkg/libsecsecrets"
	"github.com/gokrazy/rsync/internal/rsyncopts"
	"github.com/mmcloughlin/md4"
)

// ProtectedSecret encapsulates sensitive credential material in RAM using AES-256-GCM
// via libsecsecrets and ensures zeroing upon destruction.
type ProtectedSecret struct {
	key       []byte
	encrypted []byte
}

// NewProtectedSecret creates a memory-protected secret handle for rawPass.
func NewProtectedSecret(rawPass string) (*ProtectedSecret, error) {
	ctx := context.Background()
	keyHex, err := libsecsecrets.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed generating master key: %w", err)
	}
	key, err := libsecsecrets.ResolveKey(ctx, keyHex, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed resolving key: %w", err)
	}

	encBytes, err := libsecsecrets.EncryptBytes(ctx, []byte(rawPass), key)
	if err != nil {
		libsecsecrets.ZeroBuffer(key)
		return nil, fmt.Errorf("failed encrypting secret: %w", err)
	}

	return &ProtectedSecret{
		key:       key,
		encrypted: encBytes,
	}, nil
}

// Reveal decrypts and returns the plaintext password bytes.
// Caller MUST call libsecsecrets.ZeroBuffer on the returned byte slice after use.
func (ps *ProtectedSecret) Reveal() ([]byte, error) {
	if ps == nil || len(ps.key) == 0 {
		return nil, fmt.Errorf("secret unavailable or destroyed")
	}
	return libsecsecrets.DecryptBytes(context.Background(), ps.encrypted, ps.key)
}

// Destroy zeros all cryptographic keys and encrypted payloads in RAM.
func (ps *ProtectedSecret) Destroy() {
	if ps == nil {
		return
	}
	if len(ps.key) > 0 {
		libsecsecrets.ZeroBuffer(ps.key)
		ps.key = nil
	}
	if len(ps.encrypted) > 0 {
		libsecsecrets.ZeroBuffer(ps.encrypted)
		ps.encrypted = nil
	}
}

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

func getPasswordSecret(opts *rsyncopts.Options, urlPass string) (*ProtectedSecret, error) {
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

	sec, err := NewProtectedSecret(rawPass)
	if err != nil {
		return nil, fmt.Errorf("creating protected secret handle: %v", err)
	}
	return sec, nil
}

func generateAuthHashSecret(sec *ProtectedSecret, challenge string) (string, error) {
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
	sec, err := NewProtectedSecret(password)
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

