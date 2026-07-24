package permissions_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/state/storememory"
)

func TestApprovalService_AllowOnceResumesExactlyOneCoalescedInvocation(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	input := approvalInput("printf one")

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- service.Authorize(ctx, input) }()
	}
	request := waitForPendingApproval(t, store)
	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
	require.NoError(t, err)

	errs := []error{<-results, <-results}
	allowed := 0
	for _, resultErr := range errs {
		if resultErr == nil {
			allowed++
		}
	}
	require.Equal(t, 1, allowed)
	grants, err := service.ListGrants(context.Background(), permissions.GrantQuery{})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, permissions.GrantConsumed, grants[0].Status)
}

func TestApprovalService_GetGrantResolvesRequestIDsAndEffectiveExpiry(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, store := newApprovalService(t, permissions.ApprovalOptions{Now: func() time.Time { return now }})
	ctx := context.Background()
	request := permissions.ApprovalRequest{
		ID: "approval_lookup", Fingerprint: "fingerprint", Status: permissions.ApprovalPending,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	_, _, err := store.CreateApprovalRequest(ctx, request)
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(
		ctx, request.ID, permissions.ApprovalApproved, permissions.GrantSession, now.Add(-time.Hour),
	)
	require.NoError(t, err)
	grant := permissions.ApprovalGrant{
		ID: "grant_lookup", RequestID: request.ID, Fingerprint: request.Fingerprint,
		Scope: permissions.GrantSession, Status: permissions.GrantActive,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}
	_, err = store.CreateApprovalGrant(ctx, grant)
	require.NoError(t, err)

	loaded, ok, err := service.GetGrant(ctx, "  "+request.ID+"  ")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, grant.ID, loaded.ID)
	require.Equal(t, permissions.GrantExpired, loaded.Status)

	listed, err := service.ListGrants(ctx, permissions.GrantQuery{})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, permissions.GrantExpired, listed[0].Status)

	loaded, ok, err = service.GetGrant(ctx, "  "+grant.ID+"  ")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, grant.ID, loaded.ID)

	_, ok, err = service.GetGrant(ctx, "approval_missing")
	require.NoError(t, err)
	require.False(t, ok)

	var unavailable *permissions.ApprovalService
	_, _, err = unavailable.GetGrant(ctx, grant.ID)
	require.EqualError(t, err, "approval service is required")
}

func TestApprovalService_AuthorizeBatchBindsOperationOrder(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	network, err := permissions.NetworkTargetFromURL(
		"https://example.com/news?token=secret", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	inputs := []permissions.EvaluationInput{
		{ApprovalReason: "Personal browser attachment exposes signed-in sessions.", Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate,
			Effects: []permissions.Effect{permissions.EffectWrite}, Target: "tab=one",
		}},
		{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
			Effects: []permissions.Effect{permissions.EffectRead, permissions.EffectNetwork}, Network: &network,
		}},
	}
	result := make(chan error, 1)
	go func() { result <- service.AuthorizeBatch(ctx, inputs) }()
	request := waitForPendingApproval(t, store)
	require.Equal(t, permissions.ActionExecute, request.Action)
	require.ElementsMatch(t, []permissions.Effect{
		permissions.EffectRead, permissions.EffectWrite, permissions.EffectNetwork,
	}, request.Effects)
	require.Equal(t, "browser · approve 2 operations", request.Summary)
	require.Contains(t, request.Reason, "Personal browser attachment exposes signed-in sessions.")
	require.Contains(t, request.Reason, "GET https://example.com:443/news")
	require.NotContains(t, request.Reason, "secret")
	require.NotContains(t, request.Reason, network.QueryHash)
	_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	require.NoError(t, <-result)

	reversed := []permissions.EvaluationInput{inputs[1], inputs[0]}
	reversedResult := make(chan error, 1)
	go func() { reversedResult <- service.AuthorizeBatch(ctx, reversed) }()
	reversedRequest := waitForPendingApproval(t, store)
	require.NotEqual(t, request.Fingerprint, reversedRequest.Fingerprint)
	_, err = service.Resolve(context.Background(), reversedRequest.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	require.NoError(t, <-reversedResult)
	requests, err := service.List(context.Background(), permissions.ApprovalQuery{})
	require.NoError(t, err)
	require.Len(t, requests, 2)
}

func TestApprovalService_SingleNetworkApprovalSummaryIncludesSafeTarget(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	network, err := permissions.NetworkTargetFromURL(
		"https://example.com/news?token=secret", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		result <- service.Authorize(ctx, permissions.EvaluationInput{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
			Effects: []permissions.Effect{
				permissions.EffectRead, permissions.EffectNetwork, permissions.EffectExternalSystem,
			},
			Network: &network,
		}})
	}()
	request := waitForPendingApproval(t, store)

	require.Equal(t, "browser · read network GET https://example.com:443/news", request.Summary)
	require.NotContains(t, request.Summary, "secret")
	require.NotContains(t, request.Summary, network.QueryHash)
	_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
	require.NoError(t, err)
	require.NoError(t, <-result)
}

func TestApprovalService_SingleCommandApprovalSummaryUsesExecutableBasename(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	target := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "/private/secret/tool",
		ResolvedPath: "/private/secret/tool", PlanDigest: "plan", Complete: true,
	}
	result := make(chan error, 1)
	go func() {
		result <- service.Authorize(ctx, permissions.EvaluationInput{Operation: permissions.Operation{
			Tool: "run_command", Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
			Effects: []permissions.Effect{permissions.EffectExecution}, Command: &target,
		}})
	}()

	request := waitForPendingApproval(t, store)
	require.Equal(t, "run_command · execute process tool", request.Summary)
	require.NotContains(t, request.Summary, "/private/secret")
	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
	require.NoError(t, err)
	require.NoError(t, <-result)
}

