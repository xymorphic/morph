package browser

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
)

type ChromiumBackend struct{}

type networkAuthorization struct {
	id        uint64
	ctx       context.Context
	cancel    context.CancelFunc
	authorize NetworkRequestAuthorizer
}

type networkActivity struct {
	pending int
	last    time.Time
	changed chan struct{}
}

type relatedTarget struct {
	targetType   string
	ownerTabID   string
	clientTabIDs map[string]struct{}
	sessions     map[target.SessionID]*relatedTargetSession
}

type relatedTargetSession struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type chromiumSession struct {
	ctx                context.Context
	cancelContext      context.CancelFunc
	cancelBootstrap    context.CancelFunc
	cancelAllocator    context.CancelFunc
	process            *browserProcess
	once               sync.Once
	mu                 sync.Mutex
	activeTabID        string
	openingTargets     int
	tabContexts        map[string]context.Context
	tabCancels         map[string]context.CancelFunc
	proxyUser          string
	proxySecret        string
	networkAuthorizers map[string]networkAuthorization
	nextAuthorization  uint64
	transportPermits   *transportPermitLedger
	openingTabIDs      map[string]struct{}
	openingTabReady    map[string]chan struct{}
	networkErrors      map[string]error
	networkActivity    map[string]*networkActivity
	relatedTargets     map[string]*relatedTarget
	relatedSessions    map[target.SessionID]string
	pageSessions       map[target.SessionID]struct{}
	rootTabID          string
	consoleMessages    map[string][]ConsoleMessage
	popupEvents        map[string]chan struct{}
	dialogResponses    map[string]dialogResponse
	downloadEvents     chan any
	downloadArmed      bool
	downloadFrameIDs   map[cdp.FrameID]struct{}
	downloadGUID       string
	downloadMaxBytes   int64
	downloadLimitSent  bool
	downloadRoot       string
	attachmentScope    string
	browserContextID   string
	attachmentTargets  map[string]struct{}
	quarantinedTargets map[string]struct{}
	attached           bool
	closeErr           error
}

type dialogResponse struct {
	accept     bool
	promptText string
	result     chan error
}

func (ChromiumBackend) Start(ctx context.Context, opts LaunchOptions) (BackendSession, error) {
	if opts.Timeout <= 0 {
		return nil, errors.New("browser startup timeout must be greater than zero")
	}
	if opts.ProxyURL != "" && (opts.ProxyUser == "" || opts.ProxySecret == "") {
		return nil, errors.New("browser proxy credentials are required")
	}
	if opts.ProxyURL == "" && (opts.ProxyUser != "" || opts.ProxySecret != "") {
		return nil, errors.New("browser proxy URL is required for proxy credentials")
	}
	startCtx, cancelStart := context.WithTimeout(ctx, opts.Timeout)
	defer cancelStart()

	var allocatorCtx context.Context
	var cancelAllocator context.CancelFunc
	var process *browserProcess
	switch opts.Mode {
	case config.BrowserProfileManagedEphemeral, config.BrowserProfileManagedPersistent:
		if strings.TrimSpace(opts.Executable) == "" {
			return nil, errors.New("browser executable is required")
		}
		if strings.TrimSpace(opts.DataDir) == "" {
			return nil, errors.New("browser data directory is required")
		}
		process = newBrowserProcess()
		allocatorOptions := append(append([]chromedp.ExecAllocatorOption(nil), chromedp.DefaultExecAllocatorOptions[:]...),
			chromedp.ExecPath(opts.Executable),
			chromedp.UserDataDir(opts.DataDir),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("no-default-browser-check", true),
			chromedp.Flag("disable-background-networking", false),
			chromedp.Flag("disable-component-update", true),
			chromedp.Flag("disable-sync", true),
			chromedp.Flag("disable-quic", true),
			chromedp.Flag("deny-permission-prompts", true),
			chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"),
			chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),
			chromedp.Flag("remote-debugging-address", "127.0.0.1"),
			chromedp.ModifyCmdFunc(func(command *exec.Cmd) {
				process.configure(command)
			}),
			chromedp.WSURLReadTimeout(opts.Timeout),
		)
		if opts.ProxyURL != "" {
			allocatorOptions = append(allocatorOptions,
				chromedp.ProxyServer(opts.ProxyURL),
				chromedp.Flag("proxy-bypass-list", "<-loopback>"),
			)
		}
		allocatorCtx, cancelAllocator = chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	case config.BrowserProfileRemoteCDP, config.BrowserProfileExistingSession:
		if strings.TrimSpace(opts.CDPEndpoint) == "" {
			return nil, errors.New("browser CDP endpoint is required")
		}
		allocatorCtx, cancelAllocator = chromedp.NewRemoteAllocator(context.Background(), opts.CDPEndpoint)
	default:
		return nil, errors.New("browser profile mode is invalid")
	}

	var browserCtx context.Context
	var cancelContext context.CancelFunc
	var cancelBootstrap context.CancelFunc
	var err error
	if opts.Mode == config.BrowserProfileManagedEphemeral ||
		opts.Mode == config.BrowserProfileManagedPersistent {
		browserCtx, cancelContext, cancelBootstrap, err = prepareManagedBrowserContext(startCtx, allocatorCtx)
	} else {
		browserCtx, cancelContext, cancelBootstrap, err = prepareInitialBrowserContext(startCtx, allocatorCtx, opts)
	}
	if err != nil {
		cancelAllocator()
		return nil, err
	}
	if isAttachedProfile(opts.Mode) {
		cancelContext = getAttachedContextCancel(browserCtx, cancelContext)
	}
	session := &chromiumSession{
		ctx: browserCtx, cancelContext: cancelContext, cancelBootstrap: cancelBootstrap,
		cancelAllocator: cancelAllocator, process: process,
		tabContexts: make(map[string]context.Context), tabCancels: make(map[string]context.CancelFunc),
		networkAuthorizers: make(map[string]networkAuthorization), openingTabIDs: make(map[string]struct{}),
		transportPermits: opts.transportPermits, openingTabReady: make(map[string]chan struct{}),
		networkErrors: make(map[string]error), networkActivity: make(map[string]*networkActivity),
		relatedTargets: make(map[string]*relatedTarget), relatedSessions: make(map[target.SessionID]string),
		pageSessions:    make(map[target.SessionID]struct{}),
		consoleMessages: make(map[string][]ConsoleMessage), popupEvents: make(map[string]chan struct{}),
		dialogResponses: make(map[string]dialogResponse), downloadEvents: make(chan any, 4),
		proxyUser: opts.ProxyUser, proxySecret: opts.ProxySecret,
		downloadRoot:    opts.DownloadRoot,
		attachmentScope: opts.AttachmentScope, browserContextID: opts.BrowserContextID,
		attachmentTargets:  make(map[string]struct{}, len(opts.TargetIDs)),
		quarantinedTargets: make(map[string]struct{}), attached: isAttachedProfile(opts.Mode),
	}
	for _, id := range opts.TargetIDs {
		session.attachmentTargets[id] = struct{}{}
	}
	actions := []chromedp.Action{
		network.Enable(),
		page.Enable(),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			_, _, _, _, _, err := browser.GetVersion().Do(actionCtx)
			return err
		}),
	}
	chromedp.ListenTarget(browserCtx, session.getRequestListener(browserCtx, ""))
	if !session.attached {
		chromedp.ListenTarget(browserCtx, session.getRelatedTargetListener(""))
		chromedp.ListenBrowser(browserCtx, session.getBrowserRelatedTargetListener())
		actions = append([]chromedp.Action{
			target.SetAutoAttach(true, true).WithFlatten(true).WithFilter(getRelatedTargetFilter()),
		}, actions...)
	}
	actions = append([]chromedp.Action{fetch.Enable().WithHandleAuthRequests(opts.ProxyUser != "")}, actions...)
	actions = append([]chromedp.Action{
		chromedp.ActionFunc(session.setRootTargetContext),
	}, actions...)
	ready := make(chan error, 1)
	go func() {
		ready <- chromedp.Run(browserCtx, actions...)
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = session.Close(context.Background())
			if opts.Mode == config.BrowserProfileRemoteCDP || opts.Mode == config.BrowserProfileExistingSession {
				return nil, errors.New("browser CDP connection failed")
			}
			return nil, err
		}
		if process != nil {
			if err := process.attach(); err != nil {
				_ = session.Close(context.Background())
				return nil, err
			}
		}
		chromedp.ListenBrowser(browserCtx, session.getPageTargetLifecycleListener())
		chromedp.ListenBrowser(browserCtx, session.getDownloadListener())
		return session, nil
	case <-startCtx.Done():
		_ = session.Close(context.Background())
		return nil, startCtx.Err()
	}
}

