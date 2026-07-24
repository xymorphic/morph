package storesqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wandxy/morph/internal/permissions"
)

func TestPermissionStore_PersistsPendingRequestAcrossReopenForRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	_, _, err = store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{
		ID: "approval_restart", Fingerprint: "fingerprint", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.NoError(t, store.Close())

	reopened, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	service, err := permissions.NewApprovalService(reopened, permissions.ApprovalOptions{Now: func() time.Time { return now }})
	require.NoError(t, err)
	require.NoError(t, service.Recover(context.Background()))
	request, ok, err := reopened.GetApprovalRequest(context.Background(), "approval_restart")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, permissions.ApprovalCancelled, request.Status)
}

func TestPermissionStore_PersistsApprovalLifecycle(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	request := permissions.ApprovalRequest{
		ID: "approval_one", Fingerprint: "fingerprint", Actor: permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		SurfaceKind: permissions.SurfaceKindLocal, Surface: permissions.SurfaceTUI, Profile: "default", SessionID: "session",
		Tool: "run_command", Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
		Effects: []permissions.Effect{permissions.EffectExecution}, Summary: "run command",
		Operations: []string{"run_command · execute process", "run_command · read file workspace"},
		Status:     permissions.ApprovalPending,
		CreatedAt:  now, ExpiresAt: now.Add(time.Minute),
	}

	created, inserted, err := store.CreateApprovalRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, request, created)
	coalesced, inserted, err := store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: "approval_two", Fingerprint: request.Fingerprint, Actor: request.Actor, SessionID: request.SessionID,
	})
	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, request.ID, coalesced.ID)

	resolved, err := store.ResolveApprovalRequest(ctx, request.ID, permissions.ApprovalApproved, permissions.GrantOnce, now)
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalApproved, resolved.Status)
	grant := permissions.ApprovalGrant{
		ID: "grant_one", RequestID: request.ID, Fingerprint: request.Fingerprint, Actor: request.Actor,
		Profile: request.Profile, SessionID: request.SessionID, Scope: permissions.GrantOnce,
		Operations: request.Operations,
		Status:     permissions.GrantActive, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, err = store.CreateApprovalGrant(ctx, grant)
	require.NoError(t, err)
	found, ok, err := store.FindApprovalGrant(ctx, request.Fingerprint, request.Actor, request.Profile, request.SessionID, now)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, grant.ID, found.ID)
	require.Equal(t, request.Operations, found.Operations)
	consumed, err := store.ConsumeApprovalGrant(ctx, grant.ID, now)
	require.NoError(t, err)
	require.Equal(t, permissions.GrantConsumed, consumed.Status)

	requests, err := store.ListApprovalRequests(ctx, permissions.ApprovalQuery{Status: permissions.ApprovalApproved})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, request.Operations, requests[0].Operations)
	grants, err := store.ListApprovalGrants(ctx, permissions.GrantQuery{Status: permissions.GrantConsumed})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, request.Operations, grants[0].Operations)
}

func TestPermissionStore_PersistsCompositeOperationsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(path)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	operations := []string{
		"run_command · execute process",
		"run_command · read file workspace",
		"run_command · write file /tmp/result",
	}
	request := permissions.ApprovalRequest{
		ID: "approval_composite", Fingerprint: "batch:fingerprint",
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Profile: "default", SessionID: "session", Status: permissions.ApprovalPending,
		Operations: operations, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err = store.CreateApprovalRequest(ctx, request)
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(
		ctx, request.ID, permissions.ApprovalApproved, permissions.GrantSession, now,
	)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(ctx, permissions.ApprovalGrant{
		ID: "grant_composite", RequestID: request.ID, Fingerprint: request.Fingerprint,
		Actor: request.Actor, Profile: request.Profile, SessionID: request.SessionID,
		Scope: permissions.GrantSession, Status: permissions.GrantActive,
		Operations: operations, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, store.Close())

	reopened, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })

	loadedRequest, ok, err := reopened.GetApprovalRequest(ctx, request.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, operations, loadedRequest.Operations)

	loadedGrant, ok, err := reopened.FindApprovalGrant(
		ctx, request.Fingerprint, request.Actor, request.Profile, request.SessionID, now,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, operations, loadedGrant.Operations)
}