func TestApprovalService_PrepareBatchConsumesOnceGrantOnlyOnCommit(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	inputs := []permissions.EvaluationInput{
		{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate,
			Effects: []permissions.Effect{permissions.EffectWrite}, Target: "tab=one",
		}},
		{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
			Effects: []permissions.Effect{permissions.EffectRead, permissions.EffectNetwork}, Target: "host=example.com",
		}},
	}
	result := make(chan permissions.BatchApproval, 1)
	errorsResult := make(chan error, 1)
	go func() {
		prepared, err := service.PrepareBatch(ctx, inputs)
		result <- prepared
		errorsResult <- err
	}()
	request := waitForPendingApproval(t, store)
	require.Equal(t, []string{
		"browser · update browser",
		"browser · read network",
	}, request.Operations)
	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
	require.NoError(t, err)
	prepared := <-result
	require.NoError(t, <-errorsResult)
	grants, err := service.ListGrants(context.Background(), permissions.GrantQuery{})
	require.NoError(t, err)
	require.Equal(t, permissions.GrantActive, grants[0].Status)
	require.Equal(t, request.Operations, grants[0].Operations)

	require.NoError(t, prepared.Commit(ctx))
	require.NoError(t, prepared.Commit(ctx))
	grants, err = service.ListGrants(context.Background(), permissions.GrantQuery{})
	require.NoError(t, err)
	require.Equal(t, permissions.GrantConsumed, grants[0].Status)
}

func TestApprovalService_PrepareBatchPersistsEveryOperationWhileBoundingPromptText(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	inputs := make([]permissions.EvaluationInput, 10)
	for index := range inputs {
		inputs[index] = permissions.EvaluationInput{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate,
			Effects: []permissions.Effect{permissions.EffectWrite}, Target: fmt.Sprintf("tab=%d", index),
		}}
	}

	result := make(chan error, 1)
	go func() {
		prepared, err := service.PrepareBatch(ctx, inputs)
		if err == nil {
			err = prepared.Commit(ctx)
		}
		result <- err
	}()

	request := waitForPendingApproval(t, store)
	require.Len(t, request.Operations, len(inputs))
	require.Contains(t, request.Reason, "2 more operations")
	require.NotContains(t, request.Operations, "2 more operations")

	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
	require.NoError(t, err)
	require.NoError(t, <-result)
	grants, err := service.ListGrants(context.Background(), permissions.GrantQuery{})
	require.NoError(t, err)
	require.Equal(t, request.Operations, grants[0].Operations)
}

func TestApprovalService_PrepareOperationBatchRejectsApprovalOutsideCompleteBatch(t *testing.T) {
	service, _ := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	asked := []permissions.EvaluationInput{{Operation: permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
		Target: "host=unexpected.example",
	}}}
	complete := []permissions.EvaluationInput{{Operation: permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate,
		Target: "tab=one",
	}}}

	_, err := service.PrepareOperationBatch(ctx, asked, complete)

	require.EqualError(t, err, "approval operation is not part of the complete batch")
}

func TestApprovalService_PrepareOperationBatchValidatesInputsAndAuthorization(t *testing.T) {
	service, _ := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	valid := approvalInput("printf one")
	invalid := permissions.EvaluationInput{Operation: permissions.Operation{
		Resource: permissions.ResourceProcess,
		Action:   "invalid",
	}}

	_, err := service.PrepareOperationBatch(context.Background(), nil, []permissions.EvaluationInput{valid})
	require.EqualError(t, err, "approval batch requires at least one operation")
	_, err = service.PrepareOperationBatch(
		context.Background(),
		[]permissions.EvaluationInput{valid},
		[]permissions.EvaluationInput{valid},
	)
	require.EqualError(t, err, "authorization context is required")

	ctx := approvalContext(context.Background(), "session-a")
	_, err = service.PrepareOperationBatch(
		ctx,
		[]permissions.EvaluationInput{valid},
		[]permissions.EvaluationInput{invalid},
	)
	require.EqualError(t, err, "permission action is invalid")
	_, err = service.PrepareOperationBatch(
		ctx,
		[]permissions.EvaluationInput{invalid},
		[]permissions.EvaluationInput{valid},
	)
	require.EqualError(t, err, "permission action is invalid")

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = service.PrepareOperationBatch(
		canceledCtx,
		[]permissions.EvaluationInput{valid},
		[]permissions.EvaluationInput{valid},
	)
	require.ErrorIs(t, err, context.Canceled)
	_, err = service.PrepareOperationBatch(
		canceledCtx,
		[]permissions.EvaluationInput{valid},
		[]permissions.EvaluationInput{
			valid,
			{Operation: permissions.Operation{
				Tool: "run_command", Resource: permissions.ResourceFile,
				Action: permissions.ActionRead, Target: "workspace",
			}},
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, service.AuthorizeBatch(canceledCtx, []permissions.EvaluationInput{valid}), context.Canceled)
}

func TestApprovalService_PrepareOperationBatchSupportsSingleOperation(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	input := approvalInput("printf one")
	result := make(chan permissions.BatchApproval, 1)
	errorsResult := make(chan error, 1)
	go func() {
		prepared, err := service.PrepareOperationBatch(
			ctx,
			[]permissions.EvaluationInput{input},
			[]permissions.EvaluationInput{input},
		)
		result <- prepared
		errorsResult <- err
	}()

	request := waitForPendingApproval(t, store)
	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	prepared := <-result
	require.NoError(t, <-errorsResult)
	require.NoError(t, prepared.Commit(ctx))
}

func TestApprovalService_RateLimitsInteractivePromptsAndReportsMetrics(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{
		RequestTTL: time.Second,
		RateLimit:  1,
		RateWindow: time.Minute,
	})
	ctx, cancel := context.WithCancel(approvalContext(context.Background(), "session"))
	cancel()
	require.ErrorIs(t, service.Authorize(ctx, approvalInput("first")), context.Canceled)

	err := service.Authorize(approvalContext(context.Background(), "session"), approvalInput("second"))
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeApprovalRateLimited, decisionErr.Code)
	require.Equal(t, "approval request rate limit exceeded", decisionErr.Error())
	requests, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, permissions.ApprovalMetrics{
		RequestsCreated: 1, RequestsRateLimited: 1,
	}, service.Metrics())
	require.Equal(t, permissions.ApprovalMetrics{}, (*permissions.ApprovalService)(nil).Metrics())
}

