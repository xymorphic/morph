package storememory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/permissions"
)

func TestPermissionStore_ApprovalRequestLifecycle(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	request := permissions.ApprovalRequest{
		ID: "approval_one", Fingerprint: "fingerprint", Actor: permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		SessionID: "session", Effects: []permissions.Effect{permissions.EffectWrite}, Status: permissions.ApprovalPending,
		Operations: []string{"run_command · execute process"},
		CreatedAt:  now, ExpiresAt: now.Add(time.Minute),
	}

	created, inserted, err := store.CreateApprovalRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, request, created)
	request.Effects[0] = permissions.EffectRead
	request.Operations[0] = "changed"
	loaded, ok, err := store.GetApprovalRequest(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []permissions.Effect{permissions.EffectWrite}, loaded.Effects)
	require.Equal(t, []string{"run_command · execute process"}, loaded.Operations)

	coalesced, inserted, err := store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: "approval_coalesced", Fingerprint: created.Fingerprint, Actor: created.Actor,
		SessionID: created.SessionID, Status: permissions.ApprovalPending,
	})
	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, created.ID, coalesced.ID)

	_, _, err = store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: created.ID, Fingerprint: "different", Status: permissions.ApprovalPending,
	})
	require.EqualError(t, err, "approval request already exists")
	_, ok, err = store.GetApprovalRequest(ctx, "missing")
	require.NoError(t, err)
	require.False(t, ok)

	resolved, err := store.ResolveApprovalRequest(ctx, created.ID, permissions.ApprovalApproved, permissions.GrantOnce, now)
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalApproved, resolved.Status)
	require.Equal(t, permissions.GrantOnce, resolved.Scope)
	require.Equal(t, now, resolved.ResolvedAt)

	resolved, err = store.ResolveApprovalRequest(ctx, created.ID, permissions.ApprovalApproved, permissions.GrantOnce, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, now, resolved.ResolvedAt)
	_, err = store.ResolveApprovalRequest(ctx, created.ID, permissions.ApprovalDenied, "", now)
	require.EqualError(t, err, "approval request is already resolved")

	failedAt := now.Add(2 * time.Minute)
	failed, err := store.ResolveApprovalRequest(ctx, created.ID, permissions.ApprovalFailed, permissions.GrantOnce, failedAt)
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalFailed, failed.Status)
	require.Equal(t, failedAt, failed.ResolvedAt)
	_, err = store.ResolveApprovalRequest(ctx, "missing", permissions.ApprovalDenied, "", now)
	require.EqualError(t, err, "approval request not found")

	store.approvalRequests["pending"] = permissions.ApprovalRequest{ID: "pending", Status: permissions.ApprovalPending}
	store.approvalRequests["terminal"] = permissions.ApprovalRequest{ID: "terminal", Status: permissions.ApprovalDenied}
	cancelled, err := store.CancelPendingApprovals(ctx, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), cancelled)
	require.Equal(t, permissions.ApprovalCancelled, store.approvalRequests["pending"].Status)
	require.Equal(t, now, store.approvalRequests["pending"].ResolvedAt)
}

func TestPermissionStore_ListsApprovalRecordsWithFiltersAndPagination(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store.approvalRequests = map[string]permissions.ApprovalRequest{
		"old": {ID: "old", Status: permissions.ApprovalDenied, CreatedAt: now, Effects: []permissions.Effect{permissions.EffectRead}},
		"mid": {ID: "mid", Status: permissions.ApprovalApproved, CreatedAt: now.Add(time.Minute)},
		"new": {ID: "new", Status: permissions.ApprovalDenied, CreatedAt: now.Add(2 * time.Minute)},
	}

	requests, err := store.ListApprovalRequests(ctx, permissions.ApprovalQuery{Status: permissions.ApprovalDenied, Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"old"}, approvalRequestIDs(requests))
	requests[0].Effects[0] = permissions.EffectWrite
	require.Equal(t, permissions.EffectRead, store.approvalRequests["old"].Effects[0])
	requests, err = store.ListApprovalRequests(ctx, permissions.ApprovalQuery{Offset: 3})
	require.NoError(t, err)
	require.Nil(t, requests)
	requests, err = store.ListApprovalRequests(ctx, permissions.ApprovalQuery{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"new"}, approvalRequestIDs(requests))

	store.approvalGrants = map[string]permissions.ApprovalGrant{
		"old": {ID: "old", Status: permissions.GrantRevoked, CreatedAt: now},
		"mid": {ID: "mid", Status: permissions.GrantActive, CreatedAt: now.Add(time.Minute)},
		"new": {ID: "new", Status: permissions.GrantRevoked, CreatedAt: now.Add(2 * time.Minute)},
	}
	grants, err := store.ListApprovalGrants(ctx, permissions.GrantQuery{Status: permissions.GrantRevoked, Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"old"}, approvalGrantIDs(grants))
	grants, err = store.ListApprovalGrants(ctx, permissions.GrantQuery{Offset: 3})
	require.NoError(t, err)
	require.Nil(t, grants)
	grants, err = store.ListApprovalGrants(ctx, permissions.GrantQuery{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"new"}, approvalGrantIDs(grants))
}