func TestPermissionStore_RejectsCorruptGrantOperations(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	require.NoError(t, store.db.Create(&approvalGrantModel{
		ID: "grant_corrupt", OperationsJSON: "{", Status: string(permissions.GrantRevoked),
	}).Error)

	_, err := store.ListApprovalGrants(context.Background(), permissions.GrantQuery{})
	require.Error(t, err)
}

func TestPermissionStore_PersistsAlwaysGrantAcrossReopenAndSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	actor := permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"}
	request := permissions.ApprovalRequest{
		ID: "approval_always", Fingerprint: "exact-operation", Actor: actor, Profile: "default",
		SessionID: "session-a", Status: permissions.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err = store.CreateApprovalRequest(context.Background(), request)
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(
		context.Background(), request.ID, permissions.ApprovalApproved, permissions.GrantAlways, now,
	)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(context.Background(), permissions.ApprovalGrant{
		ID: "grant_always", RequestID: request.ID, Fingerprint: request.Fingerprint, Actor: actor,
		Profile: request.Profile, SessionID: request.SessionID, Scope: permissions.GrantAlways,
		Status: permissions.GrantActive, CreatedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, store.Close())

	reopened, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	found, ok, err := reopened.FindApprovalGrant(
		context.Background(), request.Fingerprint, actor, request.Profile, "session-b", now.Add(100*365*24*time.Hour),
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, permissions.GrantAlways, found.Scope)
	require.True(t, found.ExpiresAt.IsZero())

	legacyRequest := permissions.ApprovalRequest{
		ID: "approval_legacy", Fingerprint: "legacy-operation", Actor: actor, Profile: "default",
		SessionID: "session-a", Status: permissions.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err = reopened.CreateApprovalRequest(context.Background(), legacyRequest)
	require.NoError(t, err)
	_, err = reopened.ResolveApprovalRequest(
		context.Background(), legacyRequest.ID, permissions.ApprovalApproved, permissions.GrantDurable, now,
	)
	require.NoError(t, err)
	_, err = reopened.CreateApprovalGrant(context.Background(), permissions.ApprovalGrant{
		ID: "grant_legacy", RequestID: legacyRequest.ID, Fingerprint: legacyRequest.Fingerprint, Actor: actor,
		Profile: legacyRequest.Profile, SessionID: legacyRequest.SessionID, Scope: permissions.GrantDurable,
		Status: permissions.GrantActive, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	require.NoError(t, err)
	_, ok, err = reopened.FindApprovalGrant(
		context.Background(), legacyRequest.Fingerprint, actor, legacyRequest.Profile, "session-b", now.Add(30*time.Minute),
	)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = reopened.FindApprovalGrant(
		context.Background(), legacyRequest.Fingerprint, actor, legacyRequest.Profile, "session-b", now.Add(2*time.Hour),
	)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPermissionStore_ExpiresRevokesAndRecovers(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	_, _, err := store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: "approval_pending", Fingerprint: "pending", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	count, err := store.CancelPendingApprovals(ctx, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	expired := permissions.ApprovalGrant{
		ID: "grant_expired", RequestID: "approval_expired", Fingerprint: "expired",
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Scope: permissions.GrantSession,
		Status: permissions.GrantActive, CreatedAt: now.Add(-time.Hour), ExpiresAt: now,
	}
	_, _, err = store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: expired.RequestID, Status: permissions.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(ctx, expired.RequestID, permissions.ApprovalApproved, permissions.GrantSession, now)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(ctx, expired)
	require.NoError(t, err)
	_, ok, err := store.FindApprovalGrant(ctx, expired.Fingerprint, expired.Actor, "", "", now)
	require.NoError(t, err)
	require.False(t, ok)

	active := expired
	active.ID = "grant_active"
	active.RequestID = "approval_other"
	active.Fingerprint = "active"
	active.ExpiresAt = now.Add(time.Hour)
	_, _, err = store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: active.RequestID, Status: permissions.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(ctx, active.RequestID, permissions.ApprovalApproved, permissions.GrantSession, now)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(ctx, active)
	require.NoError(t, err)
	revoked, err := store.RevokeApprovalGrant(ctx, active.ID, now)
	require.NoError(t, err)
	require.Equal(t, permissions.GrantRevoked, revoked.Status)
	revoked, err = store.RevokeApprovalGrant(ctx, active.ID, now)
	require.NoError(t, err)
	require.Equal(t, permissions.GrantRevoked, revoked.Status)
}

func TestPermissionStore_PrunesTerminalHistoryInBoundedBatches(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour)

	createResolved := func(id string, status permissions.ApprovalStatus, resolvedAt time.Time) {
		t.Helper()
		_, _, err := store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
			ID: id, Fingerprint: id, Status: permissions.ApprovalPending,
			CreatedAt: resolvedAt.Add(-time.Minute), ExpiresAt: resolvedAt,
		})
		require.NoError(t, err)
		_, err = store.ResolveApprovalRequest(ctx, id, status, "", resolvedAt)
		require.NoError(t, err)
	}
	createResolved("old_denied", permissions.ApprovalDenied, old)
	createResolved("recent_denied", permissions.ApprovalDenied, now.Add(-time.Hour))
	_, _, err := store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: "pending", Fingerprint: "pending", Status: permissions.ApprovalPending, CreatedAt: old, ExpiresAt: old,
	})
	require.NoError(t, err)

	for _, id := range []string{"active", "consumed"} {
		_, _, err = store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
			ID: "request_" + id, Fingerprint: id, Status: permissions.ApprovalPending,
			CreatedAt: old.Add(-time.Minute), ExpiresAt: old,
		})
		require.NoError(t, err)
		_, err = store.ResolveApprovalRequest(ctx, "request_"+id, permissions.ApprovalApproved, permissions.GrantOnce, old)
		require.NoError(t, err)
		_, err = store.CreateApprovalGrant(ctx, permissions.ApprovalGrant{
			ID: "grant_" + id, RequestID: "request_" + id, Fingerprint: id,
			Scope: permissions.GrantOnce, Status: permissions.GrantActive,
			CreatedAt: old, ExpiresAt: now.Add(time.Hour),
		})
		require.NoError(t, err)
	}
	_, _, err = store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: "request_always", Fingerprint: "always", Status: permissions.ApprovalPending,
		CreatedAt: old.Add(-time.Minute), ExpiresAt: old,
	})
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(ctx, "request_always", permissions.ApprovalApproved, permissions.GrantAlways, old)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(ctx, permissions.ApprovalGrant{
		ID: "grant_always", RequestID: "request_always", Fingerprint: "always",
		Scope: permissions.GrantAlways, Status: permissions.GrantActive, CreatedAt: old,
	})
	require.NoError(t, err)
	_, err = store.ConsumeApprovalGrant(ctx, "grant_consumed", old.Add(time.Hour))
	require.NoError(t, err)

	opts := permissions.ApprovalPruneOptions{
		Now: now, RequestRetention: 30 * 24 * time.Hour, GrantRetention: 30 * 24 * time.Hour,
		BatchSize: 10, DryRun: true,
	}
	preview, err := store.PruneApprovals(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, int64(2), preview.Requests)
	require.Equal(t, int64(1), preview.Grants)
	_, ok, err := store.GetApprovalRequest(ctx, "old_denied")
	require.NoError(t, err)
	require.True(t, ok)

	opts.DryRun = false
	pruned, err := store.PruneApprovals(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, preview.Requests, pruned.Requests)
	require.Equal(t, preview.Grants, pruned.Grants)
	for _, id := range []string{"old_denied", "request_consumed"} {
		_, ok, err = store.GetApprovalRequest(ctx, id)
		require.NoError(t, err)
		require.False(t, ok)
	}
	for _, id := range []string{"recent_denied", "pending", "request_active", "request_always"} {
		_, ok, err = store.GetApprovalRequest(ctx, id)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

func TestPermissionStore_DeletesOnlyTerminalApprovalRecords(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	request := permissions.ApprovalRequest{
		ID: "approval_delete", Fingerprint: "delete", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err := store.CreateApprovalRequest(ctx, request)
	require.NoError(t, err)
	_, err = store.DeleteApprovalRequest(ctx, request.ID, now)
	require.EqualError(t, err, "pending approval request cannot be deleted")
	_, err = store.ResolveApprovalRequest(ctx, request.ID, permissions.ApprovalApproved, permissions.GrantAlways, now)
	require.NoError(t, err)
	grant := permissions.ApprovalGrant{
		ID: "grant_delete", RequestID: request.ID, Fingerprint: request.Fingerprint,
		Scope: permissions.GrantAlways, Status: permissions.GrantActive, CreatedAt: now,
	}
	_, err = store.CreateApprovalGrant(ctx, grant)
	require.NoError(t, err)
	require.EqualError(t, store.DeleteApprovalGrant(ctx, grant.ID, now), "active approval grant cannot be deleted; revoke it first")
	linkedGrantID, err := store.DeleteApprovalRequest(ctx, request.ID, now)
	require.NoError(t, err)
	require.Equal(t, grant.ID, linkedGrantID)
	_, ok, err := store.GetApprovalRequest(ctx, request.ID)
	require.NoError(t, err)
	require.False(t, ok)
	grants, err := store.ListApprovalGrants(ctx, permissions.GrantQuery{})
	require.NoError(t, err)
	require.Empty(t, grants)
	_, err = store.DeleteApprovalRequest(ctx, "approval_missing", now)
	require.EqualError(t, err, "approval request not found")
	require.EqualError(t, store.DeleteApprovalGrant(ctx, "grant_missing", now), "approval grant not found")
}

func TestPermissionStore_ListSupportsOffsetAndLimit(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	for index, id := range []string{"oldest", "middle", "newest"} {
		_, _, err := store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{
			ID: id, Fingerprint: id, Status: permissions.ApprovalPending,
			CreatedAt: now.Add(time.Duration(index) * time.Minute), ExpiresAt: now.Add(time.Hour),
		})
		require.NoError(t, err)
	}
	requests, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, "middle", requests[0].ID)
}

