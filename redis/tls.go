package redis

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// tlsConfig builds a *tls.Config from the TLS_* fields of Config.
// Returns (nil, nil) when c.TLS is false.
func (c Config) tlsConfig() (*tls.Config, error) {
	if !c.TLS {
		return nil, nil
	}

	cfg := &tls.Config{
		InsecureSkipVerify: c.TLSInsecureSkipVerify, //nolint:gosec // opt-in via config
	}

	switch c.TLSMinVersion {
	case "", "1.2":
		cfg.MinVersion = tls.VersionTLS12
	case "1.3":
		cfg.MinVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("redis: unsupported TLSMinVersion %q (want \"1.2\" or \"1.3\")", c.TLSMinVersion)
	}

	if c.TLSServerName != "" {
		cfg.ServerName = c.TLSServerName
	} else {
		cfg.ServerName = c.Host
	}

	if c.TLSCAFile != "" {
		pem, err := os.ReadFile(c.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("redis: read TLSCAFile %q: %w", c.TLSCAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("redis: TLSCAFile %q: no PEM certificates found", c.TLSCAFile)
		}
		cfg.RootCAs = pool
	}

	switch {
	case c.TLSCertFile != "" && c.TLSKeyFile != "":
		cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("redis: load TLS client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	case c.TLSCertFile != "" || c.TLSKeyFile != "":
		return nil, fmt.Errorf("redis: TLSCertFile and TLSKeyFile must both be set or both empty")
	}

	return cfg, nil
}
