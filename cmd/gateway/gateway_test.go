package gateway

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	"github.com/xymorphic/morph/internal/profile"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
)

var errGatewayTestWrite = errors.New("write failed")

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errGatewayTestWrite
}

func TestSetOutputReturnsPreviousAndDiscardsNil(t *testing.T) {
	originalOutput := gatewayOutput
	t.Cleanup(func() { gatewayOutput = originalOutput })
	var output bytes.Buffer

	previous := SetOutput(&output)
	require.Same(t, originalOutput, previous)
	previous = SetOutput(nil)
	require.Same(t, &output, previous)

	_, err := gatewayOutput.Write([]byte("discarded"))
	require.NoError(t, err)
	require.Empty(t, output.String())
}

func TestGatewayCommandShowsHelpWithoutSubcommand(t *testing.T) {
	var output bytes.Buffer
	cmd := NewCommand()
	cmd.Writer = &output

	err := cmd.Run(context.Background(), []string{"gateway"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "Manage external gateway integrations")
}

func TestRuntimeCommandsCallRPCAndPrintStatus(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	stub := &agentstub.AgentServiceStub{
		GatewayStatusResult: rpcclient.GatewayStatus{
			State:        "running",
			Address:      "127.0.0.1",
			Port:         50052,
			TelegramMode: "polling",
			SlackMode:    "socket",
		},
	}
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return stub, nil
	}

	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "status"}))
	expected := "Gateway\n" +
		"  State:       running\n" +
		"  Address:     127.0.0.1\n" +
		"  Port:        50052\n" +
		"  Telegram:    polling\n" +
		"  Slack:       socket\n" +
		"  Last error:  -\n"
	require.Equal(t, expected, output.String())

	output.Reset()
	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "start"}))
	require.True(t, stub.GatewayStarted)
	require.Equal(t, expected, output.String())

	output.Reset()
	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "stop"}))
	require.True(t, stub.GatewayStopped)
	require.Equal(t, expected, output.String())

	output.Reset()
	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "restart"}))
	require.True(t, stub.GatewayRestarted)
	require.Equal(t, expected, output.String())
}

func TestRuntimeCommandPrintsSafeLastError(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{
			GatewayStatusResult: rpcclient.GatewayStatus{
				State:        "failed",
				Address:      "127.0.0.1",
				Port:         50052,
				TelegramMode: "polling",
				SlackMode:    "socket",
				LastError:    "slack socket: [REDACTED]",
			},
		}, nil
	}

	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "status"}))
	require.Contains(t, output.String(), "Last error:  slack socket: [REDACTED]")
}

func TestRuntimeCommandReturnsWriteError(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})
	gatewayOutput = errWriter{}
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{
			GatewayStatusResult: rpcclient.GatewayStatus{State: "running"},
		}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "status"})
	require.ErrorIs(t, err, errGatewayTestWrite)
}

func TestRuntimeCommandsReturnClientAndRPCErrors(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	t.Cleanup(func() { newClient = originalNewClient })
	clientErr := errors.New("client unavailable")
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return nil, clientErr
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "status"})
	require.ErrorIs(t, err, clientErr)

	rpcErr := errors.New("gateway rpc failed")
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{GatewayStatusErr: rpcErr}, nil
	}

	err = NewCommand().Run(context.Background(), []string{"gateway", "status"})
	require.ErrorIs(t, err, rpcErr)

	err = NewCommand().Run(context.Background(), []string{"gateway", "start"})
	require.ErrorIs(t, err, rpcErr)

	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return nil, clientErr
	}

	err = NewCommand().Run(context.Background(), []string{"gateway", "start"})
	require.ErrorIs(t, err, clientErr)
}

func TestSetWebhookTelegramCommandCallsProvider(t *testing.T) {
	setGatewayTestProfileConfig(t, `
gateway:
  telegram:
    botToken: telegram-token
    webhookSecret: webhook-secret
`)
	originalSetTelegramWebhook := setTelegramWebhook
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		setTelegramWebhook = originalSetTelegramWebhook
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	var gotCfg config.GatewayTelegramConfig
	var gotURL string
	setTelegramWebhook = func(_ context.Context, cfg config.GatewayTelegramConfig, url string) error {
		gotCfg = cfg
		gotURL = url
		return nil
	}

	err := NewCommand().Run(
		context.Background(),
		[]string{"gateway", "setwebhook", "telegram", "https://example.com/gateway/telegram/webhook"},
	)

	require.NoError(t, err)
	require.Equal(t, "telegram-token", gotCfg.BotToken)
	require.Equal(t, "webhook-secret", gotCfg.WebhookSecret)
	require.Equal(t, "https://example.com/gateway/telegram/webhook", gotURL)
	require.Equal(t, "telegram webhook set url=https://example.com/gateway/telegram/webhook\n", output.String())

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{"gateway", "setwebhook", "telegram", ""})

	require.NoError(t, err)
	require.Empty(t, gotURL)
	require.Equal(t, "telegram webhook unset\n", output.String())
}

func TestSetWebhookTelegramCommandReturnsProviderErrors(t *testing.T) {
	setGatewayTestProfileConfig(t, `
gateway:
  telegram:
    botToken: telegram-token
    webhookSecret: webhook-secret
`)
	originalSetTelegramWebhook := setTelegramWebhook
	t.Cleanup(func() { setTelegramWebhook = originalSetTelegramWebhook })
	setTelegramWebhook = func(context.Context, config.GatewayTelegramConfig, string) error {
		return errors.New("telegram failed")
	}

	err := NewCommand().Run(
		context.Background(),
		[]string{"gateway", "setwebhook", "telegram", "https://example.com/gateway/telegram/webhook"},
	)
	require.EqualError(t, err, "telegram failed")
}