func TestApprovalService_UnattendedAskNotifiesTrustedChannelWithoutCreatingRequest(t *testing.T) {
	notifier := &approvalNotifierStub{}
	service, store := newApprovalService(t, permissions.ApprovalOptions{Notifier: notifier})
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorAutomation, ID: "auto_1"},
		Surface: permissions.SurfaceAutomation,
	})

	err := service.Authorize(ctx, approvalInput("scheduled"))
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeApprovalRequired, decisionErr.Code)
	require.Equal(t, "approval requires an interactive local owner surface", decisionErr.Error())
	require.Len(t, notifier.notices, 1)
	require.Equal(t, permissions.ActorAutomation, notifier.notices[0].Actor.Kind)
	require.Equal(t, "run_command · execute process", notifier.notices[0].Summary)
	requests, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
	require.NoError(t, err)
	require.Empty(t, requests)
	require.Equal(t, uint64(1), service.Metrics().RemoteNotices)
}

func TestApprovalService_SubagentCannotPersistGrantFromInteractiveSurface(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{})
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorSubagent, ID: "child"},
		Surface: permissions.SurfaceTUI,
	})

	err := service.Authorize(ctx, approvalInput("delegated"))
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeApprovalRequired, decisionErr.Code)
	requests, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
	require.NoError(t, err)
	require.Empty(t, requests)
}

type approvalNotifierStub struct {
	notices []permissions.ApprovalNotice
}

func (s *approvalNotifierStub) NotifyApprovalRequired(_ context.Context, notice permissions.ApprovalNotice) {
	s.notices = append(s.notices, notice)
}

func TestApprovalService_CancellingOneCoalescedWaiterDoesNotCancelTheOther(t *testing.T) {
	store := storememory.NewStore()
	wrapper := &approvalStoreObserver{ApprovalStore: store, waits: make(chan struct{}, 2)}
	service, err := permissions.NewApprovalService(wrapper, permissions.ApprovalOptions{
		RequestTTL: time.Second, RateLimit: 1, RateWindow: time.Minute,
	})
	require.NoError(t, err)
	base := approvalContext(context.Background(), "session")
	cancelledCtx, cancel := context.WithCancel(base)
	results := make(chan error, 2)
	go func() { results <- service.Authorize(cancelledCtx, approvalInput("shared")) }()
	go func() { results <- service.Authorize(base, approvalInput("shared")) }()
	request := waitForPendingApproval(t, store)
	<-wrapper.waits
	<-wrapper.waits
	cancel()
	require.ErrorIs(t, <-results, context.Canceled)
	current, ok, err := service.Get(context.Background(), request.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, permissions.ApprovalPending, current.Status)
	_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	require.NoError(t, <-results)
	metrics := service.Metrics()
	require.Equal(t, uint64(1), metrics.RequestsCreated)
	require.Equal(t, uint64(1), metrics.RequestsCoalesced)
	require.Zero(t, metrics.RequestsRateLimited)
}

type approvalStoreObserver struct {
	permissions.ApprovalStore
	waits chan struct{}
}

func (s *approvalStoreObserver) GetApprovalRequest(
	ctx context.Context,
	id string,
) (permissions.ApprovalRequest, bool, error) {
	request, ok, err := s.ApprovalStore.GetApprovalRequest(ctx, id)
	if err == nil && ok && request.Status == permissions.ApprovalPending {
		s.waits <- struct{}{}
	}
	return request, ok, err
}

func TestApprovalService_SessionGrantReusesOnlyMatchingTargetAndSession(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session-a")
	input := approvalInput("printf one")

	result := make(chan error, 1)
	go func() { result <- service.Authorize(ctx, input) }()
	request := waitForPendingApproval(t, store)
	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	require.NoError(t, <-result)
	require.NoError(t, service.Authorize(ctx, input))
	require.Equal(t, uint64(1), service.Metrics().GrantsReused)

	cancelledCtx, cancel := context.WithCancel(approvalContext(context.Background(), "session-a"))
	cancel()
	err = service.Authorize(cancelledCtx, approvalInput("printf two"))
	require.ErrorIs(t, err, context.Canceled)

	otherCtx, otherCancel := context.WithCancel(approvalContext(context.Background(), "session-b"))
	otherCancel()
	err = service.Authorize(otherCtx, input)
	require.ErrorIs(t, err, context.Canceled)
}

func TestApprovalService_AllowOnceGrantCannotBeConsumedByAnotherSession(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	input := approvalInput("session-bound")
	ctx := approvalContext(context.Background(), "session-a")
	request := approvedRequestForInput(t, ctx, input, "approval_session_a")
	_, _, err := store.CreateApprovalRequest(context.Background(), pendingRequestFrom(request))
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(
		context.Background(), request.ID, permissions.ApprovalApproved, permissions.GrantOnce, time.Now(),
	)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(context.Background(), grantForRequest(request, permissions.GrantOnce))
	require.NoError(t, err)

	otherCtx, cancel := context.WithCancel(approvalContext(context.Background(), "session-b"))
	cancel()
	require.ErrorIs(t, service.Authorize(otherCtx, input), context.Canceled)
}

