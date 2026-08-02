package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
)

func TestGatewayService_PairingListApproveRevokeAndClear(t *testing.T) {
	now := time.Now().UTC()
	stub := &agentstub.AgentServiceStub{
		PairingRequests: []pairing.PendingRequest{{
			Source:      "telegram",
			SenderID:    "123",
			DisplayName: "Ada",
			CreatedAt:   now,
			LastSeenAt:  now,
			ExpiresAt:   now.Add(time.Hour),
		}},
		PairedSenders: []pairing.ApprovedSender{{
			Source:      "telegram",
			SenderID:    "456",
			DisplayName: "Grace",
			CreatedAt:   now,
			UpdatedAt:   now,
		}},
	}
	svc := newAllowedServiceWithOptions(stub, ServiceOptions{GatewayPairingSecret: "secret"})
	gatewayService := NewGatewayService(svc)

	list, err := gatewayService.ListPairings(context.Background(), &morphpb.ListGatewayPairingsRequest{Source: "telegram"})
	require.NoError(t, err)
	require.Len(t, list.GetPending(), 1)
	require.Equal(t, "123", list.GetPending()[0].GetSenderId())
	require.NotNil(t, list.GetPending()[0].GetCreatedAt())
	require.Len(t, list.GetApproved(), 1)
	require.Equal(t, "456", list.GetApproved()[0].GetSenderId())
	require.NotNil(t, list.GetApproved()[0].GetCreatedAt())

	code, err := pairing.NewManager(pairing.Options{Store: stub, Secret: "secret"}).Code("telegram", "123", time.Now().UTC())
	require.NoError(t, err)
	approved, err := gatewayService.ApprovePairing(context.Background(), &morphpb.ApproveGatewayPairingRequest{
		Source: "telegram",
		Code:   code,
	})
	require.NoError(t, err)
	require.True(t, approved.GetApproved())
	require.Equal(t, "123", approved.GetSender().GetSenderId())

	_, err = gatewayService.RevokePairing(context.Background(), &morphpb.RevokeGatewayPairingRequest{
		Source:   "telegram",
		SenderId: "123",
	})
	require.NoError(t, err)
	require.Equal(t, "telegram", stub.RevokedPairingSource)
	require.Equal(t, "123", stub.RevokedPairingSender)

	_, err = gatewayService.ClearPendingPairings(context.Background(), &morphpb.ClearPendingGatewayPairingsRequest{Source: "telegram"})
	require.NoError(t, err)
	require.Equal(t, "telegram", stub.ClearedPairingSource)

	emptyList, err := gatewayService.ListPairings(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, emptyList.GetPending(), 0)
	require.Len(t, emptyList.GetApproved(), 1)

	_, err = gatewayService.ClearPendingPairings(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "", stub.ClearedPairingSource)

	require.Nil(t, timestampOrNil(time.Time{}))
}

func TestGatewayService_RuntimeStatusStartStopAndRestart(t *testing.T) {
	cfg := config.NewDefaultConfig().Gateway
	cfg.Enabled = true
	cfg.AuthToken = " gateway-auth-token "
	cfg.Address = " 127.0.0.1 "
	cfg.Port = 50052
	runtime := &gatewayRuntimeStub{
		status: gateway.Status{
			State:        gateway.StateStopped,
			Address:      cfg.Address,
			Port:         cfg.Port,
			SlackMode:    cfg.Slack.Mode,
			TelegramMode: cfg.Telegram.Mode,
		},
	}
	svc := newAllowedServiceWithOptions(&agentstub.AgentServiceStub{}, ServiceOptions{
		GatewayConfig:  cfg,
		GatewayRuntime: runtime,
	})
	gatewayService := NewGatewayService(svc)

	statusResp, err := gatewayService.Status(context.Background(), &morphpb.GetGatewayStatusRequest{})
	require.NoError(t, err)
	require.Equal(t, "stopped", statusResp.GetStatus().GetState())
	require.Equal(t, int32(50052), statusResp.GetStatus().GetPort())

	startCtx, cancelStart := context.WithCancel(context.Background())
	startResp, err := gatewayService.Start(startCtx, &morphpb.StartGatewayRequest{})
	require.NoError(t, err)
	require.True(t, runtime.started)
	require.Equal(t, "127.0.0.1", runtime.startCfg.Address)
	require.Equal(t, "gateway-auth-token", runtime.startCfg.AuthToken)
	require.Equal(t, "running", startResp.GetStatus().GetState())
	cancelStart()
	requireRuntimeContextActive(t, runtime.startCtx)

	stopResp, err := gatewayService.Stop(context.Background(), &morphpb.StopGatewayRequest{})
	require.NoError(t, err)
	require.True(t, runtime.stopped)
	require.Equal(t, "stopped", stopResp.GetStatus().GetState())

	runtime.started = false
	runtime.stopped = false
	restartCtx, cancelRestart := context.WithCancel(context.Background())
	restartResp, err := gatewayService.Restart(restartCtx, &morphpb.RestartGatewayRequest{})
	require.NoError(t, err)
	require.True(t, runtime.started)
	require.True(t, runtime.stopped)
	require.Equal(t, "running", restartResp.GetStatus().GetState())
	require.Same(t, restartCtx, runtime.stopCtx)
	cancelRestart()
	requireRuntimeContextActive(t, runtime.startCtx)
}

func TestGatewayService_RejectsNilService(t *testing.T) {
	resp, err := (*GatewayService)(nil).Status(context.Background(), nil)
	requireStatusError(t, err, codes.Internal, "service is required")
	require.Nil(t, resp)

	resp, err = (&GatewayService{}).Status(context.Background(), nil)
	requireStatusError(t, err, codes.Internal, "service is required")
	require.Nil(t, resp)
}

func requireRuntimeContextActive(t *testing.T, ctx context.Context) {
	t.Helper()

	select {
	case <-ctx.Done():
		t.Fatal("runtime context should not be canceled with the RPC request")
	default:
	}
}
