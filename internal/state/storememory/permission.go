package storememory

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/wandxy/morph/internal/permissions"
)

func (s *Store) CreateApprovalRequest(
	_ context.Context,
	request permissions.ApprovalRequest,
) (permissions.ApprovalRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.approvalRequests {
		if existing.Status == permissions.ApprovalPending && existing.Fingerprint == request.Fingerprint &&
			existing.Actor == request.Actor && existing.SessionID == request.SessionID {
			return cloneApprovalRequest(existing), false, nil
		}
	}
	if _, exists := s.approvalRequests[request.ID]; exists {
		return permissions.ApprovalRequest{}, false, errors.New("approval request already exists")
	}
	s.approvalRequests[request.ID] = cloneApprovalRequest(request)
	return cloneApprovalRequest(request), true, nil
}

func (s *Store) GetApprovalRequest(
	_ context.Context,
	id string,
) (permissions.ApprovalRequest, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	request, ok := s.approvalRequests[id]
	return cloneApprovalRequest(request), ok, nil
}

func (s *Store) ListApprovalRequests(
	_ context.Context,
	query permissions.ApprovalQuery,
) ([]permissions.ApprovalRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]permissions.ApprovalRequest, 0, len(s.approvalRequests))
	for _, request := range s.approvalRequests {
		if query.Status != "" && request.Status != query.Status {
			continue
		}

		result = append(result, cloneApprovalRequest(request))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if query.Offset > 0 {
		if query.Offset >= len(result) {
			return nil, nil
		}

		result = result[query.Offset:]
	}
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}
	return result, nil
}

func (s *Store) ResolveApprovalRequest(
	_ context.Context,
	id string,
	status permissions.ApprovalStatus,
	scope permissions.GrantScope,
	now time.Time,
) (permissions.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.approvalRequests[id]
	if !ok {
		return permissions.ApprovalRequest{}, errors.New("approval request not found")
	}
	if request.Status != permissions.ApprovalPending {
		if request.Status == permissions.ApprovalApproved && status == permissions.ApprovalFailed {
			request.Status = status
			request.ResolvedAt = now
			s.approvalRequests[id] = request
			return cloneApprovalRequest(request), nil
		}
		if request.Status == status && request.Scope == scope {
			return cloneApprovalRequest(request), nil
		}

		return permissions.ApprovalRequest{}, errors.New("approval request is already resolved")
	}
	request.Status = status
	request.Scope = scope
	request.ResolvedAt = now
	s.approvalRequests[id] = request
	return cloneApprovalRequest(request), nil
}

func (s *Store) CancelPendingApprovals(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var count int64
	for id, request := range s.approvalRequests {
		if request.Status != permissions.ApprovalPending {
			continue
		}

		request.Status = permissions.ApprovalCancelled
		request.ResolvedAt = now
		s.approvalRequests[id] = request
		count++
	}

	return count, nil
}

func (s *Store) CreateApprovalGrant(
	_ context.Context,
	grant permissions.ApprovalGrant,
) (permissions.ApprovalGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.approvalGrants[grant.ID]; exists {
		return permissions.ApprovalGrant{}, errors.New("approval grant already exists")
	}
	request, exists := s.approvalRequests[grant.RequestID]
	if !exists || request.Status != permissions.ApprovalApproved {
		return permissions.ApprovalGrant{}, errors.New("approval request is not approved")
	}
	s.approvalGrants[grant.ID] = cloneApprovalGrant(grant)
	request.GrantID = grant.ID
	s.approvalRequests[grant.RequestID] = request
	return cloneApprovalGrant(grant), nil
}

func (s *Store) FindApprovalGrant(
	_ context.Context,
	fingerprint string,
	actor permissions.Actor,
	profile string,
	sessionID string,
	now time.Time,
) (permissions.ApprovalGrant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, grant := range s.approvalGrants {
		if grant.Status == permissions.GrantActive && grant.IsExpiredAt(now) {
			grant.Status = permissions.GrantExpired
			s.approvalGrants[id] = grant
			continue
		}
		if grant.Status != permissions.GrantActive || grant.Fingerprint != fingerprint || grant.Actor != actor ||
			grant.Profile != profile ||
			(grant.Scope != permissions.GrantAlways && grant.Scope != permissions.GrantDurable && grant.SessionID != sessionID) {
			continue
		}

		return cloneApprovalGrant(grant), true, nil
	}

	return permissions.ApprovalGrant{}, false, nil
}

func (s *Store) ConsumeApprovalGrant(
	_ context.Context,
	id string,
	now time.Time,
) (permissions.ApprovalGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	grant, ok := s.approvalGrants[id]
	if !ok {
		return permissions.ApprovalGrant{}, errors.New("approval grant not found")
	}
	if grant.Status != permissions.GrantActive || grant.Scope != permissions.GrantOnce || !grant.ExpiresAt.After(now) {
		return permissions.ApprovalGrant{}, errors.New("approval grant is not consumable")
	}
	grant.Status = permissions.GrantConsumed
	grant.ConsumedAt = now
	s.approvalGrants[id] = grant
	return cloneApprovalGrant(grant), nil
}