func TestPermissionStore_RejectsInvalidAndConflictingRecords(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	request := permissions.ApprovalRequest{
		ID: "approval_duplicate", Fingerprint: "first", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err := store.CreateApprovalRequest(ctx, request)
	require.NoError(t, err)
	request.Fingerprint = "second"
	_, inserted, err := store.CreateApprovalRequest(ctx, request)
	require.Error(t, err)
	require.False(t, inserted)

	corrupt := approvalRequestModel{
		ID: "approval_corrupt", Fingerprint: "corrupt", Status: string(permissions.ApprovalPending),
		EffectsJSON: "not-json", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	require.NoError(t, store.db.Create(&corrupt).Error)
	_, inserted, err = store.CreateApprovalRequest(ctx, permissions.ApprovalRequest{
		ID: "approval_coalesced", Fingerprint: corrupt.Fingerprint, Status: permissions.ApprovalPending,
	})
	require.Error(t, err)
	require.False(t, inserted)
	_, ok, err := store.GetApprovalRequest(ctx, corrupt.ID)
	require.Error(t, err)
	require.False(t, ok)
	_, err = store.ListApprovalRequests(ctx, permissions.ApprovalQuery{})
	require.Error(t, err)
	require.NoError(t, store.db.Delete(&corrupt).Error)

	_, err = store.ResolveApprovalRequest(ctx, "missing", permissions.ApprovalDenied, "", now)
	require.EqualError(t, err, "approval request not found")
	resolved, err := store.ResolveApprovalRequest(ctx, request.ID, permissions.ApprovalDenied, "", now)
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalDenied, resolved.Status)
	resolved, err = store.ResolveApprovalRequest(ctx, request.ID, permissions.ApprovalDenied, "", now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, now, resolved.ResolvedAt)
	_, err = store.ResolveApprovalRequest(ctx, request.ID, permissions.ApprovalApproved, permissions.GrantOnce, now)
	require.EqualError(t, err, "approval request is already resolved")

	failedRequest := permissions.ApprovalRequest{
		ID: "approval_failed", Fingerprint: "failed", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err = store.CreateApprovalRequest(ctx, failedRequest)
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(ctx, failedRequest.ID, permissions.ApprovalApproved, permissions.GrantOnce, now)
	require.NoError(t, err)
	failed, err := store.ResolveApprovalRequest(ctx, failedRequest.ID, permissions.ApprovalFailed, permissions.GrantOnce, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalFailed, failed.Status)

	_, err = store.CreateApprovalGrant(ctx, permissions.ApprovalGrant{ID: "grant_unapproved", RequestID: request.ID})
	require.EqualError(t, err, "approval request is not approved")
	grant := permissions.ApprovalGrant{
		ID: "grant_duplicate", RequestID: failedRequest.ID, Scope: permissions.GrantOnce,
		Status: permissions.GrantActive, ExpiresAt: now.Add(time.Hour),
	}
	_, err = store.CreateApprovalGrant(ctx, grant)
	require.EqualError(t, err, "approval request is not approved")

	_, err = store.ConsumeApprovalGrant(ctx, "missing", now)
	require.EqualError(t, err, "approval grant is not consumable")
	_, err = store.RevokeApprovalGrant(ctx, "missing", now)
	require.EqualError(t, err, "approval grant not found")

	require.NoError(t, store.db.Create(&approvalGrantModel{
		ID: "grant_consumed", RequestID: "request_consumed", Status: string(permissions.GrantConsumed),
	}).Error)
	_, err = store.RevokeApprovalGrant(ctx, "grant_consumed", now)
	require.EqualError(t, err, "approval grant is not active")
	require.NoError(t, store.DeleteApprovalGrant(ctx, "grant_consumed", now))
}

func TestPermissionStore_ConversionsPreserveOptionalTimestamps(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	request := permissions.ApprovalRequest{
		ID: "approval", Effects: []permissions.Effect{permissions.EffectRead}, ResolvedAt: now,
	}
	model := approvalRequestToModel(request)
	require.NotNil(t, model.ResolvedAt)
	converted, err := approvalRequestFromModel(model)
	require.NoError(t, err)
	require.Equal(t, request, converted)
	_, err = approvalRequestFromModel(approvalRequestModel{EffectsJSON: "bad"})
	require.Error(t, err)

	grant := permissions.ApprovalGrant{ID: "grant", ConsumedAt: now, RevokedAt: now.Add(time.Minute)}
	grantModel := approvalGrantToModel(grant)
	require.NotNil(t, grantModel.ConsumedAt)
	require.NotNil(t, grantModel.RevokedAt)
	convertedGrant, err := approvalGrantFromModel(grantModel)
	require.NoError(t, err)
	require.Equal(t, grant, convertedGrant)
}

func TestPermissionStore_PropagatesDatabaseErrors(t *testing.T) {
	expected := errors.New("database failed")

	t.Run("create request lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "create-request", "permission_approval_requests", expected)
		_, _, err := store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{ID: "approval"})
		require.ErrorIs(t, err, expected)
	})

	t.Run("get request", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "get-request", "permission_approval_requests", expected)
		_, _, err := store.GetApprovalRequest(context.Background(), "approval")
		require.ErrorIs(t, err, expected)
	})

	t.Run("list requests", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "list-requests", "permission_approval_requests", expected)
		_, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
		require.ErrorIs(t, err, expected)
	})

	t.Run("resolve request lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "resolve-request", "permission_approval_requests", expected)
		_, err := store.ResolveApprovalRequest(context.Background(), "approval", permissions.ApprovalDenied, "", time.Now())
		require.ErrorIs(t, err, expected)
	})

	t.Run("find grant expiry update", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionUpdateError(t, store, "find-grant", "permission_approval_grants", expected)
		_, _, err := store.FindApprovalGrant(context.Background(), "fingerprint", permissions.Actor{}, "", "", time.Now())
		require.ErrorIs(t, err, expected)
	})

	t.Run("consume grant update", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionUpdateError(t, store, "consume-grant", "permission_approval_grants", expected)
		_, err := store.ConsumeApprovalGrant(context.Background(), "grant", time.Now())
		require.ErrorIs(t, err, expected)
	})

	t.Run("list grants", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "list-grants", "permission_approval_grants", expected)
		_, err := store.ListApprovalGrants(context.Background(), permissions.GrantQuery{})
		require.ErrorIs(t, err, expected)
	})

	t.Run("revoke grant lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "revoke-grant", "permission_approval_grants", expected)
		_, err := store.RevokeApprovalGrant(context.Background(), "grant", time.Now())
		require.ErrorIs(t, err, expected)
	})

	t.Run("delete request lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "delete-request", "permission_approval_requests", expected)
		_, err := store.DeleteApprovalRequest(context.Background(), "approval", time.Now())
		require.ErrorIs(t, err, expected)
	})

	t.Run("delete grant lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "delete-grant", "permission_approval_grants", expected)
		err := store.DeleteApprovalGrant(context.Background(), "grant", time.Now())
		require.ErrorIs(t, err, expected)
	})

	t.Run("prune grant lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "prune-grants", "permission_approval_grants", expected)
		_, err := store.PruneApprovals(context.Background(), permissions.ApprovalPruneOptions{BatchSize: 1})
		require.ErrorIs(t, err, expected)
	})
}

