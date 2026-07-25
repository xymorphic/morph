package rpcmeta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/permissions"
)

func TestPermissionSurface_RoundTripsSupportedClientSurface(t *testing.T) {
	for _, surface := range []permissions.Surface{permissions.SurfaceCLI, permissions.SurfaceTUI} {
		t.Run(string(surface), func(t *testing.T) {
			var nilContext context.Context
			outgoing := WithOutgoingPermissionSurface(nilContext, surface)
			outgoingMetadata, ok := metadata.FromOutgoingContext(outgoing)
			require.True(t, ok)
			incoming := metadata.NewIncomingContext(context.Background(), outgoingMetadata)

			require.Equal(t, surface, PermissionSurfaceFromIncomingContext(incoming))
		})
	}
}

func TestPermissionSurface_DefaultsUnsupportedOrMissingValuesToRPC(t *testing.T) {
	var nilContext context.Context
	require.Equal(t, permissions.SurfaceRPC, PermissionSurfaceFromIncomingContext(nilContext))
	require.Equal(t, permissions.SurfaceRPC, PermissionSurfaceFromIncomingContext(context.Background()))

	outgoing := WithOutgoingPermissionSurface(context.Background(), permissions.SurfaceSlack)
	_, ok := metadata.FromOutgoingContext(outgoing)
	require.False(t, ok)

	incoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		permissionSurfaceKey, string(permissions.SurfaceCLI),
		permissionSurfaceKey, string(permissions.SurfaceSlack),
	))
	require.Equal(t, permissions.SurfaceRPC, PermissionSurfaceFromIncomingContext(incoming))
}

func TestPermissionPreset_RoundTripsSupportedValue(t *testing.T) {
	var nilContext context.Context
	outgoing := WithOutgoingPermissionPreset(nilContext, permissions.PresetApproveForMe)
	outgoingMetadata, ok := metadata.FromOutgoingContext(outgoing)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), outgoingMetadata)

	preset, ok := PermissionPresetFromIncomingContext(incoming)
	require.True(t, ok)
	require.Equal(t, permissions.PresetApproveForMe, preset)

	_, ok = PermissionPresetFromIncomingContext(context.Background())
	require.False(t, ok)
	_, ok = PermissionPresetFromIncomingContext(nilContext)
	require.False(t, ok)
	invalidIncoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		permissionPresetKey, "unrestricted",
	))
	_, ok = PermissionPresetFromIncomingContext(invalidIncoming)
	require.False(t, ok)
	unchanged := WithOutgoingPermissionPreset(context.Background(), "invalid")
	_, ok = metadata.FromOutgoingContext(unchanged)
	require.False(t, ok)
}

func TestPermissionActor_RequiresMatchingAuthenticatedOwnerSource(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		expects permissions.ActorKind
	}{
		{
			name:    "authenticated TUI",
			ctx:     incomingPermissionContext(permissions.SurfaceTUI, "tui", true),
			expects: permissions.ActorLocalOwner,
		},
		{
			name:    "authenticated CLI",
			ctx:     incomingPermissionContext(permissions.SurfaceCLI, "cli", true),
			expects: permissions.ActorLocalOwner,
		},
		{
			name: "unauthenticated TUI spoof",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				permissionSurfaceKey, string(permissions.SurfaceTUI),
			)),
			expects: permissions.ActorRPCClient,
		},
		{
			name:    "source mismatch",
			ctx:     incomingPermissionContext(permissions.SurfaceTUI, "rpc", true),
			expects: permissions.ActorRPCClient,
		},
		{
			name:    "non-owner role",
			ctx:     incomingPermissionContext(permissions.SurfaceTUI, "tui", false),
			expects: permissions.ActorRPCClient,
		},
		{
			name: "delegated owner role",
			ctx: incomingPermissionContext(
				permissions.SurfaceTUI,
				"tui",
				true,
				false,
			),
			expects: permissions.ActorRPCClient,
		},
		{
			name:    "generic RPC",
			ctx:     context.Background(),
			expects: permissions.ActorRPCClient,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expects, PermissionActorFromIncomingContext(test.ctx).Kind)
		})
	}
}

func TestPermissionActor_UsesAuthenticatedPrincipalWithoutGrantingOwnerAuthority(t *testing.T) {
	var nilContext context.Context
	ctx := WithAuthenticatedPrincipal(nilContext, morphauth.Principal{
		IdentityID: "client-123", SessionID: "session", TokenID: "token",
		Roles: []string{morphauth.RoleOperator}, Source: "rpc",
	})
	actor := PermissionActorFromIncomingContext(ctx)
	require.Equal(t, permissions.Actor{Kind: permissions.ActorRPCClient, ID: "client-123"}, actor)

	unchanged := WithAuthenticatedPrincipal(context.Background(), morphauth.Principal{})
	require.Equal(t, permissions.ActorRPCClient, PermissionActorFromIncomingContext(unchanged).Kind)
	require.Equal(t, permissions.ActorRPCClient, PermissionActorFromIncomingContext(nilContext).Kind)
}

func incomingPermissionContext(
	surface permissions.Surface,
	source string,
	owner bool,
	rootAuthorization ...bool,
) context.Context {
	outgoing := WithOutgoingPermissionSurface(context.Background(), surface)
	outgoingMetadata, _ := metadata.FromOutgoingContext(outgoing)
	ctx := metadata.NewIncomingContext(context.Background(), outgoingMetadata)
	roles := []string{morphauth.RoleOperator}
	if owner {
		roles = []string{morphauth.RoleOwner}
	}
	root := owner
	if len(rootAuthorization) > 0 {
		root = rootAuthorization[0]
	}
	return WithAuthenticatedPrincipal(ctx, morphauth.Principal{
		IdentityID: "identity", OwnerID: "default", UserID: "user",
		Roles: roles, RootAuthorization: root,
		SessionID: "session", TokenID: "token", Source: source,
	})
}
