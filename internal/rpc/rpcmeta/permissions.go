package rpcmeta

import (
	"context"

	"github.com/xymorphic/morph/internal/permissions"
	"google.golang.org/grpc/metadata"
)

const (
	permissionSurfaceKey = "x-morph-permission-surface"
	permissionPresetKey  = "x-morph-permission-preset"
)

func WithOutgoingPermissionSurface(ctx context.Context, surface permissions.Surface) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !isSupportedClientSurface(surface) {
		return ctx
	}

	return metadata.AppendToOutgoingContext(ctx, permissionSurfaceKey, string(surface))
}

func PermissionSurfaceFromIncomingContext(ctx context.Context) permissions.Surface {
	if ctx == nil {
		return permissions.SurfaceRPC
	}

	values := metadata.ValueFromIncomingContext(ctx, permissionSurfaceKey)
	if len(values) == 0 {
		return permissions.SurfaceRPC
	}
	surface := permissions.Surface(values[len(values)-1])
	if !isSupportedClientSurface(surface) {
		return permissions.SurfaceRPC
	}

	return surface
}

func WithOutgoingPermissionPreset(ctx context.Context, preset permissions.Preset) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := permissions.ParsePreset(string(preset)); err != nil {
		return ctx
	}

	return metadata.AppendToOutgoingContext(ctx, permissionPresetKey, string(preset))
}

func PermissionPresetFromIncomingContext(ctx context.Context) (permissions.Preset, bool) {
	if ctx == nil {
		return "", false
	}

	values := metadata.ValueFromIncomingContext(ctx, permissionPresetKey)
	if len(values) == 0 {
		return "", false
	}
	preset, err := permissions.ParsePreset(values[len(values)-1])
	if err != nil {
		return "", false
	}

	return preset, true
}

func PermissionActorFromIncomingContext(ctx context.Context) permissions.Actor {
	if ctx != nil {
		if principal, ok := AuthenticatedPrincipal(ctx); ok {
			surface := PermissionSurfaceFromIncomingContext(ctx)
			if principal.IsRootOwner() && isSupportedClientSurface(surface) &&
				principal.Source == string(surface) {
				return permissions.Actor{Kind: permissions.ActorLocalOwner, ID: principal.OwnerID}
			}
			return permissions.Actor{Kind: permissions.ActorRPCClient, ID: principal.IdentityID}
		}
	}

	return permissions.Actor{Kind: permissions.ActorRPCClient}
}

func isSupportedClientSurface(surface permissions.Surface) bool {
	return surface == permissions.SurfaceCLI || surface == permissions.SurfaceTUI
}