func TestPermissionStore_PropagatesTransactionalWriteErrors(t *testing.T) {
	expected := errors.New("write failed")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	t.Run("resolve pending request save", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestModel(t, store, approvalRequestModel{
			ID: "approval", Status: string(permissions.ApprovalPending), EffectsJSON: "null",
		})
		addPermissionUpdateError(t, store, "resolve-pending", "permission_approval_requests", expected)
		_, err := store.ResolveApprovalRequest(context.Background(), "approval", permissions.ApprovalDenied, "", now)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalPending)
	})

	t.Run("resolve approved request as failed", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestModel(t, store, approvalRequestModel{
			ID: "approval", Status: string(permissions.ApprovalApproved), EffectsJSON: "null",
		})
		addPermissionUpdateError(t, store, "resolve-failed", "permission_approval_requests", expected)
		_, err := store.ResolveApprovalRequest(context.Background(), "approval", permissions.ApprovalFailed, "", now)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalApproved)
	})

	t.Run("create grant", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionCreateError(t, store, "create-grant", "permission_approval_grants", expected)
		_, err := store.CreateApprovalGrant(context.Background(), permissions.ApprovalGrant{ID: "grant"})
		require.ErrorIs(t, err, expected)
		requirePermissionGrantCount(t, store, "grant", 0)
	})

	t.Run("link grant to request", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestModel(t, store, approvalRequestModel{
			ID: "approval", Status: string(permissions.ApprovalApproved), EffectsJSON: "null",
		})
		addPermissionUpdateError(t, store, "link-grant", "permission_approval_requests", expected)
		_, err := store.CreateApprovalGrant(context.Background(), permissions.ApprovalGrant{
			ID: "grant", RequestID: "approval",
		})
		require.ErrorIs(t, err, expected)
		requirePermissionGrantCount(t, store, "grant", 0)
		var request approvalRequestModel
		require.NoError(t, store.db.First(&request, "id = ?", "approval").Error)
		require.Empty(t, request.GrantID)
	})

	t.Run("load matching grant", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "find-matching-grant", "permission_approval_grants", expected)
		_, _, err := store.FindApprovalGrant(context.Background(), "fingerprint", permissions.Actor{}, "", "", now)
		require.ErrorIs(t, err, expected)
	})

	t.Run("load consumed grant", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		require.NoError(t, store.db.Create(&approvalGrantModel{
			ID: "grant", Status: string(permissions.GrantActive), Scope: string(permissions.GrantOnce),
			ExpiresAt: now.Add(time.Hour),
		}).Error)
		addPermissionQueryError(t, store, "load-consumed-grant", "permission_approval_grants", expected)
		_, err := store.ConsumeApprovalGrant(context.Background(), "grant", now)
		require.ErrorIs(t, err, expected)
		requirePermissionGrantStatus(t, store, "grant", permissions.GrantActive)
	})

	t.Run("load linked grant for request deletion", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestModel(t, store, approvalRequestModel{
			ID: "approval", Status: string(permissions.ApprovalApproved), GrantID: "grant", EffectsJSON: "null",
		})
		addPermissionQueryError(t, store, "load-linked-grant", "permission_approval_grants", expected)
		_, err := store.DeleteApprovalRequest(context.Background(), "approval", now)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalApproved)
	})

	t.Run("revoke linked grant before request deletion", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestWithGrantModels(t, store, now)
		addPermissionUpdateError(t, store, "revoke-linked-grant", "permission_approval_grants", expected)
		_, err := store.DeleteApprovalRequest(context.Background(), "approval", now)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalApproved)
		requirePermissionGrantStatus(t, store, "grant", permissions.GrantActive)
	})

	t.Run("delete linked grant", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestWithGrantModels(t, store, now)
		addPermissionDeleteError(t, store, "delete-linked-grant", "permission_approval_grants", expected)
		_, err := store.DeleteApprovalRequest(context.Background(), "approval", now)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalApproved)
		requirePermissionGrantStatus(t, store, "grant", permissions.GrantActive)
	})

	t.Run("delete terminal request", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		createPermissionRequestModel(t, store, approvalRequestModel{
			ID: "approval", Status: string(permissions.ApprovalDenied), EffectsJSON: "null",
		})
		addPermissionDeleteError(t, store, "delete-request", "permission_approval_requests", expected)
		_, err := store.DeleteApprovalRequest(context.Background(), "approval", now)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalDenied)
	})

	t.Run("delete terminal grant", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		require.NoError(t, store.db.Create(&approvalGrantModel{
			ID: "grant", Status: string(permissions.GrantConsumed),
		}).Error)
		addPermissionDeleteError(t, store, "delete-grant", "permission_approval_grants", expected)
		err := store.DeleteApprovalGrant(context.Background(), "grant", now)
		require.ErrorIs(t, err, expected)
		requirePermissionGrantStatus(t, store, "grant", permissions.GrantConsumed)
	})
}

