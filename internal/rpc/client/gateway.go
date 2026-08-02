package client

import (
	"context"
	"fmt"

	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *GatewayService) ListPairings(ctx context.Context, source string) (GatewayPairingList, error) {
	client, err := s.getClient()
	if err != nil {
		return GatewayPairingList{}, err
	}

	prepareRPCConnection(s.reconnector)
	sourceValue := str.String(source)
	resp, err := client.ListPairings(ctx, &morphpb.ListGatewayPairingsRequest{Source: sourceValue.Trim()})
	if err != nil {
		return GatewayPairingList{}, err
	}

	result := GatewayPairingList{}
	for _, pending := range resp.GetPending() {
		result.Pending = append(result.Pending, protoGatewayPairingRequestToGatewayPairingRequest(pending))
	}
	for _, approved := range resp.GetApproved() {
		result.Approved = append(result.Approved, protoGatewayPairedSenderToGatewayPairedSender(approved))
	}

	return result, nil
}

func (s *GatewayService) GatewayStatus(ctx context.Context) (GatewayStatus, error) {
	client, err := s.getClient()
	if err != nil {
		return GatewayStatus{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Status(ctx, &morphpb.GetGatewayStatusRequest{})
	if err != nil {
		return GatewayStatus{}, err
	}

	return gatewayStatusFromProto(resp.GetStatus()), nil
}

func (s *GatewayService) Start(ctx context.Context) (GatewayStatus, error) {
	client, err := s.getClient()
	if err != nil {
		return GatewayStatus{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Start(ctx, &morphpb.StartGatewayRequest{})
	if err != nil {
		return GatewayStatus{}, err
	}

	return gatewayStatusFromProto(resp.GetStatus()), nil
}

func (s *GatewayService) Stop(ctx context.Context) (GatewayStatus, error) {
	client, err := s.getClient()
	if err != nil {
		return GatewayStatus{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Stop(ctx, &morphpb.StopGatewayRequest{})
	if err != nil {
		return GatewayStatus{}, err
	}

	return gatewayStatusFromProto(resp.GetStatus()), nil
}

func (s *GatewayService) Restart(ctx context.Context) (GatewayStatus, error) {
	client, err := s.getClient()
	if err != nil {
		return GatewayStatus{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Restart(ctx, &morphpb.RestartGatewayRequest{})
	if err != nil {
		return GatewayStatus{}, err
	}

	return gatewayStatusFromProto(resp.GetStatus()), nil
}

func (s *GatewayService) ApprovePairing(
	ctx context.Context,
	source string,
	code string,
) (GatewayPairedSender, bool, error) {
	client, err := s.getClient()
	if err != nil {
		return GatewayPairedSender{}, false, err
	}

	prepareRPCConnection(s.reconnector)
	sourceValue := str.String(source)
	codeValue := str.String(code)
	resp, err := client.ApprovePairing(ctx, &morphpb.ApproveGatewayPairingRequest{
		Source: sourceValue.Trim(),
		Code:   codeValue.Trim(),
	})
	if err != nil {
		return GatewayPairedSender{}, false, err
	}

	return protoGatewayPairedSenderToGatewayPairedSender(resp.GetSender()), resp.GetApproved(), nil
}

func (s *GatewayService) RevokePairing(ctx context.Context, source string, senderID string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}

	prepareRPCConnection(s.reconnector)
	sourceValue := str.String(source)
	senderIDValue := str.String(senderID)
	_, err = client.RevokePairing(ctx, &morphpb.RevokeGatewayPairingRequest{
		Source:   sourceValue.Trim(),
		SenderId: senderIDValue.Trim(),
	})

	return err
}

func (s *GatewayService) ClearPendingPairings(ctx context.Context, source string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}

	prepareRPCConnection(s.reconnector)
	sourceValue := str.String(source)
	_, err = client.ClearPendingPairings(ctx, &morphpb.ClearPendingGatewayPairingsRequest{
		Source: sourceValue.Trim(),
	})

	return err
}

func (s *GatewayService) getClient() (morphpb.GatewayServiceClient, error) {
	if s != nil && s.client != nil {
		return s.client, nil
	}

	return nil, fmt.Errorf("morph: gateway service client is required")
}

func gatewayStatusFromProto(status *morphpb.GatewayStatus) GatewayStatus {
	if status == nil {
		return GatewayStatus{}
	}

	state := str.String(status.GetState())
	address := str.String(status.GetAddress())
	slackMode := str.String(status.GetSlackMode())
	telegramMode := str.String(status.GetTelegramMode())
	lastError := str.String(status.GetLastError())
	return GatewayStatus{
		State:        state.Trim(),
		Address:      address.Trim(),
		Port:         int(status.GetPort()),
		SlackMode:    slackMode.Trim(),
		TelegramMode: telegramMode.Trim(),
		LastError:    lastError.Trim(),
	}
}

func protoGatewayPairingRequestToGatewayPairingRequest(
	request *morphpb.GatewayPairingRequest,
) GatewayPairingRequest {
	if request == nil {
		return GatewayPairingRequest{}
	}

	source := str.String(request.GetSource())
	senderID := str.String(request.GetSenderId())
	displayName := str.String(request.GetDisplayName())
	return GatewayPairingRequest{
		Source:      source.Trim(),
		SenderID:    senderID.Trim(),
		DisplayName: displayName.Trim(),
		CreatedAt:   protoTimestampToTime(request.GetCreatedAt()),
		LastSeenAt:  protoTimestampToTime(request.GetLastSeenAt()),
		ExpiresAt:   protoTimestampToTime(request.GetExpiresAt()),
	}
}

func protoGatewayPairedSenderToGatewayPairedSender(sender *morphpb.GatewayPairedSender) GatewayPairedSender {
	if sender == nil {
		return GatewayPairedSender{}
	}

	source := str.String(sender.GetSource())
	senderID := str.String(sender.GetSenderId())
	displayName := str.String(sender.GetDisplayName())
	return GatewayPairedSender{
		Source:      source.Trim(),
		SenderID:    senderID.Trim(),
		DisplayName: displayName.Trim(),
		CreatedAt:   protoTimestampToTime(sender.GetCreatedAt()),
		UpdatedAt:   protoTimestampToTime(sender.GetUpdatedAt()),
	}
}
