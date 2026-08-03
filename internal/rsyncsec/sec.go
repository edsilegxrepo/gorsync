package rsyncsec

import (
	"context"
	"fmt"

	"github.com/edsilegxrepo/secretprotector/pkg/libsecsecrets"
)

// ProtectedSecret encapsulates sensitive credential material in RAM using AES-256-GCM
// via libsecsecrets and enforces strict zeroing of memory upon destruction.
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
// Caller MUST execute libsecsecrets.ZeroBuffer(b) on the returned byte slice after use.
func (ps *ProtectedSecret) Reveal() ([]byte, error) {
	if ps == nil || len(ps.key) == 0 {
		return nil, fmt.Errorf("secret unavailable or destroyed")
	}
	return libsecsecrets.DecryptBytes(context.Background(), ps.encrypted, ps.key)
}

// Destroy zeros all cryptographic keys and encrypted payloads in RAM via ZeroBuffer.
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
