package storesqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/xymorphic/morph/pkg/gateway/pairing"
	"github.com/xymorphic/morph/pkg/str"
	"gorm.io/gorm"
)

func (s *Store) SaveGatewayPairingRequest(ctx context.Context, request pairing.PendingRequest) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	model, err := gatewayPairingRequestToModel(request)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Save(&model).Error
}

func (s *Store) GetGatewayPairingRequest(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	if s == nil || s.db == nil {
		return pairing.PendingRequest{}, false, errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	senderIDValue := str.String(senderID)
	senderID = senderIDValue.Trim()
	if source == "" {
		return pairing.PendingRequest{}, false, errors.New("gateway pairing source is required")
	}
	if senderID == "" {
		return pairing.PendingRequest{}, false, errors.New("gateway pairing sender id is required")
	}

	var model gatewayPairingRequestModel
	if err := s.db.WithContext(ctx).First(&model, "source = ? AND sender_id = ?", source, senderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pairing.PendingRequest{}, false, nil
		}

		return pairing.PendingRequest{}, false, err
	}

	request, err := gatewayPairingRequestFromModel(model)
	return request, err == nil, err
}

func (s *Store) ListGatewayPairingRequests(
	ctx context.Context,
	source string,
) ([]pairing.PendingRequest, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	query := s.db.WithContext(ctx).Order("source ASC, sender_id ASC")
	if source != "" {
		query = query.Where("source = ?", source)
	}

	var models []gatewayPairingRequestModel
	if err := query.Find(&models).Error; err != nil {
		return nil, err
	}

	requests := make([]pairing.PendingRequest, 0, len(models))
	for _, model := range models {
		request, err := gatewayPairingRequestFromModel(model)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}

	return requests, nil
}

func (s *Store) DeleteGatewayPairingRequest(ctx context.Context, source string, senderID string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	senderIDValue2 := str.String(senderID)
	senderID = senderIDValue2.Trim()
	if source == "" {
		return errors.New("gateway pairing source is required")
	}
	if senderID == "" {
		return errors.New("gateway pairing sender id is required")
	}

	return s.db.WithContext(ctx).Delete(&gatewayPairingRequestModel{}, "source = ? AND sender_id = ?",
		source, senderID).Error
}

func (s *Store) ClearGatewayPairingRequests(ctx context.Context, source string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	query := s.db.WithContext(ctx)
	if source == "" {
		return query.Where("1 = 1").Delete(&gatewayPairingRequestModel{}).Error
	}

	return query.Delete(&gatewayPairingRequestModel{}, "source = ?", source).Error
}

func (s *Store) SaveGatewayPairedSender(ctx context.Context, sender pairing.ApprovedSender) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	model, err := gatewayPairedSenderToModel(sender)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Save(&model).Error
}

func (s *Store) GetGatewayPairedSender(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	if s == nil || s.db == nil {
		return pairing.ApprovedSender{}, false, errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	senderIDValue3 := str.String(senderID)
	senderID = senderIDValue3.Trim()
	if source == "" {
		return pairing.ApprovedSender{}, false, errors.New("gateway pairing source is required")
	}
	if senderID == "" {
		return pairing.ApprovedSender{}, false, errors.New("gateway pairing sender id is required")
	}

	var model gatewayPairedSenderModel
	if err := s.db.WithContext(ctx).First(&model, "source = ? AND sender_id = ?", source, senderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pairing.ApprovedSender{}, false, nil
		}

		return pairing.ApprovedSender{}, false, err
	}

	sender, err := gatewayPairedSenderFromModel(model)
	return sender, err == nil, err
}

func (s *Store) ListGatewayPairedSenders(
	ctx context.Context,
	source string,
) ([]pairing.ApprovedSender, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	query := s.db.WithContext(ctx).Order("source ASC, sender_id ASC")
	if source != "" {
		query = query.Where("source = ?", source)
	}

	var models []gatewayPairedSenderModel
	if err := query.Find(&models).Error; err != nil {
		return nil, err
	}

	senders := make([]pairing.ApprovedSender, 0, len(models))
	for _, model := range models {
		sender, err := gatewayPairedSenderFromModel(model)
		if err != nil {
			return nil, err
		}
		senders = append(senders, sender)
	}

	return senders, nil
}

