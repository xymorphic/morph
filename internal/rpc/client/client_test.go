package client

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	protomock "github.com/wandxy/morph/internal/mocks/proto"
	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/trace"
)

func TestClient_CreateSessionReturnsSummary(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		CreateResp: &morphpb.CreateSessionResponse{
			Session: &morphpb.SessionSummary{
				Id:            "project-a",
				Title:         "Project Planning",
				TitleSource:   storage.SessionTitleSourceGenerated,
				UpdatedAtUnix: time.Unix(10, 0).UTC().Unix(),
			},
		},
	}
	client := NewSessionService(stub)

	session, err := client.Create(context.Background(), "project-a")

	require.NoError(t, err)
	require.Equal(t, "project-a", session.ID)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, storage.SessionTitleSourceGenerated, session.TitleSource)
	require.Equal(t, "project-a", stub.CreateReq.GetId())
	require.Nil(t, stub.CreateReq.AutoSwitch)
}

func TestClient_CreateSessionWithOptionsSendsAutoSwitch(t *testing.T) {
	autoSwitch := false
	stub := &protomock.MorphServiceClientStub{
		CreateResp: &morphpb.CreateSessionResponse{
			Session: &morphpb.SessionSummary{Id: "project-a"},
		},
	}
	client := NewSessionService(stub)

	session, err := client.CreateWithOptions(context.Background(), CreateSessionOptions{
		ID:         " project-a ",
		AutoSwitch: &autoSwitch,
	})

	require.NoError(t, err)
	require.Equal(t, "project-a", session.ID)
	require.Equal(t, "project-a", stub.CreateReq.GetId())
	require.NotNil(t, stub.CreateReq.AutoSwitch)
	require.False(t, stub.CreateReq.GetAutoSwitch())
}

func TestClient_CreateSessionWithOptionsPropagatesError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	session, err := client.CreateWithOptions(context.Background(), CreateSessionOptions{ID: "project-a"})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, session.ID)
	require.Equal(t, "project-a", stub.CreateReq.GetId())
}

func TestClient_CreateSessionWithOptionsReturnsEmptySessionForMissingSummary(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{CreateResp: &morphpb.CreateSessionResponse{}}
	client := NewSessionService(stub)

	session, err := client.CreateWithOptions(context.Background(), CreateSessionOptions{ID: "project-a"})

	require.NoError(t, err)
	require.Empty(t, session.ID)
	require.Equal(t, "project-a", stub.CreateReq.GetId())
}

func TestClient_CreateSessionWithOptionsRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).CreateWithOptions(context.Background(), CreateSessionOptions{})

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_ListSessionsReturnsItems(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		ListResp: &morphpb.ListSessionsResponse{
			Sessions: []*morphpb.SessionSummary{
				{
					Id:            "default",
					OriginSource:  storage.SessionOriginSourceCLI,
					Title:         "Daily Planning",
					TitleSource:   storage.SessionTitleSourceGenerated,
					UpdatedAtUnix: 10,
				},
				{Id: "project-a", UpdatedAtUnix: 20},
			},
		},
	}
	client := NewSessionService(stub)

	sessions, err := client.List(context.Background())

	require.NoError(t, err)
	require.NotNil(t, stub.ListReq)
	require.False(t, stub.ListReq.GetArchived())
	require.Len(t, sessions, 2)
	require.Equal(t, "default", sessions[0].ID)
	require.Equal(t, storage.SessionOriginSourceCLI, sessions[0].Origin.Source)
	require.Equal(t, "Daily Planning", sessions[0].Title)
	require.Equal(t, storage.SessionTitleSourceGenerated, sessions[0].TitleSource)
	require.Equal(t, "project-a", sessions[1].ID)
}

func TestClient_CreateSessionWithOptionsSendsOriginSource(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		CreateResp: &morphpb.CreateSessionResponse{
			Session: &morphpb.SessionSummary{Id: "project-a"},
		},
	}
	client := NewSessionService(stub)

	session, err := client.CreateWithOptions(context.Background(), CreateSessionOptions{
		ID:           "project-a",
		OriginSource: storage.SessionOriginSourceTUI,
	})

	require.NoError(t, err)
	require.Equal(t, "project-a", session.ID)
	require.NotNil(t, stub.CreateReq)
	require.Equal(t, storage.SessionOriginSourceTUI, stub.CreateReq.GetOriginSource())
}

func TestClient_ListSessionsSendsOriginSourceFilter(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		ListResp: &morphpb.ListSessionsResponse{},
	}
	client := NewSessionService(stub)

	_, err := client.List(context.Background(), SessionListOptions{
		OriginSource: storage.SessionOriginSourceAutomation,
	})

	require.NoError(t, err)
	require.NotNil(t, stub.ListReq)
	require.Equal(t, storage.SessionOriginSourceAutomation, stub.ListReq.GetOriginSource())
}

