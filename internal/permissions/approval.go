package permissions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wandxy/morph/pkg/nanoid"
)

const (
	ApprovalRequestIDPrefix         = "approval_"
	ApprovalGrantIDPrefix           = "grant_"
	DefaultApprovalRequestTTL       = 2 * time.Minute
	DefaultApprovalOnceTTL          = 2 * time.Minute
	DefaultApprovalSessionTTL       = 8 * time.Hour
	DefaultApprovalRequestRetention = 30 * 24 * time.Hour
	DefaultApprovalGrantRetention   = 30 * 24 * time.Hour
	DefaultApprovalCleanupInterval  = time.Hour
	DefaultApprovalCleanupBatchSize = 100
	DefaultApprovalRateLimit        = 10
	DefaultApprovalRateWindow       = time.Minute
)

type ApprovalStatus string

const (
	ApprovalPending   ApprovalStatus = "pending"
	ApprovalApproved  ApprovalStatus = "approved"
	ApprovalDenied    ApprovalStatus = "denied"
	ApprovalExpired   ApprovalStatus = "expired"
	ApprovalCancelled ApprovalStatus = "cancelled"
	ApprovalFailed    ApprovalStatus = "failed"
)

type GrantStatus string

const (
	GrantActive   GrantStatus = "active"
	GrantConsumed GrantStatus = "consumed"
	GrantExpired  GrantStatus = "expired"
	GrantRevoked  GrantStatus = "revoked"
)

type GrantScope string

const (
	GrantOnce    GrantScope = "once"
	GrantSession GrantScope = "session"
	GrantAlways  GrantScope = "always"
	GrantDurable GrantScope = "durable"
)

type ApprovalRequest struct {
	ID          string
	Fingerprint string
	Actor       Actor
	SurfaceKind SurfaceKind
	Surface     Surface
	Profile     string
	SessionID   string
	RunID       string
	Tool        string
	Resource    Resource
	Action      Action
	Effects     []Effect
	Summary     string
	Reason      string
	Operations  []string
	Status      ApprovalStatus
	Scope       GrantScope
	GrantID     string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ResolvedAt  time.Time
}

type ApprovalGrant struct {
	ID          string
	RequestID   string
	Fingerprint string
	Actor       Actor
	Profile     string
	SessionID   string
	Operations  []string
	Scope       GrantScope
	Status      GrantStatus
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  time.Time
	RevokedAt   time.Time
}

func (g ApprovalGrant) IsExpiredAt(now time.Time) bool {
	return g.Scope != GrantAlways && !g.ExpiresAt.After(now)
}

func (g ApprovalGrant) IsActiveAt(now time.Time) bool {
	return g.Status == GrantActive && !g.IsExpiredAt(now)
}

type ApprovalQuery struct {
	Status ApprovalStatus
	Limit  int
	Offset int
}

type GrantQuery struct {
	Status GrantStatus
	Limit  int
	Offset int
}

type ApprovalPruneOptions struct {
	Now              time.Time
	RequestRetention time.Duration
	GrantRetention   time.Duration
	BatchSize        int
	DryRun           bool
}

type ApprovalPruneResult struct {
	Requests      int64
	Grants        int64
	RequestCutoff time.Time
	GrantCutoff   time.Time
	DryRun        bool
}

type ApprovalRecordKind string

const (
	ApprovalRecordRequest ApprovalRecordKind = "request"
	ApprovalRecordGrant   ApprovalRecordKind = "grant"
)

type ApprovalDeleteResult struct {
	ID            string
	Kind          ApprovalRecordKind
	LinkedGrantID string
}

