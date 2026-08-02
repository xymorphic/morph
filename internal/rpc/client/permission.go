package client

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
)

func (s *PermissionService) ListApprovalRequests(
	ctx context.Context,
	query permissions.ApprovalQuery,
) ([]permissions.ApprovalRequest, error) {
	response, err := s.client.ListRequests(ctx, &morphpb.ListPermissionRequestsRequest{
		Status: string(query.Status), Limit: int32(query.Limit), Offset: int32(query.Offset),
	})
	if err != nil {
		return nil, err
	}
	result := make([]permissions.ApprovalRequest, len(response.GetRequests()))
	for index, request := range response.GetRequests() {
		result[index] = approvalRequestFromProto(request)
	}

	return result, nil
}

func (s *PermissionService) GetApprovalRequest(
	ctx context.Context,
	id string,
) (permissions.ApprovalRequest, bool, error) {
	response, err := s.client.GetRequest(ctx, &morphpb.GetPermissionRequestRequest{Id: id})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return permissions.ApprovalRequest{}, false, nil
		}
		return permissions.ApprovalRequest{}, false, err
	}
	if response.GetRequest() == nil {
		return permissions.ApprovalRequest{}, false, nil
	}
	return approvalRequestFromProto(response.GetRequest()), true, nil
}

func (s *PermissionService) ResolveApprovalRequest(
	ctx context.Context,
	id string,
	approved bool,
	scope permissions.GrantScope,
) (permissions.ApprovalRequest, error) {
	response, err := s.client.ResolveRequest(ctx, &morphpb.ResolvePermissionRequestRequest{
		Id: id, Approved: approved, Scope: string(scope),
	})
	if err != nil {
		return permissions.ApprovalRequest{}, err
	}
	return approvalRequestFromProto(response.GetRequest()), nil
}

func (s *PermissionService) ListApprovalGrants(
	ctx context.Context,
	query permissions.GrantQuery,
) ([]permissions.ApprovalGrant, error) {
	response, err := s.client.ListGrants(ctx, &morphpb.ListPermissionGrantsRequest{
		Status: string(query.Status), Limit: int32(query.Limit), Offset: int32(query.Offset),
	})
	if err != nil {
		return nil, err
	}
	result := make([]permissions.ApprovalGrant, len(response.GetGrants()))
	for index, grant := range response.GetGrants() {
		result[index] = approvalGrantFromProto(grant)
	}

	return result, nil
}

func (s *PermissionService) GetApprovalGrant(
	ctx context.Context,
	id string,
) (permissions.ApprovalGrant, bool, error) {
	response, err := s.client.GetGrant(ctx, &morphpb.GetPermissionGrantRequest{Id: id})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return permissions.ApprovalGrant{}, false, nil
		}
		return permissions.ApprovalGrant{}, false, err
	}
	if response.GetGrant() == nil {
		return permissions.ApprovalGrant{}, false, nil
	}
	return approvalGrantFromProto(response.GetGrant()), true, nil
}

func (s *PermissionService) PruneApprovals(
	ctx context.Context,
	dryRun bool,
) (permissions.ApprovalPruneResult, error) {
	response, err := s.client.Prune(ctx, &morphpb.PrunePermissionApprovalsRequest{DryRun: dryRun})
	if err != nil {
		return permissions.ApprovalPruneResult{}, err
	}
	return permissions.ApprovalPruneResult{
		Requests: response.GetRequests(), Grants: response.GetGrants(), DryRun: response.GetDryRun(),
		RequestCutoff: protoTimestampToTime(response.GetRequestCutoff()),
		GrantCutoff:   protoTimestampToTime(response.GetGrantCutoff()),
	}, nil
}

func (s *PermissionService) RevokeApprovalGrant(
	ctx context.Context,
	id string,
) (permissions.ApprovalGrant, error) {
	response, err := s.client.RevokeGrant(ctx, &morphpb.RevokePermissionGrantRequest{Id: id})
	if err != nil {
		return permissions.ApprovalGrant{}, err
	}
	return approvalGrantFromProto(response.GetGrant()), nil
}

func (s *PermissionService) DeleteApprovalRecord(
	ctx context.Context,
	id string,
) (permissions.ApprovalDeleteResult, error) {
	response, err := s.client.DeleteRecord(ctx, &morphpb.DeletePermissionRecordRequest{Id: id})
	if err != nil {
		return permissions.ApprovalDeleteResult{}, err
	}
	return permissions.ApprovalDeleteResult{
		ID: response.GetId(), Kind: permissions.ApprovalRecordKind(response.GetKind()),
		LinkedGrantID: response.GetLinkedGrantId(),
	}, nil
}

func approvalRequestFromProto(value *morphpb.PermissionApprovalRequest) permissions.ApprovalRequest {
	if value == nil {
		return permissions.ApprovalRequest{}
	}

	effects := make([]permissions.Effect, len(value.GetEffects()))
	for index, effect := range value.GetEffects() {
		effects[index] = permissions.Effect(effect)
	}

	return permissions.ApprovalRequest{
		ID: value.GetId(), Actor: permissions.Actor{Kind: permissions.ActorKind(value.GetActorKind())},
		SurfaceKind: permissions.SurfaceKind(value.GetSurfaceKind()), Surface: permissions.Surface(value.GetSurface()),
		Profile: value.GetProfile(), SessionID: value.GetSessionId(), Tool: value.GetTool(),
		Resource: permissions.Resource(value.GetResource()), Action: permissions.Action(value.GetAction()), Effects: effects,
		Summary: value.GetSummary(), Reason: value.GetReason(), Status: permissions.ApprovalStatus(value.GetStatus()),
		Operations: append([]string(nil), value.GetOperations()...),
		Scope:      permissions.GrantScope(value.GetScope()), GrantID: value.GetGrantId(),
		CreatedAt: protoTimestampToTime(value.GetCreatedAt()), ExpiresAt: protoTimestampToTime(value.GetExpiresAt()),
		ResolvedAt: protoTimestampToTime(value.GetResolvedAt()),
	}
}

func approvalGrantFromProto(value *morphpb.PermissionGrant) permissions.ApprovalGrant {
	if value == nil {
		return permissions.ApprovalGrant{}
	}

	return permissions.ApprovalGrant{
		ID: value.GetId(), RequestID: value.GetRequestId(), Fingerprint: value.GetFingerprint(), Actor: permissions.Actor{
			Kind: permissions.ActorKind(value.GetActorKind()),
			ID:   value.GetActorId(),
		},
		Profile: value.GetProfile(), SessionID: value.GetSessionId(), Scope: permissions.GrantScope(value.GetScope()),
		Status: permissions.GrantStatus(value.GetStatus()), CreatedAt: protoTimestampToTime(value.GetCreatedAt()),
		ExpiresAt: protoTimestampToTime(value.GetExpiresAt()), ConsumedAt: protoTimestampToTime(value.GetConsumedAt()),
		RevokedAt: protoTimestampToTime(value.GetRevokedAt()), Operations: append([]string(nil), value.GetOperations()...),
	}
}
