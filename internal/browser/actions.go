package browser

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
)

const (
	defaultActionTimeout             = 15 * time.Second
	maxActionTimeout                 = 2 * time.Minute
	networkQuietPeriod               = 50 * time.Millisecond
	optionalNetworkSettlementTimeout = 2 * time.Second
	maxSnapshotNodes                 = 500
	maxSnapshotChars                 = 30_000
)

var errBackgroundAuthorityUnavailable = errors.New("browser background connection has no active action authority")
var errBackgroundRuleRequired = errors.New("browser background connection requires an explicit structured allow rule")
var errBackgroundNetworkPolicyDenied = errors.New("browser background connection denied by network policy")
var errOptionalNetworkSettlementTimedOut = errors.New("optional browser network settlement timed out")

func (s *Service) ResolveOperations(ctx context.Context, action Action, request ActionRequest) ([]permissions.Operation, error) {
	inputs, err := s.ResolvePermissionInputs(ctx, action, request)
	if err != nil {
		return nil, err
	}
	operations := make([]permissions.Operation, len(inputs))
	for index, input := range inputs {
		operations[index] = input.Operation
	}
	return operations, nil
}

func (s *Service) ResolvePermissionInputs(
	ctx context.Context,
	action Action,
	request ActionRequest,
) ([]permissions.EvaluationInput, error) {
	if s == nil {
		return nil, errors.New("browser service is required")
	}
	if action == ActionExportArtifact {
		operations, err := s.getArtifactExportOperations(ctx, ArtifactExportRequest{
			Handle: request.Handle, Path: request.Path, FileTarget: request.FileTarget, TargetScope: request.TargetScope,
		})
		if err != nil {
			return nil, err
		}
		return getEvaluationInputs(operations), nil
	}
	if action != ActionStart {
		operations, err := s.resolveOperations(ctx, action, request, true)
		if err != nil {
			return nil, err
		}
		return getEvaluationInputs(operations), nil
	}

	profileName := strings.TrimSpace(request.Profile)
	if profileName == "" {
		profileName = s.cfg.DefaultProfile
	}
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return nil, errors.New("browser profile is not configured")
	}
	attached, err := s.resolveAttachment(profile)
	if err != nil {
		return nil, err
	}
	return getStartEvaluationInputs(profile, attached)
}

func (s *Service) resolveOperations(
	ctx context.Context,
	action Action,
	request ActionRequest,
	lockSession bool,
) ([]permissions.Operation, error) {
	if s == nil {
		return nil, errors.New("browser service is required")
	}
	profile := strings.TrimSpace(request.Profile)
	if profile == "" {
		profile = s.cfg.DefaultProfile
	}
	browserRequest := permissions.BrowserRequest{Profile: profile, Action: string(action)}
	if action == ActionStart {
		configured, ok := s.cfg.Profile(profile)
		if !ok {
			return nil, errors.New("browser profile is not configured")
		}
		attached, err := s.resolveAttachment(configured)
		if err != nil {
			return nil, err
		}
		browserRequest.ProfileMode = configured.Mode
		browserRequest.AttachmentScope = attached.scope
		browserRequest.AttachmentID = attached.identity
		browserRequest.Personal = configured.Mode == config.BrowserProfileExistingSession
	}

	if requiresSession(action) {
		owner, err := ownerFromContext(ctx)
		if err != nil {
			return nil, err
		}
		runtime, err := s.getOwnedSession(request.SessionID, owner)
		if err != nil {
			return nil, err
		}
		if s.getRuntimeState(runtime) != SessionReady {
			return nil, &Error{Code: ErrorNotReady, Operation: action, Err: errors.New("browser session is not ready")}
		}
		browserRequest.Profile = runtime.Profile
		browserRequest.OwnerID = runtime.Owner.Actor.ID
		browserRequest.ProfileMode = runtime.ProfileMode
		browserRequest.AttachmentScope = runtime.attachment.scope
		browserRequest.AttachmentID = runtime.attachment.identity
		browserRequest.Personal = runtime.ProfileMode == config.BrowserProfileExistingSession
		if action == ActionOpen && runtime.attachment.scope == config.BrowserAttachmentTargets {
			return nil, &Error{
				Code: ErrorUnavailable, Operation: action,
				Err: errors.New("target-scoped browser attachment cannot create tabs"),
			}
		}
		if isAttachedProfile(runtime.ProfileMode) && actionMayUseNetwork(action) && !isFullAccess(ctx) {
			return nil, &Error{
				Code: ErrorUnavailable, Operation: action,
				Err: errors.New("remote browser network actions require full_access"),
			}
		}
		if requiresTab(action) {
			tab, err := s.getTabForResolution(ctx, runtime, request.TabID, lockSession)
			if err != nil {
				return nil, err
			}
			browserRequest.TabTarget = tab.URL
			if action == ActionType || action == ActionUpload || action == ActionAcceptDialog || action == ActionDismissDialog {
				if reference, ok := tab.refs[strings.TrimSpace(request.Ref)]; ok {
					browserRequest.CredentialBearing = reference.Sensitive
				}
			}
			if action == ActionDownload {
				reference, ok := tab.refs[strings.TrimSpace(request.Ref)]
				if !ok || strings.TrimSpace(reference.TargetURL) == "" {
					return nil, errors.New("browser download reference does not have a target URL")
				}
				target, err := permissions.NetworkTargetFromURL(
					reference.TargetURL, "GET", permissions.NetworkRequestDownload,
				)
				if err != nil {
					return nil, err
				}
				browserRequest.Network = &target
			}
		}
	}
	if action == ActionUpload {
		if err := checkCanonicalFileTarget(request.Path, request.FileTarget); err != nil {
			return nil, err
		}
		browserRequest.FileTarget = request.FileTarget
		browserRequest.TargetScope = request.TargetScope
	}

	if hasNavigationTarget(action) {
		var targetURL string
		if action == ActionOpen || action == ActionNavigate {
			targetURL = strings.TrimSpace(request.URL)
			if targetURL == "" {
				return nil, errors.New("browser navigation URL is required")
			}
		} else {
			owner, err := ownerFromContext(ctx)
			if err != nil {
				return nil, err
			}
			runtime, err := s.getOwnedSession(request.SessionID, owner)
			if err != nil {
				return nil, err
			}
			tab, err := s.getTabForResolution(ctx, runtime, request.TabID, lockSession)
			if err != nil {
				return nil, err
			}
			targetURL = tab.URL
		}
		target, err := permissions.NetworkTargetFromURL(targetURL, "GET", permissions.NetworkRequestNavigation)
		if err != nil {
			return nil, err
		}
		browserRequest.Network = &target
	}

	return browserRequest.Operations()
}