type ApprovalStore interface {
	CreateApprovalRequest(context.Context, ApprovalRequest) (ApprovalRequest, bool, error)
	GetApprovalRequest(context.Context, string) (ApprovalRequest, bool, error)
	ListApprovalRequests(context.Context, ApprovalQuery) ([]ApprovalRequest, error)
	ResolveApprovalRequest(context.Context, string, ApprovalStatus, GrantScope, time.Time) (ApprovalRequest, error)
	CancelPendingApprovals(context.Context, time.Time) (int64, error)
	CreateApprovalGrant(context.Context, ApprovalGrant) (ApprovalGrant, error)
	GetApprovalGrant(context.Context, string) (ApprovalGrant, bool, error)
	FindApprovalGrant(context.Context, string, Actor, string, string, time.Time) (ApprovalGrant, bool, error)
	ConsumeApprovalGrant(context.Context, string, time.Time) (ApprovalGrant, error)
	ListApprovalGrants(context.Context, GrantQuery) ([]ApprovalGrant, error)
	RevokeApprovalGrant(context.Context, string, time.Time) (ApprovalGrant, error)
	DeleteApprovalRequest(context.Context, string, time.Time) (string, error)
	DeleteApprovalGrant(context.Context, string, time.Time) error
	PruneApprovals(context.Context, ApprovalPruneOptions) (ApprovalPruneResult, error)
}

type ApprovalAuditor interface {
	ApprovalChanged(context.Context, ApprovalRequest)
}

type ApprovalNotice struct {
	Actor    Actor
	Surface  Surface
	Tool     string
	Resource Resource
	Action   Action
	Effects  []Effect
	Summary  string
	Reason   string
}

type ApprovalNotifier interface {
	NotifyApprovalRequired(context.Context, ApprovalNotice)
}

type ApprovalMetrics struct {
	RequestsCreated     uint64
	RequestsCoalesced   uint64
	RequestsRateLimited uint64
	GrantsReused        uint64
	RemoteNotices       uint64
}

type Approver interface {
	Authorize(context.Context, EvaluationInput) error
}

type BatchApproval interface {
	Commit(context.Context) error
}

type BatchApprover interface {
	PrepareBatch(context.Context, []EvaluationInput) (BatchApproval, error)
}

type OperationBatchApprover interface {
	PrepareOperationBatch(context.Context, []EvaluationInput, []EvaluationInput) (BatchApproval, error)
}

type ApprovalOptions struct {
	RequestTTL       time.Duration
	OnceTTL          time.Duration
	SessionTTL       time.Duration
	Now              func() time.Time
	Auditor          ApprovalAuditor
	RequestRetention time.Duration
	GrantRetention   time.Duration
	CleanupInterval  time.Duration
	CleanupBatchSize int
	RateLimit        int
	RateWindow       time.Duration
	Notifier         ApprovalNotifier
}

type ApprovalService struct {
	store          ApprovalStore
	opts           ApprovalOptions
	mu             sync.Mutex
	waiters        map[string][]chan ApprovalRequest
	rateWindows    map[string][]time.Time
	pendingPrompts map[string]struct{}
	metrics        ApprovalMetrics
}

