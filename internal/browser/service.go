package browser

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/nanoid"
)

const (
	browserSessionIDPrefix  = "browser_"
	maxBrowserStartAttempts = 2
)

var browserLog = logutils.Module("browser")

type Service struct {
	cfg                   config.BrowserConfig
	checker               permissions.Checker
	approver              permissions.Approver
	backend               Backend
	policy                NetworkPolicy
	artifacts             *artifactStore
	attachmentIdentityKey []byte
	attachmentKeyMu       sync.RWMutex
	resolveCredential     CredentialResolver
	now                   func() time.Time
	lifetime              context.Context
	mu                    sync.RWMutex
	sessions              map[string]*managedSession
	cancel                context.CancelFunc
	closed                bool
}

func (s *Service) SetApprover(approver permissions.Approver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.approver = approver
	s.mu.Unlock()
}

type managedSession struct {
	Session
	backend      BackendSession
	proxy        *egressProxy
	permits      *transportPermitLedger
	remoteRelay  *remoteCDPRelay
	lease        *profileLease
	dataDir      string
	downloadRoot string
	ephemeral    bool
	resourceMu   sync.Mutex
	cleanupOnce  sync.Once
	cleanupErr   error
	cleaned      bool
	tabMu        sync.RWMutex
	tabs         map[string]*managedTab
	activeTabID  string
	actionMu     sync.Mutex
	attachment   attachment
}

type managedTab struct {
	Tab
	refs      map[string]managedReference
	sensitive bool
}

type managedReference struct {
	NodeID    int64
	Sensitive bool
	TargetURL string
}

type ServiceOption func(*Service)

func WithNow(now func() time.Time) ServiceOption {
	return func(service *Service) {
		service.now = now
	}
}

func NewService(
	ctx context.Context,
	cfg config.BrowserConfig,
	checker permissions.Checker,
	backend Backend,
	options ...ServiceOption,
) (*Service, error) {
	if checker == nil {
		return nil, errors.New("browser permission checker is required")
	}
	if backend == nil {
		return nil, errors.New("browser backend is required")
	}
	policy, err := NewNetworkPolicy(cfg.Network)
	if err != nil {
		return nil, err
	}
	if cfg.StartTimeout <= 0 || cfg.InactivityTimeout <= 0 || cfg.CleanupInterval <= 0 || cfg.TerminalRetention <= 0 {
		return nil, errors.New("browser lifecycle durations must be greater than zero")
	}
	if len(cfg.Profiles) == 0 {
		return nil, errors.New("browser profiles are required")
	}
	if _, ok := cfg.Profile(cfg.DefaultProfile); !ok {
		return nil, errors.New("browser default profile is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithCancel(ctx)
	service := &Service{
		cfg: cfg, checker: checker, backend: backend, policy: policy,
		now:               func() time.Time { return time.Now().UTC() },
		resolveCredential: resolveEnvironmentCredential,
		sessions:          make(map[string]*managedSession), lifetime: cleanupCtx, cancel: cancel,
	}
	for _, option := range options {
		option(service)
	}
	if service.now == nil {
		cancel()
		return nil, errors.New("browser clock is required")
	}
	if service.resolveCredential == nil {
		cancel()
		return nil, errors.New("browser credential resolver is required")
	}
	service.artifacts, err = newArtifactStore(cfg.Artifacts, service.now)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := service.artifacts.cleanup(nil); err != nil {
		cancel()
		return nil, err
	}
	go service.cleanupInactive(cleanupCtx)

	return service, nil
}

func (s *Service) Start(ctx context.Context, request StartRequest) (Session, error) {
	if s == nil {
		return Session{}, errors.New("browser service is required")
	}
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return Session{}, &Error{Code: ErrorClosed, Operation: ActionStart, Err: errors.New("browser service is closed")}
	}
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return Session{}, err
	}
	profileName := strings.TrimSpace(request.Profile)
	if profileName == "" {
		profileName = s.cfg.DefaultProfile
	}
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return Session{}, &Error{
			Code: ErrorInvalidRequest, Operation: ActionStart,
			Err: errors.New("browser profile is not configured"),
		}
	}
	if !s.cfg.Enabled {
		return Session{}, &Error{
			Code: ErrorUnavailable, Operation: ActionStart,
			Err: errors.New("browser service is disabled"),
		}
	}
	attached, err := s.resolveAttachment(profile)
	if err != nil {
		return Session{}, &Error{Code: ErrorInvalidRequest, Operation: ActionStart, Err: err}
	}
	if err := s.authorizeStart(ctx, profile, attached); err != nil {
		return Session{}, err
	}
	fullAccess := isFullAccess(ctx)
	sessionID := nanoid.MustGenerate(browserSessionIDPrefix)
	createdAt := s.now()
	for attempt := 1; attempt <= maxBrowserStartAttempts; attempt++ {
		runtime, err := s.startRuntime(ctx, sessionID, createdAt, owner, profile, attached, fullAccess)
		if err == nil {
			logEvent := browserLog.Debug()
			if attempt > 1 {
				logEvent = browserLog.Info()
			}
			logEvent.
				Str("browser_profile", profile.Name).
				Str("browser_session_id", runtime.ID).
				Int("browser_start_attempt", attempt).
				Bool("browser_start_recovered", attempt > 1).
				Msg("Browser session started")
			return runtime.Session, nil
		}
		if !s.canRetryStart(ctx, err, attempt) {
			browserLog.Warn().
				Str("browser_profile", profile.Name).
				Str("browser_session_id", runtime.ID).
				Str("browser_error_code", string(getBrowserErrorCode(err))).
				Int("browser_start_attempt", attempt).
				Bool("browser_start_will_retry", false).
				Msg("Browser session failed to start")
			return runtime.Session, err
		}
		browserLog.Warn().
			Str("browser_profile", profile.Name).
			Str("browser_session_id", runtime.ID).
			Str("browser_error_code", string(getBrowserErrorCode(err))).
			Int("browser_start_attempt", attempt).
			Int("browser_start_max_attempts", maxBrowserStartAttempts).
			Bool("browser_start_will_retry", true).
			Msg("Browser start attempt failed; retrying with a fresh session")
	}

	return Session{}, &Error{
		Code: ErrorStartFailed, Operation: ActionStart,
		Err: errors.New("browser start attempts exhausted"),
	}
}