func TestPermissionStore_ApprovalGrantLifecycle(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	actor := permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"}
	approved := permissions.ApprovalRequest{ID: "approved", Status: permissions.ApprovalApproved}
	store.approvalRequests[approved.ID] = approved
	grant := permissions.ApprovalGrant{
		ID: "grant_once", RequestID: approved.ID, Fingerprint: "fingerprint", Actor: actor,
		Profile: "default", SessionID: "session", Scope: permissions.GrantOnce, Status: permissions.GrantActive,
		Operations: []string{"run_command · execute process", "run_command · read file workspace"},
		CreatedAt:  now, ExpiresAt: now.Add(time.Hour),
	}

	created, err := store.CreateApprovalGrant(ctx, grant)
	require.NoError(t, err)
	require.Equal(t, grant, created)
	require.Equal(t, grant.ID, store.approvalRequests[approved.ID].GrantID)
	created.Operations[0] = "changed"
	require.Equal(t, grant.Operations, store.approvalGrants[grant.ID].Operations)
	loaded, ok, err := store.GetApprovalGrant(ctx, grant.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, grant, loaded)
	loaded.Operations[0] = "changed"
	require.Equal(t, grant.Operations, store.approvalGrants[grant.ID].Operations)
	_, ok, err = store.GetApprovalGrant(ctx, "missing")
	require.NoError(t, err)
	require.False(t, ok)
	_, err = store.CreateApprovalGrant(ctx, grant)
	require.EqualError(t, err, "approval grant already exists")
	_, err = store.CreateApprovalGrant(ctx, permissions.ApprovalGrant{ID: "missing", RequestID: "missing"})
	require.EqualError(t, err, "approval request is not approved")
	store.approvalRequests["pending"] = permissions.ApprovalRequest{ID: "pending", Status: permissions.ApprovalPending}
	_, err = store.CreateApprovalGrant(ctx, permissions.ApprovalGrant{ID: "pending", RequestID: "pending"})
	require.EqualError(t, err, "approval request is not approved")

	found, ok, err := store.FindApprovalGrant(ctx, grant.Fingerprint, actor, grant.Profile, grant.SessionID, now)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, grant.ID, found.ID)
	found.Operations[0] = "changed"
	require.Equal(t, grant.Operations, store.approvalGrants[grant.ID].Operations)
	_, ok, err = store.FindApprovalGrant(ctx, "different", actor, grant.Profile, grant.SessionID, now)
	require.NoError(t, err)
	require.False(t, ok)

	consumed, err := store.ConsumeApprovalGrant(ctx, grant.ID, now)
	require.NoError(t, err)
	require.Equal(t, permissions.GrantConsumed, consumed.Status)
	require.Equal(t, now, consumed.ConsumedAt)
	_, err = store.ConsumeApprovalGrant(ctx, grant.ID, now)
	require.EqualError(t, err, "approval grant is not consumable")
	_, err = store.ConsumeApprovalGrant(ctx, "missing", now)
	require.EqualError(t, err, "approval grant not found")

	expired := permissions.ApprovalGrant{
		ID: "expired", Fingerprint: "expired", Actor: actor, Profile: "default", SessionID: "session",
		Scope: permissions.GrantSession, Status: permissions.GrantActive, ExpiresAt: now,
	}
	store.approvalGrants[expired.ID] = expired
	_, ok, err = store.FindApprovalGrant(ctx, expired.Fingerprint, actor, expired.Profile, expired.SessionID, now)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, permissions.GrantExpired, store.approvalGrants[expired.ID].Status)

	always := permissions.ApprovalGrant{
		ID: "always", Fingerprint: "always", Actor: actor, Profile: "default", SessionID: "other",
		Scope: permissions.GrantAlways, Status: permissions.GrantActive,
	}
	store.approvalGrants[always.ID] = always
	found, ok, err = store.FindApprovalGrant(ctx, always.Fingerprint, actor, always.Profile, "new-session", now.Add(100*365*24*time.Hour))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, always.ID, found.ID)

	active := permissions.ApprovalGrant{ID: "active", Status: permissions.GrantActive, Scope: permissions.GrantSession, ExpiresAt: now.Add(time.Hour)}
	store.approvalGrants[active.ID] = active
	revoked, err := store.RevokeApprovalGrant(ctx, active.ID, now)
	require.NoError(t, err)
	require.Equal(t, permissions.GrantRevoked, revoked.Status)
	revoked, err = store.RevokeApprovalGrant(ctx, active.ID, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, now, revoked.RevokedAt)
	_, err = store.RevokeApprovalGrant(ctx, grant.ID, now)
	require.EqualError(t, err, "approval grant is not active")
	_, err = store.RevokeApprovalGrant(ctx, "missing", now)
	require.EqualError(t, err, "approval grant not found")
}