func TestClient_ListSessionsWithArchivedOptionSendsArchivedFlagAndMarksItems(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		ListResp: &morphpb.ListSessionsResponse{
			Sessions: []*morphpb.SessionSummary{
				{Id: "project-a", Title: "Archived Planning", UpdatedAtUnix: 20},
			},
		},
	}
	client := NewSessionService(stub)
	archived := true

	sessions, err := client.List(context.Background(), SessionListOptions{Archived: &archived})

	require.NoError(t, err)
	require.NotNil(t, stub.ListReq)
	require.True(t, stub.ListReq.GetArchived())
	require.Len(t, sessions, 1)
	require.Equal(t, "project-a", sessions[0].ID)
	require.True(t, sessions[0].Archived)
}

func TestClient_ListSessionsWithArchivedOptionRequiresClient(t *testing.T) {
	archived := true

	_, err := (*SessionService)(nil).List(context.Background(), SessionListOptions{Archived: &archived})

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_ListSessionsWithArchivedOptionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)
	archived := true

	sessions, err := client.List(context.Background(), SessionListOptions{Archived: &archived})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, sessions)
	require.True(t, stub.ListReq.GetArchived())
}

func TestClient_UseSessionSendsSessionID(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{}
	client := NewSessionService(stub)

	err := client.Use(context.Background(), "project-a")

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.UseReq.GetId())
}

func TestClient_UseSessionRequiresClient(t *testing.T) {
	err := (*SessionService)(nil).Use(context.Background(), "project-a")

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_UseSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	err := client.Use(context.Background(), "project-a")

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "project-a", stub.UseReq.GetId())
}

func TestClient_ArchiveSessionSendsSessionID(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{}
	client := NewSessionService(stub)

	err := client.Archive(context.Background(), " project-a ")

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.ArchiveReq.GetId())
}

func TestClient_UnarchiveSessionSendsSessionIDAndReturnsSession(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		UnarchiveResp: &morphpb.UnarchiveSessionResponse{
			Session: &morphpb.SessionSummary{
				Id:          "project-a",
				Title:       "Project Planning",
				TitleSource: storage.SessionTitleSourceManual,
			},
		},
	}
	client := NewSessionService(stub)

	session, err := client.Unarchive(context.Background(), " project-a ")

	require.NoError(t, err)
	require.Equal(t, "project-a", session.ID)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, storage.SessionTitleSourceManual, session.TitleSource)
	require.Equal(t, "project-a", stub.UnarchiveReq.GetId())
}

func TestClient_UnarchiveSessionRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Unarchive(context.Background(), "project-a")

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_UnarchiveSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	session, err := client.Unarchive(context.Background(), "project-a")

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, session.ID)
	require.Equal(t, "project-a", stub.UnarchiveReq.GetId())
}

func TestClient_RenameSessionSendsTitle(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		RenameResp: &morphpb.RenameSessionResponse{
			Session: &morphpb.SessionSummary{
				Id:          "project-a",
				Title:       "Project Planning",
				TitleSource: storage.SessionTitleSourceManual,
			},
		},
	}
	client := NewSessionService(stub)

	session, err := client.Rename(context.Background(), " project-a ", " Project Planning ")

	require.NoError(t, err)
	require.Equal(t, "project-a", session.ID)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, storage.SessionTitleSourceManual, session.TitleSource)
	require.Equal(t, "project-a", stub.RenameReq.GetId())
	require.Equal(t, "Project Planning", stub.RenameReq.GetTitle())
}

func TestClient_RenameSessionRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Rename(context.Background(), "project-a", "Title")

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_RenameSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	session, err := client.Rename(context.Background(), "project-a", "Title")

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, session.ID)
	require.Equal(t, "project-a", stub.RenameReq.GetId())
	require.Equal(t, "Title", stub.RenameReq.GetTitle())
}

func TestClient_ArchiveSessionRequiresClient(t *testing.T) {
	err := (*SessionService)(nil).Archive(context.Background(), "project-a")

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_ArchiveSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	err := client.Archive(context.Background(), "project-a")

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "project-a", stub.ArchiveReq.GetId())
}

func TestClient_CurrentSessionReturnsValue(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{CurrentResp: &morphpb.CurrentSessionResponse{
		Id:          "project-a",
		Title:       "Project Planning",
		TitleSource: storage.SessionTitleSourceGenerated,
	}}
	client := NewSessionService(stub)

	session, err := client.Current(context.Background())

	require.NoError(t, err)
	require.Equal(t, "project-a", session.ID)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, storage.SessionTitleSourceGenerated, session.TitleSource)
}

func TestClient_CurrentSessionRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Current(context.Background())

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_CurrentSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	session, err := client.Current(context.Background())

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, session.ID)
}