func TestPermissionStore_CoversGrantPaginationAndInvalidStates(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	for index, id := range []string{"oldest", "middle", "newest"} {
		require.NoError(t, store.db.Create(&approvalGrantModel{
			ID: id, RequestID: "request_" + id, Status: string(permissions.GrantRevoked),
			CreatedAt: now.Add(time.Duration(index) * time.Minute),
		}).Error)
	}
	grants, err := store.ListApprovalGrants(context.Background(), permissions.GrantQuery{
		Status: permissions.GrantRevoked, Limit: 1, Offset: 1,
	})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, "middle", grants[0].ID)

	createPermissionRequestModel(t, store, approvalRequestModel{
		ID: "unknown_request", Status: "unknown", EffectsJSON: "null",
	})
	_, err = store.DeleteApprovalRequest(context.Background(), "unknown_request", now)
	require.EqualError(t, err, "approval request is not terminal")

	require.NoError(t, store.db.Create(&approvalGrantModel{
		ID: "unknown_grant", RequestID: "request_unknown", Status: "unknown",
	}).Error)
	err = store.DeleteApprovalGrant(context.Background(), "unknown_grant", now)
	require.EqualError(t, err, "approval grant is not terminal")

	_, err = store.PruneApprovals(context.Background(), permissions.ApprovalPruneOptions{})
	require.EqualError(t, err, "approval cleanup batch size must be greater than zero")
}