func (s *Service) Tabs(ctx context.Context, sessionID string) ([]Tab, error) {
	runtime, backend, err := s.getInteractiveRuntime(ctx, sessionID, ActionTabs)
	if err != nil {
		return nil, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	operations, err := s.resolveOperations(ctx, ActionTabs, ActionRequest{SessionID: sessionID}, false)
	if err != nil {
		return nil, err
	}
	if err := s.checkOperations(ctx, operations); err != nil {
		return nil, err
	}

	tabs, err := backend.ListTabs(ctx)
	if err != nil {
		return nil, getActionError(ActionTabs, err)
	}
	result := s.setBackendTabs(runtime, tabs)
	s.touchRuntime(runtime)
	return result, nil
}

func (s *Service) Open(ctx context.Context, request ActionRequest) (Tab, error) {
	runtime, backend, err := s.getInteractiveRuntime(ctx, request.SessionID, ActionOpen)
	if err != nil {
		return Tab{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, ActionOpen, request, false); err != nil {
		return Tab{}, err
	}
	actionCtx, finishAction, err := s.prepareNetworkAction(ctx, runtime, backend, ActionOpen, "")
	if err != nil {
		return Tab{}, getActionError(ActionOpen, err)
	}
	defer finishAction()
	backendTab, err := backend.OpenTab(actionCtx, request.URL)
	if err != nil {
		return Tab{}, getActionError(ActionOpen, err)
	}
	if !isBackendTabAllowed(runtime, backendTab) {
		_ = backend.CloseTab(context.WithoutCancel(ctx), backendTab.ID)
		return Tab{}, &Error{
			Code: ErrorOwnership, Operation: ActionOpen,
			Err: errors.New("created browser tab is outside the configured attachment scope"),
		}
	}
	if err := waitForOptionalNetworkSettlement(actionCtx, backend, backendTab.ID); err != nil {
		return Tab{}, getActionError(ActionOpen, err)
	}
	tab := s.setBackendTab(runtime, backendTab, true)
	s.touchRuntime(runtime)
	return tab, nil
}

func (s *Service) Focus(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runTabMutation(ctx, ActionFocus, request, false, func(
		ctx context.Context, backend InteractiveBackendSession, tab *managedTab,
	) error {
		return backend.FocusTab(ctx, tab.ID)
	})
}

func (s *Service) CloseTab(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runTabMutation(ctx, ActionClose, request, false, func(
		ctx context.Context, backend InteractiveBackendSession, tab *managedTab,
	) error {
		return backend.CloseTab(ctx, tab.ID)
	})
}

func (s *Service) Navigate(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runNavigation(ctx, ActionNavigate, request, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string,
	) (BackendTab, error) {
		return backend.Navigate(ctx, tabID, request.URL)
	})
}

func (s *Service) Back(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runNavigation(ctx, ActionBack, request, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string,
	) (BackendTab, error) {
		return backend.Back(ctx, tabID)
	})
}