func (s *Store) ListApprovalGrants(
	_ context.Context,
	query permissions.GrantQuery,
) ([]permissions.ApprovalGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]permissions.ApprovalGrant, 0, len(s.approvalGrants))
	for _, grant := range s.approvalGrants {
		if query.Status != "" && grant.Status != query.Status {
			continue
		}

		result = append(result, cloneApprovalGrant(grant))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if query.Offset > 0 {
		if query.Offset >= len(result) {
			return nil, nil
		}

		result = result[query.Offset:]
	}
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}
	return result, nil
}

func (s *Store) PruneApprovals(
	_ context.Context,
	opts permissions.ApprovalPruneOptions,
) (permissions.ApprovalPruneResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := permissions.ApprovalPruneResult{
		RequestCutoff: opts.Now.Add(-opts.RequestRetention),
		GrantCutoff:   opts.Now.Add(-opts.GrantRetention),
		DryRun:        opts.DryRun,
	}
	if opts.BatchSize <= 0 {
		return result, errors.New("approval cleanup batch size must be greater than zero")
	}
	grantIDs := make(map[string]struct{})
	for id, grant := range s.approvalGrants {
		if len(grantIDs) >= opts.BatchSize || !isPrunableGrant(grant, result.GrantCutoff) {
			continue
		}

		grantIDs[id] = struct{}{}
	}
	result.Grants = int64(len(grantIDs))
	requestIDs := make([]string, 0, opts.BatchSize)
	for id, request := range s.approvalRequests {
		if len(requestIDs) >= opts.BatchSize || request.Status == permissions.ApprovalPending ||
			request.ResolvedAt.IsZero() || request.ResolvedAt.After(result.RequestCutoff) {
			continue
		}

		if request.GrantID != "" {
			if _, pruning := grantIDs[request.GrantID]; !pruning {
				if _, retained := s.approvalGrants[request.GrantID]; retained {
					continue
				}
			}
		}
		requestIDs = append(requestIDs, id)
	}
	result.Requests = int64(len(requestIDs))
	if opts.DryRun {
		return result, nil
	}
	for id := range grantIDs {
		delete(s.approvalGrants, id)
	}
	for _, id := range requestIDs {
		delete(s.approvalRequests, id)
	}

	return result, nil
}

func isPrunableGrant(grant permissions.ApprovalGrant, cutoff time.Time) bool {
	switch grant.Status {
	case permissions.GrantConsumed:
		return !grant.ConsumedAt.IsZero() && !grant.ConsumedAt.After(cutoff)
	case permissions.GrantRevoked:
		return !grant.RevokedAt.IsZero() && !grant.RevokedAt.After(cutoff)
	case permissions.GrantExpired, permissions.GrantActive:
		return grant.Scope != permissions.GrantAlways && !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(cutoff)
	default:
		return false
	}
}

func (s *Store) RevokeApprovalGrant(
	_ context.Context,
	id string,
	now time.Time,
) (permissions.ApprovalGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	grant, ok := s.approvalGrants[id]
	if !ok {
		return permissions.ApprovalGrant{}, errors.New("approval grant not found")
	}
	if grant.Status == permissions.GrantRevoked {
		return cloneApprovalGrant(grant), nil
	}
	if grant.Status != permissions.GrantActive {
		return permissions.ApprovalGrant{}, errors.New("approval grant is not active")
	}
	grant.Status = permissions.GrantRevoked
	grant.RevokedAt = now
	s.approvalGrants[id] = grant
	return cloneApprovalGrant(grant), nil
}

func (s *Store) DeleteApprovalRequest(_ context.Context, id string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.approvalRequests[id]
	if !ok {
		return "", errors.New("approval request not found")
	}
	switch request.Status {
	case permissions.ApprovalApproved, permissions.ApprovalDenied, permissions.ApprovalExpired,
		permissions.ApprovalCancelled, permissions.ApprovalFailed:
	case permissions.ApprovalPending:
		return "", errors.New("pending approval request cannot be deleted")
	default:
		return "", errors.New("approval request is not terminal")
	}
	deletedGrantID := ""
	if request.GrantID != "" {
		grant, exists := s.approvalGrants[request.GrantID]
		if exists && grant.IsActiveAt(now) {
			grant.Status = permissions.GrantRevoked
			grant.RevokedAt = now
			s.approvalGrants[request.GrantID] = grant
		}
		if exists {
			deletedGrantID = request.GrantID
			delete(s.approvalGrants, request.GrantID)
		}
	}
	delete(s.approvalRequests, id)
	return deletedGrantID, nil
}

func (s *Store) DeleteApprovalGrant(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	grant, ok := s.approvalGrants[id]
	if !ok {
		return errors.New("approval grant not found")
	}
	switch grant.Status {
	case permissions.GrantConsumed, permissions.GrantExpired, permissions.GrantRevoked:
	case permissions.GrantActive:
		if grant.IsActiveAt(now) {
			return errors.New("active approval grant cannot be deleted; revoke it first")
		}
	default:
		return errors.New("approval grant is not terminal")
	}
	delete(s.approvalGrants, id)
	return nil
}

func cloneApprovalRequest(request permissions.ApprovalRequest) permissions.ApprovalRequest {
	request.Effects = append([]permissions.Effect(nil), request.Effects...)
	request.Operations = append([]string(nil), request.Operations...)
	return request
}

func cloneApprovalGrant(grant permissions.ApprovalGrant) permissions.ApprovalGrant {
	grant.Operations = append([]string(nil), grant.Operations...)
	return grant
}
