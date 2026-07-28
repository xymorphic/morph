package proto

import (
	"context"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	morphpb "github.com/wandxy/morph/internal/rpc/proto"
)

// MorphServiceClientStub is a test stub for morph service client.
type MorphServiceClientStub struct {
	Err                error
	RecvErr            error
	SubmitMessageReq   *morphpb.SubmitSessionMessageRequest
	SubmitMessageResp  *morphpb.SubmitSessionMessageResponse
	StateReq           *morphpb.GetSessionStateRequest
	StateResp          *morphpb.GetSessionStateResponse
	ObserveReq         *morphpb.ObserveSessionRequest
	ObserveEvents      []*morphpb.ObserveSessionResponse
	EditQueuedReq      *morphpb.EditQueuedSessionMessageRequest
	EditQueuedResp     *morphpb.EditQueuedSessionMessageResponse
	RemoveQueuedReq    *morphpb.RemoveQueuedSessionMessageRequest
	RemoveQueuedResp   *morphpb.RemoveQueuedSessionMessageResponse
	PromoteQueuedReq   *morphpb.PromoteQueuedSessionMessageRequest
	PromoteQueuedResp  *morphpb.PromoteQueuedSessionMessageResponse
	SteerQueuedReq     *morphpb.SteerQueuedSessionMessageRequest
	SteerQueuedResp    *morphpb.SteerQueuedSessionMessageResponse
	InterruptRunReq    *morphpb.InterruptSessionRunRequest
	InterruptRunResp   *morphpb.InterruptSessionRunResponse
	CreateResp         *morphpb.CreateSessionResponse
	CreateReq          *morphpb.CreateSessionRequest
	ListResp           *morphpb.ListSessionsResponse
	ListReq            *morphpb.ListSessionsRequest
	UseReq             *morphpb.UseSessionRequest
	ArchiveReq         *morphpb.ArchiveSessionRequest
	UnarchiveReq       *morphpb.UnarchiveSessionRequest
	UnarchiveResp      *morphpb.UnarchiveSessionResponse
	RenameReq          *morphpb.RenameSessionRequest
	RenameResp         *morphpb.RenameSessionResponse
	CurrentResp        *morphpb.CurrentSessionResponse
	CompactResp        *morphpb.CompactSessionResponse
	CompactReq         *morphpb.CompactSessionRequest
	RepairResp         *morphpb.RepairSessionResponse
	RepairReq          *morphpb.RepairSessionRequest
	StatusResp         *morphpb.GetSessionStatusResponse
	StatusReq          *morphpb.GetSessionStatusRequest
	TimelineResp       *morphpb.GetSessionTimelineResponse
	TimelineReq        *morphpb.GetSessionTimelineRequest
	ProvidersResp      *morphpb.ListProvidersResponse
	ProvidersReq       *morphpb.ListProvidersRequest
	RuntimeModelResp   *morphpb.RuntimeModelResponse
	RuntimeModelReq    *morphpb.RuntimeModelRequest
	ModelsResp         *morphpb.ListModelsResponse
	ModelsReq          *morphpb.ListModelsRequest
	SelectReq          *morphpb.SelectModelRequest
	SelectResp         *morphpb.SelectModelResponse
	APIKeyReq          *morphpb.SetProviderAPIKeyRequest
	APIKeyResp         *morphpb.SetProviderAPIKeyResponse
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
	OnListModels       func()
}

func (s *MorphServiceClientStub) Create(_ context.Context, req *morphpb.CreateSessionRequest, _ ...grpc.CallOption) (*morphpb.CreateSessionResponse, error) {
	s.CreateReq = req
	return s.CreateResp, s.Err
}

func (s *MorphServiceClientStub) List(_ context.Context, req *morphpb.ListSessionsRequest, _ ...grpc.CallOption) (*morphpb.ListSessionsResponse, error) {
	s.ListReq = req
	return s.ListResp, s.Err
}

func (s *MorphServiceClientStub) Use(_ context.Context, req *morphpb.UseSessionRequest, _ ...grpc.CallOption) (*morphpb.UseSessionResponse, error) {
	s.UseReq = req
	return &morphpb.UseSessionResponse{Id: req.GetId()}, s.Err
}

func (s *MorphServiceClientStub) Archive(_ context.Context, req *morphpb.ArchiveSessionRequest, _ ...grpc.CallOption) (*morphpb.ArchiveSessionResponse, error) {
	s.ArchiveReq = req
	return &morphpb.ArchiveSessionResponse{Id: req.GetId()}, s.Err
}