func (s *Service) Forward(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runNavigation(ctx, ActionForward, request, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string,
	) (BackendTab, error) {
		return backend.Forward(ctx, tabID)
	})
}

func (s *Service) Reload(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runNavigation(ctx, ActionReload, request, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string,
	) (BackendTab, error) {
		return backend.Reload(ctx, tabID)
	})
}

func (s *Service) Snapshot(ctx context.Context, request ActionRequest) (Snapshot, error) {
	runtime, backend, err := s.getInteractiveRuntime(ctx, request.SessionID, ActionSnapshot)
	if err != nil {
		return Snapshot{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, ActionSnapshot, request, false); err != nil {
		return Snapshot{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Snapshot{}, err
	}
	backendSnapshot, err := backend.Snapshot(ctx, tab.ID)
	if err != nil {
		return Snapshot{}, getActionError(ActionSnapshot, err)
	}
	result := s.setSnapshot(runtime, tab.ID, backendSnapshot)
	s.touchRuntime(runtime)
	return result, nil
}

func (s *Service) Click(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runElementMutation(ctx, ActionClick, request, true, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string, nodeID int64,
	) error {
		return backend.Click(ctx, tabID, nodeID)
	})
}

func (s *Service) Type(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runElementMutation(ctx, ActionType, request, true, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string, nodeID int64,
	) error {
		return backend.Type(ctx, tabID, nodeID, request.Text, request.Replace)
	})
}

func (s *Service) Select(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runElementMutation(ctx, ActionSelect, request, true, func(
		ctx context.Context, backend InteractiveBackendSession, tabID string, nodeID int64,
	) error {
		return backend.Select(ctx, tabID, nodeID, request.Value)
	})
}

func (s *Service) Press(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runTabMutation(ctx, ActionPress, request, true, func(
		ctx context.Context, backend InteractiveBackendSession, tab *managedTab,
	) error {
		return backend.Press(ctx, tab.ID, request.Key)
	})
}

func (s *Service) Scroll(ctx context.Context, request ActionRequest) (Tab, error) {
	return s.runTabMutation(ctx, ActionScroll, request, false, func(
		ctx context.Context, backend InteractiveBackendSession, tab *managedTab,
	) error {
		return backend.Scroll(ctx, tab.ID, request.X, request.Y)
	})
}