func TestApprovalService_DenyExpiryCancellationAndRecoveryAreTerminal(t *testing.T) {
	t.Run("deny", func(t *testing.T) {
		service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
		result := make(chan error, 1)
		go func() {
			result <- service.Authorize(approvalContext(context.Background(), "session"), approvalInput("deny"))
		}()
		request := waitForPendingApproval(t, store)
		_, err := service.Resolve(context.Background(), request.ID, false, "")
		require.NoError(t, err)
		decisionErr, ok := permissions.GetDecisionError(<-result)
		require.True(t, ok)
		require.Equal(t, permissions.ErrorCodeDenied, decisionErr.Code)
		require.Equal(t, "approval denied", decisionErr.Evaluation.Reason)
	})

	t.Run("expiry", func(t *testing.T) {
		service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: 10 * time.Millisecond})
		err := service.Authorize(approvalContext(context.Background(), "session"), approvalInput("expire"))
		decisionErr, ok := permissions.GetDecisionError(err)
		require.True(t, ok)
		require.Equal(t, "approval expired", decisionErr.Evaluation.Reason)
		requests, listErr := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
		require.NoError(t, listErr)
		require.Equal(t, permissions.ApprovalExpired, requests[0].Status)
	})

	t.Run("cancellation", func(t *testing.T) {
		auditor := &approvalAuditorStub{}
		service, store := newApprovalService(t, permissions.ApprovalOptions{
			RequestTTL: time.Second,
			Auditor:    auditor,
		})
		ctx := context.WithValue(approvalContext(context.Background(), "session"), approvalAuditContextKey{}, "trace")
		ctx, cancel := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() { result <- service.Authorize(ctx, approvalInput("cancel")) }()
		request := waitForPendingApproval(t, store)
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)
		request, ok, err := store.GetApprovalRequest(context.Background(), request.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, permissions.ApprovalCancelled, request.Status)
		require.Equal(t, permissions.ApprovalCancelled, auditor.last().Status)
		require.Equal(t, "trace", auditor.lastContextValue)
	})

	t.Run("restart recovery", func(t *testing.T) {
		_, store := newApprovalService(t, permissions.ApprovalOptions{})
		_, _, err := store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{
			ID: "approval_stale", Fingerprint: "fingerprint", Status: permissions.ApprovalPending,
		})
		require.NoError(t, err)
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		require.NoError(t, service.Recover(context.Background()))
		request, ok, err := service.Get(context.Background(), "approval_stale")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, permissions.ApprovalCancelled, request.Status)
	})
}

func TestApprovalService_ResolutionAndGrantConstraints(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
	ctx := approvalContext(context.Background(), "session")
	input := approvalInput("credential command")
	input.Operation.Effects = []permissions.Effect{permissions.EffectCredentialBearing}
	result := make(chan error, 1)
	go func() { result <- service.Authorize(ctx, input) }()
	request := waitForPendingApproval(t, store)

	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantAlways)
	require.EqualError(t, err, "always approval is unavailable for these effects")
	current, ok, err := service.Get(context.Background(), request.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, permissions.ApprovalPending, current.Status)

	resolved, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	require.NoError(t, <-result)
	again, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
	require.NoError(t, err)
	require.Equal(t, resolved.ID, again.ID)
	_, err = service.Resolve(context.Background(), request.ID, false, "")
	require.EqualError(t, err, "approval request is already resolved")
}

func TestApprovalService_UnattendedSurfaceFailsWithoutCreatingRequest(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{})
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorAutomation, ID: "job"},
		Surface: permissions.SurfaceAutomation,
	})
	err := service.Authorize(ctx, approvalInput("command"))
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeApprovalRequired, decisionErr.Code)
	requests, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{})
	require.NoError(t, err)
	require.Empty(t, requests)
}

func TestApprovalService_AlwaysGrantIsUnexpiringAndReusableAcrossSessionsAndRestarts(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	service, store := newApprovalService(t, permissions.ApprovalOptions{
		RequestTTL: time.Second, Now: func() time.Time { return now },
	})
	input := approvalInput("workspace/file.txt")
	input.Operation.Effects = []permissions.Effect{permissions.EffectWrite}
	result := make(chan error, 1)
	go func() { result <- service.Authorize(approvalContext(context.Background(), "session-a"), input) }()
	request := waitForPendingApproval(t, store)
	_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantAlways)
	require.NoError(t, err)
	require.NoError(t, <-result)

	grants, err := service.ListGrants(context.Background(), permissions.GrantQuery{})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, permissions.GrantAlways, grants[0].Scope)
	require.True(t, grants[0].ExpiresAt.IsZero())
	require.True(t, grants[0].IsActiveAt(now.Add(100*365*24*time.Hour)))
	pruned, err := store.PruneApprovals(context.Background(), permissions.ApprovalPruneOptions{
		Now: now.Add(100 * 365 * 24 * time.Hour), RequestRetention: 24 * time.Hour,
		GrantRetention: 24 * time.Hour, BatchSize: 10,
	})
	require.NoError(t, err)
	require.Zero(t, pruned.Grants)
	require.Zero(t, pruned.Requests)
	require.NoError(t, service.Authorize(approvalContext(context.Background(), "session-b"), input))

	restarted, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{Now: func() time.Time { return now }})
	require.NoError(t, err)
	require.NoError(t, restarted.Authorize(approvalContext(context.Background(), "session-c"), input))

	differentCtx, cancel := context.WithCancel(approvalContext(context.Background(), "session-c"))
	cancel()
	require.ErrorIs(t, restarted.Authorize(differentCtx, approvalInput("workspace/other.txt")), context.Canceled)
}