func TestPermissionStore_PrunesAndDeletesTerminalRecords(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour)
	cutoff := now.Add(-30 * 24 * time.Hour)

	require.True(t, isPrunableGrant(permissions.ApprovalGrant{Status: permissions.GrantConsumed, ConsumedAt: old}, cutoff))
	require.True(t, isPrunableGrant(permissions.ApprovalGrant{Status: permissions.GrantRevoked, RevokedAt: old}, cutoff))
	require.True(t, isPrunableGrant(permissions.ApprovalGrant{Status: permissions.GrantExpired, Scope: permissions.GrantSession, ExpiresAt: old}, cutoff))
	require.False(t, isPrunableGrant(permissions.ApprovalGrant{Status: permissions.GrantConsumed}, cutoff))
	require.False(t, isPrunableGrant(permissions.ApprovalGrant{Status: permissions.GrantActive, Scope: permissions.GrantAlways}, cutoff))
	require.False(t, isPrunableGrant(permissions.ApprovalGrant{Status: permissions.GrantStatus("unknown")}, cutoff))

	store.approvalGrants = map[string]permissions.ApprovalGrant{
		"consumed": {ID: "consumed", Status: permissions.GrantConsumed, ConsumedAt: old},
		"revoked":  {ID: "revoked", Status: permissions.GrantRevoked, RevokedAt: old},
		"expired":  {ID: "expired", Status: permissions.GrantExpired, Scope: permissions.GrantSession, ExpiresAt: old},
		"always":   {ID: "always", Status: permissions.GrantActive, Scope: permissions.GrantAlways},
		"recent":   {ID: "recent", Status: permissions.GrantConsumed, ConsumedAt: now},
	}
	store.approvalRequests = map[string]permissions.ApprovalRequest{
		"old":             {ID: "old", Status: permissions.ApprovalDenied, ResolvedAt: old},
		"pending":         {ID: "pending", Status: permissions.ApprovalPending, ResolvedAt: old},
		"unresolved":      {ID: "unresolved", Status: permissions.ApprovalDenied},
		"recent":          {ID: "recent", Status: permissions.ApprovalDenied, ResolvedAt: now},
		"linked_pruned":   {ID: "linked_pruned", Status: permissions.ApprovalApproved, GrantID: "consumed", ResolvedAt: old},
		"linked_retained": {ID: "linked_retained", Status: permissions.ApprovalApproved, GrantID: "always", ResolvedAt: old},
		"linked_missing":  {ID: "linked_missing", Status: permissions.ApprovalApproved, GrantID: "missing", ResolvedAt: old},
	}
	opts := permissions.ApprovalPruneOptions{
		Now: now, RequestRetention: 30 * 24 * time.Hour, GrantRetention: 30 * 24 * time.Hour,
		BatchSize: 20, DryRun: true,
	}
	preview, err := store.PruneApprovals(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, int64(3), preview.Grants)
	require.Equal(t, int64(3), preview.Requests)
	require.Len(t, store.approvalGrants, 5)

	opts.DryRun = false
	pruned, err := store.PruneApprovals(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, preview.Grants, pruned.Grants)
	require.Equal(t, preview.Requests, pruned.Requests)
	require.NotContains(t, store.approvalRequests, "old")
	require.NotContains(t, store.approvalRequests, "linked_pruned")
	require.NotContains(t, store.approvalRequests, "linked_missing")
	require.Contains(t, store.approvalRequests, "linked_retained")
	require.NotContains(t, store.approvalGrants, "consumed")
	require.NotContains(t, store.approvalGrants, "revoked")
	require.NotContains(t, store.approvalGrants, "expired")
	require.Contains(t, store.approvalGrants, "always")
	_, err = store.PruneApprovals(ctx, permissions.ApprovalPruneOptions{BatchSize: 0})
	require.EqualError(t, err, "approval cleanup batch size must be greater than zero")

	store.approvalRequests["pending_delete"] = permissions.ApprovalRequest{ID: "pending_delete", Status: permissions.ApprovalPending}
	_, err = store.DeleteApprovalRequest(ctx, "pending_delete", now)
	require.EqualError(t, err, "pending approval request cannot be deleted")
	store.approvalRequests["unknown_delete"] = permissions.ApprovalRequest{ID: "unknown_delete", Status: permissions.ApprovalStatus("unknown")}
	_, err = store.DeleteApprovalRequest(ctx, "unknown_delete", now)
	require.EqualError(t, err, "approval request is not terminal")
	_, err = store.DeleteApprovalRequest(ctx, "missing", now)
	require.EqualError(t, err, "approval request not found")

	store.approvalRequests["with_active"] = permissions.ApprovalRequest{
		ID: "with_active", Status: permissions.ApprovalApproved, GrantID: "active_delete",
	}
	store.approvalGrants["active_delete"] = permissions.ApprovalGrant{
		ID: "active_delete", Status: permissions.GrantActive, Scope: permissions.GrantSession, ExpiresAt: now.Add(time.Hour),
	}
	linked, err := store.DeleteApprovalRequest(ctx, "with_active", now)
	require.NoError(t, err)
	require.Equal(t, "active_delete", linked)
	require.NotContains(t, store.approvalGrants, "active_delete")

	store.approvalRequests["without_grant"] = permissions.ApprovalRequest{
		ID: "without_grant", Status: permissions.ApprovalDenied, GrantID: "missing_grant",
	}
	linked, err = store.DeleteApprovalRequest(ctx, "without_grant", now)
	require.NoError(t, err)
	require.Empty(t, linked)

	deleteCases := []struct {
		name    string
		grant   permissions.ApprovalGrant
		wantErr string
	}{
		{name: "consumed", grant: permissions.ApprovalGrant{Status: permissions.GrantConsumed}},
		{name: "expired", grant: permissions.ApprovalGrant{Status: permissions.GrantExpired}},
		{name: "revoked", grant: permissions.ApprovalGrant{Status: permissions.GrantRevoked}},
		{name: "elapsed active", grant: permissions.ApprovalGrant{Status: permissions.GrantActive, Scope: permissions.GrantSession, ExpiresAt: now}},
		{name: "live active", grant: permissions.ApprovalGrant{Status: permissions.GrantActive, Scope: permissions.GrantSession, ExpiresAt: now.Add(time.Hour)}, wantErr: "active approval grant cannot be deleted; revoke it first"},
		{name: "unknown", grant: permissions.ApprovalGrant{Status: permissions.GrantStatus("unknown")}, wantErr: "approval grant is not terminal"},
	}
	for _, test := range deleteCases {
		t.Run(test.name, func(t *testing.T) {
			id := "delete_" + test.name
			value := test.grant
			value.ID = id
			store.approvalGrants[id] = value
			err := store.DeleteApprovalGrant(ctx, id, now)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				require.Contains(t, store.approvalGrants, id)
				return
			}
			require.NoError(t, err)
			require.NotContains(t, store.approvalGrants, id)
		})
	}
	require.EqualError(t, store.DeleteApprovalGrant(ctx, "missing", now), "approval grant not found")
}

func approvalRequestIDs(values []permissions.ApprovalRequest) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID
	}
	return result
}

func approvalGrantIDs(values []permissions.ApprovalGrant) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID
	}
	return result
}