func (s *MorphServiceClientStub) Unarchive(_ context.Context, req *morphpb.UnarchiveSessionRequest, _ ...grpc.CallOption) (*morphpb.UnarchiveSessionResponse, error) {
	s.UnarchiveReq = req
	return s.UnarchiveResp, s.Err
}

func (s *MorphServiceClientStub) Rename(_ context.Context, req *morphpb.RenameSessionRequest, _ ...grpc.CallOption) (*morphpb.RenameSessionResponse, error) {
	s.RenameReq = req
	return s.RenameResp, s.Err
}

func (s *MorphServiceClientStub) Current(context.Context, *morphpb.CurrentSessionRequest, ...grpc.CallOption) (*morphpb.CurrentSessionResponse, error) {
	return s.CurrentResp, s.Err
}

func (s *MorphServiceClientStub) Compact(_ context.Context, req *morphpb.CompactSessionRequest, _ ...grpc.CallOption) (*morphpb.CompactSessionResponse, error) {
	s.CompactReq = req
	return s.CompactResp, s.Err
}

func (s *MorphServiceClientStub) Repair(_ context.Context, req *morphpb.RepairSessionRequest, _ ...grpc.CallOption) (*morphpb.RepairSessionResponse, error) {
	s.RepairReq = req
	return s.RepairResp, s.Err
}

func (s *MorphServiceClientStub) Status(_ context.Context, req *morphpb.GetSessionStatusRequest, _ ...grpc.CallOption) (*morphpb.GetSessionStatusResponse, error) {
	s.StatusReq = req
	return s.StatusResp, s.Err
}

func (s *MorphServiceClientStub) Timeline(_ context.Context, req *morphpb.GetSessionTimelineRequest, _ ...grpc.CallOption) (*morphpb.GetSessionTimelineResponse, error) {
	s.TimelineReq = req
	return s.TimelineResp, s.Err
}

func (s *MorphServiceClientStub) SubmitMessage(
	_ context.Context,
	req *morphpb.SubmitSessionMessageRequest,
	_ ...grpc.CallOption,
) (*morphpb.SubmitSessionMessageResponse, error) {
	s.SubmitMessageReq = req
	return s.SubmitMessageResp, s.Err
}

func (s *MorphServiceClientStub) State(
	_ context.Context,
	req *morphpb.GetSessionStateRequest,
	_ ...grpc.CallOption,
) (*morphpb.GetSessionStateResponse, error) {
	s.StateReq = req
	return s.StateResp, s.Err
}

func (s *MorphServiceClientStub) Observe(
	_ context.Context,
	req *morphpb.ObserveSessionRequest,
	_ ...grpc.CallOption,
) (grpc.ServerStreamingClient[morphpb.ObserveSessionResponse], error) {
	s.ObserveReq = req
	if s.Err != nil {
		return nil, s.Err
	}
	return &observeStreamStub{events: s.ObserveEvents, err: s.RecvErr}, nil
}

func (s *MorphServiceClientStub) EditQueuedMessage(
	_ context.Context,
	req *morphpb.EditQueuedSessionMessageRequest,
	_ ...grpc.CallOption,
) (*morphpb.EditQueuedSessionMessageResponse, error) {
	s.EditQueuedReq = req
	return s.EditQueuedResp, s.Err
}

func (s *MorphServiceClientStub) RemoveQueuedMessage(
	_ context.Context,
	req *morphpb.RemoveQueuedSessionMessageRequest,
	_ ...grpc.CallOption,
) (*morphpb.RemoveQueuedSessionMessageResponse, error) {
	s.RemoveQueuedReq = req
	return s.RemoveQueuedResp, s.Err
}

func (s *MorphServiceClientStub) PromoteQueuedMessage(
	_ context.Context,
	req *morphpb.PromoteQueuedSessionMessageRequest,
	_ ...grpc.CallOption,
) (*morphpb.PromoteQueuedSessionMessageResponse, error) {
	s.PromoteQueuedReq = req
	return s.PromoteQueuedResp, s.Err
}

func (s *MorphServiceClientStub) SteerQueuedMessage(
	_ context.Context,
	req *morphpb.SteerQueuedSessionMessageRequest,
	_ ...grpc.CallOption,
) (*morphpb.SteerQueuedSessionMessageResponse, error) {
	s.SteerQueuedReq = req
	return s.SteerQueuedResp, s.Err
}

func (s *MorphServiceClientStub) InterruptRun(
	_ context.Context,
	req *morphpb.InterruptSessionRunRequest,
	_ ...grpc.CallOption,
) (*morphpb.InterruptSessionRunResponse, error) {
	s.InterruptRunReq = req
	return s.InterruptRunResp, s.Err
}