func (s *chromiumSession) setRootTargetContext(ctx context.Context) error {
	chromiumCtx := chromedp.FromContext(ctx)
	if chromiumCtx == nil || chromiumCtx.Target == nil {
		return errors.New("browser root target is unavailable")
	}
	rootTabID := string(chromiumCtx.Target.TargetID)
	s.mu.Lock()
	s.rootTabID = rootTabID
	s.tabContexts[rootTabID] = ctx
	s.mu.Unlock()
	chromedp.ListenTarget(ctx, s.getPageEffectListener(ctx, rootTabID))
	chromedp.ListenTarget(ctx, s.getConsoleListener(rootTabID))
	return nil
}

func prepareManagedBrowserContext(
	ctx context.Context,
	allocatorCtx context.Context,
) (context.Context, context.CancelFunc, context.CancelFunc, error) {
	bootstrapCtx, cancelBootstrap := chromedp.NewContext(allocatorCtx)
	stop := context.AfterFunc(ctx, cancelBootstrap)
	defer stop()
	chromiumCtx := chromedp.FromContext(bootstrapCtx)
	if chromiumCtx == nil || chromiumCtx.Allocator == nil {
		cancelBootstrap()
		return nil, nil, nil, errors.New("browser connection is unavailable")
	}
	browser, err := chromiumCtx.Allocator.Allocate(bootstrapCtx)
	if err != nil {
		cancelBootstrap()
		return nil, nil, nil, err
	}
	chromiumCtx.Browser = browser
	attached := make(chan *target.EventAttachedToTarget, 1)
	listenerCtx, cancelListener := context.WithCancel(bootstrapCtx)
	chromedp.ListenBrowser(listenerCtx, func(event any) {
		attachedEvent, ok := event.(*target.EventAttachedToTarget)
		if !ok || attachedEvent.TargetInfo == nil || attachedEvent.TargetInfo.Type != "page" ||
			attachedEvent.TargetInfo.Subtype != "" {
			return
		}
		select {
		case attached <- attachedEvent:
		default:
		}
	})
	browserExecutor := cdp.WithExecutor(bootstrapCtx, browser)
	if err := target.SetAutoAttach(true, true).
		WithFlatten(true).
		WithFilter(getBrowserRelatedTargetFilter()).
		Do(browserExecutor); err != nil {
		cancelListener()
		cancelBootstrap()
		return nil, nil, nil, err
	}
	var initial *target.EventAttachedToTarget
	select {
	case <-ctx.Done():
		cancelListener()
		cancelBootstrap()
		return nil, nil, nil, ctx.Err()
	case initial = <-attached:
		cancelListener()
	}
	browserCtx, cancel := chromedp.NewContext(
		bootstrapCtx,
		chromedp.WithExistingTargetSession(initial.TargetInfo.TargetID, initial.SessionID),
		chromedp.WithExistingTargetSessionType(initial.TargetInfo.Type),
		chromedp.WithExistingTargetSessionWaitingForDebugger(initial.WaitingForDebugger),
	)
	return browserCtx, cancel, cancelBootstrap, nil
}

