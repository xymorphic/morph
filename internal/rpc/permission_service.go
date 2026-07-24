package rpc

import (
	"context"
	"time"

	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PermissionService struct {
	morphpb.UnimplementedPermissionServiceServer
	service *Service
}

func NewPermissionService(service *Service) *PermissionService {
	return &PermissionService{service: service}
}

func (s *PermissionService) ListRequests(
	ctx context.Context,
	req *morphpb.ListPermissionRequestsRequest,
) (*morphpb.ListPermissionRequestsResponse, error) {
	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	requests, err := service.List(ctx, permissions.ApprovalQuery{
		Status: permissions.ApprovalStatus(req.GetStatus()),
		Limit:  int(req.GetLimit()),
		Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	result := &morphpb.ListPermissionRequestsResponse{Requests: make([]*morphpb.PermissionApprovalRequest, len(requests))}
	for index, request := range requests {
		result.Requests[index] = approvalRequestToProto(request)
	}

	return result, nil
}

func (s *PermissionService) GetRequest(
	ctx context.Context,
	req *morphpb.GetPermissionRequestRequest,
) (*morphpb.GetPermissionRequestResponse, error) {
	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	request, ok, err := service.Get(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "approval request not found")
	}
	return &morphpb.GetPermissionRequestResponse{Request: approvalRequestToProto(request)}, nil
}

func (s *PermissionService) ResolveRequest(
	ctx context.Context,
	req *morphpb.ResolvePermissionRequestRequest,
) (*morphpb.ResolvePermissionRequestResponse, error) {
	if err := checkInteractivePermissionOperator(ctx); err != nil {
		return nil, err
	}

	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	request, err := service.Resolve(ctx, req.GetId(), req.GetApproved(), permissions.GrantScope(req.GetScope()))
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &morphpb.ResolvePermissionRequestResponse{Request: approvalRequestToProto(request)}, nil
}

func (s *PermissionService) ListGrants(
	ctx context.Context,
	req *morphpb.ListPermissionGrantsRequest,
) (*morphpb.ListPermissionGrantsResponse, error) {
	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	grants, err := service.ListGrants(ctx, permissions.GrantQuery{
		Status: permissions.GrantStatus(req.GetStatus()),
		Limit:  int(req.GetLimit()),
		Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	result := &morphpb.ListPermissionGrantsResponse{Grants: make([]*morphpb.PermissionGrant, len(grants))}
	for index, grant := range grants {
		result.Grants[index] = approvalGrantToProto(grant)
	}

	return result, nil
}

func (s *PermissionService) GetGrant(
	ctx context.Context,
	req *morphpb.GetPermissionGrantRequest,
) (*morphpb.GetPermissionGrantResponse, error) {
	if err := checkInteractivePermissionOperator(ctx); err != nil {
		return nil, err
	}

	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	grant, ok, err := service.GetGrant(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "approval grant not found")
	}
	return &morphpb.GetPermissionGrantResponse{Grant: approvalGrantDetailsToProto(grant)}, nil
}

func (s *PermissionService) Prune(
	ctx context.Context,
	req *morphpb.PrunePermissionApprovalsRequest,
) (*morphpb.PrunePermissionApprovalsResponse, error) {
	if err := checkInteractivePermissionOperator(ctx); err != nil {
		return nil, err
	}

	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	result, err := service.Prune(ctx, req.GetDryRun())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &morphpb.PrunePermissionApprovalsResponse{
		Requests: result.Requests, Grants: result.Grants,
		RequestCutoff: permissionTimestampOrNil(result.RequestCutoff),
		GrantCutoff:   permissionTimestampOrNil(result.GrantCutoff), DryRun: result.DryRun,
	}, nil
}

func (s *PermissionService) RevokeGrant(
	ctx context.Context,
	req *morphpb.RevokePermissionGrantRequest,
) (*morphpb.RevokePermissionGrantResponse, error) {
	if err := checkInteractivePermissionOperator(ctx); err != nil {
		return nil, err
	}

	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	grant, err := service.Revoke(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &morphpb.RevokePermissionGrantResponse{Grant: approvalGrantToProto(grant)}, nil
}

func (s *PermissionService) DeleteRecord(
	ctx context.Context,
	req *morphpb.DeletePermissionRecordRequest,
) (*morphpb.DeletePermissionRecordResponse, error) {
	if err := checkInteractivePermissionOperator(ctx); err != nil {
		return nil, err
	}

	service, err := s.getService()
	if err != nil {
		return nil, err
	}
	result, err := service.Delete(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &morphpb.DeletePermissionRecordResponse{
		Id: result.ID, Kind: string(result.Kind), LinkedGrantId: result.LinkedGrantID,
	}, nil
}

func checkInteractivePermissionOperator(ctx context.Context) error {
	surface := rpcmeta.PermissionSurfaceFromIncomingContext(ctx)
	actor := rpcmeta.PermissionActorFromIncomingContext(ctx)
	if actor.Kind != permissions.ActorLocalOwner ||
		(surface != permissions.SurfaceCLI && surface != permissions.SurfaceTUI) {
		return status.Error(codes.PermissionDenied, "permission approvals require an interactive local client")
	}
	return nil
}

func (s *PermissionService) getService() (*permissions.ApprovalService, error) {
	if s == nil || s.service == nil || s.service.approvalService == nil {
		return nil, status.Error(codes.Unavailable, "approval service is unavailable")
	}

	return s.service.approvalService, nil
}

func approvalRequestToProto(request permissions.ApprovalRequest) *morphpb.PermissionApprovalRequest {
	effects := make([]string, len(request.Effects))
	for index, effect := range request.Effects {
		effects[index] = string(effect)
	}

	return &morphpb.PermissionApprovalRequest{
		Id: request.ID, ActorKind: string(request.Actor.Kind), SurfaceKind: string(request.SurfaceKind),
		Surface: string(request.Surface), Profile: request.Profile, SessionId: request.SessionID,
		Tool: request.Tool, Resource: string(request.Resource), Action: string(request.Action), Effects: effects,
		Summary: request.Summary, Reason: request.Reason, Status: string(request.Status), Scope: string(request.Scope),
		GrantId: request.GrantID, CreatedAt: permissionTimestampOrNil(request.CreatedAt), ExpiresAt: permissionTimestampOrNil(request.ExpiresAt),
		ResolvedAt: permissionTimestampOrNil(request.ResolvedAt), Operations: append([]string(nil), request.Operations...),
	}
}

func approvalGrantToProto(grant permissions.ApprovalGrant) *morphpb.PermissionGrant {
	return &morphpb.PermissionGrant{
		Id: grant.ID, RequestId: grant.RequestID, ActorKind: string(grant.Actor.Kind), Profile: grant.Profile,
		SessionId: grant.SessionID, Scope: string(grant.Scope), Status: string(grant.Status),
		CreatedAt: permissionTimestampOrNil(grant.CreatedAt), ExpiresAt: permissionTimestampOrNil(grant.ExpiresAt),
		ConsumedAt: permissionTimestampOrNil(grant.ConsumedAt), RevokedAt: permissionTimestampOrNil(grant.RevokedAt),
		Operations: append([]string(nil), grant.Operations...),
	}
}

func approvalGrantDetailsToProto(grant permissions.ApprovalGrant) *morphpb.PermissionGrant {
	result := approvalGrantToProto(grant)
	result.ActorId = grant.Actor.ID
	result.Fingerprint = grant.Fingerprint
	return result
}

func permissionTimestampOrNil(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}

	return timestamppb.New(value)
}
