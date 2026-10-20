package ovsdb

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"
)

// Config defines connection and TLS parameters for an OVSDB database (NB or SB).
type Config struct {
	Endpoints        []string
	CertFile         string
	KeyFile          string
	CAFile           string
	TLSConfig        *tls.Config
	InactivityCheck  time.Duration
	ReconnectTimeout time.Duration
}

// DefaultConfig returns default connection settings for local or cluster-internal access.
func DefaultConfig() Config {
	return Config{
		Endpoints:        []string{"tcp:127.0.0.1:6641"},
		InactivityCheck:  30 * time.Second,
		ReconnectTimeout: 10 * time.Second,
	}
}

// BuildTLSConfig builds a *tls.Config from cert, key, and CA files if provided.
func (c *Config) BuildTLSConfig() (*tls.Config, error) {
	if c.TLSConfig != nil {
		return c.TLSConfig, nil
	}
	if c.CertFile == "" && c.KeyFile == "" && c.CAFile == "" {
		return nil, nil
	}

	if c.CertFile == "" || c.KeyFile == "" || c.CAFile == "" {
		return nil, fmt.Errorf("tls requires cert, key, and ca files all to be specified")
	}

	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client keypair: %w", err)
	}

	caCert, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read ca file: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append ca cert to pool")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