func TestClient_CompactSessionReturnsResult(t *testing.T) {
	now := time.Unix(123, 0).UTC()
	stub := &protomock.MorphServiceClientStub{CompactResp: &morphpb.CompactSessionResponse{
		Id:                   "project-a",
		SourceEndOffset:      12,
		SourceMessageCount:   20,
		UpdatedAt:            timestamppb.New(now),
		CurrentContextLength: 4000,
		TotalContextLength:   128000,
	}}
	client := NewSessionService(stub)

	result, err := client.Compact(context.Background(), "project-a")

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.CompactReq.GetId())
	require.Equal(t, "project-a", result.SessionID)
	require.Equal(t, 12, result.SourceEndOffset)
	require.Equal(t, 20, result.SourceMessageCount)
	require.Equal(t, now, result.UpdatedAt)
	require.Equal(t, 4000, result.CurrentContextLength)
	require.Equal(t, 128000, result.TotalContextLength)
}

func TestClient_CompactSessionRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Compact(context.Background(), "project-a")

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_CompactSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	result, err := client.Compact(context.Background(), "project-a")

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.SessionID)
	require.Equal(t, "project-a", stub.CompactReq.GetId())
}

func TestClient_RepairSessionReturnsResult(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{RepairResp: &morphpb.RepairSessionResponse{
		Type: morphpb.RepairSessionRequest_VECTOR,
		Vector: &morphpb.VectorRepairResponse{
			SessionsScanned:    2,
			MessagesScanned:    3,
			RowsScanned:        4,
			MissingRows:        5,
			StaleRows:          6,
			UnchangedRows:      7,
			RebuiltRows:        8,
			DeletedSources:     9,
			Batches:            10,
			AttemptedSources:   11,
			RecoveredSources:   12,
			StillFailedSources: 13,
		},
	}}
	client := NewSessionService(stub)

	result, err := client.Repair(context.Background(), RepairSessionOptions{
		SessionID: " project-a ",
		Full:      true,
	})

	require.NoError(t, err)
	require.Equal(t, morphpb.RepairSessionRequest_VECTOR, stub.RepairReq.GetType())
	require.Equal(t, "project-a", stub.RepairReq.GetVector().GetId())
	require.True(t, stub.RepairReq.GetVector().GetFull())
	require.Equal(t, 2, result.SessionsScanned)
	require.Equal(t, 3, result.MessagesScanned)
	require.Equal(t, 4, result.RowsScanned)
	require.Equal(t, 5, result.MissingRows)
	require.Equal(t, 6, result.StaleRows)
	require.Equal(t, 7, result.UnchangedRows)
	require.Equal(t, 8, result.RebuiltRows)
	require.Equal(t, 9, result.DeletedSources)
	require.Equal(t, 10, result.Batches)
	require.Equal(t, 11, result.AttemptedSources)
	require.Equal(t, 12, result.RecoveredSources)
	require.Equal(t, 13, result.StillFailedSources)
}

func TestClient_RepairSessionRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Repair(context.Background(), RepairSessionOptions{})

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_RepairSessionReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	result, err := client.Repair(context.Background(), RepairSessionOptions{SessionID: "project-a"})

	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Equal(t, "project-a", stub.RepairReq.GetVector().GetId())
}

func TestClient_GetSessionStatusReturnsResult(t *testing.T) {
	created := time.Date(2024, 4, 1, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2024, 4, 2, 11, 0, 0, 0, time.UTC)
	stub := &protomock.MorphServiceClientStub{StatusResp: &morphpb.GetSessionStatusResponse{
		Id:               "project-a",
		Size:             20,
		CreatedAt:        timestamppb.New(created),
		UpdatedAt:        timestamppb.New(updated),
		CompactionStatus: "pending",
		Context: &morphpb.GetSessionStatusResponse_Context{
			Offset:       12,
			Length:       128000,
			Used:         64000,
			Remaining:    64000,
			UsedPct:      0.5,
			RemainingPct: 0.5,
			UsageSource:  "estimated",
		},
	}}
	client := NewSessionService(stub)

	result, err := client.Status(context.Background(), "project-a")

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.StatusReq.GetContext().GetId())
	require.Equal(t, "project-a", result.SessionID)
	require.Equal(t, 12, result.Offset)
	require.Equal(t, 20, result.Size)
	require.Equal(t, 128000, result.Length)
	require.Equal(t, 64000, result.Used)
	require.Equal(t, 64000, result.Remaining)
	require.Equal(t, 0.5, result.UsedPct)
	require.Equal(t, 0.5, result.RemainingPct)
	require.Equal(t, "estimated", result.UsageSource)
	require.True(t, created.Equal(result.CreatedAt))
	require.True(t, updated.Equal(result.UpdatedAt))
	require.Equal(t, "pending", result.CompactionStatus)
}

func TestClient_GetSessionStatusRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Status(context.Background(), "project-a")

	require.EqualError(t, err, "morph: session service client is required")
}

func TestClient_GetSessionStatusReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	result, err := client.Status(context.Background(), "project-a")

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.SessionID)
	require.Equal(t, "project-a", stub.StatusReq.GetContext().GetId())
}