func NewApprovalService(store ApprovalStore, opts ApprovalOptions) (*ApprovalService, error) {
	if store == nil {
		return nil, errors.New("approval store is required")
	}

	if opts.RequestTTL <= 0 {
		opts.RequestTTL = DefaultApprovalRequestTTL
	}
	if opts.OnceTTL <= 0 {
		opts.OnceTTL = DefaultApprovalOnceTTL
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = DefaultApprovalSessionTTL
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.RequestRetention < 0 || opts.GrantRetention < 0 {
		return nil, errors.New("approval retention must be greater than or equal to zero")
	}
	if opts.RequestRetention == 0 {
		opts.RequestRetention = DefaultApprovalRequestRetention
	}
	if opts.GrantRetention == 0 {
		opts.GrantRetention = DefaultApprovalGrantRetention
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = DefaultApprovalCleanupInterval
	}
	if opts.CleanupBatchSize <= 0 {
		opts.CleanupBatchSize = DefaultApprovalCleanupBatchSize
	}
	if opts.RateLimit <= 0 {
		opts.RateLimit = DefaultApprovalRateLimit
	}
	if opts.RateWindow <= 0 {
		opts.RateWindow = DefaultApprovalRateWindow
	}

	return &ApprovalService{
		store:          store,
		opts:           opts,
		waiters:        make(map[string][]chan ApprovalRequest),
		rateWindows:    make(map[string][]time.Time),
		pendingPrompts: make(map[string]struct{}),
	}, nil
}

func (s *ApprovalService) Metrics() ApprovalMetrics {
	if s == nil {
		return ApprovalMetrics{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.metrics
}

func (s *ApprovalService) Recover(ctx context.Context) error {
	if s == nil || s.store == nil {
		return errors.New("approval service is required")
	}
	if _, err := s.store.CancelPendingApprovals(ctx, s.opts.Now()); err != nil {
		return err
	}

	_, err := s.Prune(ctx, false)
	return err
}

func (s *ApprovalService) StartCleanup(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(s.opts.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.Prune(context.WithoutCancel(ctx), false)
			}
		}
	}()
}

func (s *ApprovalService) Prune(ctx context.Context, dryRun bool) (ApprovalPruneResult, error) {
	if s == nil || s.store == nil {
		return ApprovalPruneResult{}, errors.New("approval service is required")
	}

	return s.store.PruneApprovals(ctx, ApprovalPruneOptions{
		Now:              s.opts.Now(),
		RequestRetention: s.opts.RequestRetention,
		GrantRetention:   s.opts.GrantRetention,
		BatchSize:        s.opts.CleanupBatchSize,
		DryRun:           dryRun,
	})
}

func (s *ApprovalService) Authorize(ctx context.Context, input EvaluationInput) error {
	_, err := s.authorize(ctx, input, true)
	return err
}

func (s *ApprovalService) authorize(
	ctx context.Context,
	input EvaluationInput,
	consumeOnce bool,
) (ApprovalGrant, error) {
	if s == nil || s.store == nil {
		return ApprovalGrant{}, errors.New("approval service is unavailable")
	}

	authorization, operation, fingerprint, err := normalizeApprovalInput(ctx, input)
	if err != nil {
		return ApprovalGrant{}, err
	}
	now := s.opts.Now()
	summary := strings.TrimSpace(input.ApprovalSummary)
	if summary == "" {
		summary = getApprovalSummary(operation)
	}
	grant, ok, err := s.store.FindApprovalGrant(
		ctx,
		fingerprint,
		authorization.Actor,
		authorization.Profile,
		authorization.SessionID,
		now,
	)
	if err != nil {
		return ApprovalGrant{}, fmt.Errorf("failed to read approval grant: %w", err)
	}
	if ok {
		if consumeOnce && grant.Scope == GrantOnce {
			if _, err := s.store.ConsumeApprovalGrant(ctx, grant.ID, now); err != nil {
				return ApprovalGrant{}, fmt.Errorf("failed to consume approval grant: %w", err)
			}
		}
		s.incrementMetric(func(metrics *ApprovalMetrics) { metrics.GrantsReused++ })
		return grant, nil
	}
	if !isInteractiveApprovalActor(authorization) {
		if s.opts.Notifier != nil {
			s.opts.Notifier.NotifyApprovalRequired(ctx, ApprovalNotice{
				Actor:    authorization.Actor,
				Surface:  authorization.Surface,
				Tool:     operation.Tool,
				Resource: operation.Resource,
				Action:   operation.Action,
				Effects:  append([]Effect(nil), operation.Effects...),
				Summary:  summary,
				Reason:   input.ApprovalReason,
			})
			s.incrementMetric(func(metrics *ApprovalMetrics) { metrics.RemoteNotices++ })
		}
		return ApprovalGrant{}, &DecisionError{
			Code: ErrorCodeApprovalRequired,
			Evaluation: Evaluation{
				Decision: DecisionAsk,
				Reason:   "approval requires an interactive local owner surface",
			}}
	}
	if !s.reserveApprovalPrompt(authorization, fingerprint, now) {
		s.incrementMetric(func(metrics *ApprovalMetrics) { metrics.RequestsRateLimited++ })
		return ApprovalGrant{}, &DecisionError{
			Code: ErrorCodeApprovalRateLimited,
			Evaluation: Evaluation{
				Decision: DecisionDeny,
				Reason:   "approval request rate limit exceeded",
			}}
	}

	request := ApprovalRequest{
		ID:          nanoid.MustGenerate(ApprovalRequestIDPrefix),
		Fingerprint: fingerprint,
		Actor:       authorization.Actor,
		SurfaceKind: authorization.SurfaceKind,
		Surface:     authorization.Surface,
		Profile:     authorization.Profile,
		SessionID:   authorization.SessionID,
		RunID:       authorization.RunID,
		Tool:        operation.Tool,
		Resource:    operation.Resource,
		Action:      operation.Action,
		Effects:     append([]Effect(nil), operation.Effects...),
		Summary:     summary,
		Reason:      input.ApprovalReason,
		Operations:  getApprovalOperations(input, operation),
		Status:      ApprovalPending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.opts.RequestTTL),
	}
	request, inserted, err := s.store.CreateApprovalRequest(ctx, request)
	if err != nil {
		s.releaseApprovalPrompt(fingerprint)
		return ApprovalGrant{}, fmt.Errorf("failed to create approval request: %w", err)
	}
	if inserted {
		s.incrementMetric(func(metrics *ApprovalMetrics) { metrics.RequestsCreated++ })
	} else {
		s.incrementMetric(func(metrics *ApprovalMetrics) { metrics.RequestsCoalesced++ })
	}
	s.audit(ctx, request)

	resolved, err := s.wait(ctx, request)
	if err != nil {
		return ApprovalGrant{}, err
	}
	s.audit(ctx, resolved)
	if resolved.Status != ApprovalApproved {
		return ApprovalGrant{}, &DecisionError{
			Code: ErrorCodeDenied,
			Evaluation: Evaluation{
				Decision: DecisionDeny,
				Reason:   "approval " + string(resolved.Status),
			}}
	}
	grant, ok, err = s.store.FindApprovalGrant(
		ctx,
		fingerprint,
		authorization.Actor,
		authorization.Profile,
		authorization.SessionID,
		s.opts.Now(),
	)
	if err != nil {
		return ApprovalGrant{}, fmt.Errorf("failed to verify approval grant: %w", err)
	}
	if !ok {
		return ApprovalGrant{}, errors.New("approved request has no matching grant")
	}
	if consumeOnce && grant.Scope == GrantOnce {
		if _, err := s.store.ConsumeApprovalGrant(ctx, grant.ID, s.opts.Now()); err != nil {
			return ApprovalGrant{}, fmt.Errorf("failed to consume approval grant: %w", err)
		}
	}
	return grant, nil
}

