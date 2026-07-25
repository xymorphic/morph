package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/wandxy/morph/internal/profile"
)

const (
	AuthTLSDisabled = "disabled"
	AuthTLSServer   = "server"
	AuthTLSMutual   = "mutual"
)

type AuthConfig struct {
	Key               string        `yaml:"key,omitempty"`
	Generation        uint64        `yaml:"generation"`
	Token             string        `yaml:"token,omitempty"`
	Audience          string        `yaml:"audience,omitempty"`
	CLITokenTTL       time.Duration `yaml:"cliTokenTTL"`
	TUITokenTTL       time.Duration `yaml:"tuiTokenTTL"`
	MaximumTokenTTL   time.Duration `yaml:"maximumTokenTTL"`
	SessionIdleTTL    time.Duration `yaml:"sessionIdleTTL"`
	SessionMaximumTTL time.Duration `yaml:"sessionMaximumTTL"`
	MaximumTokenBytes int           `yaml:"maximumTokenBytes"`
	NonceBytes        int           `yaml:"nonceBytes"`
	TLS               AuthTLSConfig `yaml:"tls"`
}

type AuthTLSConfig struct {
	Mode              string `yaml:"mode"`
	ServerCertificate string `yaml:"serverCertificate,omitempty"`
	ServerKey         string `yaml:"serverKey,omitempty"`
	ServerCA          string `yaml:"serverCA,omitempty"`
	ClientCA          string `yaml:"clientCA,omitempty"`
	ClientCertificate string `yaml:"clientCertificate,omitempty"`
	ClientKey         string `yaml:"clientKey,omitempty"`
	ServerName        string `yaml:"serverName,omitempty"`
	MinimumVersion    string `yaml:"minimumVersion,omitempty"`
}

func (c *Config) normalizeAuth() {
	c.Auth.Key = strings.TrimSpace(c.Auth.Key)
	c.Auth.Token = strings.TrimSpace(c.Auth.Token)
	c.Auth.Audience = strings.TrimSpace(c.Auth.Audience)
	if c.Auth.Generation == 0 {
		c.Auth.Generation = 1
	}
	if c.Auth.Audience == "" {
		profileName := strings.TrimSpace(profile.Active().Name)
		if profileName == "" {
			profileName = profile.DefaultName
		}
		c.Auth.Audience = "morph-rpc:" + profileName
	}
	if c.Auth.CLITokenTTL <= 0 {
		c.Auth.CLITokenTTL = 5 * time.Minute
	}
	if c.Auth.TUITokenTTL <= 0 {
		c.Auth.TUITokenTTL = 8 * time.Hour
	}
	if c.Auth.MaximumTokenTTL <= 0 {
		c.Auth.MaximumTokenTTL = 24 * time.Hour
	}
	if c.Auth.SessionIdleTTL <= 0 {
		c.Auth.SessionIdleTTL = 15 * time.Minute
	}
	if c.Auth.SessionMaximumTTL <= 0 {
		c.Auth.SessionMaximumTTL = 24 * time.Hour
	}
	if c.Auth.MaximumTokenBytes <= 0 {
		c.Auth.MaximumTokenBytes = 16 * 1024
	}
	if c.Auth.NonceBytes <= 0 {
		c.Auth.NonceBytes = 24
	}
	c.Auth.TLS.Mode = strings.ToLower(strings.TrimSpace(c.Auth.TLS.Mode))
	if c.Auth.TLS.Mode == "" {
		c.Auth.TLS.Mode = AuthTLSDisabled
	}
	c.Auth.TLS.ServerCertificate = strings.TrimSpace(c.Auth.TLS.ServerCertificate)
	c.Auth.TLS.ServerKey = strings.TrimSpace(c.Auth.TLS.ServerKey)
	c.Auth.TLS.ServerCA = strings.TrimSpace(c.Auth.TLS.ServerCA)
	c.Auth.TLS.ClientCA = strings.TrimSpace(c.Auth.TLS.ClientCA)
	c.Auth.TLS.ClientCertificate = strings.TrimSpace(c.Auth.TLS.ClientCertificate)
	c.Auth.TLS.ClientKey = strings.TrimSpace(c.Auth.TLS.ClientKey)
	c.Auth.TLS.ServerName = strings.TrimSpace(c.Auth.TLS.ServerName)
	c.Auth.TLS.MinimumVersion = strings.ToLower(strings.TrimSpace(c.Auth.TLS.MinimumVersion))
	if c.Auth.TLS.MinimumVersion == "" {
		c.Auth.TLS.MinimumVersion = "1.3"
	}
}