func TestPairingListCommandCallsRPC(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	stub := &agentstub.AgentServiceStub{
		PairingRequests: []pairing.PendingRequest{{
			Source:      "telegram",
			SenderID:    "123",
			DisplayName: "Ada",
			ExpiresAt:   now,
		}},
		PairedSenders: []pairing.ApprovedSender{{
			Source:      "telegram",
			SenderID:    "456",
			DisplayName: "Grace",
		}},
	}
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "list", "telegram"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "Pending\n")
	require.Contains(t, output.String(), "  SOURCE    SENDER ID  NAME  EXPIRES\n")
	require.Contains(t, output.String(), "  telegram  123        Ada   2026-06-08T12:00:00Z\n")
	require.Contains(t, output.String(), "Approved\n")
	require.Contains(t, output.String(), "  SOURCE    SENDER ID  NAME\n")
	require.Contains(t, output.String(), "  telegram  456        Grace\n")
}

func TestPairingListCommandShowsNoneForEmptySections(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "list"})

	require.NoError(t, err)
	require.Equal(t, "Pending\n  None\n\nApproved\n  None\n", output.String())
}

func TestPairingListCommandFormatsMissingAndLongNames(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{
			PairingRequests: []pairing.PendingRequest{{
				Source:      "telegram",
				SenderID:    "123",
				DisplayName: "A very long pairing display name that should be capped safely",
			}},
			PairedSenders: []pairing.ApprovedSender{{Source: "slack", SenderID: "456"}},
		}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "list"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "A very long pairing display name that...")
	require.Contains(t, output.String(), "slack   456        -")
	require.Contains(t, output.String(), "  -\n\nApproved")
}

func TestPairingListCommandReturnsWriteError(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})
	gatewayOutput = errWriter{}
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "list"})

	require.ErrorIs(t, err, errGatewayTestWrite)
}

func TestPairingApproveRevokeAndClearCommandsCallRPC(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})

	var output bytes.Buffer
	gatewayOutput = &output
	stub := &agentstub.AgentServiceStub{
		ApprovedPairing: rpcclient.GatewayPairedSender{Source: "telegram", SenderID: "123"},
		PairingApproved: true,
	}
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return stub, nil
	}

	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "pairing", "approve", "telegram", "12345678"}))
	require.Equal(t, "telegram", stub.PairingSource)
	require.Equal(t, "12345678", stub.PairingCode)
	require.Contains(t, output.String(), "approved telegram 123\n")

	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "pairing", "revoke", "telegram", "123"}))
	require.Equal(t, "telegram", stub.RevokedPairingSource)
	require.Equal(t, "123", stub.RevokedPairingSender)

	require.NoError(t, NewCommand().Run(context.Background(), []string{"gateway", "pairing", "clear-pending", "telegram"}))
	require.Equal(t, "telegram", stub.ClearedPairingSource)
}

func TestPairingApproveRevokeAndClearCommandsReturnWriteErrors(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	originalOutput := gatewayOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		gatewayOutput = originalOutput
	})
	gatewayOutput = errWriter{}
	stub := &agentstub.AgentServiceStub{
		ApprovedPairing: rpcclient.GatewayPairedSender{Source: "telegram", SenderID: "123"},
		PairingApproved: true,
	}
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "approve", "telegram", "12345678"})
	require.ErrorIs(t, err, errGatewayTestWrite)

	err = NewCommand().Run(context.Background(), []string{"gateway", "pairing", "revoke", "telegram", "123"})
	require.ErrorIs(t, err, errGatewayTestWrite)

	err = NewCommand().Run(context.Background(), []string{"gateway", "pairing", "clear-pending"})
	require.ErrorIs(t, err, errGatewayTestWrite)
}

func TestPairingCommandsRejectMissingRequiredArgs(t *testing.T) {
	originalNewClient := newClient
	t.Cleanup(func() { newClient = originalNewClient })
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		t.Fatal("gateway command should reject missing args before opening RPC client")
		return nil, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "approve", "telegram"})
	require.EqualError(t, err, "source and code are required")

	err = NewCommand().Run(context.Background(), []string{"gateway", "pairing", "revoke", "telegram"})
	require.EqualError(t, err, "source and sender id are required")
}

func TestPairingApproveCommandRejectsMissingMatch(t *testing.T) {
	setGatewayTestProfile(t)
	originalNewClient := newClient
	t.Cleanup(func() { newClient = originalNewClient })
	newClient = func(context.Context, *config.Config) (gatewayClient, error) {
		return &agentstub.AgentServiceStub{}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"gateway", "pairing", "approve", "telegram", "12345678"})

	require.EqualError(t, err, "no pending gateway pairing matched code")
}

func setGatewayTestProfile(t *testing.T) {
	t.Helper()
	original := profile.Active()
	t.Cleanup(func() { profile.SetActive(original) })
	profile.SetActive(profile.Profile{
		Name:        "test",
		HomeDir:     t.TempDir(),
		RuntimePath: t.TempDir() + "/runtime.json",
	})
}

func setGatewayTestProfileConfig(t *testing.T, data string) {
	t.Helper()
	original := profile.Active()
	t.Cleanup(func() { profile.SetActive(original) })
	homeDir := t.TempDir()
	configPath := filepath.Join(homeDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(data), 0o600))
	profile.SetActive(profile.Profile{
		Name:        "test",
		HomeDir:     homeDir,
		ConfigPath:  configPath,
		EnvPath:     filepath.Join(homeDir, ".env"),
		RuntimePath: filepath.Join(homeDir, "runtime.json"),
	})
}