func TestPermissionStore_PrunePropagatesQueryAndDeleteErrors(t *testing.T) {
	expected := errors.New("prune failed")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour)
	opts := permissions.ApprovalPruneOptions{
		Now: now, RequestRetention: 30 * 24 * time.Hour, GrantRetention: 30 * 24 * time.Hour, BatchSize: 10,
	}

	t.Run("request lookup", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		addPermissionQueryError(t, store, "prune-requests", "permission_approval_requests", expected)
		_, err := store.PruneApprovals(context.Background(), opts)
		require.ErrorIs(t, err, expected)
	})

	t.Run("grant delete", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		require.NoError(t, store.db.Create(&approvalGrantModel{
			ID: "grant", RequestID: "request", Status: string(permissions.GrantConsumed),
			ConsumedAt: &old, CreatedAt: old,
		}).Error)
		addPermissionDeleteError(t, store, "prune-grants", "permission_approval_grants", expected)
		_, err := store.PruneApprovals(context.Background(), opts)
		require.ErrorIs(t, err, expected)
		requirePermissionGrantStatus(t, store, "grant", permissions.GrantConsumed)
	})

	t.Run("request delete", func(t *testing.T) {
		store := newAutomationSQLiteStore(t)
		resolvedAt := old
		createPermissionRequestModel(t, store, approvalRequestModel{
			ID: "approval", Status: string(permissions.ApprovalDenied), EffectsJSON: "null",
			ResolvedAt: &resolvedAt, CreatedAt: old,
		})
		addPermissionDeleteError(t, store, "prune-requests", "permission_approval_requests", expected)
		_, err := store.PruneApprovals(context.Background(), opts)
		require.ErrorIs(t, err, expected)
		requirePermissionRequestStatus(t, store, "approval", permissions.ApprovalDenied)
	})
}