func (s *Service) startRuntime(
	ctx context.Context,
	sessionID string,
	createdAt time.Time,
	owner Owner,
	profile config.BrowserProfileConfig,
	attached attachment,
	fullAccess bool,
) (*managedSession, error) {
	runtime := &managedSession{Session: Session{
		ID: sessionID, Profile: profile.Name, ProfileMode: profile.Mode,
		State: SessionStarting, Owner: owner, CreatedAt: createdAt, LastActive: s.now(), Warning: GetProfileWarning(profile),
	}, tabs: make(map[string]*managedTab), attachment: attached, permits: newTransportPermitLedger(s.now)}
	runtime.permits.sessionID = runtime.ID
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return runtime, &Error{
			Code: ErrorClosed, Operation: ActionStart,
			Err: errors.New("browser service is closed"),
		}
	}
	s.sessions[runtime.ID] = runtime
	s.mu.Unlock()

	launch, err := s.prepareLaunch(ctx, profile, runtime, fullAccess)
	if err != nil {
		wrapped := &Error{Code: ErrorStartFailed, Operation: ActionStart, Err: err}
		s.failStart(runtime, wrapped)
		return runtime, wrapped
	}
	startCtx, cancel := context.WithTimeout(ctx, s.cfg.StartTimeout)
	stopLifetimeWait := context.AfterFunc(s.lifetime, cancel)
	defer stopLifetimeWait()
	defer cancel()
	backendSession, err := s.backend.Start(startCtx, launch)
	if err != nil {
		wrapped := &Error{Code: ErrorStartFailed, Operation: ActionStart, Retryable: true, Err: err}
		s.failStart(runtime, wrapped)
		return runtime, wrapped
	}
	runtime.resourceMu.Lock()
	if runtime.cleaned {
		runtime.resourceMu.Unlock()
		_ = backendSession.Close(context.Background())
		cause := &Error{
			Code: ErrorClosed, Operation: ActionStart, Err: errors.New("browser service closed during startup"),
		}
		s.failStart(runtime, cause)
		return runtime, cause
	}
	runtime.backend = backendSession
	runtime.resourceMu.Unlock()
	if err := backendSession.Health(startCtx); err != nil {
		wrapped := &Error{Code: ErrorHealthFailed, Operation: ActionStart, Retryable: true, Err: err}
		s.failStart(runtime, wrapped)
		return runtime, wrapped
	}

	s.mu.Lock()
	if s.closed || runtime.State == SessionStopping || runtime.State == SessionStopped {
		s.mu.Unlock()
		_ = s.cleanupRuntime(context.Background(), runtime)
		return runtime, &Error{
			Code: ErrorClosed, Operation: ActionStart,
			Err: errors.New("browser service closed during startup"),
		}
	}
	runtime.State = SessionReady
	runtime.LastActive = s.now()
	s.mu.Unlock()

	return runtime, nil
}

