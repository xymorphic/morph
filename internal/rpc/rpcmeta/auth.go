package rpcmeta

import (
	"context"

	morphauth "github.com/wandxy/morph/internal/auth"
)

type authenticatedPrincipalKey struct{}

func WithAuthenticatedPrincipal(ctx context.Context, principal morphauth.Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if principal.IdentityID == "" || principal.SessionID == "" || principal.TokenID == "" {
		return ctx
	}

	return context.WithValue(ctx, authenticatedPrincipalKey{}, principal)
}

func AuthenticatedPrincipal(ctx context.Context) (morphauth.Principal, bool) {
	if ctx == nil {
		return morphauth.Principal{}, false
	}
	principal, ok := ctx.Value(authenticatedPrincipalKey{}).(morphauth.Principal)
	if !ok || principal.IdentityID == "" {
		return morphauth.Principal{}, false
	}

	return principal, true
}
