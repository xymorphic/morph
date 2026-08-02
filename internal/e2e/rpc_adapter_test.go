package e2e

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	morphagent "github.com/xymorphic/morph/internal/agent"
	models "github.com/xymorphic/morph/internal/model"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

type adapterHarness struct {
	adapter       RootChatAdapter
	createSession func(context.Context, string) error
	messages      func(context.Context, string) ([]morphmsg.Message, error)
	close         func() error
}

func TestRPCAdapter_HappyPathMatchesDirectHarness(t *testing.T) {
	builders := map[string]func(*testing.T) adapterHarness{
		"direct": func(t *testing.T) adapterHarness {
			t.Helper()

			h, err := NewHarness(context.Background(), HarnessOptions{
				Spec:        testHarnessSpec(t),
				Config:      testHarnessConfig(),
				ModelClient: NewTextClient("hello from adapter"),
			})
			require.NoError(t, err)

			service, ok := h.agent.(morphagent.ServiceAPI)
			require.True(t, ok)

			return adapterHarness{
				adapter: h,
				createSession: func(ctx context.Context, id string) error {
					_, createErr := service.CreateSession(ctx, id)
					return createErr
				},
				messages: h.Messages,
				close:    h.Close,
			}
		},
		"rpc": func(t *testing.T) adapterHarness {
			t.Helper()

			h, err := NewRPCHarness(context.Background(), HarnessOptions{
				Spec:        testHarnessSpec(t),
				Config:      testHarnessConfig(),
				ModelClient: NewTextClient("hello from adapter"),
			})
			require.NoError(t, err)

			return adapterHarness{
				adapter: NewRPCAdapter(h),
				createSession: func(ctx context.Context, id string) error {
					client, clientErr := h.Client(ctx)
					require.NoError(t, clientErr)
					defer func() {
						require.NoError(t, client.Close())
					}()

					autoSwitch := false
					_, createErr := client.Session.CreateWithOptions(ctx, rpcclient.CreateSessionOptions{
						ID:         id,
						AutoSwitch: &autoSwitch,
					})
					return createErr
				},
				messages: h.Messages,
				close:    h.Close,
			}
		},
	}

	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			adapter := build(t)
			t.Cleanup(func() {
				require.NoError(t, adapter.close())
			})

			result, err := adapter.adapter.Send(context.Background(), RootChatRequest{Message: "hello adapter"})
			require.NoError(t, err)
			assert.Equal(t, "hello from adapter", result.Reply)
			assert.Equal(t, "default", result.SessionID)

			messages, err := adapter.messages(context.Background(), result.SessionID)
			require.NoError(t, err)
			require.Len(t, messages, 2)
			assert.Equal(t, "hello adapter", messages[0].Content)
			assert.Equal(t, "hello from adapter", messages[1].Content)
		})
	}
}

func TestRPCAdapter_ExplicitSessionMatchesDirectHarness(t *testing.T) {
	builders := map[string]func(*testing.T) adapterHarness{
		"direct": func(t *testing.T) adapterHarness {
			t.Helper()

			h, err := NewHarness(context.Background(), HarnessOptions{
				Spec:        testHarnessSpec(t),
				Config:      testHarnessConfig(),
				ModelClient: NewTextClient("session specific"),
			})
			require.NoError(t, err)

			service, ok := h.agent.(morphagent.ServiceAPI)
			require.True(t, ok)

			return adapterHarness{
				adapter: h,
				createSession: func(ctx context.Context, id string) error {
					_, createErr := service.CreateSession(ctx, id)
					return createErr
				},
				messages: h.Messages,
				close:    h.Close,
			}
		},
		"rpc": func(t *testing.T) adapterHarness {
			t.Helper()

			h, err := NewRPCHarness(context.Background(), HarnessOptions{
				Spec:        testHarnessSpec(t),
				Config:      testHarnessConfig(),
				ModelClient: NewTextClient("session specific"),
			})
			require.NoError(t, err)

			return adapterHarness{
				adapter: NewRPCAdapter(h),
				createSession: func(ctx context.Context, id string) error {
					client, clientErr := h.Client(ctx)
					require.NoError(t, clientErr)
					defer func() {
						require.NoError(t, client.Close())
					}()

					autoSwitch := false
					_, createErr := client.Session.CreateWithOptions(ctx, rpcclient.CreateSessionOptions{
						ID:         id,
						AutoSwitch: &autoSwitch,
					})
					return createErr
				},
				messages: h.Messages,
				close:    h.Close,
			}
		},
	}

	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			adapter := build(t)
			t.Cleanup(func() {
				require.NoError(t, adapter.close())
			})

			sessionID := "ses_123456789012345678901"
			require.NoError(t, adapter.createSession(context.Background(), sessionID))

			result, err := adapter.adapter.Send(context.Background(), RootChatRequest{
				Message:   "hello explicit",
				SessionID: sessionID,
			})
			require.NoError(t, err)
			assert.Equal(t, "session specific", result.Reply)
			assert.Equal(t, sessionID, result.SessionID)

			messages, err := adapter.messages(context.Background(), sessionID)
			require.NoError(t, err)
			require.Len(t, messages, 2)
			assert.Equal(t, []morphmsg.Role{morphmsg.RoleUser, morphmsg.RoleAssistant}, []morphmsg.Role{messages[0].Role, messages[1].Role})
		})
	}
}