func TestApprovalService_AlwaysGrantRejectsUnsafeEffects(t *testing.T) {
	for _, effect := range []permissions.Effect{
		permissions.EffectDestructive,
		permissions.EffectCredentialBearing,
		permissions.EffectPrivilegeChanging,
		permissions.EffectExecution,
		permissions.EffectNetwork,
		permissions.EffectExternalSystem,
	} {
		t.Run(string(effect), func(t *testing.T) {
			service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second})
			input := approvalInput(string(effect))
			input.Operation.Effects = []permissions.Effect{effect}
			result := make(chan error, 1)
			ctx, cancel := context.WithCancel(approvalContext(context.Background(), "session"))
			defer cancel()
			go func() { result <- service.Authorize(ctx, input) }()
			request := waitForPendingApproval(t, store)
			_, err := service.Resolve(context.Background(), request.ID, true, permissions.GrantAlways)
			require.EqualError(t, err, "always approval is unavailable for these effects")
			cancel()
			require.ErrorIs(t, <-result, context.Canceled)
		})
	}
}

func TestApprovalService_PruneSupportsDryRunAndBackgroundCleanup(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := storememory.NewStore()
	service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{
		Now: func() time.Time { return now }, RequestRetention: 24 * time.Hour,
		GrantRetention: 24 * time.Hour, CleanupInterval: 5 * time.Millisecond, CleanupBatchSize: 10,
	})
	require.NoError(t, err)
	old := now.Add(-48 * time.Hour)
	_, _, err = store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{
		ID: "old", Fingerprint: "old", Status: permissions.ApprovalPending, CreatedAt: old, ExpiresAt: old,
	})
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(context.Background(), "old", permissions.ApprovalDenied, "", old)
	require.NoError(t, err)

	preview, err := service.Prune(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, int64(1), preview.Requests)
	require.True(t, preview.DryRun)
	_, ok, err := service.Get(context.Background(), "old")
	require.NoError(t, err)
	require.True(t, ok)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.StartCleanup(ctx)
	require.Eventually(t, func() bool {
		_, found, getErr := service.Get(context.Background(), "old")
		return getErr == nil && !found
	}, time.Second, time.Millisecond)
}

