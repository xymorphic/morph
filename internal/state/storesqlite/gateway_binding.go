package storesqlite

import (
	"context"
	"errors"
	"time"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
	"gorm.io/gorm"
)

func (s *Store) SaveGatewayBinding(ctx context.Context, binding base.GatewayBinding) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	keyValue := str.String(binding.Key)
	key := keyValue.Trim()
	if key == "" {
		return errors.New("gateway binding key is required")
	}
	sessionIDValue := str.String(binding.SessionID)
	sessionID := sessionIDValue.Trim()
	if err := base.ValidateSessionID(sessionID); err != nil {
		return err
	}

	var session sessionModel
	if err := s.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("session not found")
		}

		return err
	}

	now := time.Now().UTC()
	var existing gatewayBindingModel
	err := s.db.WithContext(ctx).First(&existing, "key = ?", key).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		existing = gatewayBindingModel{Key: key}
	}
	if !existing.CreatedAt.IsZero() {
		binding.CreatedAt = existing.CreatedAt
	} else if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = now
	}

	model := gatewayBindingModel{
		Key:       key,
		SessionID: sessionID,
		CreatedAt: binding.CreatedAt,
		UpdatedAt: binding.UpdatedAt,
	}

	return s.db.WithContext(ctx).Save(&model).Error
}

func (s *Store) GetGatewayBinding(ctx context.Context, key string) (base.GatewayBinding, bool, error) {
	if s == nil || s.db == nil {
		return base.GatewayBinding{}, false, errors.New("store is required")
	}

	keyValue2 := str.String(key)
	key = keyValue2.Trim()
	if key == "" {
		return base.GatewayBinding{}, false, errors.New("gateway binding key is required")
	}

	var model gatewayBindingModel
	if err := s.db.WithContext(ctx).First(&model, "key = ?", key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return base.GatewayBinding{}, false, nil
		}

		return base.GatewayBinding{}, false, err
	}

	return base.GatewayBinding{
		Key:       model.Key,
		SessionID: model.SessionID,
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}, true, nil
}