func (s *ApprovalService) AuthorizeBatch(ctx context.Context, inputs []EvaluationInput) error {
	prepared, err := s.PrepareBatch(ctx, inputs)
	if err != nil {
		return err
	}
	return prepared.Commit(ctx)
}

func (s *ApprovalService) PrepareBatch(ctx context.Context, inputs []EvaluationInput) (BatchApproval, error) {
	return s.PrepareOperationBatch(ctx, inputs, inputs)
}

func (s *ApprovalService) PrepareOperationBatch(
	ctx context.Context,
	asked []EvaluationInput,
	complete []EvaluationInput,
) (BatchApproval, error) {
	if len(asked) == 0 || len(complete) == 0 {
		return nil, errors.New("approval batch requires at least one operation")
	}
	authorization, ok := FromContext(ctx)
	if !ok {
		return nil, errors.New("authorization context is required")
	}
	fingerprints := make([]string, 0, len(complete))
	completeFingerprints := make(map[string]struct{}, len(complete))
	operations := make([]Operation, 0, len(complete))
	effects := make([]Effect, 0)
	ownerRequired := false
	for _, input := range complete {
		operation, err := input.Operation.Normalize()
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
		fingerprint := Fingerprint(authorization, operation)
		fingerprints = append(fingerprints, fingerprint)
		completeFingerprints[fingerprint] = struct{}{}
		for _, effect := range operation.Effects {
			if !slices.Contains(effects, effect) {
				effects = append(effects, effect)
			}
		}
		ownerRequired = ownerRequired || operation.OwnerRequired
	}
	for _, input := range asked {
		operation, err := input.Operation.Normalize()
		if err != nil {
			return nil, err
		}
		if _, exists := completeFingerprints[Fingerprint(authorization, operation)]; !exists {
			return nil, errors.New("approval operation is not part of the complete batch")
		}
	}
	if len(complete) == 1 {
		grant, err := s.authorize(ctx, asked[0], false)
		if err != nil {
			return nil, err
		}
		return &preparedBatchApproval{service: s, grant: grant}, nil
	}
	slices.Sort(effects)
	digest := sha256.Sum256([]byte(strings.Join(fingerprints, "\x00")))
	tool := getBatchTool(operations)
	grant, err := s.authorize(ctx, EvaluationInput{
		Operation: Operation{
			Tool: tool, Resource: operations[0].Resource, Action: ActionExecute,
			Effects: effects, Target: "batch:" + hex.EncodeToString(digest[:]), OwnerRequired: ownerRequired,
		},
		ApprovalSummary:    fmt.Sprintf("%s · approve %d operations", tool, len(complete)),
		ApprovalReason:     getBatchApprovalReason(asked, operations),
		approvalOperations: getApprovalOperationSummaries(operations),
	}, false)
	if err != nil {
		return nil, err
	}
	return &preparedBatchApproval{service: s, grant: grant}, nil
}