func addPermissionQueryError(t *testing.T, store *Store, name string, table string, expected error) {
	t.Helper()
	callbackName := "test:permission-query-" + name
	failed := false
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == table && !failed {
			failed = true
			_ = tx.AddError(expected)
		}
	}))
	t.Cleanup(func() { require.NoError(t, store.db.Callback().Query().Remove(callbackName)) })
}

func addPermissionUpdateError(t *testing.T, store *Store, name string, table string, expected error) {
	t.Helper()
	callbackName := "test:permission-update-" + name
	failed := false
	require.NoError(t, store.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == table && !failed {
			failed = true
			_ = tx.AddError(expected)
		}
	}))
	t.Cleanup(func() { require.NoError(t, store.db.Callback().Update().Remove(callbackName)) })
}

func addPermissionCreateError(t *testing.T, store *Store, name string, table string, expected error) {
	t.Helper()
	callbackName := "test:permission-create-" + name
	failed := false
	require.NoError(t, store.db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == table && !failed {
			failed = true
			_ = tx.AddError(expected)
		}
	}))
	t.Cleanup(func() { require.NoError(t, store.db.Callback().Create().Remove(callbackName)) })
}

func addPermissionDeleteError(t *testing.T, store *Store, name string, table string, expected error) {
	t.Helper()
	callbackName := "test:permission-delete-" + name
	failed := false
	require.NoError(t, store.db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == table && !failed {
			failed = true
			_ = tx.AddError(expected)
		}
	}))
	t.Cleanup(func() { require.NoError(t, store.db.Callback().Delete().Remove(callbackName)) })
}