func (s *Service) Wait(ctx context.Context, request ActionRequest) (Tab, error) {
	runtime, backend, err := s.getInteractiveRuntime(ctx, request.SessionID, ActionWait)
	if err != nil {
		return Tab{}, err
	}
	timeout, err := getActionTimeout(request.Timeout)
	if err != nil {
		return Tab{}, err
	}

	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, ActionWait, request, false); err != nil {
		return Tab{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Tab{}, err
	}
	var nodeID int64
	if request.Condition == WaitVisible {
		nodeID, err = getReference(tab, request.Ref)
		if err != nil {
			return Tab{}, err
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := backend.Wait(waitCtx, tab.ID, request.Condition, request.Value, nodeID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return Tab{}, &Error{Code: ErrorTimeout, Operation: ActionWait, Retryable: true, Err: errors.New("browser wait timed out")}
		}
		return Tab{}, getActionError(ActionWait, err)
	}
	s.touchRuntime(runtime)
	return tab.Tab, nil
}

func (s *Service) authorizeAction(
	ctx context.Context,
	action Action,
	request ActionRequest,
	lockSession bool,
) error {
	operations, err := s.resolveOperations(ctx, action, request, lockSession)
	if err != nil {
		return err
	}
	return s.checkOperations(ctx, operations)
}

func (s *Service) getInteractiveRuntime(
	ctx context.Context,
	sessionID string,
	action Action,
) (*managedSession, InteractiveBackendSession, error) {
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := s.getOwnedSession(sessionID, owner)
	if err != nil {
		return nil, nil, err
	}
	if s.getRuntimeState(runtime) != SessionReady {
		return nil, nil, &Error{Code: ErrorNotReady, Operation: action, Err: errors.New("browser session is not ready")}
	}
	backend, ok := runtime.backend.(InteractiveBackendSession)
	if !ok {
		return nil, nil, &Error{Code: ErrorUnavailable, Operation: action, Err: errors.New("browser backend does not support interaction")}
	}
	return runtime, backend, nil
}

func (s *Service) runNavigation(
	ctx context.Context,
	action Action,
	request ActionRequest,
	run func(context.Context, InteractiveBackendSession, string) (BackendTab, error),
) (Tab, error) {
	runtime, backend, err := s.getInteractiveRuntime(ctx, request.SessionID, action)
	if err != nil {
		return Tab{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, action, request, false); err != nil {
		return Tab{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Tab{}, err
	}
	actionCtx, finishAction, err := s.prepareNetworkAction(ctx, runtime, backend, action, tab.ID)
	if err != nil {
		return Tab{}, getActionError(action, err)
	}
	defer finishAction()
	backendTab, err := run(actionCtx, backend, tab.ID)
	if err != nil {
		return Tab{}, getActionError(action, err)
	}
	if err := waitForOptionalNetworkSettlement(actionCtx, backend, tab.ID); err != nil {
		return Tab{}, getActionError(action, err)
	}
	result := s.setBackendTab(runtime, backendTab, true)
	s.bumpTabGeneration(runtime, result.ID)
	s.touchRuntime(runtime)
	return s.getTabCopy(runtime, result.ID), nil
}

func (s *Service) runElementMutation(
	ctx context.Context,
	action Action,
	request ActionRequest,
	invalidate bool,
	run func(context.Context, InteractiveBackendSession, string, int64) error,
) (Tab, error) {
	return s.runTabMutation(ctx, action, request, invalidate, func(
		ctx context.Context, backend InteractiveBackendSession, tab *managedTab,
	) error {
		nodeID, err := getReference(tab, request.Ref)
		if err != nil {
			return err
		}
		return run(ctx, backend, tab.ID, nodeID)
	})
}

func (s *Service) runTabMutation(
	ctx context.Context,
	action Action,
	request ActionRequest,
	invalidate bool,
	run func(context.Context, InteractiveBackendSession, *managedTab) error,
) (Tab, error) {
	runtime, backend, err := s.getInteractiveRuntime(ctx, request.SessionID, action)
	if err != nil {
		return Tab{}, err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	if err := s.authorizeAction(ctx, action, request, false); err != nil {
		return Tab{}, err
	}
	tab, err := s.getTab(ctx, runtime, request.TabID)
	if err != nil {
		return Tab{}, err
	}
	actionCtx, finishAction, err := s.prepareNetworkAction(ctx, runtime, backend, action, tab.ID)
	if err != nil {
		return Tab{}, getActionError(action, err)
	}
	defer finishAction()
	if err := run(actionCtx, backend, tab); err != nil {
		return Tab{}, getActionError(action, err)
	}
	if err := waitForNetworkSettlement(actionCtx, backend, tab.ID); err != nil {
		return Tab{}, getActionError(action, err)
	}
	if action == ActionClose {
		runtime.tabMu.Lock()
		delete(runtime.tabs, tab.ID)
		if runtime.activeTabID == tab.ID {
			runtime.activeTabID = ""
		}
		runtime.tabMu.Unlock()
	} else if action == ActionFocus {
		runtime.tabMu.Lock()
		setActiveTab(runtime, tab.ID)
		runtime.tabMu.Unlock()
	} else if invalidate {
		s.bumpTabGeneration(runtime, tab.ID)
	}
	s.touchRuntime(runtime)
	if action == ActionClose {
		return tab.Tab, nil
	}
	return s.getTabCopy(runtime, tab.ID), nil
}

func (s *Service) prepareNetworkAction(
	ctx context.Context,
	runtime *managedSession,
	backend InteractiveBackendSession,
	action Action,
	tabID string,
) (context.Context, func(), error) {
	if !actionMayUseNetwork(action) {
		return ctx, func() {}, nil
	}
	s.setRuntimePolicy(ctx, runtime)
	actionCtx, budget := newActionBudget(ctx, defaultActionTimeout)
	restoreNetwork, err := s.setNetworkAuthorizer(actionCtx, runtime, backend, action, tabID, budget.Pause)
	if err != nil {
		budget.Close()
		return nil, nil, err
	}
	return actionCtx, func() {
		restoreNetwork()
		budget.Close()
	}, nil
}

func (s *Service) setNetworkAuthorizer(
	ctx context.Context,
	runtime *managedSession,
	backend InteractiveBackendSession,
	action Action,
	tabID string,
	pause func() func(),
) (func(), error) {
	authorizing, ok := backend.(NetworkAuthorizingBackendSession)
	if !ok {
		return func() {}, nil
	}
	generationID := uint64(0)
	if runtime.proxy != nil {
		var err error
		generationID, err = runtime.permits.beginGeneration(ctx)
		if err != nil {
			return nil, err
		}
	}
	coordinator := newNetworkAuthorizationCoordinator(ctx, func(
		authorizationCtx context.Context,
		targets []networkAuthorizationTarget,
	) error {
		return s.authorizeNetworkTargets(authorizationCtx, runtime, action, generationID, targets)
	}, pause)
	restoreAuthorizer := authorizing.SetNetworkAuthorizer(tabID, func(
		networkCtx context.Context, target permissions.NetworkTarget,
	) error {
		logEvent := addBrowserNetworkLogFields(log.Debug(), target).
			Str("browser_session_id", runtime.ID).
			Str("browser_tab_id", tabID).
			Str("browser_action", string(action))
		logEvent.Msg("Browser network authorization started")
		if action == ActionDownload {
			target.RequestClass = permissions.NetworkRequestDownload
		}
		err := coordinator.Authorize(networkCtx, target)
		if err == nil {
			addBrowserNetworkLogFields(log.Debug(), target).
				Str("browser_session_id", runtime.ID).
				Str("browser_tab_id", tabID).
				Str("browser_action", string(action)).
				Msg("Browser network authorization completed")
			return nil
		}
		level := log.Warn()
		message := "Browser network authorization failed"
		if errors.Is(err, context.Canceled) {
			level = log.Debug()
			message = "Browser network authorization was cancelled"
		}
		addBrowserNetworkLogFields(level, target).
			Str("browser_session_id", runtime.ID).
			Str("browser_tab_id", tabID).
			Str("browser_action", string(action)).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg(message)
		return err
	})
	return func() {
		restoreAuthorizer()
		coordinator.Close()
		if generationID != 0 {
			if err := runtime.permits.revokeGeneration(generationID); err != nil {
				log.Warn().
					Str("browser_session_id", runtime.ID).
					Uint64("transport_permit_generation", generationID).
					Msg("Browser transport authority revocation completed with connection cleanup errors")
				return
			}
			log.Debug().
				Str("browser_session_id", runtime.ID).
				Uint64("transport_permit_generation", generationID).
				Msg("Browser transport authority revoked")
		}
	}, nil
}

func (s *Service) authorizeNetworkTargets(
	ctx context.Context,
	runtime *managedSession,
	action Action,
	generationID uint64,
	targets []networkAuthorizationTarget,
) error {
	operations := make([]permissions.Operation, 0, len(targets))
	for _, request := range targets {
		target := request.Target
		values, err := (permissions.BrowserRequest{
			Profile: runtime.Profile, Action: string(action), OwnerID: runtime.Owner.Actor.ID,
			ProfileMode: runtime.ProfileMode, AttachmentScope: runtime.attachment.scope,
			AttachmentID: runtime.attachment.identity,
			Personal:     runtime.ProfileMode == config.BrowserProfileExistingSession, Network: &target,
		}).Operations()
		if err != nil {
			return err
		}
		found := false
		for _, operation := range values {
			if operation.Resource == permissions.ResourceNetwork {
				operations = append(operations, operation)
				found = true
				break
			}
		}
		if !found {
			return errors.New("browser request did not resolve a network operation")
		}
	}
	if runtime.proxy == nil {
		return s.checkOperations(ctx, operations)
	}

	for attempt := 0; attempt < 2; attempt++ {
		resolvedAt := s.now()
		freshUntil := resolvedAt.Add(defaultResolutionFreshness)
		permitInputs := make([]transportPermitInput, 0, len(targets))
		policy := runtime.proxy.getPolicy()
		for index, request := range targets {
			addresses, err := policy.Resolve(ctx, request.Target)
			if err != nil {
				input := permissions.EvaluationInput{Operation: operations[index], HardDenyReason: err.Error()}
				if checkErr := s.checkEvaluationInputs(ctx, []permissions.EvaluationInput{input}, false); checkErr != nil {
					return checkErr
				}
				return err
			}
			permitInputs = append(permitInputs, transportPermitInput{
				Target: request.Target, Addresses: addresses, Uses: request.Count,
				ExpiresAt: resolvedAt.Add(defaultTransportPermitTTL), FreshUntil: freshUntil,
			})
		}
		reservation, err := runtime.permits.reserve(generationID, len(permitInputs))
		if err != nil {
			return err
		}
		if err := s.checkResolvedOperations(ctx, operations); err != nil {
			reservation.Cancel()
			return err
		}
		if !freshUntil.After(s.now()) {
			reservation.Cancel()
			continue
		}
		if err := reservation.Commit(permitInputs); err != nil {
			return err
		}
		log.Debug().
			Str("browser_session_id", runtime.ID).
			Uint64("transport_permit_generation", generationID).
			Int("transport_permit_count", len(permitInputs)).
			Time("transport_permit_expires_at", freshUntil).
			Msg("Browser transport authority installed")
		return nil
	}
	return errors.New("browser network resolution expired during authorization")
}

func (s *Service) authorizeBackgroundConnect(
	ctx context.Context,
	runtime *managedSession,
	target permissions.NetworkTarget,
) (*transportPermitLease, error) {
	generation, ok := runtime.permits.getActiveGeneration()
	if !ok {
		return nil, errBackgroundAuthorityUnavailable
	}
	target.Method = "CONNECT"
	target.RequestClass = permissions.NetworkRequestBackground
	target, err := target.Normalize()
	if err != nil {
		return nil, err
	}
	operation, err := (permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionConnect,
		Effects: []permissions.Effect{permissions.EffectNetwork, permissions.EffectExternalSystem},
		Network: &target, OwnerID: runtime.Owner.Actor.ID,
	}).Normalize()
	if err != nil {
		return nil, err
	}
	evaluation, err := s.checker.Check(generation.Context, permissions.EvaluationInput{Operation: operation})
	if err != nil {
		permissions.ObserveDecision(generation.Context, operation, evaluation)
		return nil, err
	}
	if evaluation.Decision != permissions.DecisionAllow || !evaluation.MatchedConfiguredRule {
		permissions.ObserveDecision(generation.Context, operation, permissions.Evaluation{
			Decision: permissions.DecisionDeny, ReasonCode: permissions.ReasonBackgroundRule,
			Reason: errBackgroundRuleRequired.Error(), Preset: evaluation.Preset,
		})
		return nil, errBackgroundRuleRequired
	}
	addresses, err := runtime.proxy.getPolicy().Resolve(ctx, target)
	if err != nil {
		permissions.ObserveDecision(generation.Context, operation, permissions.Evaluation{
			Decision: permissions.DecisionDeny, ReasonCode: permissions.ReasonHardDeny,
			Reason: errBackgroundNetworkPolicyDenied.Error(), Rule: evaluation.Rule, Preset: evaluation.Preset,
			MatchedConfiguredRule: true,
		})
		return nil, fmt.Errorf("%w: %v", errBackgroundNetworkPolicyDenied, err)
	}
	permissions.ObserveDecision(generation.Context, operation, evaluation)
	if err := runtime.permits.install(generation.ID, []transportPermitInput{{
		Target: target, Addresses: addresses, Uses: 1, ExpiresAt: s.now().Add(defaultTransportPermitTTL),
	}}); err != nil {
		return nil, err
	}
	lease, err := runtime.permits.acquire(target)
	if err != nil {
		return nil, err
	}
	addBrowserNetworkLogFields(log.Debug(), target).
		Str("browser_session_id", runtime.ID).
		Str("permission_rule", evaluation.Rule).
		Msg("Browser background connection received explicit transport authority")
	return lease, nil
}

func waitForNetworkSettlement(ctx context.Context, backend InteractiveBackendSession, tabID string) error {
	settling, ok := backend.(NetworkSettlingBackendSession)
	if !ok {
		return nil
	}
	return settling.WaitForNetworkIdle(ctx, tabID, networkQuietPeriod)
}

func waitForOptionalNetworkSettlement(
	ctx context.Context,
	backend InteractiveBackendSession,
	tabID string,
) error {
	settlementCtx, cancel := context.WithTimeoutCause(
		ctx,
		optionalNetworkSettlementTimeout,
		errOptionalNetworkSettlementTimedOut,
	)
	defer cancel()

	err := waitForNetworkSettlement(settlementCtx, backend, tabID)
	if errors.Is(err, errOptionalNetworkSettlementTimedOut) ||
		errors.Is(context.Cause(settlementCtx), errOptionalNetworkSettlementTimedOut) {
		return nil
	}
	return err
}

func (s *Service) getTabForResolution(
	ctx context.Context,
	runtime *managedSession,
	tabID string,
	lockSession bool,
) (*managedTab, error) {
	if lockSession {
		runtime.actionMu.Lock()
		defer runtime.actionMu.Unlock()
	}

	return s.getTab(ctx, runtime, tabID)
}

func (s *Service) getTab(ctx context.Context, runtime *managedSession, tabID string) (*managedTab, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		runtime.tabMu.RLock()
		tabID = runtime.activeTabID
		runtime.tabMu.RUnlock()
	}
	runtime.tabMu.RLock()
	tab := cloneManagedTab(runtime.tabs[tabID])
	runtime.tabMu.RUnlock()
	if tab != nil {
		return tab, nil
	}
	backend, ok := runtime.backend.(InteractiveBackendSession)
	if !ok {
		return nil, &Error{Code: ErrorUnavailable, Err: errors.New("browser backend does not support tabs")}
	}
	tabs, err := backend.ListTabs(ctx)
	if err != nil {
		return nil, err
	}
	s.setBackendTabs(runtime, tabs)
	runtime.tabMu.RLock()
	tab = cloneManagedTab(runtime.tabs[tabID])
	if tab == nil && tabID == "" && runtime.activeTabID != "" {
		tab = cloneManagedTab(runtime.tabs[runtime.activeTabID])
	}
	runtime.tabMu.RUnlock()
	if tab == nil {
		return nil, &Error{Code: ErrorNotFound, Err: errors.New("browser tab not found")}
	}
	return tab, nil
}

func (s *Service) setBackendTabs(runtime *managedSession, values []BackendTab) []Tab {
	runtime.tabMu.Lock()
	defer runtime.tabMu.Unlock()
	seen := make(map[string]struct{}, len(values))
	result := make([]Tab, 0, len(values))
	for _, value := range values {
		if !isBackendTabAllowed(runtime, value) {
			continue
		}
		seen[value.ID] = struct{}{}
		tab := setBackendTabLocked(runtime, value)
		result = append(result, tab.Tab)
	}
	for id := range runtime.tabs {
		if _, ok := seen[id]; !ok {
			delete(runtime.tabs, id)
		}
	}
	slices.SortFunc(result, func(left, right Tab) int {
		return strings.Compare(left.ID, right.ID)
	})
	return result
}

func isBackendTabAllowed(runtime *managedSession, tab BackendTab) bool {
	switch runtime.attachment.scope {
	case "", config.BrowserAttachmentBrowser:
		return true
	case config.BrowserAttachmentContext:
		return tab.BrowserContextID == runtime.attachment.contextID
	case config.BrowserAttachmentTargets:
		_, ok := runtime.attachment.targetIDs[tab.ID]
		return ok
	default:
		return false
	}
}

func (s *Service) setBackendTab(runtime *managedSession, value BackendTab, active bool) Tab {
	runtime.tabMu.Lock()
	defer runtime.tabMu.Unlock()
	value.Active = value.Active || active
	return setBackendTabLocked(runtime, value).Tab
}

func setBackendTabLocked(runtime *managedSession, value BackendTab) *managedTab {
	tab := runtime.tabs[value.ID]
	if tab == nil {
		tab = &managedTab{
			Tab:  Tab{ID: value.ID, SessionID: runtime.ID, Generation: 1},
			refs: make(map[string]managedReference),
		}
		runtime.tabs[value.ID] = tab
	}
	tab.Title = value.Title
	tab.URL = value.URL
	if value.Active || runtime.activeTabID == "" {
		setActiveTab(runtime, value.ID)
	}
	return tab
}

func setActiveTab(runtime *managedSession, id string) {
	runtime.activeTabID = id
	for tabID, tab := range runtime.tabs {
		tab.Active = tabID == id
	}
}

func (s *Service) setSnapshot(runtime *managedSession, tabID string, value BackendSnapshot) Snapshot {
	runtime.tabMu.Lock()
	defer runtime.tabMu.Unlock()
	tab := runtime.tabs[tabID]
	tab.Generation++
	tab.URL = value.URL
	tab.Title = value.Title
	tab.refs = make(map[string]managedReference)
	result := Snapshot{TabID: tab.ID, URL: tab.URL, Title: tab.Title, Generation: tab.Generation}
	charCount := 0
	for _, valueNode := range value.Nodes {
		if len(result.Nodes) >= maxSnapshotNodes || charCount >= maxSnapshotChars {
			result.Truncated = true
			break
		}
		node := SnapshotNode{
			Role: valueNode.Role, Name: valueNode.Name, Value: valueNode.Value,
			Description: valueNode.Description, Disabled: valueNode.Disabled,
			Properties: valueNode.Properties,
		}
		if valueNode.Sensitive {
			tab.sensitive = true
			node.Value = ""
			node.Properties = nil
		}
		nodeSize := getSnapshotNodeSize(node)
		if charCount+nodeSize > maxSnapshotChars {
			result.Truncated = true
			break
		}
		if valueNode.BackendNodeID != 0 && isActionableRole(node.Role) {
			node.Ref = fmt.Sprintf(
				"r%xg%de%d", getReferenceScope(runtime.ID, tab.ID), tab.Generation, len(tab.refs)+1,
			)
			tab.refs[node.Ref] = managedReference{
				NodeID: valueNode.BackendNodeID, Sensitive: valueNode.Sensitive,
				TargetURL: valueNode.Properties["url"],
			}
		}
		charCount += nodeSize
		result.Nodes = append(result.Nodes, node)
	}
	return result
}

func getReferenceScope(sessionID, tabID string) []byte {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + tabID))
	return digest[:12]
}

