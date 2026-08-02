package rpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
)

func TestService_CheckPermissionHonorsDecisionsPresetsAndOwnerRules(t *testing.T) {
	operation := permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionUpdate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
	}

	deny := NewServiceWithOptions(nil, ServiceOptions{PermissionPolicy: permissions.Policy{
		Default: permissions.DecisionDeny,
	}})
	require.Equal(t, codes.PermissionDenied, status.Code(deny.checkPermission(context.Background(), operation)))

	ask := NewServiceWithOptions(nil, ServiceOptions{PermissionPolicy: permissions.Policy{
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{
			permissions.SurfaceKindRPC: permissions.DecisionAsk,
		},
	}})
	require.Equal(t, codes.FailedPrecondition, status.Code(ask.checkPermission(context.Background(), operation)))

	allowOwnerOperation := NewServiceWithOptions(nil, ServiceOptions{PermissionPolicy: permissions.Policy{
		Rules: []permissions.Rule{{
			Name:       "allow RPC configuration",
			ActorKinds: []permissions.ActorKind{permissions.ActorRPCClient},
			Resources:  []permissions.Resource{permissions.ResourceConfiguration},
			Decision:   permissions.DecisionAllow,
		}},
	}})
	require.NoError(t, allowOwnerOperation.checkPermission(context.Background(), permissions.Operation{
		Resource: permissions.ResourceConfiguration, Action: permissions.ActionUpdate, OwnerRequired: true,
	}))
	fullAccess := NewServiceWithOptions(nil, ServiceOptions{PermissionPolicy: permissions.Policy{
		Preset: permissions.PresetFullAccess, Default: permissions.DecisionDeny,
	}})
	require.NoError(t, fullAccess.checkPermission(context.Background(), permissions.Operation{
		Resource: permissions.ResourceConfiguration, Action: permissions.ActionUpdate, OwnerRequired: true,
	}))
	require.Equal(t, codes.Internal, status.Code((*Service)(nil).checkPermission(context.Background(), operation)))

	localContext := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})
	local := NewServiceWithOptions(nil, ServiceOptions{PermissionPolicy: permissions.Policy{
		Rules: []permissions.Rule{{
			Name: "allow local", ActorKinds: []permissions.ActorKind{permissions.ActorLocalOwner}, Decision: permissions.DecisionAllow,
		}},
	}})
	require.NoError(t, local.checkPermission(localContext, operation))
}

func TestService_EnforcementPreventsModelAndSessionMutations(t *testing.T) {
	stub := &agentstub.AgentServiceStub{}
	service := NewServiceWithOptions(stub, ServiceOptions{PermissionPolicy: deniedRPCPolicy()})

	_, err := service.SelectModel(context.Background(), &morphpb.SelectModelRequest{Provider: "openai", Id: "gpt-test"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.SelectedModelID)

	_, err = service.SetProviderAPIKey(context.Background(), &morphpb.SetProviderAPIKeyRequest{
		Provider: "openai", ApiKey: "secret",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.ProviderAPIKeyID)
	require.Empty(t, stub.ProviderAPIKey)

	_, err = service.Create(context.Background(), &morphpb.CreateSessionRequest{Id: "session-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.CreatedSessionID)

	_, err = service.Use(context.Background(), &morphpb.UseSessionRequest{Id: "session-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.UsedSessionID)

	_, err = service.Archive(context.Background(), &morphpb.ArchiveSessionRequest{Id: "session-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.ArchivedSessionID)

	_, err = service.Unarchive(context.Background(), &morphpb.UnarchiveSessionRequest{Id: "session-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.UnarchivedSessionID)

	_, err = service.Rename(context.Background(), &morphpb.RenameSessionRequest{Id: "session-1", Title: "blocked"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.RenamedSessionID)
}

func TestService_EnforcementPreventsGatewayMutations(t *testing.T) {
	stub := &agentstub.AgentServiceStub{}
	runtime := &gatewayRuntimeStub{status: gateway.Status{State: gateway.StateStopped}}
	gatewayConfig := config.NewDefaultConfig().Gateway
	gatewayConfig.Enabled = true
	service := NewServiceWithOptions(stub, ServiceOptions{
		GatewayConfig: gatewayConfig, GatewayRuntime: runtime, PermissionPolicy: deniedRPCPolicy(),
	})

	_, err := service.Start(context.Background(), &morphpb.StartGatewayRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, runtime.started)

	_, err = service.Stop(context.Background(), &morphpb.StopGatewayRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, runtime.stopped)

	_, err = service.Restart(context.Background(), &morphpb.RestartGatewayRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, runtime.started)
	require.False(t, runtime.stopped)

	_, err = service.ApprovePairing(context.Background(), &morphpb.ApproveGatewayPairingRequest{
		Source: "telegram", Code: "code",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.PairingSource)
	require.Empty(t, stub.PairingCode)

	_, err = service.RevokePairing(context.Background(), &morphpb.RevokeGatewayPairingRequest{
		Source: "telegram", SenderId: "123",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.RevokedPairingSource)
	require.Empty(t, stub.RevokedPairingSender)

	_, err = service.ClearPendingPairings(context.Background(), &morphpb.ClearPendingGatewayPairingsRequest{
		Source: "telegram",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, stub.ClearedPairingSource)
}

func TestAutomationService_EnforcementPreventsMutations(t *testing.T) {
	api := &automationAPIStub{}
	service := NewAutomationService(NewServiceWithOptions(nil, ServiceOptions{
		Automation: api, PermissionPolicy: deniedRPCPolicy(),
	}))

	_, err := service.AddJob(context.Background(), &morphpb.AddAutomationJobRequest{Name: "blocked"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, api.added.Name)

	_, err = service.UpdateJob(context.Background(), &morphpb.UpdateAutomationJobRequest{Id: testRPCAutomationJobID})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, api.patch.ID)

	_, err = service.RemoveJob(context.Background(), &morphpb.RemoveAutomationJobRequest{Id: testRPCAutomationJobID})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, api.removedID)

	_, err = service.RunJob(context.Background(), &morphpb.RunAutomationJobRequest{Id: testRPCAutomationJobID})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, api.runID)
}

func deniedRPCPolicy() permissions.Policy {
	return permissions.Policy{
		Default:             permissions.DecisionDeny,
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{},
	}
}