func createPermissionRequestModel(t *testing.T, store *Store, model approvalRequestModel) {
	t.Helper()
	require.NoError(t, store.db.Create(&model).Error)
}

func createPermissionRequestWithGrantModels(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	createPermissionRequestModel(t, store, approvalRequestModel{
		ID: "approval", Status: string(permissions.ApprovalApproved), GrantID: "grant", EffectsJSON: "null",
	})
	require.NoError(t, store.db.Create(&approvalGrantModel{
		ID: "grant", RequestID: "approval", Status: string(permissions.GrantActive),
		Scope: string(permissions.GrantSession), ExpiresAt: now.Add(time.Hour),
	}).Error)
}

func requirePermissionRequestStatus(
	t *testing.T,
	store *Store,
	id string,
	status permissions.ApprovalStatus,
) {
	t.Helper()
	var model approvalRequestModel
	require.NoError(t, store.db.First(&model, "id = ?", id).Error)
	require.Equal(t, string(status), model.Status)
}

func requirePermissionGrantStatus(t *testing.T, store *Store, id string, status permissions.GrantStatus) {
	t.Helper()
	var model approvalGrantModel
	require.NoError(t, store.db.First(&model, "id = ?", id).Error)
	require.Equal(t, string(status), model.Status)
}

func requirePermissionGrantCount(t *testing.T, store *Store, id string, expected int64) {
	t.Helper()
	var count int64
	require.NoError(t, store.db.Model(&approvalGrantModel{}).Where("id = ?", id).Count(&count).Error)
	require.Equal(t, expected, count)
}