func getSnapshotNodeSize(node SnapshotNode) int {
	size := len(node.Role) + len(node.Name) + len(node.Value) + len(node.Description)
	for name, value := range node.Properties {
		size += len(name) + len(value)
	}
	return size
}

func cloneManagedTab(tab *managedTab) *managedTab {
	if tab == nil {
		return nil
	}
	clone := &managedTab{
		Tab: tab.Tab, refs: make(map[string]managedReference, len(tab.refs)), sensitive: tab.sensitive,
	}
	for ref, reference := range tab.refs {
		clone.refs[ref] = reference
	}
	return clone
}

func (s *Service) bumpTabGeneration(runtime *managedSession, tabID string) {
	runtime.tabMu.Lock()
	defer runtime.tabMu.Unlock()
	if tab := runtime.tabs[tabID]; tab != nil {
		tab.Generation++
		tab.refs = make(map[string]managedReference)
	}
}

func (s *Service) getTabCopy(runtime *managedSession, tabID string) Tab {
	runtime.tabMu.RLock()
	defer runtime.tabMu.RUnlock()
	if tab := runtime.tabs[tabID]; tab != nil {
		return tab.Tab
	}
	return Tab{}
}

func (s *Service) touchRuntime(runtime *managedSession) {
	s.mu.Lock()
	runtime.LastActive = s.now()
	s.mu.Unlock()
}

