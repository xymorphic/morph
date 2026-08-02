package browser

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestNetworkAuthorizationCoordinator_BatchesCompatibleSafeSubresources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var batches [][]networkAuthorizationTarget
	coordinator := newNetworkAuthorizationCoordinator(ctx, func(
		_ context.Context,
		targets []networkAuthorizationTarget,
	) error {
		mu.Lock()
		batches = append(batches, append([]networkAuthorizationTarget(nil), targets...))
		mu.Unlock()
		return nil
	}, nil)
	defer coordinator.Close()

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, path := range []string{"/styles.css", "/app.js"} {
		target := permissions.NetworkTarget{
			Scheme: "http", Host: "localhost", Port: 8089, Path: path, Method: "GET",
			RequestClass: permissions.NetworkRequestSubresource,
		}
		go func() {
			<-start
			results <- coordinator.Authorize(context.Background(), target)
		}()
	}
	close(start)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, batches, 1)
	require.Len(t, batches[0], 2)
	require.Equal(t, 1, batches[0][0].Count)
	require.Equal(t, 1, batches[0][1].Count)
}

func TestNetworkAuthorizationCoordinator_PreservesDuplicateRequestCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	authorized := make(chan []networkAuthorizationTarget, 1)
	coordinator := newNetworkAuthorizationCoordinator(ctx, func(
		_ context.Context,
		targets []networkAuthorizationTarget,
	) error {
		authorized <- targets
		return nil
	}, nil)
	defer coordinator.Close()

	target := permissions.NetworkTarget{
		Scheme: "http", Host: "localhost", Port: 8089, Path: "/app.js", Method: "GET",
		RequestClass: permissions.NetworkRequestSubresource,
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- coordinator.Authorize(context.Background(), target)
		}()
	}
	close(start)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	targets := <-authorized
	require.Equal(t, []networkAuthorizationTarget{{Target: target, Count: 2}}, targets)
}

func TestNetworkAuthorizationCoordinator_SeparatesIncompatibleRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var batches [][]networkAuthorizationTarget
	coordinator := newNetworkAuthorizationCoordinator(ctx, func(
		_ context.Context,
		targets []networkAuthorizationTarget,
	) error {
		mu.Lock()
		batches = append(batches, append([]networkAuthorizationTarget(nil), targets...))
		mu.Unlock()
		return nil
	}, nil)
	defer coordinator.Close()

	start := make(chan struct{})
	results := make(chan error, 2)
	targets := []permissions.NetworkTarget{
		{
			Scheme: "http", Host: "localhost", Port: 8089, Path: "/app.js", Method: "GET",
			RequestClass: permissions.NetworkRequestSubresource,
		},
		{
			Scheme: "http", Host: "localhost", Port: 8089, Path: "/submit", Method: "POST",
			RequestClass: permissions.NetworkRequestSubresource,
		},
	}
	for _, target := range targets {
		go func(target permissions.NetworkTarget) {
			<-start
			results <- coordinator.Authorize(context.Background(), target)
		}(target)
	}
	close(start)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, batches, 2)
	for _, batch := range batches {
		require.Len(t, batch, 1)
	}
}

func TestNetworkAuthorizationCoordinator_DoesNotAuthorizeCancelledRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	authorized := make(chan []networkAuthorizationTarget, 1)
	coordinator := newNetworkAuthorizationCoordinator(ctx, func(
		_ context.Context,
		targets []networkAuthorizationTarget,
	) error {
		authorized <- targets
		return nil
	}, nil)
	defer coordinator.Close()

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	err := coordinator.Authorize(requestCtx, permissions.NetworkTarget{
		Scheme: "http", Host: "localhost", Port: 8089, Path: "/app.js", Method: "GET",
		RequestClass: permissions.NetworkRequestSubresource,
	})
	require.ErrorIs(t, err, context.Canceled)
	select {
	case targets := <-authorized:
		t.Fatalf("cancelled request was authorized: %v", targets)
	case <-time.After(2 * networkAuthorizationBatchWindow):
	}
}

func TestActionBudget_PausesWhileAuthorizationWaits(t *testing.T) {
	ctx, budget := newActionBudget(context.Background(), 40*time.Millisecond)
	defer budget.Close()

	resume := budget.Pause()
	time.Sleep(80 * time.Millisecond)
	require.NoError(t, ctx.Err())
	resume()

	select {
	case <-ctx.Done():
		require.ErrorIs(t, context.Cause(ctx), errBrowserActionTimedOut)
	case <-time.After(time.Second):
		t.Fatal("action budget did not resume")
	}
}

func TestNetworkAuthorizationCoordinator_PausesTheActionBudget(t *testing.T) {
	actionCtx, budget := newActionBudget(context.Background(), 40*time.Millisecond)
	defer budget.Close()
	authorizationStarted := make(chan struct{})
	releaseAuthorization := make(chan struct{})
	coordinator := newNetworkAuthorizationCoordinator(actionCtx, func(
		_ context.Context,
		_ []networkAuthorizationTarget,
	) error {
		close(authorizationStarted)
		<-releaseAuthorization
		return nil
	}, budget.Pause)
	defer coordinator.Close()

	result := make(chan error, 1)
	go func() {
		result <- coordinator.Authorize(context.Background(), permissions.NetworkTarget{
			Scheme: "http", Host: "localhost", Port: 8089, Path: "/", Method: "GET",
			RequestClass: permissions.NetworkRequestNavigation,
		})
	}()
	<-authorizationStarted
	time.Sleep(80 * time.Millisecond)
	require.NoError(t, actionCtx.Err())
	close(releaseAuthorization)
	require.NoError(t, <-result)

	select {
	case <-actionCtx.Done():
		require.ErrorIs(t, context.Cause(actionCtx), errBrowserActionTimedOut)
	case <-time.After(time.Second):
		t.Fatal("action budget did not resume after authorization")
	}
}

func TestGetActionError_ClassifiesTimeoutAndCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
	}{
		{name: "timeout", err: errBrowserActionTimedOut, code: ErrorTimeout},
		{name: "deadline", err: context.DeadlineExceeded, code: ErrorTimeout},
		{name: "cancelled", err: context.Canceled, code: ErrorCancelled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			browserErr, ok := GetError(getActionError(ActionOpen, test.err))
			require.True(t, ok)
			require.Equal(t, test.code, browserErr.Code)
			require.True(t, browserErr.Retryable)
			require.True(t, errors.Is(browserErr, test.err))
		})
	}
}
