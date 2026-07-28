package e2e

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	models "github.com/wandxy/morph/internal/model"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/logutils"
	"google.golang.org/grpc"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestNewRPCHarness_RealClientChatSmoke(t *testing.T) {
	h, err := NewRPCHarness(context.Background(), HarnessOptions{
		Spec:        testHarnessSpec(t),
		Config:      testHarnessConfig(),
		ModelClient: NewTextClient("hello over rpc"),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	client, err := h.Client(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	reply, err := runRPCSessionTurn(context.Background(), client, "default", "hello", "", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "hello over rpc", reply)
}

func TestNewRPCHarness_StreamsTaggedProgressForSubmittedEntry(t *testing.T) {
	h, err := NewRPCHarness(context.Background(), HarnessOptions{
		Spec:   testHarnessSpec(t),
		Config: testHarnessConfig(),
		ModelClient: NewClient(StreamStep("streamed reply", models.StreamDelta{
			Channel: models.StreamChannelAssistant,
			Text:    "streamed reply",
		})),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	client, err := h.Client(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	stream := true
	var progress []rpcclient.Event
	reply, err := runRPCSessionTurn(
		context.Background(),
		client,
		"default",
		"hello",
		"",
		&stream,
		func(event rpcclient.Event) error {
			if event.TraceEvent != nil {
				return nil
			}
			progress = append(progress, event)
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "streamed reply", reply)
	require.Equal(t, []rpcclient.Event{{
		Kind:    "text_delta",
		Channel: string(models.StreamChannelAssistant),
		Text:    "streamed reply",
	}}, progress)
}

type queueRPCModelClient struct {
	mu       sync.Mutex
	requests []models.Request
	started  chan struct{}
	release  chan struct{}
}

func (c *queueRPCModelClient) Complete(
	ctx context.Context,
	request models.Request,
) (*models.Response, error) {
	c.mu.Lock()
	call := len(c.requests)
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if call == 0 {
		close(c.started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.release:
		}
	}
	return &models.Response{OutputText: "reply"}, nil
}

func (c *queueRPCModelClient) CompleteStream(
	ctx context.Context,
	request models.Request,
	onDelta func(models.StreamDelta),
) (*models.Response, error) {
	return c.Complete(ctx, request)
}

func (c *queueRPCModelClient) prompts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	prompts := make([]string, 0, len(c.requests))
	for _, request := range c.requests {
		for index := len(request.Messages) - 1; index >= 0; index-- {
			if request.Messages[index].Role == morphmsg.RoleUser {
				prompts = append(prompts, request.Messages[index].Content)
				break
			}
		}
	}
	return prompts
}

func TestNewRPCHarness_ReconnectPreservesFIFOAndDeduplicatesSubmission(t *testing.T) {
	modelClient := &queueRPCModelClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	h, err := NewRPCHarness(context.Background(), HarnessOptions{
		Spec:        testHarnessSpec(t),
		Config:      testHarnessConfig(),
		ModelClient: modelClient,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	firstClient, err := h.Client(context.Background())
	require.NoError(t, err)
	first, err := firstClient.SubmitMessage(context.Background(), rpcclient.SubmitMessageOptions{
		SessionID:          "default",
		Message:            "first",
		ClientSubmissionID: "submission-first",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	select {
	case <-modelClient.started:
	case <-time.After(time.Second):
		t.Fatal("first queued turn did not start")
	}
	retry, err := firstClient.SubmitMessage(context.Background(), rpcclient.SubmitMessageOptions{
		SessionID:          "default",
		Message:            "first",
		ClientSubmissionID: "submission-first",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, retry.ID)
	second, err := firstClient.SubmitMessage(context.Background(), rpcclient.SubmitMessageOptions{
		SessionID:          "default",
		Message:            "second",
		ClientSubmissionID: "submission-second",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)

	state, err := firstClient.State(context.Background(), "default")
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRun)
	require.Equal(t, first.ID, state.ActiveRun.QueueEntryID)
	require.Equal(t, 1, state.QueueDepth)
	require.NoError(t, firstClient.Close())

	reconnected, err := h.Client(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reconnected.Close())
	})
	state, err = reconnected.State(context.Background(), "default")
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRun)
	require.Equal(t, first.ID, state.ActiveRun.QueueEntryID)

	observeCtx, cancelObserve := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelObserve()
	terminal := make(map[string]struct{})
	observeDone := make(chan error, 1)
	stopObservation := errors.New("queue observation complete")
	go func() {
		observeDone <- reconnected.Observe(observeCtx, "default", state.Cursor, func(event rpcclient.SessionEvent) error {
			if event.Queue == nil || !isTerminalRPCQueueStatus(event.Queue.Status) {
				return nil
			}
			terminal[event.Queue.ID] = struct{}{}
			if _, firstDone := terminal[first.ID]; firstDone {
				if _, secondDone := terminal[second.ID]; secondDone {
					return stopObservation
				}
			}
			return nil
		})
	}()
	close(modelClient.release)
	require.ErrorIs(t, <-observeDone, stopObservation)
	require.Equal(t, []string{"first", "second"}, modelClient.prompts())
}

func TestRPCAdapter_TerminalErrors(t *testing.T) {
	queueTests := []struct {
		entry rpcclient.SessionQueueEntry
		want  string
	}{
		{
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusFailed, LastError: "provider unavailable"},
			want:  "provider unavailable",
		},
		{
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusFailed},
			want:  "session run failed",
		},
		{
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusCancelled},
			want:  "session run cancelled",
		},
		{entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusCompleted}},
	}
	for _, test := range queueTests {
		err := rpcQueueTerminalError(test.entry)
		if test.want == "" {
			require.NoError(t, err)
			continue
		}
		require.EqualError(t, err, test.want)
	}

	runTests := []struct {
		run  rpcclient.SessionActiveRun
		want string
	}{
		{
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusFailed, LastError: "provider unavailable"},
			want: "provider unavailable",
		},
		{
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusFailed},
			want: "session run failed",
		},
		{
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusInterrupted, Reason: "daemon_restart"},
			want: "session run interrupted: daemon_restart",
		},
		{
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusInterrupted},
			want: "session run interrupted",
		},
		{
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusCancelled},
			want: "session run cancelled",
		},
		{run: rpcclient.SessionActiveRun{Status: agentsession.RunStatusCompleted}},
	}
	for _, test := range runTests {
		err := rpcRunTerminalError(test.run)
		if test.want == "" {
			require.NoError(t, err)
			continue
		}
		require.EqualError(t, err, test.want)
	}
}