type preparedBatchApproval struct {
	service *ApprovalService
	grant   ApprovalGrant
	once    sync.Once
	err     error
}

func (p *preparedBatchApproval) Commit(ctx context.Context) error {
	if p == nil || p.service == nil || p.grant.Scope != GrantOnce {
		return nil
	}
	p.once.Do(func() {
		_, p.err = p.service.store.ConsumeApprovalGrant(ctx, p.grant.ID, p.service.opts.Now())
		if p.err != nil {
			p.err = fmt.Errorf("failed to consume approval grant: %w", p.err)
		}
	})
	return p.err
}

func (s *ApprovalService) Resolve(ctx context.Context, id string, approved bool, scope GrantScope) (ApprovalRequest, error) {
	if s == nil || s.store == nil {
		return ApprovalRequest{}, errors.New("approval service is required")
	}

	if !approved {
		scope = ""
	} else if scope != GrantOnce && scope != GrantSession && scope != GrantAlways {
		return ApprovalRequest{}, errors.New("approval scope must be one of: once, session, always")
	}
	existing, ok, err := s.store.GetApprovalRequest(ctx, id)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if !ok {
		return ApprovalRequest{}, errors.New("approval request not found")
	}
	wantedStatus := ApprovalDenied
	if approved {
		wantedStatus = ApprovalApproved
	}
	if existing.Status != ApprovalPending {
		if existing.Status == wantedStatus && existing.Scope == scope {
			return existing, nil
		}

		return ApprovalRequest{}, errors.New("approval request is already resolved")
	}
	if approved && scope == GrantAlways && !isAlwaysApprovalAvailable(existing.Effects) {
		return ApprovalRequest{}, errors.New("always approval is unavailable for these effects")
	}
	now := s.opts.Now()
	request, err := s.store.ResolveApprovalRequest(ctx, id, wantedStatus, scope, now)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if approved {
		grant := ApprovalGrant{
			ID:          nanoid.MustGenerate(ApprovalGrantIDPrefix),
			RequestID:   request.ID,
			Fingerprint: request.Fingerprint,
			Actor:       request.Actor,
			Profile:     request.Profile,
			SessionID:   request.SessionID,
			Operations:  slices.Clone(request.Operations),
			Scope:       scope,
			Status:      GrantActive,
			CreatedAt:   now,
			ExpiresAt:   s.getGrantExpiry(now, scope),
		}
		grant, err = s.store.CreateApprovalGrant(ctx, grant)
		if err != nil {
			failed, resolveErr := s.store.ResolveApprovalRequest(ctx, id, ApprovalFailed, scope, now)
			if resolveErr == nil {
				s.audit(ctx, failed)
				s.notify(failed)
			} else {
				s.failRequest(ctx, request, err)
			}
			return ApprovalRequest{}, err
		}
		request.GrantID = grant.ID
	}
	s.audit(ctx, request)
	s.notify(request)
	return request, nil
}

