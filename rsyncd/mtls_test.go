package rsyncd_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/edsilegxrepo/gorsync/rsyncd"
)

func generateCertKeyWithCN(cn string, isCA bool, parentCert *x509.Certificate, parentKey *ecdsa.PrivateKey) ([]byte, []byte, *x509.Certificate, *ecdsa.PrivateKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(24 * time.Hour)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"gorsync-test"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	if isCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
	} else {
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}

	signerCert := &template
	signerKey := priv
	if parentCert != nil && parentKey != nil {
		signerCert = parentCert
		signerKey = parentKey
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, signerCert, &priv.PublicKey, signerKey)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	parsedCert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	var certBuf, keyBuf bytes.Buffer
	if err := pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes}); err != nil {
		return nil, nil, nil, nil, err
	}

	b, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := pem.Encode(&keyBuf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}); err != nil {
		return nil, nil, nil, nil, err
	}

	return certBuf.Bytes(), keyBuf.Bytes(), parsedCert, priv, nil
}

func TestMTLSAuthenticationAndRBAC(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate Root CA
	caCertPEM, caKeyPEM, caCert, caKey, err := generateCertKeyWithCN("TestRootCA", true, nil, nil)
	if err != nil {
		t.Fatalf("failed generating Root CA: %v", err)
	}
	caPath := filepath.Join(tmpDir, "ca.crt")
	_ = os.WriteFile(caPath, caCertPEM, 0o600)
	_ = caKeyPEM

	// Generate Server Cert signed by Root CA
	srvCertPEM, srvKeyPEM, _, _, err := generateCertKeyWithCN("localhost", false, caCert, caKey)
	if err != nil {
		t.Fatalf("failed generating server cert: %v", err)
	}
	srvCertPath := filepath.Join(tmpDir, "server.crt")
	srvKeyPath := filepath.Join(tmpDir, "server.key")
	_ = os.WriteFile(srvCertPath, srvCertPEM, 0o600)
	_ = os.WriteFile(srvKeyPath, srvKeyPEM, 0o600)

	// Generate Client Cert with CN = "admin-client" signed by Root CA
	adminCertPEM, adminKeyPEM, _, _, err := generateCertKeyWithCN("admin-client", false, caCert, caKey)
	if err != nil {
		t.Fatalf("failed generating admin client cert: %v", err)
	}

	// Generate Client Cert with CN = "unauthorized-client" signed by Root CA
	unauthCertPEM, unauthKeyPEM, _, _, err := generateCertKeyWithCN("unauthorized-client", false, caCert, caKey)
	if err != nil {
		t.Fatalf("failed generating unauth client cert: %v", err)
	}

	modPath := filepath.Join(tmpDir, "mod")
	_ = os.MkdirAll(modPath, 0o755)

	srv, err := rsyncd.NewServer([]rsyncd.Module{
		{
			Name:          "adminmod",
			Path:          modPath,
			Writable:      true,
			TLSAllowedCNs: []string{"admin-client"},
		},
	}, rsyncd.WithTLSCertKeyPair(srvCertPath, srvKeyPath), rsyncd.WithTLSClientCA(caPath, true))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Serve(ctx, ln)
	}()

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	// Test 1: Valid client cert with CN = "admin-client" succeeds
	t.Run("ValidClientCN_Allowed", func(t *testing.T) {
		clientCert, err := tls.X509KeyPair(adminCertPEM, adminKeyPEM)
		if err != nil {
			t.Fatalf("failed loading client cert: %v", err)
		}

		conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			RootCAs:            caPool,
			Certificates:       []tls.Certificate{clientCert},
			ServerName:         "127.0.0.1",
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Fatalf("mTLS dial failed: %v", err)
		}
		defer conn.Close()

		// Read server greeting
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil || !bytes.HasPrefix(buf[:n], []byte("@RSYNCD:")) {
			t.Fatalf("unexpected greeting: %q, err: %v", string(buf[:n]), err)
		}

		// Send client greeting & module request
		_, _ = conn.Write([]byte("@RSYNCD: 27.0\nadminmod\n"))

		n, err = conn.Read(buf)
		if err != nil {
			t.Fatalf("read module response error: %v", err)
		}
		resp := string(buf[:n])
		if !bytes.Contains([]byte(resp), []byte("@RSYNCD: OK")) {
			t.Fatalf("expected @RSYNCD: OK, got: %q", resp)
		}
	})

	// Test 2: Client cert with CN = "unauthorized-client" rejected by RBAC
	t.Run("UnauthorizedClientCN_Rejected", func(t *testing.T) {
		clientCert, err := tls.X509KeyPair(unauthCertPEM, unauthKeyPEM)
		if err != nil {
			t.Fatalf("failed loading client cert: %v", err)
		}

		conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			RootCAs:            caPool,
			Certificates:       []tls.Certificate{clientCert},
			ServerName:         "127.0.0.1",
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Fatalf("mTLS dial failed: %v", err)
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		_, _ = conn.Write([]byte("@RSYNCD: 27.0\nadminmod\n"))

		n, _ = conn.Read(buf)
		resp := string(buf[:n])
		if !bytes.Contains([]byte(resp), []byte("@ERROR: access denied")) {
			t.Fatalf("expected access denied for unauthorized CN, got: %q", resp)
		}
	})
}