func (c *Config) validateAuth() error {
	if c.Auth.Audience == "" {
		return errors.New("auth audience is required")
	}
	if c.Auth.CLITokenTTL <= 0 || c.Auth.TUITokenTTL <= 0 ||
		c.Auth.MaximumTokenTTL <= 0 || c.Auth.SessionIdleTTL <= 0 ||
		c.Auth.SessionMaximumTTL <= 0 {
		return errors.New("auth token and session lifetimes must be greater than zero")
	}
	if c.Auth.CLITokenTTL > c.Auth.MaximumTokenTTL ||
		c.Auth.TUITokenTTL > c.Auth.MaximumTokenTTL ||
		c.Auth.SessionIdleTTL > c.Auth.SessionMaximumTTL {
		return errors.New("auth default lifetimes must not exceed their configured maximum")
	}
	if c.Auth.MaximumTokenBytes < 1024 || c.Auth.MaximumTokenBytes > 1024*1024 {
		return errors.New("auth maximum token bytes must be between 1024 and 1048576")
	}
	if c.Auth.NonceBytes < 16 || c.Auth.NonceBytes > 64 {
		return errors.New("auth nonce bytes must be between 16 and 64")
	}
	switch c.Auth.TLS.Mode {
	case AuthTLSDisabled:
		if !isLoopbackRPCAddress(c.RPC.Address) {
			return errors.New("RPC TLS is required for non-loopback addresses")
		}
	case AuthTLSServer:
		if c.Auth.TLS.ServerCertificate == "" || c.Auth.TLS.ServerKey == "" {
			return errors.New("RPC server TLS requires a certificate and key")
		}
	case AuthTLSMutual:
		if c.Auth.TLS.ServerCertificate == "" || c.Auth.TLS.ServerKey == "" ||
			c.Auth.TLS.ClientCA == "" {
			return errors.New("RPC mutual TLS requires a server certificate, server key, and client CA")
		}
	default:
		return fmt.Errorf("RPC TLS mode must be one of: %s, %s, %s",
			AuthTLSDisabled, AuthTLSServer, AuthTLSMutual)
	}
	if c.Auth.TLS.ClientCertificate != "" || c.Auth.TLS.ClientKey != "" {
		if c.Auth.TLS.ClientCertificate == "" || c.Auth.TLS.ClientKey == "" {
			return errors.New("RPC client TLS requires both a certificate and key")
		}
	}
	if c.Auth.TLS.MinimumVersion != "1.2" && c.Auth.TLS.MinimumVersion != "1.3" {
		return errors.New("RPC TLS minimum version must be 1.2 or 1.3")
	}

	return nil
}

func isLoopbackRPCAddress(address string) bool {
	address = strings.TrimSpace(strings.Trim(address, "[]"))
	if strings.EqualFold(address, "localhost") {
		return true
	}
	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}

func (c *Config) Redacted() *Config {
	if c == nil {
		return nil
	}
	redacted := cloneConfig(*c)
	if redacted.Auth.Key != "" {
		redacted.Auth.Key = "[REDACTED]"
	}
	if redacted.Auth.Token != "" {
		redacted.Auth.Token = "[REDACTED]"
	}

	return &redacted
}