func TestApprovalService_DeleteRemovesOnlyTerminalRecords(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	service, store := newApprovalService(t, permissions.ApprovalOptions{Now: func() time.Time { return now }})
	ctx := context.Background()

	pending := permissions.ApprovalRequest{
		ID: "approval_pending_delete", Fingerprint: "pending", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err := store.CreateApprovalRequest(ctx, pending)
	require.NoError(t, err)
	_, err = service.Delete(ctx, pending.ID)
	require.EqualError(t, err, "pending approval request cannot be deleted")

	terminal, err := store.ResolveApprovalRequest(
		ctx, pending.ID, permissions.ApprovalDenied, "", now,
	)
	require.NoError(t, err)
	deleted, err := service.Delete(ctx, terminal.ID)
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalDeleteResult{
		ID: terminal.ID, Kind: permissions.ApprovalRecordRequest,
	}, deleted)
	_, ok, err := store.GetApprovalRequest(ctx, terminal.ID)
	require.NoError(t, err)
	require.False(t, ok)

	approved := permissions.ApprovalRequest{
		ID: "approval_grant_delete", Fingerprint: "grant", Status: permissions.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	_, _, err = store.CreateApprovalRequest(ctx, approved)
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(ctx, approved.ID, permissions.ApprovalApproved, permissions.GrantSession, now)
	require.NoError(t, err)
	grant := permissions.ApprovalGrant{
		ID: "grant_delete", RequestID: approved.ID, Fingerprint: approved.Fingerprint,
		Scope: permissions.GrantSession, Status: permissions.GrantActive,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	_, err = store.CreateApprovalGrant(ctx, grant)
	require.NoError(t, err)
	_, err = service.Delete(ctx, grant.ID)
	require.EqualError(t, err, "active approval grant cannot be deleted; revoke it first")
	deleted, err = service.Delete(ctx, approved.ID)
	require.NoError(t, err)
	require.Equal(t, permissions.ApprovalRecordRequest, deleted.Kind)
	require.Equal(t, grant.ID, deleted.LinkedGrantID)
	grants, err := service.ListGrants(ctx, permissions.GrantQuery{})
	require.NoError(t, err)
	require.Empty(t, grants)

	expiredRequest := permissions.ApprovalRequest{
		ID: "approval_expired_grant", Fingerprint: "expired", Status: permissions.ApprovalPending,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}
	_, _, err = store.CreateApprovalRequest(ctx, expiredRequest)
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(
		ctx, expiredRequest.ID, permissions.ApprovalApproved, permissions.GrantOnce, now.Add(-time.Minute),
	)
	require.NoError(t, err)
	expiredGrant := permissions.ApprovalGrant{
		ID: "grant_expired_delete", RequestID: expiredRequest.ID, Fingerprint: expiredRequest.Fingerprint,
		Scope: permissions.GrantOnce, Status: permissions.GrantActive,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}
	_, err = store.CreateApprovalGrant(ctx, expiredGrant)
	require.NoError(t, err)
	deleted, err = service.Delete(ctx, expiredRequest.ID)
	require.NoError(t, err)
	require.Equal(t, expiredGrant.ID, deleted.LinkedGrantID)
	_, err = service.Delete(ctx, expiredGrant.ID)
	require.EqualError(t, err, "approval grant not found")

	_, err = service.Delete(ctx, "unknown")
	require.EqualError(t, err, "approval or grant id is required")
	_, err = service.Delete(ctx, "approval_missing")
	require.EqualError(t, err, "approval request not found")
	_, err = service.Delete(ctx, "grant_missing")
	require.EqualError(t, err, "approval grant not found")
}

func TestApprovalService_RevokeApprovalIDRequiresAnAssociatedGrant(t *testing.T) {
	service, store := newApprovalService(t, permissions.ApprovalOptions{})
	_, err := service.Revoke(context.Background(), "approval_missing")
	require.EqualError(t, err, "approval request not found")
	_, _, err = store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{
		ID: "approval_pending", Fingerprint: "pending", Status: permissions.ApprovalPending,
	})
	require.NoError(t, err)
	_, err = service.Revoke(context.Background(), "approval_pending")
	require.EqualError(t, err, "approval request has no grant")
	_, err = (*permissions.ApprovalService)(nil).Revoke(context.Background(), "grant")
	require.EqualError(t, err, "approval service is required")
}

func TestApprovalService_ValidatesDependenciesAndInputs(t *testing.T) {
	_, err := permissions.NewApprovalService(nil, permissions.ApprovalOptions{})
	require.EqualError(t, err, "approval store is required")
	var missing *permissions.ApprovalService
	require.EqualError(t, missing.Recover(context.Background()), "approval service is required")
	require.EqualError(t, missing.Authorize(context.Background(), approvalInput("target")), "approval service is unavailable")
	_, err = missing.Resolve(context.Background(), "id", true, permissions.GrantOnce)
	require.EqualError(t, err, "approval service is required")

	service, _ := newApprovalService(t, permissions.ApprovalOptions{})
	requests, err := service.List(context.Background(), permissions.ApprovalQuery{})
	require.NoError(t, err)
	require.Empty(t, requests)
	require.EqualError(t, service.Authorize(context.Background(), approvalInput("target")), "authorization context is required")
	invalidOperation := approvalInput("target")
	invalidOperation.Operation.Action = permissions.ActionUnknown
	require.EqualError(t, service.Authorize(approvalContext(context.Background(), "session"), invalidOperation), "permission action is invalid")
	_, err = service.Resolve(context.Background(), "id", true, "forever")
	require.EqualError(t, err, "approval scope must be one of: once, session, always")
	_, err = service.Resolve(context.Background(), "missing", false, "")
	require.EqualError(t, err, "approval request not found")
}

func TestApprovalService_AuditsRequestAndResolutionWithoutSensitiveTarget(t *testing.T) {
	auditor := &approvalAuditorStub{}
	service, store := newApprovalService(t, permissions.ApprovalOptions{RequestTTL: time.Second, Auditor: auditor})
	result := make(chan error, 1)
	go func() {
		result <- service.Authorize(
			approvalContext(context.Background(), "session"),
			approvalInput("secret command value"),
		)
	}()
	request := waitForPendingApproval(t, store)
	_, err := service.Resolve(context.Background(), request.ID, false, "")
	require.NoError(t, err)
	require.Error(t, <-result)
	require.GreaterOrEqual(t, len(auditor.requests), 2)
	for _, audited := range auditor.requests {
		require.NotContains(t, audited.Summary, "secret command value")
	}
}

func TestApprovalService_FailsClosedOnStoreFailures(t *testing.T) {
	expected := errors.New("store failed")
	ctx := approvalContext(context.Background(), "session")
	input := approvalInput("target")

	t.Run("grant lookup", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), findErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		require.ErrorContains(t, service.Authorize(ctx, input), "failed to read approval grant")
	})

	t.Run("request create", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), createRequestErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		require.ErrorContains(t, service.Authorize(ctx, input), "failed to create approval request")
	})

	t.Run("resolution read", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), getErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		_, err = service.Resolve(context.Background(), "id", false, "")
		require.ErrorIs(t, err, expected)
	})

	t.Run("resolution write", func(t *testing.T) {
		base := storememory.NewStore()
		request := permissions.ApprovalRequest{ID: "approval", Status: permissions.ApprovalPending}
		_, _, err := base.CreateApprovalRequest(context.Background(), request)
		require.NoError(t, err)
		store := &failingApprovalStore{ApprovalStore: base, resolveErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		_, err = service.Resolve(context.Background(), request.ID, false, "")
		require.ErrorIs(t, err, expected)
	})

	t.Run("wait read", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), getErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		require.ErrorIs(t, service.Authorize(ctx, input), expected)
	})

	t.Run("wait missing", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), getMissing: true}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		require.EqualError(t, service.Authorize(ctx, input), "approval request not found")
	})

	t.Run("request resolved before wait", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), getTerminal: true}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		decisionErr, ok := permissions.GetDecisionError(service.Authorize(ctx, input))
		require.True(t, ok)
		require.Equal(t, "approval denied", decisionErr.Evaluation.Reason)
	})

	t.Run("expiry write", func(t *testing.T) {
		store := &failingApprovalStore{ApprovalStore: storememory.NewStore(), resolveErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{RequestTTL: time.Millisecond})
		require.NoError(t, err)
		require.ErrorIs(t, service.Authorize(ctx, input), expected)
	})

	t.Run("existing once grant consumption", func(t *testing.T) {
		base := storememory.NewStore()
		request := approvedRequestForInput(t, ctx, input, "approval_existing")
		_, _, err := base.CreateApprovalRequest(context.Background(), pendingRequestFrom(request))
		require.NoError(t, err)
		_, err = base.ResolveApprovalRequest(context.Background(), request.ID, permissions.ApprovalApproved, permissions.GrantOnce, time.Now())
		require.NoError(t, err)
		_, err = base.CreateApprovalGrant(context.Background(), grantForRequest(request, permissions.GrantOnce))
		require.NoError(t, err)
		store := &failingApprovalStore{ApprovalStore: base, consumeErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
		require.NoError(t, err)
		require.ErrorContains(t, service.Authorize(ctx, input), "failed to consume approval grant")
	})

	t.Run("post approval grant lookup", func(t *testing.T) {
		base := storememory.NewStore()
		store := &failingApprovalStore{ApprovalStore: base, findFailAt: 2, findErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{RequestTTL: time.Second})
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() { result <- service.Authorize(ctx, input) }()
		request := waitForPendingApproval(t, base)
		_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
		require.NoError(t, err)
		require.ErrorContains(t, <-result, "failed to verify approval grant")
	})

	t.Run("post approval once consumption", func(t *testing.T) {
		base := storememory.NewStore()
		store := &failingApprovalStore{ApprovalStore: base, consumeErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{RequestTTL: time.Second})
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() { result <- service.Authorize(ctx, input) }()
		request := waitForPendingApproval(t, base)
		_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
		require.NoError(t, err)
		require.ErrorContains(t, <-result, "failed to consume approval grant")
	})

	t.Run("grant create marks request failed", func(t *testing.T) {
		base := storememory.NewStore()
		store := &failingApprovalStore{ApprovalStore: base, createGrantErr: expected}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{RequestTTL: time.Second})
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() { result <- service.Authorize(ctx, input) }()
		request := waitForPendingApproval(t, base)
		_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
		require.ErrorIs(t, err, expected)
		require.Error(t, <-result)
		request, ok, err := service.Get(context.Background(), request.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, permissions.ApprovalFailed, request.Status)
	})

	t.Run("grant and failure persistence errors still notify the waiter", func(t *testing.T) {
		base := storememory.NewStore()
		store := &failingApprovalStore{
			ApprovalStore:  base,
			createGrantErr: expected,
			resolveErr:     expected,
			resolveFailAt:  2,
		}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{RequestTTL: time.Second})
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() { result <- service.Authorize(ctx, input) }()
		request := waitForPendingApproval(t, base)
		_, err = service.Resolve(context.Background(), request.ID, true, permissions.GrantSession)
		require.ErrorIs(t, err, expected)
		decisionErr, ok := permissions.GetDecisionError(<-result)
		require.True(t, ok)
		require.Equal(t, "approval failed", decisionErr.Evaluation.Reason)
	})

	t.Run("cancellation persistence error still emits a terminal failure", func(t *testing.T) {
		base := storememory.NewStore()
		store := &failingApprovalStore{ApprovalStore: base, resolveErr: expected}
		auditor := &approvalAuditorStub{}
		service, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{
			RequestTTL: time.Second,
			Auditor:    auditor,
		})
		require.NoError(t, err)
		cancelCtx, cancel := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() { result <- service.Authorize(cancelCtx, input) }()
		_ = waitForPendingApproval(t, base)
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)
		require.Equal(t, permissions.ApprovalFailed, auditor.last().Status)
	})
}