func prepareInitialBrowserContext(
	ctx context.Context,
	allocatorCtx context.Context,
	opts LaunchOptions,
) (context.Context, context.CancelFunc, context.CancelFunc, error) {
	switch opts.AttachmentScope {
	case config.BrowserAttachmentTargets:
		if len(opts.TargetIDs) == 0 {
			return nil, nil, nil, errors.New("target-scoped browser attachment requires a target ID")
		}
		browserCtx, cancel := chromedp.NewContext(
			allocatorCtx, chromedp.WithTargetID(target.ID(opts.TargetIDs[0])),
		)
		return browserCtx, cancel, nil, nil
	case config.BrowserAttachmentContext, config.BrowserAttachmentBrowser:
		return prepareScopedBrowserContext(ctx, allocatorCtx, opts)
	default:
		browserCtx, cancel := chromedp.NewContext(allocatorCtx)
		return browserCtx, cancel, nil, nil
	}
}

func prepareScopedBrowserContext(
	ctx context.Context,
	allocatorCtx context.Context,
	opts LaunchOptions,
) (context.Context, context.CancelFunc, context.CancelFunc, error) {
	bootstrapCtx, cancelBootstrap := chromedp.NewContext(allocatorCtx)
	stop := context.AfterFunc(ctx, cancelBootstrap)
	defer stop()
	chromiumCtx := chromedp.FromContext(bootstrapCtx)
	if chromiumCtx == nil || chromiumCtx.Allocator == nil {
		cancelBootstrap()
		return nil, nil, nil, errors.New("browser connection is unavailable")
	}
	if chromiumCtx.Browser == nil {
		browser, err := chromiumCtx.Allocator.Allocate(bootstrapCtx)
		if err != nil {
			cancelBootstrap()
			return nil, nil, nil, err
		}
		chromiumCtx.Browser = browser
	}
	infos, err := target.GetTargets().Do(cdp.WithExecutor(bootstrapCtx, chromiumCtx.Browser))
	if err != nil {
		cancelBootstrap()
		return nil, nil, nil, err
	}
	selected := getAttachmentTarget(infos, opts.AttachmentScope, opts.BrowserContextID)
	if selected == "" {
		cancelBootstrap()
		return nil, nil, nil, errors.New("configured browser attachment has no page target")
	}
	browserCtx, cancel := chromedp.NewContext(bootstrapCtx, chromedp.WithTargetID(selected))
	return browserCtx, cancel, cancelBootstrap, nil
}

func getAttachmentTarget(infos []*target.Info, scope string, browserContextID string) target.ID {
	var selected *target.Info
	for _, info := range infos {
		if info == nil || info.Type != "page" || info.Subtype != "" {
			continue
		}
		if scope != config.BrowserAttachmentBrowser &&
			(scope != config.BrowserAttachmentContext || string(info.BrowserContextID) != browserContextID) {
			continue
		}
		if selected == nil || isPreferredAttachmentTarget(info, selected) {
			selected = info
		}
	}
	if selected == nil {
		return ""
	}
	return selected.TargetID
}

func isPreferredAttachmentTarget(candidate, selected *target.Info) bool {
	candidateBlank := candidate.URL == "about:blank"
	selectedBlank := selected.URL == "about:blank"
	if candidateBlank != selectedBlank {
		return candidateBlank
	}
	return string(candidate.TargetID) < string(selected.TargetID)
}

func getAttachedContextCancel(ctx context.Context, cancel context.CancelFunc) context.CancelFunc {
	return func() {
		// chromedp cancellation closes a populated Target. Reverify this detach behavior before upgrading chromedp.
		if chromiumCtx := chromedp.FromContext(ctx); chromiumCtx != nil {
			chromiumCtx.Target = nil
		}
		cancel()
	}
}

func (s *chromiumSession) getDownloadListener() func(any) {
	return func(event any) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.downloadArmed {
			return
		}
		switch value := event.(type) {
		case *browser.EventDownloadWillBegin:
			if _, owned := s.downloadFrameIDs[value.FrameID]; !owned || s.downloadGUID != "" {
				return
			}
			s.downloadGUID = value.GUID
		case *browser.EventDownloadProgress:
			if value.GUID != s.downloadGUID {
				return
			}
			if value.State == browser.DownloadProgressStateInProgress {
				overLimit := value.ReceivedBytes > float64(s.downloadMaxBytes) ||
					value.TotalBytes > float64(s.downloadMaxBytes)
				if !overLimit || s.downloadLimitSent {
					return
				}
				s.downloadLimitSent = true
			}
		default:
			return
		}
		select {
		case s.downloadEvents <- event:
		default:
		}
	}
}

