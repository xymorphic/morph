package storesqlite

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/xymorphic/morph/internal/permissions"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type approvalRequestModel struct {
	ID             string `gorm:"primaryKey"`
	Fingerprint    string `gorm:"index:idx_approval_pending_lookup"`
	ActorKind      string
	ActorID        string
	SurfaceKind    string
	Surface        string
	Profile        string
	SessionID      string `gorm:"index:idx_approval_pending_lookup"`
	RunID          string
	Tool           string
	Resource       string
	Action         string
	EffectsJSON    string
	Summary        string
	Reason         string
	OperationsJSON string
	Status         string `gorm:"index:idx_approval_pending_lookup"`
	Scope          string
	GrantID        string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	ResolvedAt     *time.Time
}

func (approvalRequestModel) TableName() string { return "permission_approval_requests" }

type approvalGrantModel struct {
	ID             string `gorm:"primaryKey"`
	RequestID      string `gorm:"uniqueIndex"`
	Fingerprint    string `gorm:"index:idx_approval_grant_lookup"`
	ActorKind      string
	ActorID        string
	Profile        string
	SessionID      string
	OperationsJSON string
	Scope          string
	Status         string `gorm:"index:idx_approval_grant_lookup"`
	CreatedAt      time.Time
	ExpiresAt      time.Time `gorm:"index:idx_approval_grant_lookup"`
	ConsumedAt     *time.Time
	RevokedAt      *time.Time
}

func (approvalGrantModel) TableName() string { return "permission_approval_grants" }

func (s *Store) CreateApprovalRequest(
	ctx context.Context,
	request permissions.ApprovalRequest,
) (permissions.ApprovalRequest, bool, error) {
	var result permissions.ApprovalRequest
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing approvalRequestModel
		err := tx.Where(
			"fingerprint = ? AND actor_kind = ? AND actor_id = ? AND session_id = ? AND status = ?",
			request.Fingerprint,
			request.Actor.Kind,
			request.Actor.ID,
			request.SessionID,
			permissions.ApprovalPending,
		).First(&existing).Error
		if err == nil {
			var convertErr error
			result, convertErr = approvalRequestFromModel(existing)
			return convertErr
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		model := approvalRequestToModel(request)
		if err := tx.Create(&model).Error; err != nil {
			return err
		}
		result = request
		created = true
		return nil
	})
	return result, created, err
}

func (s *Store) GetApprovalRequest(
	ctx context.Context,
	id string,
) (permissions.ApprovalRequest, bool, error) {
	var model approvalRequestModel
	err := s.db.WithContext(ctx).First(&model, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return permissions.ApprovalRequest{}, false, nil
	}
	if err != nil {
		return permissions.ApprovalRequest{}, false, err
	}
	request, err := approvalRequestFromModel(model)
	return request, err == nil, err
}

func (s *Store) ListApprovalRequests(
	ctx context.Context,
	query permissions.ApprovalQuery,
) ([]permissions.ApprovalRequest, error) {
	db := s.db.WithContext(ctx).Order("created_at DESC")
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}
	if query.Offset > 0 {
		db = db.Offset(query.Offset)
	}
	var models []approvalRequestModel
	if err := db.Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]permissions.ApprovalRequest, 0, len(models))
	for _, model := range models {
		request, err := approvalRequestFromModel(model)
		if err != nil {
			return nil, err
		}
		result = append(result, request)
	}

	return result, nil
}

func (s *Store) ResolveApprovalRequest(
	ctx context.Context,
	id string,
	status permissions.ApprovalStatus,
	scope permissions.GrantScope,
	now time.Time,
) (permissions.ApprovalRequest, error) {
	var result permissions.ApprovalRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model approvalRequestModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("approval request not found")
			}

			return err
		}
		if model.Status != string(permissions.ApprovalPending) {
			if model.Status == string(permissions.ApprovalApproved) && status == permissions.ApprovalFailed {
				model.Status = string(status)
				model.ResolvedAt = &now
				if err := tx.Save(&model).Error; err != nil {
					return err
				}
				var err error
				result, err = approvalRequestFromModel(model)
				return err
			}
			if model.Status != string(status) || model.Scope != string(scope) {
				return errors.New("approval request is already resolved")
			}

			var err error
			result, err = approvalRequestFromModel(model)
			return err
		}
		model.Status = string(status)
		model.Scope = string(scope)
		model.ResolvedAt = &now
		if err := tx.Save(&model).Error; err != nil {
			return err
		}
		var err error
		result, err = approvalRequestFromModel(model)
		return err
	})
	return result, err
}

