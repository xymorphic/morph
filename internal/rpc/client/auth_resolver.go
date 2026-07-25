package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/profile"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type authResolver struct {
	options Options

	tokenMu       sync.Mutex
	tokenResolved bool
	token         string
	claims        morphauth.AccessClaims
	identity      morphauth.Identity
	explicit      bool
	tokenErr      error
	lastUse       time.Time

	activationMu sync.Mutex
	active       atomic.Bool
	closing      atomic.Bool
	authClient   morphpb.AuthServiceClient
}

func newAuthResolver(options Options) *authResolver {
	return &authResolver{options: options}
}

func OptionsWithConfigAuth(options Options, cfg *config.Config) Options {
	if cfg == nil {
		return options
	}
	options.AuthToken = cfg.Auth.Token
	options.AuthKey = []byte(cfg.Auth.Key)
	options.AuthIdentityGeneration = cfg.Auth.Generation
	options.AuthAudience = cfg.Auth.Audience
	options.AuthTLS = cfg.Auth.TLS
	options.AuthNonceBytes = cfg.Auth.NonceBytes
	options.AuthSessionIdleTTL = cfg.Auth.SessionIdleTTL
	if options.PermissionSurface == "tui" {
		options.AuthTokenTTL = cfg.Auth.TUITokenTTL
	} else {
		options.AuthTokenTTL = cfg.Auth.CLITokenTTL
	}

	return options
}

func (r *authResolver) setConnection(connection grpc.ClientConnInterface) {
	r.authClient = morphpb.NewAuthServiceClient(connection)
}

func (r *authResolver) getToken() (string, error) {
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()
	if !r.tokenResolved {
		r.resolveToken()
		r.tokenResolved = true
	}
	if r.shouldRefreshIdleSession() {
		sessionID, err := randomClientID()
		if err != nil {
			r.tokenErr = err
		} else {
			r.token, r.claims, r.tokenErr = r.signAutomaticToken(r.identity, sessionID)
			if r.tokenErr == nil {
				r.active.Store(false)
			}
		}
	}
	if r.tokenErr == nil && r.shouldRenew() {
		r.token, r.claims, r.tokenErr = r.signAutomaticToken(r.identity, r.claims.SessionID)
		if r.tokenErr == nil {
			r.active.Store(false)
		}
	}
	return r.token, r.tokenErr
}

func (r *authResolver) resolveToken() {
	if token := strings.TrimSpace(r.options.AuthToken); token != "" {
		r.token = token
		r.explicit = true
		return
	}
	var identity morphauth.Identity
	var err error
	if len(r.options.AuthKey) > 0 {
		key := r.options.AuthKey
		if !morphauth.IsEncodedIdentity(key) {
			key, err = os.ReadFile(strings.TrimSpace(string(key)))
			if err != nil {
				r.tokenErr = err
				return
			}
		}
		generation := r.options.AuthIdentityGeneration
		if generation == 0 {
			generation = 1
		}
		identity, err = morphauth.ParseIdentity(key, generation)
	} else {
		active := profile.Active()
		if strings.TrimSpace(active.HomeDir) == "" {
			r.tokenErr = errors.New("active profile home is required for RPC authentication")
			return
		}
		store := credential.NewFileStore(filepath.Join(active.HomeDir, "auth.json"))
		record, found, loadErr := store.LoadMorphAuth()
		if loadErr != nil {
			r.tokenErr = loadErr
			return
		}
		if found && strings.TrimSpace(record.Token) != "" {
			r.token = strings.TrimSpace(record.Token)
			r.explicit = true
			return
		}
		identity, err = store.LoadOrCreateIdentity()
	}
	if err != nil {
		r.tokenErr = err
		return
	}
	sessionID, err := randomClientID()
	if err != nil {
		r.tokenErr = err
		return
	}
	r.identity = identity
	r.token, r.claims, r.tokenErr = r.signAutomaticToken(identity, sessionID)
}