func (s *chromiumSession) getPageTargetLifecycleListener() func(any) {
	return func(event any) {
		if destroyed, ok := event.(*target.EventTargetDestroyed); ok {
			go s.removeRelatedTarget(string(destroyed.TargetID))
			s.mu.Lock()
			delete(s.quarantinedTargets, string(destroyed.TargetID))
			delete(s.popupEvents, string(destroyed.TargetID))
			s.mu.Unlock()
			log.Debug().
				Str("browser_target_id", string(destroyed.TargetID)).
				Msg("Browser target supervision observed target destruction")
			return
		}
		created, ok := event.(*target.EventTargetCreated)
		if !ok || created.TargetInfo == nil || created.TargetInfo.Type != "page" {
			return
		}
		s.mu.Lock()
		targetID := string(created.TargetInfo.TargetID)
		if targetID == s.rootTabID || s.tabContexts[targetID] != nil {
			s.mu.Unlock()
			return
		}
		if s.attached {
			if _, opening := s.openingTabIDs[targetID]; opening {
				s.mu.Unlock()
				return
			}
			s.quarantinedTargets[targetID] = struct{}{}
			s.mu.Unlock()
			return
		}
		if s.claimOpeningTargetLocked(targetID) {
			s.mu.Unlock()
			return
		}
		s.quarantinedTargets[targetID] = struct{}{}
		s.mu.Unlock()
		log.Debug().
			Str("browser_target_id", targetID).
			Msg("Browser target supervision quarantined an unexpected page")
	}
}

func (s *chromiumSession) getRelatedTargetListener(ownerTabID string) func(any) {
	return func(event any) {
		switch value := event.(type) {
		case *target.EventAttachedToTarget:
			if value.TargetInfo == nil {
				return
			}
			go s.attachRelatedTarget(ownerTabID, value)
		case *target.EventDetachedFromTarget:
			go s.removeRelatedTargetSession(value.SessionID)
		}
	}
}

func (s *chromiumSession) handleBrowserPageTarget(event *target.EventAttachedToTarget) {
	if s == nil || event == nil || event.TargetInfo == nil {
		return
	}
	chromiumCtx := chromedp.FromContext(s.ctx)
	if chromiumCtx != nil && chromiumCtx.Target != nil &&
		chromiumCtx.Target.TargetID == event.TargetInfo.TargetID &&
		chromiumCtx.Target.SessionID == event.SessionID {
		_ = cdpruntime.RunIfWaitingForDebugger().Do(cdp.WithExecutor(context.Background(), chromiumCtx.Target))
		return
	}
	targetID := string(event.TargetInfo.TargetID)
	rootTarget := chromiumCtx != nil && chromiumCtx.Target != nil &&
		chromiumCtx.Target.TargetID == event.TargetInfo.TargetID
	if rootTarget && !event.WaitingForDebugger {
		return
	}
	s.mu.Lock()
	if _, handled := s.pageSessions[event.SessionID]; handled {
		s.mu.Unlock()
		return
	}
	s.pageSessions[event.SessionID] = struct{}{}
	allowed := rootTarget
	if !allowed {
		allowed = s.claimOpeningTargetLocked(targetID)
	}
	if !allowed {
		s.quarantinedTargets[targetID] = struct{}{}
	}
	s.mu.Unlock()
	if !allowed {
		log.Debug().
			Str("browser_target_id", targetID).
			Str("browser_target_session_id", string(event.SessionID)).
			Msg("Browser target supervision quarantined an attached popup")
	}
	if allowed && !rootTarget {
		s.attachRelatedTarget(targetID, event)
		s.signalOpeningTargetReady(targetID)
		return
	}
	popupCtx, cancel := chromedp.NewContext(
		s.ctx,
		chromedp.WithExistingTargetSession(event.TargetInfo.TargetID, event.SessionID),
		chromedp.WithExistingTargetSessionType(event.TargetInfo.Type),
		chromedp.WithExistingTargetSessionWaitingForDebugger(event.WaitingForDebugger),
		chromedp.WithExistingTargetSessionResumeOnly(),
	)
	if err := chromedp.Run(popupCtx); err != nil {
		cancel()
		_ = chromedp.Cancel(popupCtx)
		s.signalOpeningTargetReady(targetID)
		log.Warn().
			Str("browser_target_id", targetID).
			Str("browser_target_session_id", string(event.SessionID)).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser could not resume a newly attached page target")
		return
	}
	cancel()
	if err := chromedp.Cancel(popupCtx); err != nil {
		s.signalOpeningTargetReady(targetID)
		log.Warn().
			Str("browser_target_id", targetID).
			Str("browser_target_session_id", string(event.SessionID)).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser could not release a resumed page target")
		return
	}
	s.signalOpeningTargetReady(targetID)
	if allowed {
		return
	}
	browserCtx, err := s.getBrowserExecutorContext(context.Background())
	if err != nil {
		return
	}
	if err := target.CloseTarget(event.TargetInfo.TargetID).Do(browserCtx); err != nil {
		log.Warn().
			Str("browser_target_id", targetID).
			Str("browser_target_session_id", string(event.SessionID)).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser could not close a quarantined popup target")
		return
	}
	if err := waitForTargetClosure(browserCtx, event.TargetInfo.TargetID); err != nil {
		log.Warn().
			Str("browser_target_id", targetID).
			Str("browser_target_session_id", string(event.SessionID)).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser could not confirm quarantined popup closure")
		return
	}
	log.Debug().
		Str("browser_target_id", targetID).
		Str("browser_target_session_id", string(event.SessionID)).
		Msg("Browser target supervision closed a quarantined popup")
	s.mu.Lock()
	delete(s.quarantinedTargets, targetID)
	s.mu.Unlock()
}

func waitForTargetClosure(ctx context.Context, targetID target.ID) error {
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		infos, err := target.GetTargets().Do(waitCtx)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(infos, func(info *target.Info) bool {
			return info != nil && info.TargetID == targetID
		}) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return context.Cause(waitCtx)
		case <-ticker.C:
		}
	}
}

func (s *chromiumSession) claimOpeningTargetLocked(targetID string) bool {
	if _, opening := s.openingTabIDs[targetID]; opening {
		return true
	}
	if s.openingTargets <= 0 {
		return false
	}
	s.openingTargets--
	s.openingTabIDs[targetID] = struct{}{}
	if s.openingTabReady[targetID] == nil {
		s.openingTabReady[targetID] = make(chan struct{})
	}
	return true
}

