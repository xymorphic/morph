package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
)

func TestPermissionService_TranslatesApprovalLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	stub := &permissionServiceClientStub{
		request: &morphpb.PermissionApprovalRequest{
			Id: "approval_1", ActorKind: "local_owner", SurfaceKind: "local", Surface: "tui",
			Tool: "run_command", Resource: "process", Action: "execute", Effects: []string{"execution"},
			Operations: []string{"run_command · execute process"},
			Status:     "pending", CreatedAt: timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(time.Minute)),
		},
		grant: &morphpb.PermissionGrant{
			Id: "grant_1", RequestId: "approval_1", ActorKind: "automation", ActorId: "auto_1",
			Scope: "session", Status: "active", Fingerprint: "fingerprint_1",
			Operations: []string{"run_command · execute process"},
			CreatedAt:  timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(time.Hour)),
		},
	}
	service := NewPermissionService(stub)
	requests, err := service.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{
		Status: permissions.ApprovalPending, Limit: 5, Offset: 10,
	})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, []permissions.Effect{permissions.EffectExecution}, requests[0].Effects)
	require.Equal(t, []string{"run_command · execute process"}, requests[0].Operations)
	require.Equal(t, int32(5), stub.listRequest.GetLimit())
	require.Equal(t, int32(10), stub.listRequest.GetOffset())
	request, ok, err := service.GetApprovalRequest(context.Background(), "approval_1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, now, request.CreatedAt)
	_, err = service.ResolveApprovalRequest(context.Background(), "approval_1", true, permissions.GrantSession)
	require.NoError(t, err)
	require.Equal(t, "session", stub.resolveRequest.GetScope())
	grants, err := service.ListApprovalGrants(context.Background(), permissions.GrantQuery{Status: permissions.GrantActive})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, []string{"run_command · execute process"}, grants[0].Operations)
	inspected, ok, err := service.GetApprovalGrant(context.Background(), "grant_1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, permissions.Actor{Kind: permissions.ActorAutomation, ID: "auto_1"}, inspected.Actor)
	require.Equal(t, "fingerprint_1", inspected.Fingerprint)
	require.Equal(t, "grant_1", stub.getGrantRequest.GetId())
	grant, err := service.RevokeApprovalGrant(context.Background(), "grant_1")
	require.NoError(t, err)
	require.Equal(t, "grant_1", grant.ID)
	require.Equal(t, "grant_1", stub.revokeRequest.GetId())
	deleted, err := service.DeleteApprovalRecord(context.Background(), "approval_1")
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalDeleteResult{
		ID: "approval_1", Kind: permissions.ApprovalRecordRequest, LinkedGrantID: "grant_1",
	}, deleted)
	require.Equal(t, "approval_1", stub.deleteRequest.GetId())
	pruned, err := service.PruneApprovals(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, int64(2), pruned.Requests)
	require.True(t, pruned.DryRun)
}

func TestPermissionService_PropagatesRPCFailuresAndMissingValues(t *testing.T) {
	expected := errors.New("rpc failed")
	service := NewPermissionService(&permissionServiceClientStub{err: expected})
	_, err := service.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
	require.ErrorIs(t, err, expected)
	_, _, err = service.GetApprovalRequest(context.Background(), "id")
	require.ErrorIs(t, err, expected)
	_, err = service.ResolveApprovalRequest(context.Background(), "id", false, "")
	require.ErrorIs(t, err, expected)
	_, err = service.ListApprovalGrants(context.Background(), permissions.GrantQuery{})
	require.ErrorIs(t, err, expected)
	_, _, err = service.GetApprovalGrant(context.Background(), "id")
	require.ErrorIs(t, err, expected)
	_, err = service.RevokeApprovalGrant(context.Background(), "id")
	require.ErrorIs(t, err, expected)
	_, err = service.DeleteApprovalRecord(context.Background(), "id")
	require.ErrorIs(t, err, expected)
	_, err = service.PruneApprovals(context.Background(), true)
	require.ErrorIs(t, err, expected)

	service = NewPermissionService(&permissionServiceClientStub{
		err: status.Error(codes.NotFound, "not found"),
	})
	request, ok, err := service.GetApprovalRequest(context.Background(), "id")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, request.ID)
	grant, ok, err := service.GetApprovalGrant(context.Background(), "id")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, grant.ID)

	service = NewPermissionService(&permissionServiceClientStub{})
	request, ok, err = service.GetApprovalRequest(context.Background(), "id")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, request.ID)
	grant, ok, err = service.GetApprovalGrant(context.Background(), "id")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, grant.ID)
}