func (s *ApprovalService) Get(ctx context.Context, id string) (ApprovalRequest, bool, error) {
	return s.store.GetApprovalRequest(ctx, id)
}

func (s *ApprovalService) List(ctx context.Context, query ApprovalQuery) ([]ApprovalRequest, error) {
	return s.store.ListApprovalRequests(ctx, query)
}

func (s *ApprovalService) ListGrants(ctx context.Context, query GrantQuery) ([]ApprovalGrant, error) {
	grants, err := s.store.ListApprovalGrants(ctx, query)
	if err != nil {
		return nil, err
	}
	now := s.opts.Now()
	for index := range grants {
		grants[index] = getEffectiveApprovalGrant(grants[index], now)
	}
	return grants, nil
}

func (s *ApprovalService) GetGrant(ctx context.Context, id string) (ApprovalGrant, bool, error) {
	if s == nil || s.store == nil {
		return ApprovalGrant{}, false, errors.New("approval service is required")
	}

	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, ApprovalRequestIDPrefix) {
		request, ok, err := s.store.GetApprovalRequest(ctx, id)
		if err != nil || !ok {
			return ApprovalGrant{}, false, err
		}
		if request.GrantID == "" {
			return ApprovalGrant{}, false, nil
		}
		id = request.GrantID
	}
	grant, ok, err := s.store.GetApprovalGrant(ctx, id)
	if err != nil || !ok {
		return ApprovalGrant{}, ok, err
	}
	return getEffectiveApprovalGrant(grant, s.opts.Now()), true, nil
}

func getEffectiveApprovalGrant(grant ApprovalGrant, now time.Time) ApprovalGrant {
	if grant.Status == GrantActive && grant.IsExpiredAt(now) {
		grant.Status = GrantExpired
	}
	return grant
}

func (s *ApprovalService) Revoke(ctx context.Context, id string) (ApprovalGrant, error) {
	if s == nil || s.store == nil {
		return ApprovalGrant{}, errors.New("approval service is required")
	}

	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, ApprovalRequestIDPrefix) {
		request, ok, err := s.store.GetApprovalRequest(ctx, id)
		if err != nil {
			return ApprovalGrant{}, err
		}
		if !ok {
			return ApprovalGrant{}, errors.New("approval request not found")
		}
		if request.GrantID == "" {
			return ApprovalGrant{}, errors.New("approval request has no grant")
		}
		id = request.GrantID
	}
	return s.store.RevokeApprovalGrant(ctx, id, s.opts.Now())
}

func (s *ApprovalService) Delete(ctx context.Context, id string) (ApprovalDeleteResult, error) {
	if s == nil || s.store == nil {
		return ApprovalDeleteResult{}, errors.New("approval service is required")
	}

	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, ApprovalRequestIDPrefix):
		linkedGrantID, err := s.store.DeleteApprovalRequest(ctx, id, s.opts.Now())
		if err != nil {
			return ApprovalDeleteResult{}, err
		}
		return ApprovalDeleteResult{
			ID: id, Kind: ApprovalRecordRequest, LinkedGrantID: linkedGrantID,
		}, nil
	case strings.HasPrefix(id, ApprovalGrantIDPrefix):
		if err := s.store.DeleteApprovalGrant(ctx, id, s.opts.Now()); err != nil {
			return ApprovalDeleteResult{}, err
		}
		return ApprovalDeleteResult{ID: id, Kind: ApprovalRecordGrant}, nil
	default:
		return ApprovalDeleteResult{}, errors.New("approval or grant id is required")
	}
}