func TestNewRPCHarness_ErrorsAndHelpers(t *testing.T) {
	t.Run("base harness error", func(t *testing.T) {
		_, err := NewRPCHarness(context.Background(), HarnessOptions{})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e entrypoint is required")
	})

	t.Run("listen error", func(t *testing.T) {
		original := rpcListen
		rpcListen = func(string, string) (net.Listener, error) {
			return nil, errors.New("listen failed")
		}
		t.Cleanup(func() {
			rpcListen = original
		})

		_, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:        testHarnessSpec(t),
			Config:      testHarnessConfig(),
			ModelClient: NewTextClient("ok"),
		})
		require.Error(t, err)
		assert.EqualError(t, err, "listen failed")
	})

	t.Run("base harness without full service api", func(t *testing.T) {
		originalBase := newBaseHarness
		originalListen := rpcListen
		newBaseHarness = func(context.Context, HarnessOptions) (*Harness, error) {
			return &Harness{
				agent:      harnessAgentStub{reply: "ok"},
				restoreEnv: func() {},
			}, nil
		}
		rpcListen = func(string, string) (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		}
		t.Cleanup(func() {
			newBaseHarness = originalBase
			rpcListen = originalListen
		})

		_, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:        testHarnessSpec(t),
			Config:      testHarnessConfig(),
			ModelClient: NewTextClient("ok"),
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e rpc harness requires a full agent service")
	})

	t.Run("non tcp listener", func(t *testing.T) {
		originalListen := rpcListen
		originalServe := grpcServe
		rpcListen = func(string, string) (net.Listener, error) {
			return stubListener{addr: stubAddr("pipe")}, nil
		}
		grpcServe = func(*grpc.Server, net.Listener) error { return nil }
		t.Cleanup(func() {
			rpcListen = originalListen
			grpcServe = originalServe
		})

		_, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:        testHarnessSpec(t),
			Config:      testHarnessConfig(),
			ModelClient: NewTextClient("ok"),
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e rpc listener must be tcp")
	})

	t.Run("client nil harness", func(t *testing.T) {
		client, err := (*RPCHarness)(nil).Client(context.Background())
		require.Error(t, err)
		assert.Nil(t, client)
		assert.EqualError(t, err, "e2e rpc harness is required")
	})

	t.Run("close nil harness", func(t *testing.T) {
		assert.NoError(t, (*RPCHarness)(nil).Close())
		assert.Empty(t, (*RPCHarness)(nil).Address())
		assert.Zero(t, (*RPCHarness)(nil).Port())
		assert.Empty(t, (*RPCHarness)(nil).ConfigFileContents())
	})

	t.Run("close returns serve error", func(t *testing.T) {
		originalServe := grpcServe
		grpcServe = func(*grpc.Server, net.Listener) error { return errors.New("serve failed") }
		t.Cleanup(func() {
			grpcServe = originalServe
		})

		h, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:        testHarnessSpec(t),
			Config:      testHarnessConfig(),
			ModelClient: NewTextClient("ok"),
		})
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)
		err = h.Close()
		require.Error(t, err)
		assert.EqualError(t, err, "serve failed")
	})

	t.Run("close ignores server stopped", func(t *testing.T) {
		originalServe := grpcServe
		grpcServe = func(*grpc.Server, net.Listener) error { return grpc.ErrServerStopped }
		t.Cleanup(func() {
			grpcServe = originalServe
		})

		h, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:        testHarnessSpec(t),
			Config:      testHarnessConfig(),
			ModelClient: NewTextClient("ok"),
		})
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
		require.NoError(t, h.Close())
	})

	t.Run("config file contents", func(t *testing.T) {
		h := &RPCHarness{address: "127.0.0.1", port: -1234}
		assert.Contains(t, h.ConfigFileContents(), "address: 127.0.0.1")
		assert.Contains(t, h.ConfigFileContents(), "port: -1234")
		assert.Equal(t, "127.0.0.1", h.Address())
		assert.Equal(t, -1234, h.Port())
	})
}