func (s *Service) canRetryStart(ctx context.Context, err error, attempt int) bool {
	if attempt >= maxBrowserStartAttempts ||
		ctx.Err() != nil ||
		s.lifetime.Err() != nil ||
		errors.Is(err, context.Canceled) {
		return false
	}
	browserErr, ok := GetError(err)
	return ok && browserErr.Retryable
}

func getBrowserErrorCode(err error) ErrorCode {
	browserErr, ok := GetError(err)
	if !ok {
		return ""
	}
	return browserErr.Code
}

func (s *Service) Stop(ctx context.Context, id string) (Session, error) {
	if s == nil {
		return Session{}, errors.New("browser service is required")
	}
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return Session{}, err
	}
	runtime, err := s.getOwnedSession(id, owner)
	if err != nil {
		return Session{}, err
	}
	s.mu.RLock()
	if runtime.State == SessionStopped {
		result := runtime.Session
		s.mu.RUnlock()
		return result, nil
	}
	s.mu.RUnlock()
	operations, err := (permissions.BrowserRequest{
		Profile: runtime.Profile, Action: string(ActionStop), OwnerID: runtime.Owner.Actor.ID,
		ProfileMode: runtime.ProfileMode, AttachmentScope: runtime.attachment.scope,
		AttachmentID: runtime.attachment.identity, Personal: runtime.ProfileMode == config.BrowserProfileExistingSession,
	}).Operations()
	if err != nil {
		return Session{}, err
	}
	if err := s.checkOperations(ctx, operations); err != nil {
		return Session{}, err
	}

	return s.stopRuntime(ctx, runtime)
}