type permissionServiceClientStub struct {
	request         *morphpb.PermissionApprovalRequest
	grant           *morphpb.PermissionGrant
	err             error
	listRequest     *morphpb.ListPermissionRequestsRequest
	resolveRequest  *morphpb.ResolvePermissionRequestRequest
	getGrantRequest *morphpb.GetPermissionGrantRequest
	revokeRequest   *morphpb.RevokePermissionGrantRequest
	deleteRequest   *morphpb.DeletePermissionRecordRequest
}

func (s *permissionServiceClientStub) ListRequests(
	_ context.Context,
	req *morphpb.ListPermissionRequestsRequest,
	_ ...grpc.CallOption,
) (*morphpb.ListPermissionRequestsResponse, error) {
	s.listRequest = req
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.ListPermissionRequestsResponse{Requests: []*morphpb.PermissionApprovalRequest{s.request}}, nil
}

func (s *permissionServiceClientStub) GetRequest(
	context.Context,
	*morphpb.GetPermissionRequestRequest,
	...grpc.CallOption,
) (*morphpb.GetPermissionRequestResponse, error) {
	if s.err != nil {
		return nil, s.err
	}

	return &morphpb.GetPermissionRequestResponse{Request: s.request}, nil
}

func (s *permissionServiceClientStub) ResolveRequest(
	_ context.Context,
	req *morphpb.ResolvePermissionRequestRequest,
	_ ...grpc.CallOption,
) (*morphpb.ResolvePermissionRequestResponse, error) {
	s.resolveRequest = req
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.ResolvePermissionRequestResponse{Request: s.request}, nil
}

func (s *permissionServiceClientStub) ListGrants(
	context.Context,
	*morphpb.ListPermissionGrantsRequest,
	...grpc.CallOption,
) (*morphpb.ListPermissionGrantsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}

	return &morphpb.ListPermissionGrantsResponse{Grants: []*morphpb.PermissionGrant{s.grant}}, nil
}

func (s *permissionServiceClientStub) GetGrant(
	_ context.Context,
	req *morphpb.GetPermissionGrantRequest,
	_ ...grpc.CallOption,
) (*morphpb.GetPermissionGrantResponse, error) {
	s.getGrantRequest = req
	if s.err != nil {
		return nil, s.err
	}

	return &morphpb.GetPermissionGrantResponse{Grant: s.grant}, nil
}

func (s *permissionServiceClientStub) RevokeGrant(
	_ context.Context,
	req *morphpb.RevokePermissionGrantRequest,
	_ ...grpc.CallOption,
) (*morphpb.RevokePermissionGrantResponse, error) {
	s.revokeRequest = req
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.RevokePermissionGrantResponse{Grant: s.grant}, nil
}

func (s *permissionServiceClientStub) DeleteRecord(
	_ context.Context,
	req *morphpb.DeletePermissionRecordRequest,
	_ ...grpc.CallOption,
) (*morphpb.DeletePermissionRecordResponse, error) {
	s.deleteRequest = req
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.DeletePermissionRecordResponse{
		Id: req.GetId(), Kind: "request", LinkedGrantId: "grant_1",
	}, nil
}

func (s *permissionServiceClientStub) Prune(
	_ context.Context,
	req *morphpb.PrunePermissionApprovalsRequest,
	_ ...grpc.CallOption,
) (*morphpb.PrunePermissionApprovalsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}

	return &morphpb.PrunePermissionApprovalsResponse{Requests: 2, Grants: 1, DryRun: req.GetDryRun()}, nil
}