func (s *chromiumSession) signalOpeningTargetReady(targetID string) {
	s.mu.Lock()
	ready := s.openingTabReady[targetID]
	if ready != nil {
		select {
		case <-ready:
		default:
			close(ready)
		}
	}
	s.mu.Unlock()
}

func (s *chromiumSession) waitForOpeningTargetReady(ctx context.Context, targetID string) error {
	s.mu.Lock()
	ready := s.openingTabReady[targetID]
	if ready == nil {
		ready = make(chan struct{})
		s.openingTabReady[targetID] = ready
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		return nil
	}
}

func (s *chromiumSession) getBrowserRelatedTargetListener() func(any) {
	return func(event any) {
		switch value := event.(type) {
		case *target.EventAttachedToTarget:
			if value.TargetInfo == nil {
				return
			}
			switch value.TargetInfo.Type {
			case "page":
				go s.handleBrowserPageTarget(value)
			case "shared_worker":
				go s.attachRelatedTarget("", value)
			}
		case *target.EventDetachedFromTarget:
			s.mu.Lock()
			delete(s.pageSessions, value.SessionID)
			s.mu.Unlock()
			go s.removeRelatedTargetSession(value.SessionID)
		}
	}
}

func (s *chromiumSession) attachRelatedTarget(
	ownerTabID string,
	event *target.EventAttachedToTarget,
) {
	if s == nil || event == nil || event.TargetInfo == nil || event.SessionID == "" {
		return
	}
	if ownerTabID == "" && !isSharedRelatedTarget(event.TargetInfo.Type) {
		s.mu.Lock()
		ownerTabID = s.rootTabID
		s.mu.Unlock()
	}
	log.Debug().
		Str("browser_target_id", string(event.TargetInfo.TargetID)).
		Str("browser_target_session_id", string(event.SessionID)).
		Str("browser_parent_tab_id", ownerTabID).
		Str("browser_target_type", event.TargetInfo.Type).
		Bool("browser_target_waiting_for_debugger", event.WaitingForDebugger).
		Msg("Browser related target supervision preparing")
	childCtx, cancel := chromedp.NewContext(
		s.ctx,
		chromedp.WithExistingTargetSession(event.TargetInfo.TargetID, event.SessionID),
		chromedp.WithExistingTargetSessionType(event.TargetInfo.Type),
		chromedp.WithExistingTargetSessionWaitingForDebugger(event.WaitingForDebugger),
	)
	targetID := string(event.TargetInfo.TargetID)
	relatedSession := &relatedTargetSession{ctx: childCtx, cancel: cancel}
	s.mu.Lock()
	if _, exists := s.relatedSessions[event.SessionID]; exists {
		s.mu.Unlock()
		cancel()
		return
	}
	related := s.relatedTargets[targetID]
	if related == nil {
		related = &relatedTarget{
			targetType: event.TargetInfo.Type,
			sessions:   make(map[target.SessionID]*relatedTargetSession),
		}
		s.relatedTargets[targetID] = related
	}
	if isSharedRelatedTarget(event.TargetInfo.Type) {
		if related.clientTabIDs == nil {
			related.clientTabIDs = make(map[string]struct{})
		}
		if ownerTabID != "" {
			related.clientTabIDs[ownerTabID] = struct{}{}
		}
	} else if related.ownerTabID == "" {
		related.ownerTabID = ownerTabID
	}
	related.sessions[event.SessionID] = relatedSession
	s.relatedSessions[event.SessionID] = targetID
	if event.TargetInfo.Type == "page" && s.tabContexts[targetID] == nil {
		s.tabContexts[targetID] = childCtx
		s.tabCancels[targetID] = cancel
	}
	s.mu.Unlock()

	chromedp.ListenTarget(childCtx, s.getRelatedRequestListener(childCtx, targetID))
	chromedp.ListenTarget(childCtx, s.getRelatedTargetListener(ownerTabID))
	if event.TargetInfo.Type == "page" {
		chromedp.ListenTarget(childCtx, s.getPageEffectListener(childCtx, targetID))
		chromedp.ListenTarget(childCtx, s.getConsoleListener(targetID))
	}
	actions := make([]chromedp.Action, 0, 4)
	if supportsTargetFetchInterception(event.TargetInfo.Type) {
		actions = append(actions, fetch.Enable().WithHandleAuthRequests(s.proxyUser != ""))
	}
	if supportsRelatedTargetChildren(event.TargetInfo.Type) {
		actions = append(
			actions,
			target.SetAutoAttach(true, true).WithFlatten(true).WithFilter(getRelatedTargetFilter()),
		)
	}
	if event.TargetInfo.Type == "page" {
		actions = append(actions,
			page.SetInterceptFileChooserDialog(true).WithCancel(true),
			cdpruntime.Enable(),
		)
	}
	if err := chromedp.Run(childCtx, actions...); err != nil {
		log.Warn().
			Str("browser_target_id", targetID).
			Str("browser_target_session_id", string(event.SessionID)).
			Str("browser_parent_tab_id", ownerTabID).
			Str("browser_target_type", event.TargetInfo.Type).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser related target setup failed")
		s.removeRelatedTargetSession(event.SessionID)
		return
	}
	log.Debug().
		Str("browser_target_id", targetID).
		Str("browser_target_session_id", string(event.SessionID)).
		Str("browser_parent_tab_id", ownerTabID).
		Str("browser_target_type", event.TargetInfo.Type).
		Msg("Browser related target supervision started")
}

func isSharedRelatedTarget(targetType string) bool {
	return targetType == "shared_worker" || targetType == "service_worker"
}

