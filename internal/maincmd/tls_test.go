package maincmd_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/edsilegxrepo/rsync/internal/rsyncopts"
	"github.com/edsilegxrepo/rsync/internal/rsyncostest"
)

func generateTestTLSCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"gorsync test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate failed: %v", err)
	}

	certPath = filepath.Join(dir, "server.crt")
	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("os.Create certPath failed: %v", err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()

	keyPath = filepath.Join(dir, "server.key")
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("os.OpenFile keyPath failed: %v", err)
	}
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey failed: %v", err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
	keyOut.Close()

	return certPath, keyPath
}

func TestTLSOptionsParsing(t *testing.T) {
	t.Parallel()
	osenv := rsyncostest.New(t)
	opts := rsyncopts.NewOptions(osenv)

	args := []string{
		"--tls",
		"--tls-ca=/tmp/ca.crt",
		"--tls-cert=/tmp/client.crt",
		"--tls-key=/tmp/client.key",
		"--tls-insecure",
	}

	pc := rsyncopts.NewContext(opts)
	if err := pc.ParseArguments(osenv, args); err != nil {
		t.Fatalf("ParseArguments failed: %v", err)
	}
	rest := pc.GetArgs()
	if len(rest) > 0 {
		t.Fatalf("unexpected remaining args: %v", rest)
	}

	if !opts.TLS() {
		t.Errorf("expected TLS() to be true")
	}
	if opts.TLSCA() != "/tmp/ca.crt" {
		t.Errorf("expected TLSCA() /tmp/ca.crt, got %q", opts.TLSCA())
	}
	if opts.TLSCert() != "/tmp/client.crt" {
		t.Errorf("expected TLSCert() /tmp/client.crt, got %q", opts.TLSCert())
	}
	if opts.TLSKey() != "/tmp/client.key" {
		t.Errorf("expected TLSKey() /tmp/client.key, got %q", opts.TLSKey())
	}
	if !opts.TLSInsecure() {
		t.Errorf("expected TLSInsecure() to be true")
	}
}
