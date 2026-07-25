package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthConfig_NormalizesValidatesAndRedacts(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Name = "work"
	cfg.Auth = AuthConfig{
		Key: "private-key", Token: "access-token",
		TLS: AuthTLSConfig{Mode: AuthTLSDisabled},
	}
	cfg.Normalize()
	require.Equal(t, "morph-rpc:default", cfg.Auth.Audience)
	require.Equal(t, uint64(1), cfg.Auth.Generation)
	require.Equal(t, 5*time.Minute, cfg.Auth.CLITokenTTL)
	require.Equal(t, 8*time.Hour, cfg.Auth.TUITokenTTL)
	require.NoError(t, cfg.validateAuth())

	redacted := cfg.Redacted()
	require.Equal(t, "[REDACTED]", redacted.Auth.Key)
	require.Equal(t, "[REDACTED]", redacted.Auth.Token)
	require.Equal(t, "private-key", cfg.Auth.Key)
	require.Equal(t, "access-token", cfg.Auth.Token)
}

func TestAuthConfig_RequiresTLSOutsideLoopback(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Normalize()
	cfg.RPC.Address = "0.0.0.0"
	cfg.Auth.TLS.Mode = AuthTLSDisabled
	require.EqualError(t, cfg.validateAuth(), "RPC TLS is required for non-loopback addresses")

	cfg.Auth.TLS = AuthTLSConfig{
		Mode: AuthTLSServer, ServerCertificate: "server.pem",
		ServerKey: "server-key.pem", MinimumVersion: "1.3",
	}
	require.NoError(t, cfg.validateAuth())

	cfg.Auth.TLS.Mode = AuthTLSMutual
	require.EqualError(t, cfg.validateAuth(),
		"RPC mutual TLS requires a server certificate, server key, and client CA")
}

func TestConfig_ResolvesAuthKeyAndTLSPaths(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	cfg := NewDefaultConfig()
	cfg.Auth.Key = "identity.pem"
	cfg.Auth.TLS = AuthTLSConfig{
		Mode: AuthTLSServer, ServerCertificate: "server.pem",
		ServerKey: "server-key.pem", ServerCA: "ca.pem",
		ClientCertificate: "client.pem", ClientKey: "client-key.pem",
	}
	body, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NoError(t, writeAuthTestFile(configPath, body))

	loaded, err := loadConfigFile(configPath)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "identity.pem"), loaded.Auth.Key)
	require.Equal(t, filepath.Join(directory, "server.pem"), loaded.Auth.TLS.ServerCertificate)
	require.Equal(t, filepath.Join(directory, "client-key.pem"), loaded.Auth.TLS.ClientKey)
}

func writeAuthTestFile(path string, body []byte) error {
	return os.WriteFile(path, body, 0o600)
}