func TestClient_GetSessionStatusRequiresResponseContext(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{StatusResp: &morphpb.GetSessionStatusResponse{Id: "project-a"}}
	client := NewSessionService(stub)

	result, err := client.Status(context.Background(), "project-a")

	require.EqualError(t, err, "morph: get session status response context is required")
	require.Empty(t, result.SessionID)
}

type gatewayProtoClientStub struct {
	Err                error
	GatewayStatusReq   *morphpb.GetGatewayStatusRequest
	GatewayStatusResp  *morphpb.GetGatewayStatusResponse
	GatewayStartReq    *morphpb.StartGatewayRequest
	GatewayStartResp   *morphpb.StartGatewayResponse
	GatewayStopReq     *morphpb.StopGatewayRequest
	GatewayStopResp    *morphpb.StopGatewayResponse
	GatewayRestartReq  *morphpb.RestartGatewayRequest
	GatewayRestartResp *morphpb.RestartGatewayResponse
	PairingsReq        *morphpb.ListGatewayPairingsRequest
	PairingsResp       *morphpb.ListGatewayPairingsResponse
	ApproveReq         *morphpb.ApproveGatewayPairingRequest
	ApproveResp        *morphpb.ApproveGatewayPairingResponse
	RevokeReq          *morphpb.RevokeGatewayPairingRequest
	ClearReq           *morphpb.ClearPendingGatewayPairingsRequest
}

func (s *gatewayProtoClientStub) Status(_ context.Context, req *morphpb.GetGatewayStatusRequest, _ ...grpc.CallOption) (*morphpb.GetGatewayStatusResponse, error) {
	s.GatewayStatusReq = req
	return s.GatewayStatusResp, s.Err
}

func (s *gatewayProtoClientStub) Start(_ context.Context, req *morphpb.StartGatewayRequest, _ ...grpc.CallOption) (*morphpb.StartGatewayResponse, error) {
	s.GatewayStartReq = req
	return s.GatewayStartResp, s.Err
}

func (s *gatewayProtoClientStub) Stop(_ context.Context, req *morphpb.StopGatewayRequest, _ ...grpc.CallOption) (*morphpb.StopGatewayResponse, error) {
	s.GatewayStopReq = req
	return s.GatewayStopResp, s.Err
}

func (s *gatewayProtoClientStub) Restart(_ context.Context, req *morphpb.RestartGatewayRequest, _ ...grpc.CallOption) (*morphpb.RestartGatewayResponse, error) {
	s.GatewayRestartReq = req
	return s.GatewayRestartResp, s.Err
}

func (s *gatewayProtoClientStub) ListPairings(_ context.Context, req *morphpb.ListGatewayPairingsRequest, _ ...grpc.CallOption) (*morphpb.ListGatewayPairingsResponse, error) {
	s.PairingsReq = req
	return s.PairingsResp, s.Err
}

func (s *gatewayProtoClientStub) ApprovePairing(_ context.Context, req *morphpb.ApproveGatewayPairingRequest, _ ...grpc.CallOption) (*morphpb.ApproveGatewayPairingResponse, error) {
	s.ApproveReq = req
	return s.ApproveResp, s.Err
}

func (s *gatewayProtoClientStub) RevokePairing(_ context.Context, req *morphpb.RevokeGatewayPairingRequest, _ ...grpc.CallOption) (*morphpb.RevokeGatewayPairingResponse, error) {
	s.RevokeReq = req
	return &morphpb.RevokeGatewayPairingResponse{}, s.Err
}

func (s *gatewayProtoClientStub) ClearPendingPairings(_ context.Context, req *morphpb.ClearPendingGatewayPairingsRequest, _ ...grpc.CallOption) (*morphpb.ClearPendingGatewayPairingsResponse, error) {
	s.ClearReq = req
	return &morphpb.ClearPendingGatewayPairingsResponse{}, s.Err
}