func (s *Store) CancelPendingApprovals(ctx context.Context, now time.Time) (int64, error) {
	result := s.db.WithContext(ctx).Model(&approvalRequestModel{}).
		Where("status = ?", permissions.ApprovalPending).
		Updates(map[string]any{"status": permissions.ApprovalCancelled, "resolved_at": now})
	return result.RowsAffected, result.Error
}

func (s *Store) CreateApprovalGrant(
	ctx context.Context,
	grant permissions.ApprovalGrant,
) (permissions.ApprovalGrant, error) {
	model := approvalGrantToModel(grant)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model).Error; err != nil {
			return err
		}

		result := tx.Model(&approvalRequestModel{}).
			Where("id = ? AND status = ?", grant.RequestID, permissions.ApprovalApproved).
			Update("grant_id", grant.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("approval request is not approved")
		}
		return nil
	})
	return grant, err
}

func (s *Store) FindApprovalGrant(
	ctx context.Context,
	fingerprint string,
	actor permissions.Actor,
	profile string,
	sessionID string,
	now time.Time,
) (permissions.ApprovalGrant, bool, error) {
	if err := s.db.WithContext(ctx).Model(&approvalGrantModel{}).
		Where("status = ? AND scope <> ? AND expires_at <= ?", permissions.GrantActive, permissions.GrantAlways, now).
		Update("status", permissions.GrantExpired).Error; err != nil {
		return permissions.ApprovalGrant{}, false, err
	}

	var model approvalGrantModel
	err := s.db.WithContext(ctx).
		Where("fingerprint = ? AND actor_kind = ? AND actor_id = ? AND profile = ? AND status = ?",
			fingerprint, actor.Kind, actor.ID, profile, permissions.GrantActive).
		Where("scope = ? OR expires_at > ?", permissions.GrantAlways, now).
		Where("scope IN ? OR session_id = ?", []permissions.GrantScope{permissions.GrantAlways, permissions.GrantDurable}, sessionID).
		Order("created_at DESC").First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return permissions.ApprovalGrant{}, false, nil
	}
	if err != nil {
		return permissions.ApprovalGrant{}, false, err
	}
	grant, err := approvalGrantFromModel(model)
	return grant, err == nil, err
}

func (s *Store) GetApprovalGrant(
	ctx context.Context,
	id string,
) (permissions.ApprovalGrant, bool, error) {
	var model approvalGrantModel
	err := s.db.WithContext(ctx).First(&model, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return permissions.ApprovalGrant{}, false, nil
	}
	if err != nil {
		return permissions.ApprovalGrant{}, false, err
	}
	grant, err := approvalGrantFromModel(model)
	return grant, err == nil, err
}

func (s *Store) ConsumeApprovalGrant(
	ctx context.Context,
	id string,
	now time.Time,
) (permissions.ApprovalGrant, error) {
	var model approvalGrantModel
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&approvalGrantModel{}).
			Where("id = ? AND status = ? AND scope = ? AND expires_at > ?", id, permissions.GrantActive, permissions.GrantOnce, now).
			Updates(map[string]any{"status": permissions.GrantConsumed, "consumed_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("approval grant is not consumable")
		}

		return tx.First(&model, "id = ?", id).Error
	})
	if err != nil {
		return permissions.ApprovalGrant{}, err
	}

	return approvalGrantFromModel(model)
}

func (s *Store) ListApprovalGrants(
	ctx context.Context,
	query permissions.GrantQuery,
) ([]permissions.ApprovalGrant, error) {
	db := s.db.WithContext(ctx).Order("created_at DESC")
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}
	if query.Offset > 0 {
		db = db.Offset(query.Offset)
	}
	var models []approvalGrantModel
	if err := db.Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]permissions.ApprovalGrant, len(models))
	for index, model := range models {
		grant, err := approvalGrantFromModel(model)
		if err != nil {
			return nil, err
		}
		result[index] = grant
	}

	return result, nil
}

func (s *Store) RevokeApprovalGrant(
	ctx context.Context,
	id string,
	now time.Time,
) (permissions.ApprovalGrant, error) {
	var model approvalGrantModel
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("approval grant not found")
			}

			return err
		}
		if model.Status == string(permissions.GrantRevoked) {
			return nil
		}
		if model.Status != string(permissions.GrantActive) {
			return errors.New("approval grant is not active")
		}

		model.Status = string(permissions.GrantRevoked)
		model.RevokedAt = &now
		return tx.Save(&model).Error
	})
	if err != nil {
		return permissions.ApprovalGrant{}, err
	}
	return approvalGrantFromModel(model)
}