type failingApprovalStore struct {
	permissions.ApprovalStore
	findErr          error
	createRequestErr error
	getErr           error
	getMissing       bool
	getTerminal      bool
	createGrantErr   error
	resolveErr       error
	resolveFailAt    int
	resolveCalls     int
	consumeErr       error
	findFailAt       int
	findCalls        int
}

func (s *failingApprovalStore) FindApprovalGrant(
	ctx context.Context,
	fingerprint string,
	actor permissions.Actor,
	profile string,
	sessionID string,
	now time.Time,
) (permissions.ApprovalGrant, bool, error) {
	s.findCalls++
	if s.findErr != nil && (s.findFailAt == 0 || s.findCalls == s.findFailAt) {
		return permissions.ApprovalGrant{}, false, s.findErr
	}
	return s.ApprovalStore.FindApprovalGrant(ctx, fingerprint, actor, profile, sessionID, now)
}

func (s *failingApprovalStore) ResolveApprovalRequest(
	ctx context.Context,
	id string,
	status permissions.ApprovalStatus,
	scope permissions.GrantScope,
	now time.Time,
) (permissions.ApprovalRequest, error) {
	s.resolveCalls++
	if s.resolveErr != nil && (s.resolveFailAt == 0 || s.resolveCalls == s.resolveFailAt) {
		return permissions.ApprovalRequest{}, s.resolveErr
	}
	return s.ApprovalStore.ResolveApprovalRequest(ctx, id, status, scope, now)
}

func (s *failingApprovalStore) ConsumeApprovalGrant(
	ctx context.Context,
	id string,
	now time.Time,
) (permissions.ApprovalGrant, error) {
	if s.consumeErr != nil {
		return permissions.ApprovalGrant{}, s.consumeErr
	}

	return s.ApprovalStore.ConsumeApprovalGrant(ctx, id, now)
}

func (s *failingApprovalStore) CreateApprovalRequest(
	ctx context.Context,
	request permissions.ApprovalRequest,
) (permissions.ApprovalRequest, bool, error) {
	if s.createRequestErr != nil {
		return permissions.ApprovalRequest{}, false, s.createRequestErr
	}

	return s.ApprovalStore.CreateApprovalRequest(ctx, request)
}

func (s *failingApprovalStore) GetApprovalRequest(
	ctx context.Context,
	id string,
) (permissions.ApprovalRequest, bool, error) {
	if s.getErr != nil {
		return permissions.ApprovalRequest{}, false, s.getErr
	}

	request, ok, err := s.ApprovalStore.GetApprovalRequest(ctx, id)
	if s.getMissing {
		return permissions.ApprovalRequest{}, false, nil
	}
	if s.getTerminal && ok {
		request.Status = permissions.ApprovalDenied
	}
	return request, ok, err
}

func (s *failingApprovalStore) CreateApprovalGrant(
	ctx context.Context,
	grant permissions.ApprovalGrant,
) (permissions.ApprovalGrant, error) {
	if s.createGrantErr != nil {
		return permissions.ApprovalGrant{}, s.createGrantErr
	}

	return s.ApprovalStore.CreateApprovalGrant(ctx, grant)
}

