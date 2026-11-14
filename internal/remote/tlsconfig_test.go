package remote_test

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

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote"
)

// clientCert writes a client certificate and its key, made for the test, and
// returns where they are.
func clientCert(t *testing.T) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "user"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// TestCertNeedsTLS checks that a client certificate is used only over a
// connection with TLS, which gives it in the handshake: without one, the
// server (SMTP above all, which takes the handshake for logging in) would
// be used without logging in at all.
func TestCertNeedsTLS(t *testing.T) {
	cert, key := clientCert(t)
	s := account.Server{Host: "example.com", Auth: account.AuthTLSCert, ClientCert: cert, ClientKey: key}
	for _, security := range []account.Security{account.SecurityTLS, account.SecurityStartTLS} {
		s.Security = security
		config, err := remote.TLSConfig(s)
		if err != nil {
			t.Errorf("%s: %v", security, err)
			continue
		}
		if len(config.Certificates) != 1 {
			t.Errorf("%s: %d certificates", security, len(config.Certificates))
		}
	}
	s.Security = account.SecurityNone
	if _, err := remote.TLSConfig(s); err == nil {
		t.Error("a client certificate was to be used without TLS")
	}
}