func (s *Store) DeleteApprovalRequest(ctx context.Context, id string, now time.Time) (string, error) {
	linkedGrantID := ""
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model approvalRequestModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("approval request not found")
			}

			return err
		}
		switch permissions.ApprovalStatus(model.Status) {
		case permissions.ApprovalApproved, permissions.ApprovalDenied, permissions.ApprovalExpired,
			permissions.ApprovalCancelled, permissions.ApprovalFailed:
		case permissions.ApprovalPending:
			return errors.New("pending approval request cannot be deleted")
		default:
			return errors.New("approval request is not terminal")
		}
		if model.GrantID != "" {
			var grant approvalGrantModel
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&grant, "id = ?", model.GrantID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				loaded, convertErr := approvalGrantFromModel(grant)
				if convertErr != nil {
					return convertErr
				}
				if loaded.IsActiveAt(now) {
					grant.Status = string(permissions.GrantRevoked)
					grant.RevokedAt = &now
					if err := tx.Save(&grant).Error; err != nil {
						return err
					}
				}
			}
			if err == nil {
				linkedGrantID = model.GrantID
				if err := tx.Delete(&grant).Error; err != nil {
					return err
				}
			}
		}
		return tx.Delete(&model).Error
	})
	return linkedGrantID, err
}

func (s *Store) DeleteApprovalGrant(ctx context.Context, id string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model approvalGrantModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("approval grant not found")
			}

			return err
		}
		switch permissions.GrantStatus(model.Status) {
		case permissions.GrantConsumed, permissions.GrantExpired, permissions.GrantRevoked:
		case permissions.GrantActive:
			grant, err := approvalGrantFromModel(model)
			if err != nil {
				return err
			}
			if grant.IsActiveAt(now) {
				return errors.New("active approval grant cannot be deleted; revoke it first")
			}
		default:
			return errors.New("approval grant is not terminal")
		}

		return tx.Delete(&model).Error
	})
}