func (s *Service) Status() Status {
	if s == nil {
		return Status{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := make([]Session, 0, len(s.sessions))
	for _, runtime := range s.sessions {
		sessions = append(sessions, runtime.Session)
	}
	slices.SortFunc(sessions, func(left, right Session) int {
		return strings.Compare(left.ID, right.ID)
	})
	profiles := make([]Profile, 0, len(s.cfg.Profiles))
	for _, profile := range s.cfg.Profiles {
		available := s.cfg.Enabled
		warning := GetProfileWarning(profile)
		if isAttachedProfile(profile.Mode) {
			_, err := s.resolveAttachment(profile)
			available = available && err == nil
			if err != nil {
				warning = strings.TrimSpace(warning + " Browser attachment configuration is unavailable.")
			}
		}
		profiles = append(profiles, Profile{
			Name: profile.Name, Mode: profile.Mode, Default: profile.Name == s.cfg.DefaultProfile,
			Available: available, Warning: warning,
		})
	}
	slices.SortFunc(profiles, func(left, right Profile) int {
		return strings.Compare(left.Name, right.Name)
	})

	return Status{Enabled: s.cfg.Enabled, Profiles: profiles, Sessions: sessions}
}

func (s *Service) Touch(ctx context.Context, id string) error {
	owner, err := ownerFromContext(ctx)
	if err != nil {
		return err
	}
	runtime, err := s.getOwnedSession(id, owner)
	if err != nil {
		return err
	}
	runtime.actionMu.Lock()
	defer runtime.actionMu.Unlock()
	s.setRuntimePolicy(ctx, runtime)
	s.mu.Lock()
	if runtime.State != SessionReady {
		s.mu.Unlock()
		return errors.New("browser session is not ready")
	}
	runtime.LastActive = s.now()
	s.mu.Unlock()

	return nil
}

func (s *Service) Authorize(ctx context.Context, request permissions.BrowserRequest) error {
	operations, err := request.Operations()
	if err != nil {
		return err
	}

	return s.checkOperations(ctx, operations)
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	runtimes := make([]*managedSession, 0, len(s.sessions))
	for _, runtime := range s.sessions {
		if runtime.State != SessionStopped {
			runtimes = append(runtimes, runtime)
		}
	}
	s.mu.Unlock()
	var closeErrors []error
	for _, runtime := range runtimes {
		if _, err := s.stopRuntime(ctx, runtime); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	closeErrors = append(closeErrors, s.artifacts.cleanup(s.isArtifactOwnerActive))

	return errors.Join(closeErrors...)
}

func (s *Service) authorizeStart(
	ctx context.Context,
	profile config.BrowserProfileConfig,
	attached attachment,
) error {
	inputs, err := getStartEvaluationInputs(profile, attached)
	if err != nil {
		return err
	}
	return s.checkEvaluationInputs(ctx, inputs, true)
}

func getStartEvaluationInputs(
	profile config.BrowserProfileConfig,
	attached attachment,
) ([]permissions.EvaluationInput, error) {
	personal := profile.Mode == config.BrowserProfileExistingSession
	operations, err := (permissions.BrowserRequest{
		Profile: profile.Name, ProfileMode: profile.Mode, Action: string(ActionStart), Personal: personal,
		AttachmentScope: attached.scope, AttachmentID: attached.identity,
	}).Operations()
	if err != nil {
		return nil, err
	}
	inputs := getEvaluationInputs(operations)
	if profile.Mode != config.BrowserProfileRemoteCDP && !personal {
		return inputs, nil
	}
	target, err := permissions.NetworkTargetFromURL(
		profile.CDPEndpoint, "CONNECT", permissions.NetworkRequestCDP,
	)
	if err != nil {
		return nil, err
	}

	connect, err := (permissions.BrowserRequest{
		Profile: profile.Name, ProfileMode: profile.Mode, Action: string(ActionConnect), Network: &target,
		AttachmentScope: attached.scope, AttachmentID: attached.identity, Personal: personal,
		CredentialBearing: profile.CredentialRef != "",
	}).Operations()
	if err != nil {
		return nil, err
	}
	connectInputs := getEvaluationInputs(connect)
	warning := GetProfileWarning(profile)
	for index := range connectInputs {
		connectInputs[index].ApprovalReason = warning
		if personal {
			connectInputs[index].ApprovalSummary = "Attach to signed-in browser profile " + profile.Name
		}
	}
	return append(inputs, connectInputs...), nil
}

func (s *Service) prepareLaunch(
	ctx context.Context,
	profile config.BrowserProfileConfig,
	runtime *managedSession,
	fullAccess bool,
) (LaunchOptions, error) {
	runtime.resourceMu.Lock()
	defer runtime.resourceMu.Unlock()
	launch := LaunchOptions{
		Mode: profile.Mode, CDPEndpoint: profile.CDPEndpoint, Timeout: s.cfg.StartTimeout,
	}
	downloadBase := filepath.Join(s.cfg.Artifacts.Root, ".downloads")
	if err := os.MkdirAll(downloadBase, 0o700); err != nil {
		return LaunchOptions{}, err
	}
	if err := os.Chmod(downloadBase, 0o700); err != nil {
		return LaunchOptions{}, err
	}
	runtime.downloadRoot = filepath.Join(downloadBase, runtime.ID)
	if err := os.Mkdir(runtime.downloadRoot, 0o700); err != nil {
		return LaunchOptions{}, err
	}
	launch.DownloadRoot = runtime.downloadRoot
	if profile.Mode == config.BrowserProfileRemoteCDP || profile.Mode == config.BrowserProfileExistingSession {
		proxyPolicy := s.getNetworkPolicy(fullAccess)
		var err error
		runtime.remoteRelay, err = startRemoteCDPRelay(
			ctx, profile.CDPEndpoint, runtime.attachment.authorization, proxyPolicy,
		)
		if err != nil {
			return LaunchOptions{}, err
		}
		launch.CDPEndpoint = runtime.remoteRelay.URL()
		launch.AttachmentScope = runtime.attachment.scope
		launch.BrowserContextID = runtime.attachment.contextID
		launch.TargetIDs = slices.Collect(maps.Keys(runtime.attachment.targetIDs))
		slices.Sort(launch.TargetIDs)
		return launch, nil
	}
	executable, err := discoverChromiumExecutable(s.cfg.Executable)
	if err != nil {
		return LaunchOptions{}, err
	}
	launch.Executable = executable
	if profile.Mode == config.BrowserProfileManagedPersistent {
		if err := os.MkdirAll(profile.Directory, 0o700); err != nil {
			return LaunchOptions{}, err
		}
		if err := os.Chmod(profile.Directory, 0o700); err != nil {
			return LaunchOptions{}, err
		}
		runtime.lease, err = acquireProfileLease(profile.Directory)
		if err != nil {
			return LaunchOptions{}, err
		}
		runtime.dataDir = profile.Directory
	} else {
		if err := os.MkdirAll(s.cfg.TemporaryRoot, 0o700); err != nil {
			return LaunchOptions{}, err
		}
		if err := os.Chmod(s.cfg.TemporaryRoot, 0o700); err != nil {
			return LaunchOptions{}, err
		}
		runtime.dataDir, err = os.MkdirTemp(s.cfg.TemporaryRoot, "browser-profile-")
		if err != nil {
			return LaunchOptions{}, err
		}
		runtime.ephemeral = true
		if err := os.Chmod(runtime.dataDir, 0o700); err != nil {
			return LaunchOptions{}, err
		}
	}
	launch.DataDir = runtime.dataDir
	proxyPolicy := s.getNetworkPolicy(fullAccess)
	runtime.proxy, err = startEgressProxyWithLedger(proxyPolicy, runtime.permits)
	if err != nil {
		return LaunchOptions{}, err
	}
	runtime.proxy.sessionID = runtime.ID
	runtime.proxy.background = func(
		ctx context.Context,
		target permissions.NetworkTarget,
	) (*transportPermitLease, error) {
		return s.authorizeBackgroundConnect(ctx, runtime, target)
	}
	launch.ProxyURL = runtime.proxy.chromiumURL()
	launch.ProxyUser, launch.ProxySecret = runtime.proxy.authorization.credentials()
	launch.transportPermits = runtime.permits

	return launch, nil
}

func (s *Service) failStart(runtime *managedSession, cause error) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), s.cfg.StartTimeout)
	defer cancel()
	_ = s.cleanupRuntime(cleanupCtx, runtime)
	s.mu.Lock()
	if s.closed || runtime.State == SessionStopping || runtime.State == SessionStopped {
		runtime.State = SessionStopped
	} else {
		runtime.State = SessionFailed
	}
	runtime.Error = cause.Error()
	runtime.LastActive = s.now()
	s.mu.Unlock()
}