func getReference(tab *managedTab, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	reference, ok := tab.refs[ref]
	if !ok || ref == "" {
		return 0, &Error{Code: ErrorStaleReference, Err: errors.New("browser element reference is stale or unknown")}
	}
	return reference.NodeID, nil
}

func getActionTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout == 0 {
		return defaultActionTimeout, nil
	}
	if timeout < 0 || timeout > maxActionTimeout {
		return 0, errors.New("browser action timeout must be between zero and two minutes")
	}
	return timeout, nil
}

func getActionError(action Action, err error) error {
	if _, ok := permissions.GetDecisionError(err); ok {
		return err
	}
	var browserErr *Error
	if errors.As(err, &browserErr) {
		return err
	}
	if errors.Is(err, errBrowserActionTimedOut) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: ErrorTimeout, Operation: action, Retryable: true, Err: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Code: ErrorCancelled, Operation: action, Retryable: true, Err: err}
	}
	return &Error{Code: ErrorUnavailable, Operation: action, Retryable: true, Err: err}
}

func requiresSession(action Action) bool {
	return action != ActionStatus && action != ActionProfiles && action != ActionStart && action != ActionExportArtifact
}

func requiresTab(action Action) bool {
	switch action {
	case ActionFocus, ActionClose, ActionNavigate, ActionReload, ActionSnapshot, ActionClick, ActionType,
		ActionScreenshot, ActionPDF, ActionConsole, ActionPress, ActionScroll, ActionSelect, ActionUpload,
		ActionDownload, ActionAcceptDialog, ActionDismissDialog, ActionWait, ActionBack, ActionForward:
		return true
	default:
		return false
	}
}

func hasNavigationTarget(action Action) bool {
	return action == ActionOpen || action == ActionNavigate || action == ActionReload ||
		action == ActionBack || action == ActionForward
}

func actionMayUseNetwork(action Action) bool {
	switch action {
	case ActionOpen, ActionNavigate, ActionReload, ActionBack, ActionForward,
		ActionClick, ActionType, ActionPress, ActionSelect, ActionUpload, ActionDownload,
		ActionAcceptDialog, ActionDismissDialog:
		return true
	default:
		return false
	}
}

func isAttachedProfile(mode string) bool {
	return mode == config.BrowserProfileRemoteCDP || mode == config.BrowserProfileExistingSession
}

func isActionableRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "button", "checkbox", "combobox", "link", "listbox", "menuitem", "option", "radio",
		"searchbox", "slider", "spinbutton", "switch", "tab", "textbox":
		return true
	default:
		return false
	}
}