func (s *ApprovalService) wait(ctx context.Context, request ApprovalRequest) (ApprovalRequest, error) {
	updates := make(chan ApprovalRequest, 1)
	s.mu.Lock()
	s.waiters[request.ID] = append(s.waiters[request.ID], updates)
	s.mu.Unlock()
	removed := false
	defer func() {
		if !removed {
			s.removeWaiter(request.ID, updates)
		}
	}()
	current, ok, err := s.store.GetApprovalRequest(ctx, request.ID)
	if err != nil {
		s.failRequest(ctx, request, err)
		return ApprovalRequest{}, err
	}
	if !ok {
		err := errors.New("approval request not found")
		s.failRequest(ctx, request, err)
		return ApprovalRequest{}, err
	}
	if current.Status != ApprovalPending {
		s.releaseApprovalPrompt(request.Fingerprint)
		return current, nil
	}

	timer := time.NewTimer(max(request.ExpiresAt.Sub(s.opts.Now()), 0))
	defer timer.Stop()
	select {
	case resolved := <-updates:
		return resolved, nil
	case <-ctx.Done():
		remaining := s.removeWaiter(request.ID, updates)
		removed = true
		if remaining == 0 {
			terminalCtx := context.WithoutCancel(ctx)
			resolved, err := s.store.ResolveApprovalRequest(
				terminalCtx, request.ID, ApprovalCancelled, "", s.opts.Now(),
			)
			if err == nil {
				s.audit(terminalCtx, resolved)
				s.notify(resolved)
			} else {
				s.failRequest(terminalCtx, request, err)
			}
		}
		return ApprovalRequest{}, ctx.Err()
	case <-timer.C:
		terminalCtx := context.WithoutCancel(ctx)
		resolved, err := s.store.ResolveApprovalRequest(terminalCtx, request.ID, ApprovalExpired, "", s.opts.Now())
		if err != nil {
			s.failRequest(terminalCtx, request, err)
			return ApprovalRequest{}, err
		}
		s.audit(terminalCtx, resolved)
		s.notify(resolved)
		return resolved, nil
	}
}

func (s *ApprovalService) notify(request ApprovalRequest) {
	s.mu.Lock()
	delete(s.pendingPrompts, request.Fingerprint)
	waiters := append([]chan ApprovalRequest(nil), s.waiters[request.ID]...)
	s.mu.Unlock()
	for _, waiter := range waiters {
		select {
		case waiter <- request:
		default:
		}
	}
}

func (s *ApprovalService) removeWaiter(id string, waiter chan ApprovalRequest) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	values := s.waiters[id]
	for index, value := range values {
		if value == waiter {
			values = append(values[:index], values[index+1:]...)
			break
		}
	}
	if len(values) == 0 {
		delete(s.waiters, id)
		return 0
	}
	s.waiters[id] = values
	return len(values)
}

func (s *ApprovalService) getGrantExpiry(now time.Time, scope GrantScope) time.Time {
	switch scope {
	case GrantOnce:
		return now.Add(s.opts.OnceTTL)
	case GrantSession:
		return now.Add(s.opts.SessionTTL)
	default:
		return time.Time{}
	}
}

func (s *ApprovalService) audit(ctx context.Context, request ApprovalRequest) {
	if s.opts.Auditor != nil {
		s.opts.Auditor.ApprovalChanged(ctx, request)
	}
}

func (s *ApprovalService) failRequest(ctx context.Context, request ApprovalRequest, cause error) {
	request.Status = ApprovalFailed
	request.Reason = "approval workflow failed: " + cause.Error()
	request.ResolvedAt = s.opts.Now()
	s.audit(context.WithoutCancel(ctx), request)
	s.notify(request)
}

func normalizeApprovalInput(
	ctx context.Context,
	input EvaluationInput,
) (AuthorizationContext, Operation, string, error) {
	authorization, ok := FromContext(ctx)
	if !ok {
		return AuthorizationContext{}, Operation{}, "", errors.New("authorization context is required")
	}
	operation, err := input.Operation.Normalize()
	if err != nil {
		return AuthorizationContext{}, Operation{}, "", err
	}
	return authorization, operation, Fingerprint(authorization, operation), nil
}

func isInteractiveApprovalActor(authorization AuthorizationContext) bool {
	return authorization.Actor.Kind == ActorLocalOwner &&
		(authorization.Surface == SurfaceCLI ||
			authorization.Surface == SurfaceTUI)
}