func TestClient_GatewayPairingListApproveRevokeAndClear(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	stub := &gatewayProtoClientStub{
		PairingsResp: &morphpb.ListGatewayPairingsResponse{
			Pending: []*morphpb.GatewayPairingRequest{{
				Source:      "telegram",
				SenderId:    "123",
				DisplayName: "Ada",
				CreatedAt:   timestamppb.New(now),
				LastSeenAt:  timestamppb.New(now),
				ExpiresAt:   timestamppb.New(now.Add(time.Hour)),
			}},
			Approved: []*morphpb.GatewayPairedSender{{
				Source:      "telegram",
				SenderId:    "456",
				DisplayName: "Grace",
				CreatedAt:   timestamppb.New(now),
				UpdatedAt:   timestamppb.New(now),
			}},
		},
		ApproveResp: &morphpb.ApproveGatewayPairingResponse{
			Approved: true,
			Sender:   &morphpb.GatewayPairedSender{Source: "telegram", SenderId: "123"},
		},
	}
	client := NewGatewayService(stub)

	list, err := client.ListPairings(context.Background(), " telegram ")
	require.NoError(t, err)
	require.Equal(t, "telegram", stub.PairingsReq.GetSource())
	require.Equal(t, "123", list.Pending[0].SenderID)
	require.Equal(t, "456", list.Approved[0].SenderID)

	sender, approved, err := client.ApprovePairing(context.Background(), " telegram ", " 12345678 ")
	require.NoError(t, err)
	require.True(t, approved)
	require.Equal(t, "telegram", stub.ApproveReq.GetSource())
	require.Equal(t, "12345678", stub.ApproveReq.GetCode())
	require.Equal(t, "123", sender.SenderID)

	require.NoError(t, client.RevokePairing(context.Background(), " telegram ", " 123 "))
	require.Equal(t, "telegram", stub.RevokeReq.GetSource())
	require.Equal(t, "123", stub.RevokeReq.GetSenderId())

	require.NoError(t, client.ClearPendingPairings(context.Background(), " telegram "))
	require.Equal(t, "telegram", stub.ClearReq.GetSource())
}

func TestClient_GatewayRuntimeStatusStartStopAndRestart(t *testing.T) {
	status := &morphpb.GatewayStatus{
		State:        " running ",
		Address:      " 127.0.0.1 ",
		Port:         50052,
		SlackMode:    " socket ",
		TelegramMode: " polling ",
		LastError:    " safe error ",
	}
	stub := &gatewayProtoClientStub{
		GatewayStatusResp:  &morphpb.GetGatewayStatusResponse{Status: status},
		GatewayStartResp:   &morphpb.StartGatewayResponse{Status: status},
		GatewayStopResp:    &morphpb.StopGatewayResponse{Status: status},
		GatewayRestartResp: &morphpb.RestartGatewayResponse{Status: status},
	}
	client := NewGatewayService(stub)

	got, err := client.GatewayStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stub.GatewayStatusReq)
	require.Equal(t, "running", got.State)
	require.Equal(t, "127.0.0.1", got.Address)
	require.Equal(t, 50052, got.Port)
	require.Equal(t, "socket", got.SlackMode)
	require.Equal(t, "polling", got.TelegramMode)
	require.Equal(t, "safe error", got.LastError)

	got, err = client.Start(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stub.GatewayStartReq)
	require.Equal(t, "running", got.State)

	got, err = client.Stop(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stub.GatewayStopReq)
	require.Equal(t, "running", got.State)

	got, err = client.Restart(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stub.GatewayRestartReq)
	require.Equal(t, "running", got.State)
}

func TestClient_GatewayRuntimeReturnsRPCErrors(t *testing.T) {
	rpcErr := errors.New("rpc failed")
	client := NewGatewayService(&gatewayProtoClientStub{Err: rpcErr})
	tests := []struct {
		name string
		run  func(context.Context) (GatewayStatus, error)
	}{
		{name: "status", run: client.GatewayStatus},
		{name: "start", run: client.Start},
		{name: "stop", run: client.Stop},
		{name: "restart", run: client.Restart},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.run(context.Background())

			require.ErrorIs(t, err, rpcErr)
			require.Empty(t, result)
		})
	}
}

func TestClient_GatewayPairingRequiresClient(t *testing.T) {
	tests := []struct {
		name string
		run  func(*GatewayService) error
	}{
		{
			name: "list pairings",
			run: func(service *GatewayService) error {
				_, err := service.ListPairings(context.Background(), "")
				return err
			},
		},
		{
			name: "status",
			run: func(service *GatewayService) error {
				_, err := service.GatewayStatus(context.Background())
				return err
			},
		},
		{
			name: "start",
			run: func(service *GatewayService) error {
				_, err := service.Start(context.Background())
				return err
			},
		},
		{
			name: "stop",
			run: func(service *GatewayService) error {
				_, err := service.Stop(context.Background())
				return err
			},
		},
		{
			name: "restart",
			run: func(service *GatewayService) error {
				_, err := service.Restart(context.Background())
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, tt.run(nil), "morph: gateway service client is required")
		})
	}
}

