package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/state/storememory"
)

func TestPermissionService_ListsInspectsResolvesAndRevokes(t *testing.T) {
	store := storememory.NewStore()
	approvalService, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
	require.NoError(t, err)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	request := permissions.ApprovalRequest{
		ID: "approval_rpc", Fingerprint: "fingerprint",
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner_rpc"},
		Surface: permissions.SurfaceTUI, SurfaceKind: permissions.SurfaceKindLocal,
		Tool: "run_command", Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
		Effects: []permissions.Effect{permissions.EffectExecution}, Summary: "run command",
		Operations: []string{"run_command · execute process"},
		Status:     permissions.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err = store.CreateApprovalRequest(context.Background(), request)
	require.NoError(t, err)
	service := NewPermissionService(&Service{approvalService: approvalService})
	operatorCtx := permissionOperatorContext("tui", "127.0.0.1")

	listed, err := service.ListRequests(context.Background(), &morphpb.ListPermissionRequestsRequest{Status: "pending"})
	require.NoError(t, err)
	require.Len(t, listed.GetRequests(), 1)
	require.Equal(t, []string{"execution"}, listed.GetRequests()[0].GetEffects())
	require.Equal(t, request.Operations, listed.GetRequests()[0].GetOperations())
	got, err := service.GetRequest(context.Background(), &morphpb.GetPermissionRequestRequest{Id: request.ID})
	require.NoError(t, err)
	require.Equal(t, request.Summary, got.GetRequest().GetSummary())

	resolved, err := service.ResolveRequest(operatorCtx, &morphpb.ResolvePermissionRequestRequest{
		Id: request.ID, Approved: true, Scope: "session",
	})
	require.NoError(t, err)
	require.Equal(t, "approved", resolved.GetRequest().GetStatus())
	grants, err := service.ListGrants(context.Background(), &morphpb.ListPermissionGrantsRequest{Status: "active"})
	require.NoError(t, err)
	require.Len(t, grants.GetGrants(), 1)
	require.Equal(t, request.Operations, grants.GetGrants()[0].GetOperations())
	require.Empty(t, grants.GetGrants()[0].GetActorId())
	require.Empty(t, grants.GetGrants()[0].GetFingerprint())
	inspected, err := service.GetGrant(operatorCtx, &morphpb.GetPermissionGrantRequest{
		Id: grants.GetGrants()[0].GetId(),
	})
	require.NoError(t, err)
	require.Equal(t, "owner_rpc", inspected.GetGrant().GetActorId())
	require.Equal(t, request.Fingerprint, inspected.GetGrant().GetFingerprint())
	require.Equal(t, request.Operations, inspected.GetGrant().GetOperations())
	inspected, err = service.GetGrant(operatorCtx, &morphpb.GetPermissionGrantRequest{Id: request.ID})
	require.NoError(t, err)
	require.Equal(t, grants.GetGrants()[0].GetId(), inspected.GetGrant().GetId())
	revoked, err := service.RevokeGrant(operatorCtx, &morphpb.RevokePermissionGrantRequest{
		Id: grants.GetGrants()[0].GetId(),
	})
	require.NoError(t, err)
	require.Equal(t, "revoked", revoked.GetGrant().GetStatus())
	require.Empty(t, revoked.GetGrant().GetActorId())
	require.Empty(t, revoked.GetGrant().GetFingerprint())
	preview, err := service.Prune(operatorCtx, &morphpb.PrunePermissionApprovalsRequest{DryRun: true})
	require.NoError(t, err)
	require.True(t, preview.GetDryRun())
	deletedGrant, err := service.DeleteRecord(operatorCtx, &morphpb.DeletePermissionRecordRequest{
		Id: revoked.GetGrant().GetId(),
	})
	require.NoError(t, err)
	require.Equal(t, "grant", deletedGrant.GetKind())
	deletedRequest, err := service.DeleteRecord(operatorCtx, &morphpb.DeletePermissionRecordRequest{Id: request.ID})
	require.NoError(t, err)
	require.Equal(t, "request", deletedRequest.GetKind())
}

func TestPermissionService_FailsClosedWhenUnavailableOrInvalid(t *testing.T) {
	service := NewPermissionService(nil)
	_, err := service.ListRequests(context.Background(), &morphpb.ListPermissionRequestsRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err))

	store := storememory.NewStore()
	approvalService, createErr := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
	require.NoError(t, createErr)
	service = NewPermissionService(&Service{approvalService: approvalService})
	operatorCtx := permissionOperatorContext("cli", "127.0.0.1")
	_, err = service.GetRequest(context.Background(), &morphpb.GetPermissionRequestRequest{Id: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = service.GetGrant(operatorCtx, &morphpb.GetPermissionGrantRequest{Id: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = service.ResolveRequest(operatorCtx, &morphpb.ResolvePermissionRequestRequest{
		Id: "missing", Approved: true, Scope: "once",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = service.RevokeGrant(operatorCtx, &morphpb.RevokePermissionGrantRequest{Id: "missing"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = service.DeleteRecord(operatorCtx, &morphpb.DeletePermissionRecordRequest{Id: "approval_missing"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = service.ResolveRequest(context.Background(), &morphpb.ResolvePermissionRequestRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = service.RevokeGrant(context.Background(), &morphpb.RevokePermissionGrantRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = service.GetGrant(context.Background(), &morphpb.GetPermissionGrantRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = service.DeleteRecord(context.Background(), &morphpb.DeletePermissionRecordRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = service.Prune(context.Background(), &morphpb.PrunePermissionApprovalsRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	spoofedCtx := permissionOperatorContext("tui", "192.0.2.1")
	_, err = service.ResolveRequest(spoofedCtx, &morphpb.ResolvePermissionRequestRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func permissionOperatorContext(surface, address string) context.Context {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-morph-permission-surface",
		surface,
	))
	source := surface
	if !net.ParseIP(address).IsLoopback() {
		source = "rpc"
	}
	ctx = withTestPrincipal(ctx, source)
	return peer.NewContext(ctx, &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(address), Port: 50051},
	})
}