type approvalAuditorStub struct {
	mu               sync.Mutex
	requests         []permissions.ApprovalRequest
	lastContextValue any
}

type approvalAuditContextKey struct{}

func (s *approvalAuditorStub) last() permissions.ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.requests) == 0 {
		return permissions.ApprovalRequest{}
	}
	return s.requests[len(s.requests)-1]
}

func (s *approvalAuditorStub) ApprovalChanged(ctx context.Context, request permissions.ApprovalRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, request)
	s.lastContextValue = ctx.Value(approvalAuditContextKey{})
}

func TestFingerprint_ChangesWithMaterialTargetButNotEffectOrder(t *testing.T) {
	authorization, err := (permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"}, Surface: permissions.SurfaceTUI,
	}).Normalize()
	require.NoError(t, err)
	left, err := (approvalInput("a").Operation).Normalize()
	require.NoError(t, err)
	right := left
	right.Target = "b"
	require.NotEqual(t, permissions.Fingerprint(authorization, left), permissions.Fingerprint(authorization, right))

	left.Effects = []permissions.Effect{permissions.EffectWrite, permissions.EffectExecution}
	right = left
	right.Effects = []permissions.Effect{permissions.EffectExecution, permissions.EffectWrite}
	left, err = left.Normalize()
	require.NoError(t, err)
	right, err = right.Normalize()
	require.NoError(t, err)
	require.Equal(t, permissions.Fingerprint(authorization, left), permissions.Fingerprint(authorization, right))
}

func TestFingerprint_BindsTypedCommandPlanIdentity(t *testing.T) {
	authorization, err := (permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"}, Surface: permissions.SurfaceTUI,
	}).Normalize()
	require.NoError(t, err)
	target := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status"}, PlanDigest: "plan-a", CWDIdentity: "workspace:.",
		EnvironmentDigest: "environment-a", Complete: true, InvocationCount: 1,
	}
	operation := permissions.Operation{
		Tool: "run_command", Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
		Effects: []permissions.Effect{permissions.EffectExecution}, Command: &target,
	}
	original := permissions.Fingerprint(authorization, operation)

	changed := target
	changed.PlanDigest = "plan-b"
	operation.Command = &changed
	require.NotEqual(t, original, permissions.Fingerprint(authorization, operation))

	changed = target
	changed.CWDIdentity = "workspace:subdir"
	operation.Command = &changed
	require.NotEqual(t, original, permissions.Fingerprint(authorization, operation))

	changed = target
	changed.EnvironmentDigest = "environment-b"
	operation.Command = &changed
	require.NotEqual(t, original, permissions.Fingerprint(authorization, operation))

	operation.Command = &target
	scoped := authorization
	scoped.Scope = permissions.PermissionScope{
		Restricted: true,
		Resources:  []permissions.Resource{permissions.ResourceProcess},
		Actions:    []permissions.Action{permissions.ActionExecute},
		Effects:    []permissions.Effect{permissions.EffectExecution},
		Commands:   []commandplan.Selector{{Executable: "git", ExactArguments: []string{"status"}}},
	}
	scoped, err = scoped.Normalize()
	require.NoError(t, err)
	require.NotEqual(t, original, permissions.Fingerprint(scoped, operation))
}

func newApprovalService(
	t *testing.T,
	opts permissions.ApprovalOptions,
) (*permissions.ApprovalService, *storememory.Store) {
	t.Helper()
	store := storememory.NewStore()
	service, err := permissions.NewApprovalService(store, opts)
	require.NoError(t, err)
	return service, store
}

func approvalContext(ctx context.Context, sessionID string) context.Context {
	return permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Surface:   permissions.SurfaceTUI,
		SessionID: sessionID,
		Profile:   "default",
	})
}

func approvalInput(target string) permissions.EvaluationInput {
	return permissions.EvaluationInput{
		ApprovalReason: "command policy requires approval",
		Operation: permissions.Operation{
			Tool: "run_command", Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
			Effects: []permissions.Effect{permissions.EffectExecution}, Target: target,
		},
	}
}

func waitForPendingApproval(t *testing.T, store *storememory.Store) permissions.ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests, err := store.ListApprovalRequests(context.Background(), permissions.ApprovalQuery{
			Status: permissions.ApprovalPending,
		})
		require.NoError(t, err)
		if len(requests) > 0 {
			return requests[0]
		}
		time.Sleep(time.Millisecond)
	}
	require.FailNow(t, "approval request did not become pending")
	return permissions.ApprovalRequest{}
}

func approvedRequestForInput(
	t *testing.T,
	ctx context.Context,
	input permissions.EvaluationInput,
	id string,
) permissions.ApprovalRequest {
	t.Helper()
	authorization, ok := permissions.FromContext(ctx)
	require.True(t, ok)
	operation, err := input.Operation.Normalize()
	require.NoError(t, err)
	now := time.Now().UTC()
	return permissions.ApprovalRequest{
		ID: id, Fingerprint: permissions.Fingerprint(authorization, operation), Actor: authorization.Actor,
		Profile: authorization.Profile, SessionID: authorization.SessionID, Status: permissions.ApprovalApproved,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func pendingRequestFrom(request permissions.ApprovalRequest) permissions.ApprovalRequest {
	request.Status = permissions.ApprovalPending
	return request
}

func grantForRequest(request permissions.ApprovalRequest, scope permissions.GrantScope) permissions.ApprovalGrant {
	return permissions.ApprovalGrant{
		ID: "grant_existing", RequestID: request.ID, Fingerprint: request.Fingerprint,
		Actor: request.Actor, Profile: request.Profile, SessionID: request.SessionID,
		Scope: scope, Status: permissions.GrantActive,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
}