func TestClient_GetSessionTimelineReturnsResult(t *testing.T) {
	messageAt := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	traceAt := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	stub := &protomock.MorphServiceClientStub{TimelineResp: &morphpb.GetSessionTimelineResponse{
		Id:          "default",
		Title:       "Daily Planning",
		TitleSource: storage.SessionTitleSourceGenerated,
		Messages: []*morphpb.SessionTimelineMessage{{
			Offset:     2,
			Id:         7,
			Role:       "tool",
			Name:       "read_file",
			ToolCallId: "call_1",
			Content:    "file content",
			CreatedAt:  timestamppb.New(messageAt),
			ToolCalls: []*morphpb.SessionTimelineToolCall{{
				Id:    "call_2",
				Name:  "search",
				Input: `{"query":"hello"}`,
			}},
		}},
		TraceEvents: []*morphpb.SessionTimelineTraceEvent{{
			Id:          9,
			Sequence:    3,
			Type:        trace.EvtInputSafetyBlocked,
			Timestamp:   timestamppb.New(traceAt),
			PayloadJson: `{"blocked":true}`,
		}},
		MessagesHasMore:       true,
		TracesHasMore:         true,
		TracesTruncatedBefore: true,
		FirstTraceSequence:    3,
		LastTraceSequence:     3,
	}}
	client := NewSessionService(stub)

	result, err := client.Timeline(context.Background(), SessionTimelineOptions{
		SessionID:     " default ",
		MessageOffset: 2,
		MessageLimit:  1,
		TraceOffset:   3,
		TraceLimit:    4,
	})

	require.NoError(t, err)
	require.Equal(t, "default", stub.TimelineReq.GetId())
	require.EqualValues(t, 2, stub.TimelineReq.GetMessageOffset())
	require.EqualValues(t, 1, stub.TimelineReq.GetMessageLimit())
	require.EqualValues(t, 3, stub.TimelineReq.GetTraceOffset())
	require.EqualValues(t, 4, stub.TimelineReq.GetTraceLimit())
	require.Equal(t, "default", result.SessionID)
	require.Equal(t, "Daily Planning", result.Title)
	require.Equal(t, storage.SessionTitleSourceGenerated, result.TitleSource)
	require.True(t, result.MessagesHasMore)
	require.True(t, result.TracesHasMore)
	require.True(t, result.TracesTruncatedBefore)
	require.Equal(t, 3, result.FirstTraceSequence)
	require.Equal(t, 3, result.LastTraceSequence)
	require.Len(t, result.Messages, 1)
	require.Equal(t, 2, result.Messages[0].Offset)
	require.EqualValues(t, 7, result.Messages[0].Message.ID)
	require.Equal(t, "tool", string(result.Messages[0].Message.Role))
	require.Equal(t, "read_file", result.Messages[0].Message.Name)
	require.Equal(t, "call_1", result.Messages[0].Message.ToolCallID)
	require.Equal(t, "file content", result.Messages[0].Message.Content)
	require.Equal(t, messageAt, result.Messages[0].Message.CreatedAt)
	require.Len(t, result.Messages[0].Message.ToolCalls, 1)
	require.Equal(t, "call_2", result.Messages[0].Message.ToolCalls[0].ID)
	require.Equal(t, "search", result.Messages[0].Message.ToolCalls[0].Name)
	require.Equal(t, `{"query":"hello"}`, result.Messages[0].Message.ToolCalls[0].Input)
	require.Len(t, result.TraceEvents, 1)
	require.EqualValues(t, 9, result.TraceEvents[0].Event.ID)
	require.Equal(t, 3, result.TraceEvents[0].Event.Sequence)
	require.Equal(t, trace.EvtInputSafetyBlocked, result.TraceEvents[0].Event.Type)
	require.Equal(t, traceAt, result.TraceEvents[0].Event.Timestamp)
	require.Equal(t, map[string]any{"blocked": true}, result.TraceEvents[0].Event.Payload)
}

func TestClient_GetSessionTimelineReturnsDecodeErrors(t *testing.T) {
	client := NewSessionService(&protomock.MorphServiceClientStub{})

	_, err := client.Timeline(context.Background(), SessionTimelineOptions{})
	require.EqualError(t, err, "morph: get session timeline response is required")

	client = NewSessionService(&protomock.MorphServiceClientStub{TimelineResp: &morphpb.GetSessionTimelineResponse{
		TraceEvents: []*morphpb.SessionTimelineTraceEvent{{PayloadJson: "{"}},
	}})
	_, err = client.Timeline(context.Background(), SessionTimelineOptions{})
	require.ErrorContains(t, err, "unexpected end of JSON input")
}

func TestClient_GetSessionTimelineReturnsRPCError(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{Err: context.Canceled}
	client := NewSessionService(stub)

	_, err := client.Timeline(context.Background(), SessionTimelineOptions{})

	require.ErrorIs(t, err, context.Canceled)
}

func TestClient_GetSessionTimelineRequiresClient(t *testing.T) {
	_, err := (*SessionService)(nil).Timeline(context.Background(), SessionTimelineOptions{})

	require.EqualError(t, err, "morph: session service client is required")
}