func supportsRelatedTargetChildren(targetType string) bool {
	switch targetType {
	case "page", "iframe", "worker", "shared_worker":
		return true
	default:
		return false
	}
}

func supportsTargetFetchInterception(targetType string) bool {
	return targetType == "page" || targetType == "iframe"
}

func getRelatedTargetFilter() target.Filter {
	return target.Filter{
		{Type: "iframe"},
		{Type: "worker"},
		{Type: "service_worker"},
		{Exclude: true},
	}
}

func getBrowserRelatedTargetFilter() target.Filter {
	return target.Filter{
		{Type: "page"},
		{Type: "shared_worker"},
		{Exclude: true},
	}
}

func (s *chromiumSession) removeRelatedTargetSession(sessionID target.SessionID) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	targetID := s.relatedSessions[sessionID]
	delete(s.relatedSessions, sessionID)
	related := s.relatedTargets[targetID]
	var relatedSession *relatedTargetSession
	if related != nil {
		relatedSession = related.sessions[sessionID]
		delete(related.sessions, sessionID)
		if len(related.sessions) == 0 {
			delete(s.relatedTargets, targetID)
			if relatedSession != nil && s.tabContexts[targetID] == relatedSession.ctx {
				delete(s.tabContexts, targetID)
				delete(s.tabCancels, targetID)
			}
		}
	}
	s.mu.Unlock()
	if relatedSession != nil {
		relatedSession.cancel()
	}
}

func (s *chromiumSession) removeRelatedTarget(targetID string) {
	if s == nil || targetID == "" {
		return
	}
	s.mu.Lock()
	related := s.relatedTargets[targetID]
	if related == nil {
		s.mu.Unlock()
		return
	}
	delete(s.relatedTargets, targetID)
	delete(s.tabContexts, targetID)
	delete(s.tabCancels, targetID)
	sessions := make([]*relatedTargetSession, 0, len(related.sessions))
	for sessionID, relatedSession := range related.sessions {
		delete(s.relatedSessions, sessionID)
		sessions = append(sessions, relatedSession)
	}
	s.mu.Unlock()
	for _, relatedSession := range sessions {
		relatedSession.cancel()
	}
}

func (s *chromiumSession) getRequestListener(ctx context.Context, tabID string) func(any) {
	return s.getRequestListenerWithAuthorization(ctx, func() (networkAuthorization, string, bool) {
		effectiveTabID := s.getEffectiveTabID(tabID)
		authorization, ok := s.getNetworkAuthorization(effectiveTabID)
		if !ok && tabID == "" {
			authorization, ok = s.getNetworkAuthorization("")
		}
		return authorization, effectiveTabID, ok
	})
}

func (s *chromiumSession) getRelatedRequestListener(ctx context.Context, targetID string) func(any) {
	return s.getRequestListenerWithAuthorization(ctx, func() (networkAuthorization, string, bool) {
		return s.getRelatedNetworkAuthorization(targetID)
	})
}

func (s *chromiumSession) getRequestListenerWithAuthorization(
	ctx context.Context,
	resolveAuthorization func() (networkAuthorization, string, bool),
) func(any) {
	return func(event any) {
		switch value := event.(type) {
		case *fetch.EventRequestPaused:
			go func() {
				authorization, tabID, ok := resolveAuthorization()
				activityID := tabID
				if activityID == "" {
					activityID = string(value.RequestID)
				}
				s.markNetworkRequestStarted(activityID)
				defer s.markNetworkRequestFinished(activityID)
				requestClass := permissions.NetworkRequestSubresource
				if value.RedirectedRequestID != "" {
					requestClass = permissions.NetworkRequestRedirect
				} else if value.ResourceType == network.ResourceTypeDocument {
					requestClass = permissions.NetworkRequestNavigation
				}
				target, err := permissions.NetworkTargetFromURL(value.Request.URL, value.Request.Method, requestClass)
				if err != nil {
					log.Warn().
						Str("browser_tab_id", tabID).
						Str("network_method", value.Request.Method).
						Str("network_resource_type", string(value.ResourceType)).
						Str("error", getSafeBrowserNetworkError(err)).
						Msg("Browser intercepted an invalid network request")
					_ = chromedp.Run(ctx, fetch.FailRequest(value.RequestID, network.ErrorReasonBlockedByClient))
					return
				}
				addBrowserNetworkLogFields(log.Debug(), target).
					Str("browser_tab_id", tabID).
					Str("network_resource_type", string(value.ResourceType)).
					Msg("Browser network request intercepted")
				if !ok {
					addBrowserNetworkLogFields(log.Warn(), target).
						Str("browser_tab_id", tabID).
						Str("network_resource_type", string(value.ResourceType)).
						Msg("Browser network request had no active authorizer")
					_ = chromedp.Run(ctx, fetch.FailRequest(value.RequestID, network.ErrorReasonBlockedByClient))
					return
				}
				err = authorization.authorize(authorization.ctx, target)
				if err == nil {
					err = authorization.ctx.Err()
				}
				if err != nil {
					level := log.Warn()
					message := "Browser network request authorization failed"
					if errors.Is(err, context.Canceled) {
						level = log.Debug()
						message = "Browser network request authorization was cancelled"
					}
					addBrowserNetworkLogFields(level, target).
						Str("browser_tab_id", tabID).
						Uint64("browser_network_authorization_id", authorization.id).
						Str("network_resource_type", string(value.ResourceType)).
						Str("error", getSafeBrowserNetworkError(err)).
						Msg(message)
					s.recordNetworkError(tabID, authorization.id, err)
					_ = chromedp.Run(ctx, fetch.FailRequest(value.RequestID, network.ErrorReasonBlockedByClient))
					return
				}
				if err := s.continueAuthorizedRequest(ctx, tabID, authorization.id, value.RequestID); err != nil {
					addBrowserNetworkLogFields(log.Warn(), target).
						Str("browser_tab_id", tabID).
						Uint64("browser_network_authorization_id", authorization.id).
						Str("network_resource_type", string(value.ResourceType)).
						Str("error", getSafeBrowserNetworkError(err)).
						Msg("Browser failed to continue an authorized network request")
					_ = chromedp.Run(ctx, fetch.FailRequest(value.RequestID, network.ErrorReasonBlockedByClient))
					return
				}
				addBrowserNetworkLogFields(log.Debug(), target).
					Str("browser_tab_id", tabID).
					Uint64("browser_network_authorization_id", authorization.id).
					Str("network_resource_type", string(value.ResourceType)).
					Msg("Browser continued an authorized network request")
			}()
		case *fetch.EventAuthRequired:
			response := &fetch.AuthChallengeResponse{Response: fetch.AuthChallengeResponseResponseCancelAuth}
			if value.AuthChallenge != nil && value.AuthChallenge.Source == fetch.AuthChallengeSourceProxy {
				response.Response = fetch.AuthChallengeResponseResponseProvideCredentials
				response.Username = s.proxyUser
				response.Password = s.proxySecret
			}
			go func() {
				_ = chromedp.Run(ctx, fetch.ContinueWithAuth(value.RequestID, response))
			}()
		case *network.EventWebSocketCreated:
			s.authorizeWebSocket(resolveAuthorization, value.URL)
		}
	}
}

