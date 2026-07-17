package grpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// tlsCredentials builds server transport credentials from the TLS* fields of
// Config. Returns (nil, nil) when c.TLS is false.
func (c Config) tlsCredentials() (credentials.TransportCredentials, error) {
	if !c.TLS {
		return nil, nil
	}

	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return nil, fmt.Errorf("grpc: TLS requires both TLSCertFile and TLSKeyFile")
	}
	cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpc: load TLS cert/key: %w", err)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	switch c.TLSMinVersion {
	case "", "1.2":
		cfg.MinVersion = tls.VersionTLS12
	case "1.3":
		cfg.MinVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("grpc: unsupported TLSMinVersion %q (want \"1.2\" or \"1.3\")", c.TLSMinVersion)
	}

	if c.TLSClientCAFile != "" {
		pem, err := os.ReadFile(c.TLSClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("grpc: read TLSClientCAFile %q: %w", c.TLSClientCAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("grpc: TLSClientCAFile %q: no PEM certificates found", c.TLSClientCAFile)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return credentials.NewTLS(cfg), nil
}