func TestModelService_ListModelsReturnsProviderAuthAndOptions(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{ModelsResp: &morphpb.ListModelsResponse{
		Provider: "openai",
		AuthType: "oauth",
		Models: []*morphpb.ModelOption{{
			Id:            " gpt-5.4-mini ",
			Name:          " GPT 5.4 Mini ",
			Provider:      " openai ",
			Api:           " openai-responses ",
			ContextWindow: 272000,
			MaxTokens:     128000,
			Input:         []string{"text", "image"},
			Reasoning:     true,
			SupportsOauth: true,
			Current:       true,
		}},
	}}
	client := NewModelService(stub)

	list, err := client.ListModels(context.Background(), ModelListOptions{Provider: " openai "})

	require.NoError(t, err)
	require.NotNil(t, stub.ModelsReq)
	require.Equal(t, "openai", stub.ModelsReq.GetProvider())
	require.Equal(t, "openai", list.Provider)
	require.Equal(t, "oauth", list.AuthType)
	require.Len(t, list.Models, 1)
	require.Equal(t, "gpt-5.4-mini", list.Models[0].ID)
	require.Equal(t, "GPT 5.4 Mini", list.Models[0].Name)
	require.Equal(t, "openai", list.Models[0].Provider)
	require.Equal(t, "openai-responses", list.Models[0].API)
	require.Equal(t, 272000, list.Models[0].ContextWindow)
	require.Equal(t, 128000, list.Models[0].MaxTokens)
	require.Equal(t, []string{"text", "image"}, list.Models[0].Input)
	require.True(t, list.Models[0].Reasoning)
	require.True(t, list.Models[0].SupportsOAuth)
	require.True(t, list.Models[0].Current)
}

func TestModelService_ListProvidersReturnsOptions(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{ProvidersResp: &morphpb.ListProvidersResponse{
		Providers: []*morphpb.ProviderOption{{
			Id:             " openrouter ",
			Name:           " OpenRouter ",
			Type:           " api-key ",
			ModelCount:     12,
			SupportsApiKey: true,
			AuthType:       " api-key ",
			Current:        true,
		}},
	}}
	client := NewModelService(stub)

	list, err := client.ListProviders(context.Background())

	require.NoError(t, err)
	require.NotNil(t, stub.ProvidersReq)
	require.Len(t, list.Providers, 1)
	require.Equal(t, "openrouter", list.Providers[0].ID)
	require.Equal(t, "OpenRouter", list.Providers[0].Name)
	require.Equal(t, "api-key", list.Providers[0].Type)
	require.Equal(t, 12, list.Providers[0].ModelCount)
	require.True(t, list.Providers[0].SupportsAPIKey)
	require.False(t, list.Providers[0].SupportsOAuth)
	require.Equal(t, "api-key", list.Providers[0].AuthType)
	require.True(t, list.Providers[0].Current)
}

func TestModelService_RuntimeModelReturnsDaemonIdentity(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{RuntimeModelResp: &morphpb.RuntimeModelResponse{
		Provider:      " ollama ",
		Api:           " ollama-native ",
		Model:         " qwen3:8b ",
		BaseUrl:       " http://127.0.0.1:11434/ ",
		ContextLength: 8192,
	}}
	client := NewModelService(stub)

	runtimeModel, err := client.RuntimeModel(context.Background())

	require.NoError(t, err)
	require.NotNil(t, stub.RuntimeModelReq)
	require.Equal(t, ModelRuntime{
		Provider:      "ollama",
		API:           "ollama-native",
		Model:         "qwen3:8b",
		BaseURL:       "http://127.0.0.1:11434",
		ContextLength: 8192,
	}, runtimeModel)
}

func TestModelService_SelectModelSendsTrimmedIDAndProvider(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{SelectResp: &morphpb.SelectModelResponse{
		Model: &morphpb.ModelOption{Id: "gpt-4o", Current: true},
	}}
	client := NewModelService(stub)

	model, err := client.SelectModel(context.Background(), " gpt-4o ", ModelSelectOptions{Provider: " openai "})

	require.NoError(t, err)
	require.Equal(t, "gpt-4o", stub.SelectReq.GetId())
	require.Equal(t, "openai", stub.SelectReq.GetProvider())
	require.Equal(t, "gpt-4o", model.ID)
	require.True(t, model.Current)
}

func TestModelService_SetProviderAPIKeySendsTrimmedProviderAndKey(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{APIKeyResp: &morphpb.SetProviderAPIKeyResponse{Provider: "openrouter"}}
	client := NewModelService(stub)

	err := client.SetProviderAPIKey(context.Background(), " openrouter ", " key ")

	require.NoError(t, err)
	require.Equal(t, "openrouter", stub.APIKeyReq.GetProvider())
	require.Equal(t, "key", stub.APIKeyReq.GetApiKey())
}