func (s *Store) PruneApprovals(
	ctx context.Context,
	opts permissions.ApprovalPruneOptions,
) (permissions.ApprovalPruneResult, error) {
	result := permissions.ApprovalPruneResult{
		RequestCutoff: opts.Now.Add(-opts.RequestRetention),
		GrantCutoff:   opts.Now.Add(-opts.GrantRetention),
		DryRun:        opts.DryRun,
	}
	if opts.BatchSize <= 0 {
		return result, errors.New("approval cleanup batch size must be greater than zero")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var grants []approvalGrantModel
		grantQuery := tx.Where(
			"(status = ? AND consumed_at <= ?) OR (status = ? AND revoked_at <= ?) OR "+
				"(status IN ? AND scope <> ? AND expires_at <= ?)",
			permissions.GrantConsumed, result.GrantCutoff,
			permissions.GrantRevoked, result.GrantCutoff,
			[]permissions.GrantStatus{permissions.GrantExpired, permissions.GrantActive}, permissions.GrantAlways,
			result.GrantCutoff,
		).Order("created_at ASC").Limit(opts.BatchSize)
		if err := grantQuery.Find(&grants).Error; err != nil {
			return err
		}
		grantIDs := make([]string, len(grants))
		for index, grant := range grants {
			grantIDs[index] = grant.ID
		}
		result.Grants = int64(len(grantIDs))

		requestQuery := tx.Where("status <> ? AND resolved_at <= ?", permissions.ApprovalPending, result.RequestCutoff)
		if len(grantIDs) > 0 {
			requestQuery = requestQuery.Where(
				"grant_id = '' OR grant_id IN ? OR grant_id NOT IN (SELECT id FROM permission_approval_grants)", grantIDs,
			)
		} else {
			requestQuery = requestQuery.Where(
				"grant_id = '' OR grant_id NOT IN (SELECT id FROM permission_approval_grants)",
			)
		}
		var requests []approvalRequestModel
		if err := requestQuery.Order("created_at ASC").Limit(opts.BatchSize).Find(&requests).Error; err != nil {
			return err
		}
		requestIDs := make([]string, len(requests))
		for index, request := range requests {
			requestIDs[index] = request.ID
		}
		result.Requests = int64(len(requestIDs))
		if opts.DryRun {
			return nil
		}
		if len(grantIDs) > 0 {
			if err := tx.Where("id IN ?", grantIDs).Delete(&approvalGrantModel{}).Error; err != nil {
				return err
			}
		}
		if len(requestIDs) > 0 {
			if err := tx.Where("id IN ?", requestIDs).Delete(&approvalRequestModel{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func approvalRequestToModel(request permissions.ApprovalRequest) approvalRequestModel {
	effects, _ := json.Marshal(request.Effects)
	operations, _ := json.Marshal(request.Operations)
	model := approvalRequestModel{
		ID: request.ID, Fingerprint: request.Fingerprint, ActorKind: string(request.Actor.Kind), ActorID: request.Actor.ID,
		SurfaceKind: string(request.SurfaceKind), Surface: string(request.Surface), Profile: request.Profile,
		SessionID: request.SessionID, RunID: request.RunID, Tool: request.Tool, Resource: string(request.Resource),
		Action: string(request.Action), EffectsJSON: string(effects), Summary: request.Summary, Reason: request.Reason,
		OperationsJSON: string(operations),
		Status:         string(request.Status), Scope: string(request.Scope), GrantID: request.GrantID,
		CreatedAt: request.CreatedAt, ExpiresAt: request.ExpiresAt,
	}
	if !request.ResolvedAt.IsZero() {
		model.ResolvedAt = &request.ResolvedAt
	}
	return model
}

func approvalRequestFromModel(model approvalRequestModel) (permissions.ApprovalRequest, error) {
	var effects []permissions.Effect
	if err := json.Unmarshal([]byte(model.EffectsJSON), &effects); err != nil {
		return permissions.ApprovalRequest{}, err
	}
	var operations []string
	if model.OperationsJSON != "" {
		if err := json.Unmarshal([]byte(model.OperationsJSON), &operations); err != nil {
			return permissions.ApprovalRequest{}, err
		}
	}
	request := permissions.ApprovalRequest{
		ID: model.ID, Fingerprint: model.Fingerprint,
		Actor:       permissions.Actor{Kind: permissions.ActorKind(model.ActorKind), ID: model.ActorID},
		SurfaceKind: permissions.SurfaceKind(model.SurfaceKind), Surface: permissions.Surface(model.Surface),
		Profile: model.Profile, SessionID: model.SessionID, RunID: model.RunID, Tool: model.Tool,
		Resource: permissions.Resource(model.Resource), Action: permissions.Action(model.Action), Effects: effects,
		Summary: model.Summary, Reason: model.Reason, Operations: operations,
		Status: permissions.ApprovalStatus(model.Status),
		Scope:  permissions.GrantScope(model.Scope), GrantID: model.GrantID,
		CreatedAt: model.CreatedAt, ExpiresAt: model.ExpiresAt,
	}
	if model.ResolvedAt != nil {
		request.ResolvedAt = *model.ResolvedAt
	}
	return request, nil
}

func approvalGrantToModel(grant permissions.ApprovalGrant) approvalGrantModel {
	operations, _ := json.Marshal(grant.Operations)
	model := approvalGrantModel{
		ID: grant.ID, RequestID: grant.RequestID, Fingerprint: grant.Fingerprint,
		ActorKind: string(grant.Actor.Kind), ActorID: grant.Actor.ID, Profile: grant.Profile,
		SessionID: grant.SessionID, OperationsJSON: string(operations),
		Scope: string(grant.Scope), Status: string(grant.Status),
		CreatedAt: grant.CreatedAt, ExpiresAt: grant.ExpiresAt,
	}
	if !grant.ConsumedAt.IsZero() {
		model.ConsumedAt = &grant.ConsumedAt
	}
	if !grant.RevokedAt.IsZero() {
		model.RevokedAt = &grant.RevokedAt
	}
	return model
}

func approvalGrantFromModel(model approvalGrantModel) (permissions.ApprovalGrant, error) {
	var operations []string
	if model.OperationsJSON != "" {
		if err := json.Unmarshal([]byte(model.OperationsJSON), &operations); err != nil {
			return permissions.ApprovalGrant{}, err
		}
	}
	grant := permissions.ApprovalGrant{
		ID: model.ID, RequestID: model.RequestID, Fingerprint: model.Fingerprint,
		Actor:   permissions.Actor{Kind: permissions.ActorKind(model.ActorKind), ID: model.ActorID},
		Profile: model.Profile, SessionID: model.SessionID, Operations: operations,
		Scope:  permissions.GrantScope(model.Scope),
		Status: permissions.GrantStatus(model.Status), CreatedAt: model.CreatedAt, ExpiresAt: model.ExpiresAt,
	}
	if model.ConsumedAt != nil {
		grant.ConsumedAt = *model.ConsumedAt
	}
	if model.RevokedAt != nil {
		grant.RevokedAt = *model.RevokedAt
	}
	return grant, nil
}