func TestRPCAdapter_Errors(t *testing.T) {
	t.Run("nil adapter", func(t *testing.T) {
		result, err := (*RPCAdapter)(nil).Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.Equal(t, RootChatResult{}, result)
		assert.EqualError(t, err, "e2e rpc adapter is required")
	})

	t.Run("invalid request", func(t *testing.T) {
		adapter := NewRPCAdapter(&RPCHarness{})
		result, err := adapter.Send(context.Background(), RootChatRequest{})
		require.Error(t, err)
		assert.Equal(t, RootChatResult{}, result)
		assert.EqualError(t, err, "e2e root chat message is required")
	})

	t.Run("client creation fails", func(t *testing.T) {
		adapter := NewRPCAdapter(&RPCHarness{address: "127.0.0.1", port: -1})
		result, err := adapter.Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.Equal(t, RootChatResult{}, result)
		assert.EqualError(t, err, "rpc port must be greater than zero")
	})

	t.Run("respond error", func(t *testing.T) {
		h, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:        testHarnessSpec(t),
			Config:      testHarnessConfig(),
			ModelClient: NewClient(Step{Err: errors.New("respond failed")}),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, h.Close())
		})

		result, sendErr := NewRPCAdapter(h).Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, sendErr)
		assert.Equal(t, RootChatResult{}, result)
		assert.EqualError(t, sendErr, "respond failed")
	})

	t.Run("current session lookup fails", func(t *testing.T) {
		originalNewClient := rpcclientNewClient
		rpcclientNewClient = func(context.Context, rpcclient.Options) (rpcClientAPI, error) {
			return rpcAdapterClientStub{
				reply:      "ok",
				currentErr: errors.New("current failed"),
			}, nil
		}
		t.Cleanup(func() {
			rpcclientNewClient = originalNewClient
		})

		adapter := NewRPCAdapter(&RPCHarness{address: "127.0.0.1", port: 1234})
		result, err := adapter.Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.Equal(t, RootChatResult{}, result)
		assert.EqualError(t, err, "current failed")
	})

	t.Run("captures stream events", func(t *testing.T) {
		h, err := NewRPCHarness(context.Background(), HarnessOptions{
			Spec:   testHarnessSpec(t),
			Config: testHarnessConfig(),
			ModelClient: NewClient(Step{
				Response: &models.Response{OutputText: "streamed"},
				Stream: []models.StreamDelta{
					{Channel: models.StreamChannelReasoning, Text: "thinking"},
					{Channel: models.StreamChannelAssistant, Text: "streamed"},
				},
			}),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, h.Close())
		})

		stream := true
		result, sendErr := NewRPCAdapter(h).Send(context.Background(), RootChatRequest{
			Message: "hello",
			Stream:  &stream,
		})
		require.NoError(t, sendErr)
		assert.Equal(t, "streamed", result.Reply)
		assert.Equal(t, []Event{
			{Channel: "reasoning", Text: "thinking"},
			{Channel: "assistant", Text: "streamed"},
		}, result.Events)
	})
}