func TestModelService_ReturnsClientErrors(t *testing.T) {
	_, err := (*ModelService)(nil).RuntimeModel(context.Background())
	require.EqualError(t, err, "morph: model service client is required")

	_, err = (*ModelService)(nil).ListModels(context.Background())
	require.EqualError(t, err, "morph: model service client is required")

	_, err = (*ModelService)(nil).ListProviders(context.Background())
	require.EqualError(t, err, "morph: model service client is required")

	_, err = (*ModelService)(nil).SelectModel(context.Background(), "gpt-4o")
	require.EqualError(t, err, "morph: model service client is required")

	err = (*ModelService)(nil).SetProviderAPIKey(context.Background(), "openrouter", "key")
	require.EqualError(t, err, "morph: model service client is required")

	require.Nil(t, (*Client)(nil).ModelAPI())
	wrapped := &Client{Model: NewModelService(&protomock.MorphServiceClientStub{})}
	require.NotNil(t, wrapped.ModelAPI())

	client := NewModelService(&protomock.MorphServiceClientStub{Err: context.Canceled})
	runtimeModel, err := client.RuntimeModel(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, runtimeModel)

	providers, err := client.ListProviders(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, providers.Providers)

	list, err := client.ListModels(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, list.Provider)
	require.Empty(t, list.Models)

	model, err := client.SelectModel(context.Background(), "gpt-4o")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, model.ID)

	err = client.SetProviderAPIKey(context.Background(), "openrouter", "key")
	require.ErrorIs(t, err, context.Canceled)
}

func TestTimelineProtoAdaptersHandleNilRecords(t *testing.T) {
	message := timelineMessageFromProto(nil)
	require.Zero(t, message)

	model := protoModelOptionToModelOption(nil)
	require.Zero(t, model)

	runtimeModel := protoRuntimeModelToModelRuntime(nil)
	require.Zero(t, runtimeModel)

	provider := protoProviderOptionToProviderOption(nil)
	require.Zero(t, provider)

	event, err := timelineTraceEventFromProto(nil)
	require.NoError(t, err)
	require.Zero(t, event)

	session := protoSessionSummaryToSession(nil)
	require.Zero(t, session)

	require.Zero(t, protoTimestampToTime(nil))
	var timestamp *timestamppb.Timestamp
	require.Zero(t, protoTimestampToTime(timestamp))
}

func TestNewClient_ValidatesOptions(t *testing.T) {
	_, err := NewClient(context.Background(), Options{})
	require.EqualError(t, err, "rpc address is required")

	_, err = NewClient(context.Background(), Options{Address: "127.0.0.1"})
	require.EqualError(t, err, "rpc port must be greater than zero")

	_, err = NewClient(context.Background(), Options{Address: "\x00", Port: 1})
	require.ErrorContains(t, err, "invalid control character")
}

func TestNewClient_CreatesConnection(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	client, err := NewClient(context.Background(), Options{
		Address: "127.0.0.1",
		Port:    lis.Addr().(*net.TCPAddr).Port,
	})
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestPermissionUnaryClientInterceptor_PropagatesPermissionMetadata(t *testing.T) {
	opts := Options{
		PermissionSurface: permissions.SurfaceCLI,
		PermissionPreset:  permissions.PresetApproveForMe,
	}
	called := false

	err := permissionUnaryClientInterceptor(opts)(
		context.Background(),
		"/morph.SessionService/CreateSession",
		nil,
		nil,
		nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			called = true
			requirePermissionClientMetadata(t, ctx, permissions.SurfaceCLI, permissions.PresetApproveForMe)
			return nil
		},
	)

	require.NoError(t, err)
	require.True(t, called)
}

func TestPermissionStreamClientInterceptor_PropagatesPermissionMetadata(t *testing.T) {
	opts := Options{
		PermissionSurface: permissions.SurfaceTUI,
		PermissionPreset:  permissions.PresetAskForApproval,
	}
	called := false

	_, err := permissionStreamClientInterceptor(opts)(
		context.Background(),
		nil,
		nil,
		morphpb.SessionService_Observe_FullMethodName,
		func(
			ctx context.Context,
			_ *grpc.StreamDesc,
			_ *grpc.ClientConn,
			_ string,
			_ ...grpc.CallOption,
		) (grpc.ClientStream, error) {
			called = true
			requirePermissionClientMetadata(t, ctx, permissions.SurfaceTUI, permissions.PresetAskForApproval)
			return nil, nil
		},
	)

	require.NoError(t, err)
	require.True(t, called)
}

func requirePermissionClientMetadata(
	t *testing.T,
	ctx context.Context,
	surface permissions.Surface,
	preset permissions.Preset,
) {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), md)
	require.Equal(t, surface, rpcmeta.PermissionSurfaceFromIncomingContext(incoming))
	actualPreset, ok := rpcmeta.PermissionPresetFromIncomingContext(incoming)
	require.True(t, ok)
	require.Equal(t, preset, actualPreset)
}

func TestClient_ServiceAPIsAndCloseHandleNilValues(t *testing.T) {
	require.Nil(t, (*Client)(nil).SessionAPI())
	require.Nil(t, (*Client)(nil).ModelAPI())
	require.NoError(t, (*Client)(nil).Close())
	require.NoError(t, (&Client{}).Close())

	wrapped := &Client{
		Session: NewSessionService(&protomock.MorphServiceClientStub{}),
		Model:   NewModelService(&protomock.MorphServiceClientStub{}),
	}
	require.NotNil(t, wrapped.SessionAPI())
	require.NotNil(t, wrapped.ModelAPI())
}
