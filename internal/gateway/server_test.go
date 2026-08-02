package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/gateway/dispatch"
)

func TestGatewayHTTPServerHealth(t *testing.T) {
	server := newHTTPServer(testGatewayConfig(), nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(lis)
	}()

	resp, err := http.Get("http://" + lis.Addr().String() + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, server.Shutdown(shutdownCtx))
	require.ErrorIs(t, <-done, http.ErrServerClosed)
}

func TestGatewayHTTPServerCloseStopsServing(t *testing.T) {
	server := newHTTPServer(testGatewayConfig(), nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(lis)
	}()

	require.NoError(t, server.Close())

	require.ErrorIs(t, <-done, http.ErrServerClosed)
}

func TestGatewayHTTPServerShutdownReturnsDispatcherDrainError(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher := dispatch.New(dispatch.Options{Capacity: 1, Workers: 1})
	dispatcher.Start(runCtx)
	_, err := dispatcher.Enqueue(dispatch.Job{
		ID: "blocked",
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	require.NoError(t, err)
	ctx, stop := context.WithTimeout(context.Background(), time.Nanosecond)
	defer stop()
	server := &gatewayHTTPServer{
		server:     &http.Server{},
		cancel:     cancel,
		dispatcher: dispatcher,
	}

	err = server.Shutdown(ctx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestGatewayHTTPServerShutdownDrainsDispatcherBeforeCancelingRuntime(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	dispatcher := dispatch.New(dispatch.Options{Capacity: 1, Workers: 1})
	dispatcher.Start(runCtx)
	started := make(chan struct{})
	release := make(chan struct{})
	jobErr := make(chan error, 1)
	_, err := dispatcher.Enqueue(dispatch.Job{
		ID: "drain",
		Run: func(ctx context.Context) error {
			close(started)
			<-release
			jobErr <- ctx.Err()
			return nil
		},
	})
	require.NoError(t, err)
	server := &gatewayHTTPServer{
		server:     &http.Server{},
		cancel:     cancel,
		dispatcher: dispatcher,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Shutdown(context.Background())
	}()

	<-started
	close(release)

	require.NoError(t, <-done)
	require.NoError(t, <-jobErr)
	require.ErrorIs(t, runCtx.Err(), context.Canceled)
}

func TestHTTPComponentForcesCloseWhenShutdownTimesOut(t *testing.T) {
	server := &timeoutHTTPServer{}
	component := newHTTPComponent(testGatewayConfig(), server, noopGatewayListener{}, time.Millisecond)

	err := component.stop(context.Background())

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, server.closed)
}

func TestRunComponentsReturnsRunErrorAndStopsSiblings(t *testing.T) {
	stopped := false
	err := runComponents(context.Background(), []component{
		{
			name: "failing",
			run: func(context.Context) error {
				return errors.New("run failed")
			},
			stop: func(context.Context) error {
				stopped = true
				return errors.New("stop failed")
			},
		},
	})

	require.EqualError(t, err, "failing: run failed")
	require.True(t, stopped)
}

func TestRunComponentsWaitsForCancellationWhenNoComponentsStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runComponents(ctx, nil)
	}()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gateway components did not stop after cancellation")
	}
}

func TestRunComponentsStopsNilRunComponentOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runComponents(ctx, []component{{name: "idle"}})
	}()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gateway component did not stop after cancellation")
	}
}

func TestWaitForComponentStopReturnsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- waitForComponentStop(ctx, struct{}{})
	}()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gateway component adapter did not stop after cancellation")
	}
}

type timeoutHTTPServer struct {
	closed bool
}

func (s *timeoutHTTPServer) Serve(net.Listener) error {
	return http.ErrServerClosed
}

func (s *timeoutHTTPServer) Shutdown(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *timeoutHTTPServer) Close() error {
	s.closed = true
	return nil
}

type noopGatewayListener struct{}

func (noopGatewayListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (noopGatewayListener) Close() error {
	return nil
}

func (noopGatewayListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}