func (s *Store) DeleteGatewayPairedSender(ctx context.Context, source string, senderID string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	source = normalizeGatewayPairingSource(source)
	senderIDValue4 := str.String(senderID)
	senderID = senderIDValue4.Trim()
	if source == "" {
		return errors.New("gateway pairing source is required")
	}
	if senderID == "" {
		return errors.New("gateway pairing sender id is required")
	}

	return s.db.WithContext(ctx).Delete(&gatewayPairedSenderModel{}, "source = ? AND sender_id = ?", source, senderID).Error
}

func gatewayPairingRequestToModel(request pairing.PendingRequest) (gatewayPairingRequestModel, error) {
	request.Source = normalizeGatewayPairingSource(request.Source)
	senderIDValue5 := str.String(request.SenderID)
	request.SenderID = senderIDValue5.Trim()
	if request.Source == "" {
		return gatewayPairingRequestModel{}, errors.New("gateway pairing source is required")
	}
	if request.SenderID == "" {
		return gatewayPairingRequestModel{}, errors.New("gateway pairing sender id is required")
	}

	metadata, err := gatewayPairingMetadataToJSON(request.Metadata)
	if err != nil {
		return gatewayPairingRequestModel{}, err
	}
	displayNameValue := str.String(request.DisplayName)
	return gatewayPairingRequestModel{
		Source:      request.Source,
		SenderID:    request.SenderID,
		DisplayName: displayNameValue.Trim(),
		Metadata:    metadata,
		CreatedAt:   request.CreatedAt,
		LastSeenAt:  request.LastSeenAt,
		ExpiresAt:   request.ExpiresAt,
	}, nil
}

func gatewayPairingRequestFromModel(model gatewayPairingRequestModel) (pairing.PendingRequest, error) {
	metadata, err := gatewayPairingMetadataFromJSON(model.Metadata)
	if err != nil {
		return pairing.PendingRequest{}, err
	}

	return pairing.PendingRequest{
		Source:      model.Source,
		SenderID:    model.SenderID,
		DisplayName: model.DisplayName,
		Metadata:    metadata,
		CreatedAt:   model.CreatedAt,
		LastSeenAt:  model.LastSeenAt,
		ExpiresAt:   model.ExpiresAt,
	}, nil
}

func gatewayPairedSenderToModel(sender pairing.ApprovedSender) (gatewayPairedSenderModel, error) {
	sender.Source = normalizeGatewayPairingSource(sender.Source)
	senderIDValue6 := str.String(sender.SenderID)
	sender.SenderID = senderIDValue6.Trim()
	if sender.Source == "" {
		return gatewayPairedSenderModel{}, errors.New("gateway pairing source is required")
	}
	if sender.SenderID == "" {
		return gatewayPairedSenderModel{}, errors.New("gateway pairing sender id is required")
	}

	metadata, err := gatewayPairingMetadataToJSON(sender.Metadata)
	if err != nil {
		return gatewayPairedSenderModel{}, err
	}
	displayNameValue2 := str.String(sender.DisplayName)
	return gatewayPairedSenderModel{
		Source:      sender.Source,
		SenderID:    sender.SenderID,
		DisplayName: displayNameValue2.Trim(),
		Metadata:    metadata,
		CreatedAt:   sender.CreatedAt,
		UpdatedAt:   sender.UpdatedAt,
	}, nil
}

func gatewayPairedSenderFromModel(model gatewayPairedSenderModel) (pairing.ApprovedSender, error) {
	metadata, err := gatewayPairingMetadataFromJSON(model.Metadata)
	if err != nil {
		return pairing.ApprovedSender{}, err
	}

	return pairing.ApprovedSender{
		Source:      model.Source,
		SenderID:    model.SenderID,
		DisplayName: model.DisplayName,
		Metadata:    metadata,
		CreatedAt:   model.CreatedAt,
		UpdatedAt:   model.UpdatedAt,
	}, nil
}

func gatewayPairingMetadataToJSON(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "", nil
	}

	cleaned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		keyValue := str.String(key)
		key = keyValue.Trim()
		if key != "" {
			valueText := str.String(value)
			cleaned[key] = valueText.Trim()
		}
	}
	if len(cleaned) == 0 {
		return "", nil
	}

	data, err := json.Marshal(cleaned)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func gatewayPairingMetadataFromJSON(raw string) (map[string]string, error) {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if raw == "" {
		return nil, nil
	}

	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, err
	}

	return metadata, nil
}

func normalizeGatewayPairingSource(source string) string {
	sourceValue := str.String(source)
	return sourceValue.Normalized()
}