func (s *Service) stopRuntime(ctx context.Context, runtime *managedSession) (Session, error) {
	s.mu.Lock()
	if runtime.State == SessionStopped {
		result := runtime.Session
		s.mu.Unlock()
		return result, nil
	}
	runtime.State = SessionStopping
	s.mu.Unlock()
	cleanupCtx, cancel := getCleanupContext(ctx, s.cfg.StartTimeout)
	defer cancel()
	err := s.cleanupRuntime(cleanupCtx, runtime)
	s.mu.Lock()
	runtime.State = SessionStopped
	runtime.LastActive = s.now()
	if err != nil {
		runtime.Error = err.Error()
	}
	result := runtime.Session
	s.mu.Unlock()

	return result, err
}

func (s *Service) cleanupRuntime(ctx context.Context, runtime *managedSession) error {
	runtime.cleanupOnce.Do(func() {
		runtime.resourceMu.Lock()
		defer runtime.resourceMu.Unlock()
		runtime.cleaned = true
		var cleanupErrors []error
		if runtime.backend != nil {
			cleanupErrors = append(cleanupErrors, runtime.backend.Close(ctx))
		}
		if runtime.proxy != nil {
			cleanupErrors = append(cleanupErrors, runtime.proxy.Close(ctx))
		} else if runtime.permits != nil {
			cleanupErrors = append(cleanupErrors, runtime.permits.close())
		}
		if runtime.remoteRelay != nil {
			cleanupErrors = append(cleanupErrors, runtime.remoteRelay.Close(ctx))
		}
		if runtime.lease != nil {
			cleanupErrors = append(cleanupErrors, runtime.lease.Close())
		}
		if runtime.ephemeral && runtime.dataDir != "" {
			cleanupErrors = append(cleanupErrors, os.RemoveAll(runtime.dataDir))
		}
		if runtime.downloadRoot != "" {
			cleanupErrors = append(cleanupErrors, os.RemoveAll(runtime.downloadRoot))
		}
		runtime.cleanupErr = errors.Join(cleanupErrors...)
	})

	return runtime.cleanupErr
}