func (s *ApprovalService) reserveApprovalPrompt(
	authorization AuthorizationContext,
	fingerprint string,
	now time.Time,
) bool {
	key := string(authorization.Actor.Kind) + "\x00" + authorization.Actor.ID + "\x00" + string(authorization.Surface)
	cutoff := now.Add(-s.opts.RateWindow)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.pendingPrompts[fingerprint]; ok {
		return true
	}

	window := s.rateWindows[key]
	kept := window[:0]
	for _, createdAt := range window {
		if createdAt.After(cutoff) {
			kept = append(kept, createdAt)
		}
	}
	if len(kept) >= s.opts.RateLimit {
		s.rateWindows[key] = kept
		return false
	}
	s.rateWindows[key] = append(kept, now)
	s.pendingPrompts[fingerprint] = struct{}{}

	return true
}

func (s *ApprovalService) releaseApprovalPrompt(fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pendingPrompts, fingerprint)
}

func (s *ApprovalService) incrementMetric(update func(*ApprovalMetrics)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	update(&s.metrics)
}

func getApprovalSummary(operation Operation) string {
	summary := fmt.Sprintf("%s · %s %s", operation.Tool, operation.Action, operation.Resource)
	if operation.Network != nil {
		summary += " " + getSafeNetworkApprovalTarget(*operation.Network)
	}
	if operation.Command != nil {
		summary += " " + filepath.Base(operation.Command.Executable)
	}

	return summary
}

func getBatchTool(operations []Operation) string {
	tool := operations[0].Tool
	for _, operation := range operations[1:] {
		if operation.Tool != tool {
			return "multiple_tools"
		}
	}
	return tool
}

func getBatchApprovalReason(inputs []EvaluationInput, operations []Operation) string {
	explicitReasons := make([]string, 0, len(inputs))
	for _, input := range inputs {
		reason := strings.TrimSpace(input.ApprovalReason)
		if reason != "" && !slices.Contains(explicitReasons, reason) {
			explicitReasons = append(explicitReasons, reason)
		}
	}
	slices.Sort(explicitReasons)
	descriptions := getPresentedApprovalOperationSummaries(operations)
	details := fmt.Sprintf("Approve all %d operations: %s", len(operations), strings.Join(descriptions, "; "))
	if len(explicitReasons) == 0 {
		return details
	}
	return strings.Join(explicitReasons, "; ") + " " + details
}

func getApprovalOperations(input EvaluationInput, operation Operation) []string {
	if len(input.approvalOperations) > 0 {
		return slices.Clone(input.approvalOperations)
	}
	return []string{getApprovalSummary(operation)}
}

func getApprovalOperationSummaries(operations []Operation) []string {
	descriptions := make([]string, len(operations))
	for index, operation := range operations {
		descriptions[index] = getApprovalSummary(operation)
	}
	return descriptions
}

func getPresentedApprovalOperationSummaries(operations []Operation) []string {
	const maxPresentedOperations = 8
	limit := min(len(operations), maxPresentedOperations)
	descriptions := make([]string, 0, limit+1)
	descriptions = append(descriptions, getApprovalOperationSummaries(operations[:limit])...)
	if remaining := len(operations) - limit; remaining > 0 {
		descriptions = append(descriptions, fmt.Sprintf("%d more operations", remaining))
	}
	return descriptions
}

func getSafeNetworkApprovalTarget(target NetworkTarget) string {
	host := net.JoinHostPort(target.Host, strconv.FormatUint(uint64(target.Port), 10))
	return target.Method + " " + target.Scheme + "://" + host + target.Path
}

func isAlwaysApprovalAvailable(effects []Effect) bool {
	for _, effect := range effects {
		if !isAlwaysApprovalEffectAllowed(effect) {
			return false
		}
	}

	return true
}

func isAlwaysApprovalEffectAllowed(effect Effect) bool {
	switch effect {
	case EffectDestructive, EffectCredentialBearing, EffectPrivilegeChanging,
		EffectExecution, EffectNetwork, EffectExternalSystem:
		return false
	default:
		return true
	}
}