func (s *MorphServiceClientStub) ListModels(_ context.Context, req *morphpb.ListModelsRequest, _ ...grpc.CallOption) (*morphpb.ListModelsResponse, error) {
	if s.OnListModels != nil {
		s.OnListModels()
	}
	s.ModelsReq = req
	return s.ModelsResp, s.Err
}

func (s *MorphServiceClientStub) ListProviders(_ context.Context, req *morphpb.ListProvidersRequest, _ ...grpc.CallOption) (*morphpb.ListProvidersResponse, error) {
	s.ProvidersReq = req
	return s.ProvidersResp, s.Err
}

func (s *MorphServiceClientStub) RuntimeModel(_ context.Context, req *morphpb.RuntimeModelRequest, _ ...grpc.CallOption) (*morphpb.RuntimeModelResponse, error) {
	s.RuntimeModelReq = req
	return s.RuntimeModelResp, s.Err
}

func (s *MorphServiceClientStub) SelectModel(_ context.Context, req *morphpb.SelectModelRequest, _ ...grpc.CallOption) (*morphpb.SelectModelResponse, error) {
	s.SelectReq = req
	return s.SelectResp, s.Err
}

func (s *MorphServiceClientStub) SetProviderAPIKey(_ context.Context, req *morphpb.SetProviderAPIKeyRequest, _ ...grpc.CallOption) (*morphpb.SetProviderAPIKeyResponse, error) {
	s.APIKeyReq = req
	return s.APIKeyResp, s.Err
}

func (s *MorphServiceClientStub) GatewayStatus(_ context.Context, req *morphpb.GetGatewayStatusRequest, _ ...grpc.CallOption) (*morphpb.GetGatewayStatusResponse, error) {
	s.GatewayStatusReq = req
	return s.GatewayStatusResp, s.Err
}

func (s *MorphServiceClientStub) Start(_ context.Context, req *morphpb.StartGatewayRequest, _ ...grpc.CallOption) (*morphpb.StartGatewayResponse, error) {
	s.GatewayStartReq = req
	return s.GatewayStartResp, s.Err
}

func (s *MorphServiceClientStub) Stop(_ context.Context, req *morphpb.StopGatewayRequest, _ ...grpc.CallOption) (*morphpb.StopGatewayResponse, error) {
	s.GatewayStopReq = req
	return s.GatewayStopResp, s.Err
}

func (s *MorphServiceClientStub) Restart(_ context.Context, req *morphpb.RestartGatewayRequest, _ ...grpc.CallOption) (*morphpb.RestartGatewayResponse, error) {
	s.GatewayRestartReq = req
	return s.GatewayRestartResp, s.Err
}

func (s *MorphServiceClientStub) ListPairings(_ context.Context, req *morphpb.ListGatewayPairingsRequest, _ ...grpc.CallOption) (*morphpb.ListGatewayPairingsResponse, error) {
	s.PairingsReq = req
	return s.PairingsResp, s.Err
}

func (s *MorphServiceClientStub) ApprovePairing(_ context.Context, req *morphpb.ApproveGatewayPairingRequest, _ ...grpc.CallOption) (*morphpb.ApproveGatewayPairingResponse, error) {
	s.ApproveReq = req
	return s.ApproveResp, s.Err
}

func (s *MorphServiceClientStub) RevokePairing(_ context.Context, req *morphpb.RevokeGatewayPairingRequest, _ ...grpc.CallOption) (*morphpb.RevokeGatewayPairingResponse, error) {
	s.RevokeReq = req
	return &morphpb.RevokeGatewayPairingResponse{}, s.Err
}

func (s *MorphServiceClientStub) ClearPendingPairings(_ context.Context, req *morphpb.ClearPendingGatewayPairingsRequest, _ ...grpc.CallOption) (*morphpb.ClearPendingGatewayPairingsResponse, error) {
	s.ClearReq = req
	return &morphpb.ClearPendingGatewayPairingsResponse{}, s.Err
}

type observeStreamStub struct {
	events []*morphpb.ObserveSessionResponse
	err    error
	index  int
}

func (s *observeStreamStub) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}

func (s *observeStreamStub) Trailer() metadata.MD {
	return metadata.MD{}
}

func (s *observeStreamStub) CloseSend() error {
	return nil
}

func (s *observeStreamStub) Context() context.Context {
	return context.Background()
}

func (s *observeStreamStub) SendMsg(any) error {
	return nil
}

func (s *observeStreamStub) RecvMsg(any) error {
	return nil
}

func (s *observeStreamStub) Recv() (*morphpb.ObserveSessionResponse, error) {
	if s.index >= len(s.events) {
		if s.err != nil {
			return nil, s.err
		}

		return nil, io.EOF
	}

	event := s.events[s.index]
	s.index++
	return event, nil
}