func (s *Service) getOwnedSession(id string, owner Owner) (*managedSession, error) {
	id = strings.TrimSpace(id)
	s.mu.RLock()
	runtime, ok := s.sessions[id]
	if !ok {
		s.mu.RUnlock()
		return nil, &Error{Code: ErrorNotFound, Err: errors.New("browser session not found")}
	}
	if runtime.Owner.Actor != owner.Actor || runtime.Owner.Profile != owner.Profile ||
		runtime.Owner.SessionID != owner.SessionID {
		s.mu.RUnlock()
		return nil, &Error{Code: ErrorOwnership, Err: errors.New("browser session belongs to another owner")}
	}
	s.mu.RUnlock()

	return runtime, nil
}

func (s *Service) getRuntimeState(runtime *managedSession) SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return runtime.State
}

func (s *Service) checkOperations(ctx context.Context, operations []permissions.Operation) error {
	return s.checkEvaluationInputs(ctx, getEvaluationInputs(operations), true)
}

func (s *Service) checkResolvedOperations(ctx context.Context, operations []permissions.Operation) error {
	return s.checkEvaluationInputs(ctx, getEvaluationInputs(operations), false)
}

func getEvaluationInputs(operations []permissions.Operation) []permissions.EvaluationInput {
	inputs := make([]permissions.EvaluationInput, 0, len(operations))
	for _, operation := range operations {
		inputs = append(inputs, permissions.EvaluationInput{Operation: operation})
	}
	return inputs
}

func (s *Service) checkEvaluationInputs(
	ctx context.Context,
	inputs []permissions.EvaluationInput,
	resolveNetworkTargets bool,
) error {
	asks := make([]permissions.EvaluationInput, 0, len(inputs))
	for _, input := range inputs {
		operation := input.Operation
		operation, err := operation.Normalize()
		if err != nil {
			return err
		}
		input.Operation = operation
		if permissions.IsExactOperationAuthorized(ctx, operation) {
			continue
		}
		if resolveNetworkTargets && operation.Network != nil {
			if _, resolveErr := s.policy.Resolve(ctx, *operation.Network); resolveErr != nil {
				input.HardDenyReason = resolveErr.Error()
			}
		}
		evaluation, err := s.checker.Check(ctx, input)
		permissions.ObserveDecision(ctx, operation, evaluation)
		if err == nil {
			continue
		}
		decisionErr, isDecisionError := permissions.GetDecisionError(err)
		if !isDecisionError || decisionErr.Code != permissions.ErrorCodeApprovalRequired {
			return err
		}
		if input.ApprovalReason == "" {
			input.ApprovalReason = evaluation.Reason
		}
		asks = append(asks, input)
	}
	if len(asks) == 0 {
		return nil
	}
	s.mu.RLock()
	approver := s.approver
	s.mu.RUnlock()
	if approver == nil {
		return &permissions.DecisionError{
			Code:       permissions.ErrorCodeApprovalRequired,
			Evaluation: permissions.Evaluation{Decision: permissions.DecisionAsk, Reason: asks[0].ApprovalReason},
		}
	}
	if len(asks) == 1 {
		return approver.Authorize(ctx, asks[0])
	}
	batchApprover, ok := approver.(permissions.BatchApprover)
	if !ok {
		return errors.New("approval service does not support atomic browser operation batches")
	}
	prepared, err := batchApprover.PrepareBatch(ctx, asks)
	if err != nil {
		return err
	}
	return prepared.Commit(ctx)
}