func (r *authResolver) signAutomaticToken(
	identity morphauth.Identity,
	sessionID string,
) (string, morphauth.AccessClaims, error) {
	active := profile.Active()
	audience := strings.TrimSpace(r.options.AuthAudience)
	if audience == "" {
		audience = "morph-rpc:" + active.Name
	}
	ttl := r.options.AuthTokenTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
		if r.options.PermissionSurface == "tui" {
			ttl = 8 * time.Hour
		}
	}
	ownerID := strings.TrimSpace(r.options.AuthOwnerID)
	if ownerID == "" && strings.HasPrefix(audience, "morph-rpc:") {
		ownerID = strings.TrimSpace(strings.TrimPrefix(audience, "morph-rpc:"))
	}
	if ownerID == "" {
		ownerID = strings.TrimSpace(active.Name)
	}
	if ownerID == "" {
		ownerID = identity.ID
	}
	source := string(r.options.PermissionSurface)
	if source == "" {
		source = "rpc"
	}
	services, methods := r.getAutomaticTokenScopes()

	return morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience:              audience,
		Subject:               identity.ID,
		SessionID:             sessionID,
		OwnerID:               ownerID,
		Source:                source,
		Roles:                 []string{morphauth.RoleOwner},
		Services:              services,
		Methods:               methods,
		TTL:                   ttl,
		NonceBytes:            r.options.AuthNonceBytes,
		AuthorizationRevision: 1,
		CertificateThumbprint: r.options.authCertificateThumbprint,
	})
}

func (r *authResolver) shouldRenew() bool {
	if r.closing.Load() || r.explicit || r.options.PermissionSurface != "tui" ||
		len(r.identity.PrivateKey) == 0 || r.claims.ExpiresAt == nil {
		return false
	}
	ttl := r.options.AuthTokenTTL
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}

	return time.Until(r.claims.ExpiresAt.Time) <= ttl/10
}

func (r *authResolver) shouldRefreshIdleSession() bool {
	if r.closing.Load() || r.explicit || !r.active.Load() || r.lastUse.IsZero() ||
		len(r.identity.PrivateKey) == 0 || r.options.AuthSessionIdleTTL <= 0 {
		return false
	}

	return time.Since(r.lastUse) >= r.options.AuthSessionIdleTTL*9/10
}

func (r *authResolver) recordUse() {
	r.tokenMu.Lock()
	r.lastUse = time.Now()
	r.tokenMu.Unlock()
}

func appendMissing(values []string, targets ...string) []string {
	for _, target := range targets {
		found := false
		for _, value := range values {
			if value == target {
				found = true
				break
			}
		}
		if !found {
			values = append(values, target)
		}
	}

	return values
}

func (r *authResolver) getAutomaticTokenScopes() ([]string, []string) {
	services := append([]string(nil), r.options.AuthServices...)
	methods := append([]string(nil), r.options.AuthMethods...)
	if len(services) == 0 && len(methods) == 0 {
		return []string{morphauth.RootScope}, nil
	}

	return services, appendMissing(methods, openSessionMethod, closeSessionMethod)
}

func (r *authResolver) prepareAuthenticatedRequest(
	ctx context.Context,
	method string,
) (string, error) {
	r.activationMu.Lock()
	defer r.activationMu.Unlock()
	if r.closing.Load() && method != closeSessionMethod {
		return "", errors.New("RPC auth resolver is closing")
	}
	token, err := r.getToken()
	if err != nil {
		return "", err
	}
	if err := r.ensureActiveLocked(ctx); err != nil {
		return "", err
	}

	return token, nil
}

func (r *authResolver) ensureActiveLocked(ctx context.Context) error {
	if r.active.Load() {
		return nil
	}
	if r.closing.Load() {
		return errors.New("RPC auth resolver is closing")
	}
	if r.authClient == nil {
		return errors.New("RPC auth client is unavailable")
	}
	source := string(r.options.PermissionSurface)
	if source == "" {
		source = "rpc"
	}
	if _, err := r.authClient.OpenSession(ctx, &morphpb.OpenAuthSessionRequest{Source: source}); err != nil {
		if status.Code(err) == codes.Unimplemented {
			return errors.New("RPC daemon does not support mandatory authentication; restart it with the current Morph version")
		}
		return err
	}
	r.active.Store(true)
	r.recordUse()

	return nil
}

func (r *authResolver) close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.activationMu.Lock()
	r.tokenMu.Lock()
	if r.explicit || r.claims.SessionID == "" ||
		r.authClient == nil {
		r.tokenMu.Unlock()
		r.activationMu.Unlock()
		return nil
	}
	r.closing.Store(true)
	r.tokenMu.Unlock()
	active := r.active.Load()
	r.activationMu.Unlock()
	if !active {
		return nil
	}

	_, err := r.authClient.CloseSession(ctx, &morphpb.CloseAuthSessionRequest{})
	if err == nil {
		r.active.Store(false)
	}
	return err
}

func randomClientID() (string, error) {
	body := make([]byte, 24)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}

	return hex.EncodeToString(body), nil
}