func (s *chromiumSession) authorizeWebSocket(
	resolveAuthorization func() (networkAuthorization, string, bool),
	rawURL string,
) {
	authorization, tabID, ok := resolveAuthorization()
	if !ok {
		return
	}
	target, err := permissions.NetworkTargetFromURL(
		rawURL,
		getWebSocketPermissionMethod(rawURL),
		permissions.NetworkRequestWebSocket,
	)
	if err != nil {
		log.Warn().
			Str("browser_tab_id", tabID).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser observed an invalid WebSocket target")
		return
	}
	finishPending := func() {}
	if s.transportPermits != nil {
		finishPending = s.transportPermits.beginPending(target)
	}
	go func() {
		defer finishPending()
		activityID := tabID
		if activityID == "" {
			activityID = target.Host
		}
		s.markNetworkRequestStarted(activityID)
		defer s.markNetworkRequestFinished(activityID)
		addBrowserNetworkLogFields(log.Debug(), target).
			Str("browser_tab_id", tabID).
			Uint64("browser_network_authorization_id", authorization.id).
			Msg("Browser WebSocket authorization started")
		err := authorization.authorize(authorization.ctx, target)
		if err == nil {
			err = authorization.ctx.Err()
		}
		if err != nil {
			addBrowserNetworkLogFields(log.Warn(), target).
				Str("browser_tab_id", tabID).
				Uint64("browser_network_authorization_id", authorization.id).
				Str("error", getSafeBrowserNetworkError(err)).
				Msg("Browser WebSocket authorization failed")
			s.recordNetworkError(tabID, authorization.id, err)
			return
		}
		addBrowserNetworkLogFields(log.Debug(), target).
			Str("browser_tab_id", tabID).
			Uint64("browser_network_authorization_id", authorization.id).
			Msg("Browser WebSocket authorization completed")
	}()
}

func getWebSocketPermissionMethod(rawURL string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "wss:") {
		return http.MethodConnect
	}
	return http.MethodGet
}

func (s *chromiumSession) getEffectiveTabID(tabID string) string {
	if tabID != "" {
		return tabID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rootTabID
}

func (s *chromiumSession) getRelatedNetworkAuthorization(
	targetID string,
) (networkAuthorization, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	related := s.relatedTargets[targetID]
	if related == nil {
		return networkAuthorization{}, "", false
	}
	if !isSharedRelatedTarget(related.targetType) {
		authorization, ok := s.getNetworkAuthorizationLocked(related.ownerTabID)
		return authorization, related.ownerTabID, ok
	}
	var selected networkAuthorization
	selectedTabID := ""
	for tabID := range related.clientTabIDs {
		authorization, ok := s.getNetworkAuthorizationLocked(tabID)
		if !ok {
			continue
		}
		if selected.authorize != nil && selected.id != authorization.id {
			return networkAuthorization{}, "", false
		}
		selected = authorization
		selectedTabID = tabID
	}
	if selected.authorize == nil {
		return networkAuthorization{}, "", false
	}
	return selected, selectedTabID, true
}

func (s *chromiumSession) getNetworkAuthorization(tabID string) (networkAuthorization, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getNetworkAuthorizationLocked(tabID)
}

func (s *chromiumSession) getNetworkAuthorizationLocked(tabID string) (networkAuthorization, bool) {
	authorization, ok := s.networkAuthorizers[tabID]
	if !ok {
		if _, opening := s.openingTabIDs[tabID]; opening {
			authorization, ok = s.networkAuthorizers[""]
		}
	}
	if !ok {
		authorization, ok = s.networkAuthorizers["*"]
	}
	return authorization, ok
}

func (s *chromiumSession) setTabNetworkAuthorizationFromOpeningLocked(tabID string) {
	if authorization, ok := s.networkAuthorizers[""]; ok {
		s.networkAuthorizers[tabID] = authorization
	}
}

func (s *chromiumSession) continueAuthorizedRequest(
	ctx context.Context,
	tabID string,
	authorizationID uint64,
	requestID fetch.RequestID,
) error {
	s.mu.Lock()
	authorization, ok := s.getNetworkAuthorizationLocked(tabID)
	if !ok || authorization.id != authorizationID || authorization.ctx.Err() != nil {
		s.mu.Unlock()
		return context.Canceled
	}
	authorizationCtx := authorization.ctx
	s.mu.Unlock()

	requestCtx, done := newBoundedActionContext(ctx, authorizationCtx)
	defer done()
	return chromedp.Run(requestCtx, fetch.ContinueRequest(requestID))
}

func (s *chromiumSession) SetNetworkAuthorizer(tabID string, authorize NetworkRequestAuthorizer) func() {
	s.mu.Lock()
	if s.networkAuthorizers == nil {
		s.networkAuthorizers = make(map[string]networkAuthorization)
	}
	if s.networkErrors == nil {
		s.networkErrors = make(map[string]error)
	}
	previous := s.networkAuthorizers[tabID]
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	authorizationCtx, cancel := context.WithCancel(parent)
	s.nextAuthorization++
	authorization := networkAuthorization{
		id: s.nextAuthorization, ctx: authorizationCtx, cancel: cancel, authorize: authorize,
	}
	s.networkAuthorizers[tabID] = authorization
	delete(s.networkErrors, tabID)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			authorization.cancel()
			s.mu.Lock()
			for currentTabID, current := range s.networkAuthorizers {
				if current.id != authorization.id {
					continue
				}
				delete(s.networkAuthorizers, currentTabID)
			}
			if previous.authorize != nil {
				s.networkAuthorizers[tabID] = previous
			}
			s.mu.Unlock()
		})
	}
}

