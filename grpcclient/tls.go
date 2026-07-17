package grpcclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// transportCredentials builds client transport credentials from the TLS*
// fields of Config. Returns insecure (plaintext) credentials when c.TLS is
// false, preserving the historical default.
func (c Config) transportCredentials() (credentials.TransportCredentials, error) {
	if !c.TLS {
		return insecure.NewCredentials(), nil
	}

	cfg := &tls.Config{
		ServerName:         c.TLSServerName,
		InsecureSkipVerify: c.TLSInsecureSkipVerify, //nolint:gosec // opt-in via config
	}

	switch c.TLSMinVersion {
	case "", "1.2":
		cfg.MinVersion = tls.VersionTLS12
	case "1.3":
		cfg.MinVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("grpc-client: unsupported TLSMinVersion %q (want \"1.2\" or \"1.3\")", c.TLSMinVersion)
	}

	if c.TLSCAFile != "" {
		pem, err := os.ReadFile(c.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("grpc-client: read TLSCAFile %q: %w", c.TLSCAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("grpc-client: TLSCAFile %q: no PEM certificates found", c.TLSCAFile)
		}
		cfg.RootCAs = pool
	}

	switch {
	case c.TLSCertFile != "" && c.TLSKeyFile != "":
		cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("grpc-client: load TLS client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	case c.TLSCertFile != "" || c.TLSKeyFile != "":
		return nil, fmt.Errorf("grpc-client: TLSCertFile and TLSKeyFile must both be set or both empty")
	}

	return credentials.NewTLS(cfg), nil
}