func (s *Service) cleanupInactive(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.Close(context.Background())
			return
		case <-ticker.C:
			now := s.now()
			cutoff := now.Add(-s.cfg.InactivityTimeout)
			terminalCutoff := now.Add(-s.cfg.TerminalRetention)
			s.mu.Lock()
			stale := make([]*managedSession, 0)
			for id, runtime := range s.sessions {
				if runtime.State == SessionReady && runtime.LastActive.Before(cutoff) {
					stale = append(stale, runtime)
				}
				if isTerminalSessionState(runtime.State) && !runtime.LastActive.After(terminalCutoff) {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
			for _, runtime := range stale {
				_, _ = s.stopRuntime(context.WithoutCancel(ctx), runtime)
			}
			_ = s.artifacts.cleanup(s.isArtifactOwnerActive)
			_ = s.cleanupAbandonedRuntimeDirectories(filepath.Join(s.cfg.TemporaryRoot, "uploads"), now)
			_ = s.cleanupAbandonedRuntimeDirectories(filepath.Join(s.cfg.Artifacts.Root, ".downloads"), now)
		}
	}
}

func (s *Service) cleanupAbandonedRuntimeDirectories(root string, now time.Time) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-s.cfg.Artifacts.Retention)
	s.mu.RLock()
	active := make(map[string]struct{}, len(s.sessions))
	for id, runtime := range s.sessions {
		if !isTerminalSessionState(runtime.State) {
			active[id] = struct{}{}
		}
	}
	s.mu.RUnlock()
	var cleanupErrors []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), browserSessionIDPrefix) {
			continue
		}
		if _, ok := active[entry.Name()]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		cleanupErrors = append(cleanupErrors, os.RemoveAll(filepath.Join(root, entry.Name())))
	}
	return errors.Join(cleanupErrors...)
}

func (s *Service) isArtifactOwnerActive(owner Owner) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, runtime := range s.sessions {
		if sameArtifactOwner(runtime.Owner, owner) &&
			(runtime.State == SessionStarting || runtime.State == SessionReady || runtime.State == SessionStopping) {
			return true
		}
	}
	return false
}

func (s *Service) getNetworkPolicy(fullAccess bool) NetworkPolicy {
	policy := s.policy
	if fullAccess {
		policy.Strict = false
	}

	return policy
}

func (s *Service) setRuntimePolicy(ctx context.Context, runtime *managedSession) {
	policy := s.getNetworkPolicy(isFullAccess(ctx))
	runtime.resourceMu.Lock()
	defer runtime.resourceMu.Unlock()
	if runtime.proxy != nil {
		runtime.proxy.setPolicy(policy)
	}
	if runtime.remoteRelay != nil {
		runtime.remoteRelay.setPolicy(policy)
	}
}

func isTerminalSessionState(state SessionState) bool {
	return state == SessionStopped || state == SessionFailed
}

func ownerFromContext(ctx context.Context) (Owner, error) {
	authorization, ok := permissions.FromContext(ctx)
	if !ok || authorization.Actor.ID == "" || authorization.Profile == "" || authorization.SessionID == "" {
		return Owner{}, errors.New("browser authorization owner is required")
	}

	return Owner{
		Actor: authorization.Actor, Profile: authorization.Profile,
		SessionID: authorization.SessionID, RunID: authorization.RunID,
	}, nil
}

func isFullAccess(ctx context.Context) bool {
	if permissions.HasFullAccess(ctx) {
		return true
	}
	preset, ok := permissions.PresetFromContext(ctx)
	return ok && preset == permissions.PresetFullAccess
}

func getCleanupContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(timeout)
	if ctx != nil {
		if existing, ok := ctx.Deadline(); ok && existing.After(time.Now()) && existing.Before(deadline) {
			deadline = existing
		}
	}

	return context.WithDeadline(context.Background(), deadline)
}