func (s *chromiumSession) consumeNetworkError(tabID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.networkErrors[tabID]
	delete(s.networkErrors, tabID)
	return err
}

func (s *chromiumSession) recordNetworkError(tabID string, authorizationID uint64, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	authorization, ok := s.getNetworkAuthorizationLocked(tabID)
	if !ok || authorization.id != authorizationID || authorization.ctx.Err() != nil {
		return
	}
	if s.networkErrors == nil {
		s.networkErrors = make(map[string]error)
	}
	if s.networkErrors[tabID] == nil {
		s.networkErrors[tabID] = err
	}
}

func (s *chromiumSession) WaitForNetworkIdle(ctx context.Context, tabID string, quietPeriod time.Duration) error {
	if quietPeriod <= 0 {
		return nil
	}
	for {
		s.mu.Lock()
		activity := s.getNetworkActivityLocked(tabID)
		pending := activity.pending
		quietFor := time.Since(activity.last)
		changed := activity.changed
		s.mu.Unlock()
		if pending == 0 && quietFor >= quietPeriod {
			return nil
		}

		wait := quietPeriod - quietFor
		if wait <= 0 || pending > 0 {
			wait = quietPeriod
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *chromiumSession) markNetworkRequestStarted(tabID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	activity := s.getNetworkActivityLocked(tabID)
	activity.pending++
	s.signalNetworkActivityLocked(activity)
}

func (s *chromiumSession) markNetworkRequestFinished(tabID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	activity := s.getNetworkActivityLocked(tabID)
	if activity.pending > 0 {
		activity.pending--
	}
	s.signalNetworkActivityLocked(activity)
}

func (s *chromiumSession) getNetworkActivityLocked(tabID string) *networkActivity {
	if s.networkActivity == nil {
		s.networkActivity = make(map[string]*networkActivity)
	}
	activity := s.networkActivity[tabID]
	if activity == nil {
		activity = &networkActivity{last: time.Now(), changed: make(chan struct{})}
		s.networkActivity[tabID] = activity
	}
	return activity
}

func (s *chromiumSession) signalNetworkActivityLocked(activity *networkActivity) {
	activity.last = time.Now()
	close(activity.changed)
	activity.changed = make(chan struct{})
}

func (s *chromiumSession) Health(ctx context.Context) error {
	if s == nil || s.ctx == nil {
		return errors.New("browser session is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	healthCtx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()

	return chromedp.Run(healthCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		_, _, _, _, _, err := browser.GetVersion().Do(actionCtx)
		return err
	}))
}

func (s *chromiumSession) Close(context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.closeRelatedTargets()
		if s.process != nil {
			s.closeErr = s.process.stop()
		}
		s.preserveAttachedTargets()
		if s.cancelContext != nil {
			s.cancelContext()
		}
		if s.cancelBootstrap != nil {
			s.cancelBootstrap()
		}
		if s.cancelAllocator != nil {
			s.cancelAllocator()
		}
	})

	return s.closeErr
}

func (s *chromiumSession) closeRelatedTargets() {
	s.mu.Lock()
	sessions := make([]*relatedTargetSession, 0, len(s.relatedSessions))
	for _, related := range s.relatedTargets {
		for _, relatedSession := range related.sessions {
			sessions = append(sessions, relatedSession)
		}
	}
	s.relatedTargets = make(map[string]*relatedTarget)
	s.relatedSessions = make(map[target.SessionID]string)
	s.mu.Unlock()
	for _, relatedSession := range sessions {
		relatedSession.cancel()
	}
}

func (s *chromiumSession) preserveAttachedTargets() {
	if !s.attached {
		return
	}
	s.mu.Lock()
	contexts := make([]context.Context, 0, len(s.tabContexts)+1)
	contexts = append(contexts, s.ctx)
	for _, tabCtx := range s.tabContexts {
		contexts = append(contexts, tabCtx)
	}
	s.mu.Unlock()
	for _, ctx := range contexts {
		// Keep attached tabs open when Morph releases its contexts. This depends on chromedp cancellation semantics.
		if chromiumCtx := chromedp.FromContext(ctx); chromiumCtx != nil {
			chromiumCtx.Target = nil
		}
	}
}
