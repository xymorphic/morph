package readiness

import (
	"net"
	"os"
	"path/filepath"
	"strings"

	morphauth "github.com/xymorphic/morph/internal/auth"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/credential"
	"github.com/xymorphic/morph/internal/profile"
)

func buildRPCAuthGroup(cfg *config.Config, active profile.Profile) Group {
	if cfg == nil {
		return Group{Name: "RPC auth", Checks: []Check{
			check("config", StatusFail, "config is required"),
		}}
	}

	return Group{Name: "RPC auth", Checks: []Check{
		buildRPCIdentityCheck(cfg, active),
		buildRPCTokenSourceCheck(cfg, active),
		buildRPCTransportCheck(cfg),
	}}
}

func buildRPCIdentityCheck(cfg *config.Config, active profile.Profile) Check {
	if strings.TrimSpace(cfg.Auth.Key) != "" {
		var identity morphauth.Identity
		var err error
		if morphauth.IsEncodedIdentity([]byte(cfg.Auth.Key)) {
			identity, err = morphauth.ParseIdentity([]byte(cfg.Auth.Key), 1)
		} else {
			var body []byte
			body, err = os.ReadFile(cfg.Auth.Key)
			if err == nil {
				identity, err = morphauth.ParseIdentity(body, 1)
			}
		}
		if err != nil {
			return check("identity", StatusFail, "configured identity key is invalid or unavailable")
		}
		return check("identity", StatusPass, "configured identity "+identity.ID+" is available")
	}
	if strings.TrimSpace(active.HomeDir) == "" {
		return check("identity", StatusWarn, "profile identity will be created on daemon startup")
	}
	record, found, err := credential.NewFileStore(
		filepath.Join(active.HomeDir, "auth.json"),
	).LoadMorphAuth()
	if err != nil {
		return check("identity", StatusFail, "stored profile identity is invalid")
	}
	if !found {
		return check("identity", StatusWarn, "profile identity will be created on daemon startup")
	}

	return check("identity", StatusPass, "profile identity "+record.IdentityID+" is available")
}

func buildRPCTokenSourceCheck(cfg *config.Config, active profile.Profile) Check {
	if strings.TrimSpace(cfg.Auth.Token) != "" {
		return check("token source", StatusPass, "explicit profile token is configured")
	}
	if strings.TrimSpace(active.HomeDir) != "" {
		record, found, err := credential.NewFileStore(
			filepath.Join(active.HomeDir, "auth.json"),
		).LoadMorphAuth()
		if err == nil && found && strings.TrimSpace(record.Token) != "" {
			return check("token source", StatusPass, "stored profile token is configured")
		}
	}

	return check("token source", StatusPass, "clients mint in-memory tokens from the effective identity")
}

func buildRPCTransportCheck(cfg *config.Config) Check {
	switch cfg.Auth.TLS.Mode {
	case config.AuthTLSDisabled:
		if !isReadinessLoopback(cfg.RPC.Address) {
			return check("transport", StatusFail, "TLS is required for a non-loopback RPC listener")
		}
		return check("transport", StatusPass, "plaintext RPC is limited to loopback")
	case config.AuthTLSServer:
		return check("transport", StatusPass, "server TLS is configured; certificate changes require restart")
	case config.AuthTLSMutual:
		return check("transport", StatusPass, "mutual TLS and JWT authentication are configured")
	default:
		return check("transport", StatusFail, "RPC TLS mode is invalid")
	}
}

func isReadinessLoopback(address string) bool {
	address = strings.TrimSpace(strings.Trim(address, "[]"))
	if strings.EqualFold(address, "localhost") {
		return true
	}
	ip := net.ParseIP(address)

	return ip != nil && ip.IsLoopback()
}
