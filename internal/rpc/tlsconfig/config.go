package tlsconfig

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/wandxy/morph/internal/config"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func ServerCredentials(cfg config.AuthTLSConfig) (credentials.TransportCredentials, error) {
	if cfg.Mode == "" || cfg.Mode == config.AuthTLSDisabled {
		return nil, nil
	}
	certificate, err := tls.LoadX509KeyPair(cfg.ServerCertificate, cfg.ServerKey)
	if err != nil {
		return nil, fmt.Errorf("load RPC server certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   minimumVersion(cfg.MinimumVersion),
	}
	if cfg.Mode == config.AuthTLSMutual {
		clientRoots, err := loadCertificatePool(cfg.ClientCA)
		if err != nil {
			return nil, fmt.Errorf("load RPC client CA: %w", err)
		}
		tlsConfig.ClientCAs = clientRoots
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return credentials.NewTLS(tlsConfig), nil
}

func ClientCredentials(
	cfg config.AuthTLSConfig,
	address string,
) (credentials.TransportCredentials, string, error) {
	for _, character := range address {
		if character < ' ' || character == 0x7f {
			return nil, "", errors.New("RPC address contains an invalid control character")
		}
	}
	if cfg.Mode == "" || cfg.Mode == config.AuthTLSDisabled {
		host := address
		if parsedHost, _, err := net.SplitHostPort(address); err == nil {
			host = parsedHost
		}
		host = strings.TrimSpace(strings.Trim(host, "[]"))
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") &&
			(ip == nil || !ip.IsLoopback()) {
			return nil, "", errors.New("RPC TLS is required for non-loopback addresses")
		}
		return insecure.NewCredentials(), "", nil
	}
	tlsConfig := &tls.Config{
		MinVersion: minimumVersion(cfg.MinimumVersion),
		ServerName: cfg.ServerName,
	}
	if tlsConfig.ServerName == "" {
		host, _, err := net.SplitHostPort(address)
		if err == nil {
			tlsConfig.ServerName = host
		} else {
			tlsConfig.ServerName = address
		}
	}
	if cfg.ServerCA != "" {
		serverRoots, err := loadCertificatePool(cfg.ServerCA)
		if err != nil {
			return nil, "", fmt.Errorf("load RPC server CA: %w", err)
		}
		tlsConfig.RootCAs = serverRoots
	}
	var thumbprint string
	if cfg.Mode == config.AuthTLSMutual {
		if cfg.ClientCertificate == "" || cfg.ClientKey == "" {
			return nil, "", errors.New("RPC mutual TLS client certificate and key are required")
		}
		certificate, err := tls.LoadX509KeyPair(cfg.ClientCertificate, cfg.ClientKey)
		if err != nil {
			return nil, "", fmt.Errorf("load RPC client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
		if len(certificate.Certificate) == 0 {
			return nil, "", errors.New("RPC client certificate chain is empty")
		}
		digest := sha256.Sum256(certificate.Certificate[0])
		thumbprint = base64.RawURLEncoding.EncodeToString(digest[:])
	}

	return credentials.NewTLS(tlsConfig), thumbprint, nil
}

func loadCertificatePool(path string) (*x509.CertPool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(body) {
		return nil, errors.New("certificate file contains no valid certificates")
	}

	return pool, nil
}

func minimumVersion(version string) uint16 {
	if version == "1.2" {
		return tls.VersionTLS12
	}

	return tls.VersionTLS13
}