func TestMTLSMultiFactorPasswordAndCert(t *testing.T) {
	tmpDir := t.TempDir()

	caCertPEM, caKeyPEM, caCert, caKey, err := generateCertKeyWithCN("MFARootCA", true, nil, nil)
	if err != nil {
		t.Fatalf("failed generating CA: %v", err)
	}
	caPath := filepath.Join(tmpDir, "ca.crt")
	_ = os.WriteFile(caPath, caCertPEM, 0o600)
	_ = caKeyPEM

	srvCertPEM, srvKeyPEM, _, _, err := generateCertKeyWithCN("localhost", false, caCert, caKey)
	if err != nil {
		t.Fatalf("failed generating server cert: %v", err)
	}
	srvCertPath := filepath.Join(tmpDir, "server.crt")
	srvKeyPath := filepath.Join(tmpDir, "server.key")
	_ = os.WriteFile(srvCertPath, srvCertPEM, 0o600)
	_ = os.WriteFile(srvKeyPath, srvKeyPEM, 0o600)

	adminCertPEM, adminKeyPEM, _, _, err := generateCertKeyWithCN("admin-user", false, caCert, caKey)
	if err != nil {
		t.Fatalf("failed generating client cert: %v", err)
	}

	secretsPath := filepath.Join(tmpDir, "rsyncd.secrets")
	_ = os.WriteFile(secretsPath, []byte("admin:secret123\n"), 0o600)

	modPath := filepath.Join(tmpDir, "mfamod")
	_ = os.MkdirAll(modPath, 0o755)

	// Mode 3: Pass + mTLS Dual-Factor Authentication Module
	srv, err := rsyncd.NewServer([]rsyncd.Module{
		{
			Name:          "mfamodule",
			Path:          modPath,
			Writable:      true,
			AuthUsers:     []string{"admin"},
			SecretsFile:   secretsPath,
			TLSAllowedCNs: []string{"admin-user"},
		},
	}, rsyncd.WithTLSCertKeyPair(srvCertPath, srvKeyPath), rsyncd.WithTLSClientCA(caPath, true))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Serve(ctx, ln)
	}()

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	clientCert, _ := tls.X509KeyPair(adminCertPEM, adminKeyPEM)

	// Test Dual-Factor Auth: Both mTLS client cert AND password challenge required
	t.Run("Pass_And_mTLS_Both_Required", func(t *testing.T) {
		conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			RootCAs:            caPool,
			Certificates:       []tls.Certificate{clientCert},
			ServerName:         "127.0.0.1",
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Fatalf("mTLS dial failed: %v", err)
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		_, _ = conn.Write([]byte("@RSYNCD: 27.0\nmfamodule\n"))

		// Step 1: Server accepts mTLS CN and proceeds to Step 2: Password Challenge
		n, _ = conn.Read(buf)
		resp := string(buf[:n])
		if !bytes.Contains([]byte(resp), []byte("@RSYNCD: AUTHREQD")) {
			t.Fatalf("expected @RSYNCD: AUTHREQD challenge post-mTLS, got: %q", resp)
		}
	})
}
