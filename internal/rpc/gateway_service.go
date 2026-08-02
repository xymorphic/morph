package rpc

import (
	"context"

	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
)

type GatewayService struct {
	morphpb.UnimplementedGatewayServiceServer
	service *Service
}

func NewGatewayService(service *Service) *GatewayService {
	return &GatewayService{service: service}
}

func (s *GatewayService) getService() *Service {
	if s == nil {
		return nil
	}

	return s.service
}

func (s *GatewayService) Status(
	ctx context.Context,
	req *morphpb.GetGatewayStatusRequest,
) (*morphpb.GetGatewayStatusResponse, error) {
	return s.getService().GatewayStatus(ctx, req)
}

func (s *GatewayService) Start(
	ctx context.Context,
	req *morphpb.StartGatewayRequest,
) (*morphpb.StartGatewayResponse, error) {
	return s.getService().Start(ctx, req)
}

func (s *GatewayService) Stop(
	ctx context.Context,
	req *morphpb.StopGatewayRequest,
) (*morphpb.StopGatewayResponse, error) {
	return s.getService().Stop(ctx, req)
}

func (s *GatewayService) Restart(
	ctx context.Context,
	req *morphpb.RestartGatewayRequest,
) (*morphpb.RestartGatewayResponse, error) {
	return s.getService().Restart(ctx, req)
}

func (s *GatewayService) ListPairings(
	ctx context.Context,
	req *morphpb.ListGatewayPairingsRequest,
) (*morphpb.ListGatewayPairingsResponse, error) {
	return s.getService().ListPairings(ctx, req)
}

func (s *GatewayService) ApprovePairing(
	ctx context.Context,
	req *morphpb.ApproveGatewayPairingRequest,
) (*morphpb.ApproveGatewayPairingResponse, error) {
	return s.getService().ApprovePairing(ctx, req)
}

func (s *GatewayService) RevokePairing(
	ctx context.Context,
	req *morphpb.RevokeGatewayPairingRequest,
) (*morphpb.RevokeGatewayPairingResponse, error) {
	return s.getService().RevokePairing(ctx, req)
}

func (s *GatewayService) ClearPendingPairings(
	ctx context.Context,
	req *morphpb.ClearPendingGatewayPairingsRequest,
) (*morphpb.ClearPendingGatewayPairingsResponse, error) {
	return s.getService().ClearPendingPairings(ctx, req)
}
