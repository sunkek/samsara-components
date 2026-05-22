package redis

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTempPEM writes the given DER bytes as a PEM block of type pemType to a
// temp file inside dir and returns its absolute path.
func writeTempPEM(t *testing.T, dir, name, pemType string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp PEM: %v", err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: pemType, Bytes: der}); err != nil {
		t.Fatalf("encode PEM: %v", err)
	}
	return path
}

// genSelfSignedCA creates a self-signed CA cert + key in dir.
// Returns (caPath, certPath, keyPath).
func genSelfSignedCA(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	caPath = writeTempPEM(t, dir, "ca.pem", "CERTIFICATE", der)
	certPath = caPath // self-signed serves as client cert too
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath = writeTempPEM(t, dir, "key.pem", "EC PRIVATE KEY", keyDER)
	return
}

func TestTLSConfig_Disabled(t *testing.T) {
	cfg := Config{TLS: false, TLSCAFile: "/does/not/exist"}
	tc, err := cfg.tlsConfig()
	if err != nil {
		t.Fatalf("expected no error when TLS disabled, got %v", err)
	}
	if tc != nil {
		t.Fatalf("expected nil *tls.Config when TLS disabled, got %#v", tc)
	}
}

func TestTLSConfig_CALoaded(t *testing.T) {
	dir := t.TempDir()
	caPath, _, _ := genSelfSignedCA(t, dir)

	cfg := Config{TLS: true, Host: "example.com", TLSCAFile: caPath}
	tc, err := cfg.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if tc.RootCAs == nil {
		t.Fatal("expected RootCAs to be set")
	}
}

func TestTLSConfig_BadCAPath(t *testing.T) {
	cfg := Config{TLS: true, TLSCAFile: "/no/such/file.pem"}
	_, err := cfg.tlsConfig()
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if !strings.Contains(err.Error(), "/no/such/file.pem") {
		t.Fatalf("error should mention path, got %v", err)
	}
}

func TestTLSConfig_BadCAContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := Config{TLS: true, TLSCAFile: path}
	_, err := cfg.tlsConfig()
	if err == nil {
		t.Fatal("expected error for non-PEM CA file")
	}
}

func TestTLSConfig_MismatchedCertKey(t *testing.T) {
	cases := []Config{
		{TLS: true, TLSCertFile: "/x.pem"},
		{TLS: true, TLSKeyFile: "/y.pem"},
	}
	for i, cfg := range cases {
		if _, err := cfg.tlsConfig(); err == nil {
			t.Fatalf("case %d: expected error for half-set cert/key, got nil", i)
		}
	}
}

func TestTLSConfig_ClientCertLoaded(t *testing.T) {
	dir := t.TempDir()
	_, certPath, keyPath := genSelfSignedCA(t, dir)
	cfg := Config{TLS: true, Host: "x", TLSCertFile: certPath, TLSKeyFile: keyPath}
	tc, err := cfg.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if len(tc.Certificates) != 1 {
		t.Fatalf("expected 1 client cert, got %d", len(tc.Certificates))
	}
}

func TestTLSConfig_MinVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    uint16
		wantErr bool
	}{
		{"", tls.VersionTLS12, false},
		{"1.2", tls.VersionTLS12, false},
		{"1.3", tls.VersionTLS13, false},
		{"garbage", 0, true},
	}
	for _, c := range cases {
		cfg := Config{TLS: true, Host: "x", TLSMinVersion: c.in}
		tc, err := cfg.tlsConfig()
		if c.wantErr {
			if err == nil {
				t.Fatalf("min %q: expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("min %q: unexpected error: %v", c.in, err)
		}
		if tc.MinVersion != c.want {
			t.Fatalf("min %q: got 0x%x want 0x%x", c.in, tc.MinVersion, c.want)
		}
	}
}

func TestTLSConfig_ServerNameDefaultsToHost(t *testing.T) {
	cfg := Config{TLS: true, Host: "rag_redis"}
	tc, err := cfg.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if tc.ServerName != "rag_redis" {
		t.Fatalf("ServerName: got %q want %q", tc.ServerName, "rag_redis")
	}
}

func TestTLSConfig_ServerNameOverride(t *testing.T) {
	cfg := Config{TLS: true, Host: "rag_redis", TLSServerName: "redis.internal"}
	tc, err := cfg.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if tc.ServerName != "redis.internal" {
		t.Fatalf("ServerName: got %q want %q", tc.ServerName, "redis.internal")
	}
}

func TestTLSConfig_InsecureSkipVerify(t *testing.T) {
	cfg := Config{TLS: true, Host: "x", TLSInsecureSkipVerify: true}
	tc, err := cfg.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if !tc.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=true")
	}
}
