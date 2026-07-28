package browser

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
)

const testPageURL = "https://93.184.216.34/page"

type approverFunc func(context.Context, permissions.EvaluationInput) error

func (f approverFunc) Authorize(ctx context.Context, input permissions.EvaluationInput) error {
	return f(ctx, input)
}

type interactiveBackend struct {
	session *interactiveBackendSession
}

func (b *interactiveBackend) Start(context.Context, LaunchOptions) (BackendSession, error) {
	return b.session, nil
}

type backendFunc func(context.Context, LaunchOptions) (BackendSession, error)

func (f backendFunc) Start(ctx context.Context, options LaunchOptions) (BackendSession, error) {
	return f(ctx, options)
}

type interactiveOnlySession struct {
	BackendSession
	InteractiveBackendSession
}

type interactiveBackendSession struct {
	mu               sync.Mutex
	tabs             map[string]BackendTab
	active           string
	nextID           int
	closed           bool
	clicks           []int64
	typed            []string
	pressed          []string
	scrolled         [][2]int64
	selected         []string
	waits            []WaitCondition
	blockWait        bool
	snapshots        map[string]BackendSnapshot
	authorize        NetworkRequestAuthorizer
	authorizedTabs   []string
	requestTarget    string
	requestTargets   []permissions.NetworkTarget
	failAction       Action
	screenshot       BackendArtifact
	pdf              BackendArtifact
	console          []ConsoleMessage
	uploaded         []byte
	uploadPath       string
	download         BackendArtifact
	dialogAccepted   []bool
	dialogPrompts    []string
	networkSettles   []string
	networkSettleErr error
}

func (s *interactiveBackendSession) WaitForNetworkIdle(_ context.Context, tabID string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkSettles = append(s.networkSettles, tabID)
	return s.networkSettleErr
}

func (s *interactiveBackendSession) getFailure(action Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAction == action {
		return errors.New("backend action failed")
	}
	return nil
}

func newInteractiveBackendSession() *interactiveBackendSession {
	return &interactiveBackendSession{
		tabs: map[string]BackendTab{
			"tab-1": {ID: "tab-1", Title: "Page", URL: testPageURL, Active: true},
		},
		active: "tab-1",
		snapshots: map[string]BackendSnapshot{
			"tab-1": {
				URL: testPageURL, Title: "Page",
				Nodes: []BackendSnapshotNode{
					{BackendNodeID: 41, Role: "textbox", Name: "Name"},
					{BackendNodeID: 42, Role: "button", Name: "Submit"},
					{Role: "paragraph", Name: "Read only"},
					{
						BackendNodeID: 43, Role: "textbox", Name: "Password", Value: "secret", Sensitive: true,
						Properties: map[string]string{"value": "secret"},
					},
					{BackendNodeID: 44, Role: "link", Name: "Download", Properties: map[string]string{"url": testPageURL + "/file"}},
					{BackendNodeID: 45, Role: "button", Name: "Upload"},
				},
			},
		},
		screenshot: BackendArtifact{Kind: ArtifactScreenshot, MIMEType: "image/png", Data: []byte("png")},
		pdf:        BackendArtifact{Kind: ArtifactPDF, MIMEType: "application/pdf", Data: []byte("pdf")},
		download: BackendArtifact{
			Kind: ArtifactDownload, Name: "report.txt", MIMEType: "text/plain",
			SourceURL: testPageURL + "/file", Data: []byte("download"),
		},
		console: []ConsoleMessage{{Level: ConsoleInfo, Text: "ready", Timestamp: time.Now().UTC()}},
	}
}

func (s *interactiveBackendSession) Health(context.Context) error { return nil }

func (s *interactiveBackendSession) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *interactiveBackendSession) ListTabs(context.Context) ([]BackendTab, error) {
	if err := s.getFailure(ActionTabs); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]BackendTab, 0, len(s.tabs))
	for _, tab := range s.tabs {
		tab.Active = tab.ID == s.active
		result = append(result, tab)
	}
	return result, nil
}

func (s *interactiveBackendSession) OpenTab(_ context.Context, rawURL string) (BackendTab, error) {
	if err := s.getFailure(ActionOpen); err != nil {
		return BackendTab{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := "opened-tab"
	tab := BackendTab{ID: id, URL: rawURL, Title: "Opened", Active: true}
	s.tabs[id] = tab
	s.active = id
	return tab, nil
}

func (s *interactiveBackendSession) FocusTab(_ context.Context, tabID string) error {
	if err := s.getFailure(ActionFocus); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tabs[tabID]; !ok {
		return errors.New("tab not found")
	}
	s.active = tabID
	return nil
}

func (s *interactiveBackendSession) CloseTab(_ context.Context, tabID string) error {
	if err := s.getFailure(ActionClose); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tabs, tabID)
	if s.active == tabID {
		s.active = ""
	}
	return nil
}

func (s *interactiveBackendSession) Navigate(_ context.Context, tabID, rawURL string) (BackendTab, error) {
	return s.navigate(ActionNavigate, tabID, rawURL)
}

func (s *interactiveBackendSession) navigate(action Action, tabID, rawURL string) (BackendTab, error) {
	if err := s.getFailure(action); err != nil {
		return BackendTab{}, err
	}
	s.mu.Lock()
	authorize := s.authorize
	requestTarget := s.requestTarget
	requestTargets := append([]permissions.NetworkTarget(nil), s.requestTargets...)
	s.mu.Unlock()
	if authorize != nil && len(requestTargets) > 0 {
		results := make(chan error, len(requestTargets))
		for _, target := range requestTargets {
			go func(target permissions.NetworkTarget) {
				results <- authorize(context.Background(), target)
			}(target)
		}
		for range requestTargets {
			if err := <-results; err != nil {
				return BackendTab{}, err
			}
		}
	}
	if requestTarget == "" {
		requestTarget = rawURL
	}
	if authorize != nil && len(requestTargets) == 0 {
		target, err := permissions.NetworkTargetFromURL(
			requestTarget, "GET", permissions.NetworkRequestNavigation,
		)
		if err != nil {
			return BackendTab{}, err
		}
		if err := authorize(context.Background(), target); err != nil {
			return BackendTab{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tab := s.tabs[tabID]
	tab.URL = rawURL
	tab.Title = "Navigated"
	s.tabs[tabID] = tab
	return tab, nil
}

func (s *interactiveBackendSession) SetNetworkAuthorizer(tabID string, authorize NetworkRequestAuthorizer) func() {
	s.mu.Lock()
	previous := s.authorize
	s.authorize = authorize
	s.authorizedTabs = append(s.authorizedTabs, tabID)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.authorize = previous
		s.mu.Unlock()
	}
}

func (s *interactiveBackendSession) Back(ctx context.Context, tabID string) (BackendTab, error) {
	return s.navigate(ActionBack, tabID, testPageURL+"/back")
}

func (s *interactiveBackendSession) Forward(ctx context.Context, tabID string) (BackendTab, error) {
	return s.navigate(ActionForward, tabID, testPageURL+"/forward")
}

func (s *interactiveBackendSession) Reload(ctx context.Context, tabID string) (BackendTab, error) {
	return s.navigate(ActionReload, tabID, testPageURL+"/reload")
}

func (s *interactiveBackendSession) Snapshot(_ context.Context, tabID string) (BackendSnapshot, error) {
	if err := s.getFailure(ActionSnapshot); err != nil {
		return BackendSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshots[tabID], nil
}

func (s *interactiveBackendSession) Click(_ context.Context, _ string, nodeID int64) error {
	if err := s.getFailure(ActionClick); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clicks = append(s.clicks, nodeID)
	return nil
}

func (s *interactiveBackendSession) Type(_ context.Context, _ string, nodeID int64, text string, _ bool) error {
	if err := s.getFailure(ActionType); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clicks = append(s.clicks, nodeID)
	s.typed = append(s.typed, text)
	return nil
}

func (s *interactiveBackendSession) Press(_ context.Context, _ string, key string) error {
	if err := s.getFailure(ActionPress); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pressed = append(s.pressed, key)
	return nil
}

func (s *interactiveBackendSession) Scroll(_ context.Context, _ string, x, y int64) error {
	if err := s.getFailure(ActionScroll); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scrolled = append(s.scrolled, [2]int64{x, y})
	return nil
}

func (s *interactiveBackendSession) Select(_ context.Context, _ string, nodeID int64, value string) error {
	if err := s.getFailure(ActionSelect); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clicks = append(s.clicks, nodeID)
	s.selected = append(s.selected, value)
	return nil
}

func (s *interactiveBackendSession) Wait(
	ctx context.Context,
	_ string,
	condition WaitCondition,
	_ string,
	_ int64,
) error {
	if err := s.getFailure(ActionWait); err != nil {
		return err
	}
	s.mu.Lock()
	s.waits = append(s.waits, condition)
	block := s.blockWait
	s.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (s *interactiveBackendSession) Screenshot(context.Context, string, bool) (BackendArtifact, error) {
	if err := s.getFailure(ActionScreenshot); err != nil {
		return BackendArtifact{}, err
	}
	return s.screenshot, nil
}

func (s *interactiveBackendSession) PDF(context.Context, string) (BackendArtifact, error) {
	if err := s.getFailure(ActionPDF); err != nil {
		return BackendArtifact{}, err
	}
	return s.pdf, nil
}

func (s *interactiveBackendSession) Console(context.Context, string, int) ([]ConsoleMessage, error) {
	if err := s.getFailure(ActionConsole); err != nil {
		return nil, err
	}
	return append([]ConsoleMessage(nil), s.console...), nil
}

func (s *interactiveBackendSession) Upload(_ context.Context, _ string, _ int64, stagedPath string) error {
	if err := s.getFailure(ActionUpload); err != nil {
		return err
	}
	content, err := os.ReadFile(stagedPath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.uploadPath = stagedPath
	s.uploaded = content
	s.mu.Unlock()
	return nil
}

func (s *interactiveBackendSession) Download(
	_ context.Context,
	_ string,
	_ int64,
	_ int64,
) (BackendArtifact, error) {
	if err := s.getFailure(ActionDownload); err != nil {
		return BackendArtifact{}, err
	}
	s.mu.Lock()
	authorize := s.authorize
	s.mu.Unlock()
	if authorize != nil {
		target, err := permissions.NetworkTargetFromURL(
			s.download.SourceURL, "GET", permissions.NetworkRequestNavigation,
		)
		if err != nil {
			return BackendArtifact{}, err
		}
		if err := authorize(context.Background(), target); err != nil {
			return BackendArtifact{}, err
		}
	}
	return s.download, nil
}

func (s *interactiveBackendSession) RespondToDialog(
	_ context.Context,
	_ string,
	_ int64,
	accept bool,
	promptText string,
) error {
	action := ActionDismissDialog
	if accept {
		action = ActionAcceptDialog
	}
	if err := s.getFailure(action); err != nil {
		return err
	}
	s.mu.Lock()
	s.dialogAccepted = append(s.dialogAccepted, accept)
	s.dialogPrompts = append(s.dialogPrompts, promptText)
	s.mu.Unlock()
	return nil
}

func TestService_ActionsMaintainOwnedTabsAndRejectStaleReferences(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	tabs, err := service.Tabs(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, []Tab{{
		ID: "tab-1", SessionID: session.ID, Title: "Page", URL: testPageURL, Active: true, Generation: 1,
	}}, tabs)

	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	require.Equal(t, uint64(2), snapshot.Generation)
	require.Regexp(t, `^r[0-9a-f]{24}g2e1$`, snapshot.Nodes[0].Ref)
	require.Regexp(t, `^r[0-9a-f]{24}g2e2$`, snapshot.Nodes[1].Ref)
	require.Empty(t, snapshot.Nodes[2].Ref)
	require.Regexp(t, `^r[0-9a-f]{24}g2e3$`, snapshot.Nodes[3].Ref)
	require.Empty(t, snapshot.Nodes[3].Value)
	require.Nil(t, snapshot.Nodes[3].Properties)
	typeOperations, err := service.ResolveOperations(ctx, ActionType, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: snapshot.Nodes[3].Ref,
	})
	require.NoError(t, err)
	require.Contains(t, typeOperations[0].Effects, permissions.EffectCredentialBearing)

	clicked, err := service.Click(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: snapshot.Nodes[1].Ref,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(3), clicked.Generation)
	require.Equal(t, []int64{42}, backendSession.clicks)

	_, err = service.Click(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: snapshot.Nodes[1].Ref,
	})
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorStaleReference, browserErr.Code)
	require.Equal(t, []int64{42}, backendSession.clicks)

	other := testBrowserContext("other", "session")
	_, err = service.Tabs(other, session.ID)
	require.EqualError(t, err, "browser session belongs to another owner")
}

func TestService_SnapshotRefsCannotCrossTabs(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.tabs["tab-2"] = BackendTab{ID: "tab-2", Title: "Other", URL: testPageURL + "/other"}
	backendSession.snapshots["tab-2"] = backendSession.snapshots["tab-1"]
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)
	first, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	second, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-2"})
	require.NoError(t, err)
	require.NotEqual(t, first.Nodes[0].Ref, second.Nodes[0].Ref)

	_, err = service.Click(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-2", Ref: first.Nodes[0].Ref,
	})
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorStaleReference, browserErr.Code)
}

func TestService_ResolveOperationsUsesAuthoritativeTabAndNavigationTargets(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	operations, err := service.ResolveOperations(ctx, ActionNavigate, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.35/next",
	})
	require.NoError(t, err)
	require.Len(t, operations, 2)
	require.Equal(t, permissions.ResourceBrowser, operations[0].Resource)
	require.Contains(t, operations[0].Target, "93.184.216.34")
	require.Equal(t, permissions.ResourceNetwork, operations[1].Resource)
	require.Equal(t, "93.184.216.35", operations[1].Network.Host)

	operations, err = service.ResolveOperations(ctx, ActionBack, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.99/untrusted",
	})
	require.NoError(t, err)
	require.Len(t, operations, 2)
	require.Equal(t, "93.184.216.34", operations[1].Network.Host)

	_, err = service.ResolveOperations(ctx, ActionNavigate, ActionRequest{
		SessionID: session.ID, TabID: "tab-1",
	})
	require.EqualError(t, err, "browser navigation URL is required")
}

func TestService_ResolveOperationsClassifiesEverySupportedAction(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	refs := make(map[string]string)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}
	artifact, err := service.Screenshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	exportRoot, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	exportPath := filepath.Join(exportRoot, "export.png")

	for _, action := range SupportedActions() {
		t.Run(string(action), func(t *testing.T) {
			request := ActionRequest{
				SessionID: session.ID, TabID: "tab-1",
				Ref: snapshot.Nodes[0].Ref, Condition: WaitLoad,
			}
			if action == ActionUpload {
				request.Ref = refs["Upload"]
				request.Path = "/tmp/upload.txt"
				request.FileTarget = "/tmp/upload.txt"
				request.TargetScope = permissions.TargetScopeExternal
			}
			if action == ActionDownload {
				request.Ref = refs["Download"]
			}
			if action == ActionAcceptDialog || action == ActionDismissDialog {
				request.Ref = refs["Submit"]
			}
			if action == ActionOpen || action == ActionNavigate {
				request.URL = testPageURL + "/target"
			}
			if action == ActionExportArtifact {
				request = ActionRequest{
					Handle: artifact.Handle, Path: exportPath, FileTarget: filepath.ToSlash(exportPath),
					TargetScope: permissions.TargetScopeExternal,
				}
			}
			if action == ActionStatus || action == ActionProfiles || action == ActionStart {
				request.SessionID = ""
				request.TabID = ""
			}
			operations, operationErr := service.ResolveOperations(ctx, action, request)
			require.NoError(t, operationErr)
			require.NotEmpty(t, operations)
			for _, operation := range operations {
				require.Equal(t, "browser", operation.Tool)
				require.NotEqual(t, permissions.ActionUnknown, operation.Action)
			}
		})
	}
}

func TestService_RichBackendFailuresReturnStableErrors(t *testing.T) {
	tests := []struct {
		action Action
		run    func(context.Context, *Service, string, map[string]string, string) error
	}{
		{action: ActionScreenshot, run: func(ctx context.Context, service *Service, sessionID string, _ map[string]string, _ string) error {
			_, err := service.Screenshot(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1"})
			return err
		}},
		{action: ActionPDF, run: func(ctx context.Context, service *Service, sessionID string, _ map[string]string, _ string) error {
			_, err := service.PDF(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1"})
			return err
		}},
		{action: ActionConsole, run: func(ctx context.Context, service *Service, sessionID string, _ map[string]string, _ string) error {
			_, err := service.Console(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1"})
			return err
		}},
		{action: ActionUpload, run: func(ctx context.Context, service *Service, sessionID string, refs map[string]string, source string) error {
			_, err := service.Upload(ctx, ActionRequest{
				SessionID: sessionID, TabID: "tab-1", Ref: refs["Upload"], Path: source,
				FileTarget: filepath.ToSlash(source), TargetScope: permissions.TargetScopeExternal,
			})
			return err
		}},
		{action: ActionDownload, run: func(ctx context.Context, service *Service, sessionID string, refs map[string]string, _ string) error {
			_, err := service.Download(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1", Ref: refs["Download"]})
			return err
		}},
		{action: ActionAcceptDialog, run: func(ctx context.Context, service *Service, sessionID string, refs map[string]string, _ string) error {
			_, err := service.AcceptDialog(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1", Ref: refs["Submit"]})
			return err
		}},
		{action: ActionDismissDialog, run: func(ctx context.Context, service *Service, sessionID string, refs map[string]string, _ string) error {
			_, err := service.DismissDialog(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1", Ref: refs["Submit"]})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			backend := newInteractiveBackendSession()
			service, err := NewService(
				context.Background(), testBrowserConfig(t), allowChecker(), &interactiveBackend{session: backend},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
			ctx := testBrowserContext("owner", "session")
			session, err := service.Start(ctx, StartRequest{})
			require.NoError(t, err)
			snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
			require.NoError(t, err)
			refs := make(map[string]string)
			for _, node := range snapshot.Nodes {
				refs[node.Name] = node.Ref
			}
			source := filepath.Join(t.TempDir(), "upload.txt")
			require.NoError(t, os.WriteFile(source, []byte("approved"), 0o600))
			backend.failAction = test.action

			err = test.run(ctx, service, session.ID, refs, source)
			browserErr, ok := GetError(err)
			require.True(t, ok)
			require.Equal(t, ErrorUnavailable, browserErr.Code)
			require.Equal(t, test.action, browserErr.Operation)
			require.True(t, browserErr.Retryable)
			if test.action == ActionUpload {
				require.NoDirExists(t, filepath.Join(service.cfg.TemporaryRoot, "uploads", session.ID))
			}
		})
	}
}

func TestService_RejectsArtifactKindMismatchBeforePersistence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*interactiveBackendSession)
		run    func(context.Context, *Service, string, map[string]string) error
	}{
		{
			name: "screenshot", mutate: func(backend *interactiveBackendSession) { backend.screenshot.Kind = ArtifactPDF },
			run: func(ctx context.Context, service *Service, sessionID string, _ map[string]string) error {
				_, err := service.Screenshot(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1"})
				return err
			},
		},
		{
			name: "pdf", mutate: func(backend *interactiveBackendSession) { backend.pdf.Kind = ArtifactScreenshot },
			run: func(ctx context.Context, service *Service, sessionID string, _ map[string]string) error {
				_, err := service.PDF(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1"})
				return err
			},
		},
		{
			name: "download", mutate: func(backend *interactiveBackendSession) { backend.download.Kind = ArtifactScreenshot },
			run: func(ctx context.Context, service *Service, sessionID string, refs map[string]string) error {
				_, err := service.Download(ctx, ActionRequest{SessionID: sessionID, TabID: "tab-1", Ref: refs["Download"]})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newInteractiveBackendSession()
			test.mutate(backend)
			service, err := NewService(
				context.Background(), testBrowserConfig(t), allowChecker(), &interactiveBackend{session: backend},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
			ctx := testBrowserContext("owner", "session")
			session, err := service.Start(ctx, StartRequest{})
			require.NoError(t, err)
			snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
			require.NoError(t, err)
			refs := make(map[string]string)
			for _, node := range snapshot.Nodes {
				refs[node.Name] = node.Ref
			}

			require.EqualError(t, test.run(ctx, service, session.ID, refs), "browser backend returned the wrong artifact kind")
			entries, err := os.ReadDir(service.cfg.Artifacts.Root)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.True(t, entries[0].IsDir())
			require.Equal(t, ".downloads", entries[0].Name())
		})
	}
}

func TestService_NavigateAndWaitUpdateGenerationAndActivity(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)

	tab, err := service.Navigate(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.35/next",
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), tab.Generation)
	require.Equal(t, "https://93.184.216.35/next", tab.URL)

	tab, err = service.Wait(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Condition: WaitURL, Value: "next",
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), tab.Generation)
	require.Equal(t, []WaitCondition{WaitURL}, backendSession.waits)
}

func TestService_NavigateDeniesServerObservedRedirectBeforeBackendMutation(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.requestTarget = "https://93.184.216.36/redirect"
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		if input.Operation.Network != nil && input.Operation.Network.Host == "93.184.216.36" {
			evaluation := permissions.Evaluation{Decision: permissions.DecisionDeny, Reason: "redirect denied"}
			return evaluation, &permissions.DecisionError{
				Code: permissions.ErrorCodeDenied, Evaluation: evaluation,
			}
		}
		return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
	})
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker,
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)

	_, err = service.Navigate(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.35/next",
	})
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeDenied, decisionErr.Code)
	backendSession.mu.Lock()
	require.Equal(t, testPageURL, backendSession.tabs["tab-1"].URL)
	backendSession.mu.Unlock()
}

func TestService_NavigateApprovesServerObservedRedirect(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.requestTarget = "https://93.184.216.36/redirect"
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		if input.Operation.Network == nil || input.Operation.Network.Host != "93.184.216.36" {
			return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
		}
		evaluation := permissions.Evaluation{Decision: permissions.DecisionAsk, Reason: "approve redirect"}
		return evaluation, &permissions.DecisionError{
			Code: permissions.ErrorCodeApprovalRequired, Evaluation: evaluation,
		}
	})
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker,
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	var approved permissions.EvaluationInput
	service.SetApprover(approverFunc(func(_ context.Context, input permissions.EvaluationInput) error {
		approved = input
		return nil
	}))
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)

	tab, err := service.Navigate(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.35/next",
	})
	require.NoError(t, err)
	require.Equal(t, "approve redirect", approved.ApprovalReason)
	require.Equal(t, "93.184.216.36", approved.Operation.Network.Host)
	require.Equal(t, "https://93.184.216.35/next", tab.URL)
}

func TestService_NavigateBatchesConcurrentSafeSubresourceApprovals(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.requestTargets = []permissions.NetworkTarget{
		{
			Scheme: "https", Host: "93.184.216.35", Port: 443, Path: "/styles.css", Method: "GET",
			RequestClass: permissions.NetworkRequestSubresource,
		},
		{
			Scheme: "https", Host: "93.184.216.35", Port: 443, Path: "/app.js", Method: "GET",
			RequestClass: permissions.NetworkRequestSubresource,
		},
	}
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		if input.Operation.Network == nil ||
			input.Operation.Network.RequestClass != permissions.NetworkRequestSubresource {
			return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
		}
		evaluation := permissions.Evaluation{Decision: permissions.DecisionAsk, Reason: "approve page resources"}
		return evaluation, &permissions.DecisionError{
			Code: permissions.ErrorCodeApprovalRequired, Evaluation: evaluation,
		}
	})
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker,
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	approver := &recordingBatchApprover{}
	service.SetApprover(approver)
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	_, err = service.Navigate(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.35/next",
	})
	require.NoError(t, err)
	require.Len(t, approver.inputs, 2)
	require.Equal(t, 1, approver.commits)
	paths := []string{approver.inputs[0].Operation.Network.Path, approver.inputs[1].Operation.Network.Path}
	require.ElementsMatch(t, []string{"/styles.css", "/app.js"}, paths)
}

func TestService_NavigateDoesNotMutateWhenRedirectApprovalFails(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.requestTarget = "https://93.184.216.36/redirect"
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		if input.Operation.Network == nil || input.Operation.Network.Host != "93.184.216.36" {
			return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
		}
		evaluation := permissions.Evaluation{Decision: permissions.DecisionAsk, Reason: "approve redirect"}
		return evaluation, &permissions.DecisionError{
			Code: permissions.ErrorCodeApprovalRequired, Evaluation: evaluation,
		}
	})
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker,
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	expected := errors.New("approval cancelled")
	service.SetApprover(approverFunc(func(context.Context, permissions.EvaluationInput) error {
		return expected
	}))
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)

	_, err = service.Navigate(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", URL: "https://93.184.216.35/next",
	})
	require.ErrorIs(t, err, expected)
	backendSession.mu.Lock()
	require.Equal(t, testPageURL, backendSession.tabs["tab-1"].URL)
	backendSession.mu.Unlock()
}

func TestService_InteractiveActionsDispatchAndMaintainTabState(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	opened, err := service.Open(ctx, ActionRequest{SessionID: session.ID, URL: testPageURL + "/opened"})
	require.NoError(t, err)
	require.Equal(t, "opened-tab", opened.ID)
	require.True(t, opened.Active)

	focused, err := service.Focus(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	require.True(t, focused.Active)

	for action, run := range map[Action]func(ActionRequest) (Tab, error){
		ActionType:   func(request ActionRequest) (Tab, error) { return service.Type(ctx, request) },
		ActionSelect: func(request ActionRequest) (Tab, error) { return service.Select(ctx, request) },
	} {
		snapshot, snapshotErr := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
		require.NoError(t, snapshotErr)
		request := ActionRequest{
			SessionID: session.ID, TabID: "tab-1", Ref: snapshot.Nodes[0].Ref,
			Text: "Ada", Value: "choice", Replace: true,
		}
		updated, runErr := run(request)
		require.NoError(t, runErr, action)
		require.Greater(t, updated.Generation, snapshot.Generation)
	}

	pressed, err := service.Press(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1", Key: "Enter"})
	require.NoError(t, err)
	require.NotZero(t, pressed.Generation)
	scrolled, err := service.Scroll(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1", X: 4, Y: 9})
	require.NoError(t, err)
	require.Equal(t, pressed.Generation, scrolled.Generation)

	for _, navigation := range []func(context.Context, ActionRequest) (Tab, error){service.Back, service.Forward, service.Reload} {
		tab, navigationErr := navigation(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
		require.NoError(t, navigationErr)
		require.NotEmpty(t, tab.URL)
	}

	visibleSnapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	for _, request := range []ActionRequest{
		{SessionID: session.ID, TabID: "tab-1", Condition: WaitLoad},
		{SessionID: session.ID, TabID: "tab-1", Condition: WaitText, Value: "Page"},
		{SessionID: session.ID, TabID: "tab-1", Condition: WaitURL, Value: "page"},
		{SessionID: session.ID, TabID: "tab-1", Condition: WaitVisible, Ref: visibleSnapshot.Nodes[0].Ref},
	} {
		_, err = service.Wait(ctx, request)
		require.NoError(t, err)
	}

	closed, err := service.CloseTab(ctx, ActionRequest{SessionID: session.ID, TabID: opened.ID})
	require.NoError(t, err)
	require.Equal(t, opened.ID, closed.ID)
	tabs, err := service.Tabs(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, tabs, 1)

	backendSession.mu.Lock()
	require.Equal(t, []string{"Ada"}, backendSession.typed)
	require.Equal(t, []string{"choice"}, backendSession.selected)
	require.Equal(t, []string{"Enter"}, backendSession.pressed)
	require.Equal(t, [][2]int64{{4, 9}}, backendSession.scrolled)
	require.Equal(t, []WaitCondition{WaitLoad, WaitText, WaitURL, WaitVisible}, backendSession.waits)
	require.Contains(t, backendSession.authorizedTabs, "")
	require.Contains(t, backendSession.authorizedTabs, "tab-1")
	backendSession.mu.Unlock()
}

func TestService_SnapshotTruncatesBeforeExceedingOutputLimit(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.snapshots["tab-1"] = BackendSnapshot{
		URL: testPageURL,
		Nodes: []BackendSnapshotNode{{
			BackendNodeID: 41,
			Role:          "textbox",
			Properties:    map[string]string{"value": strings.Repeat("x", maxSnapshotChars)},
		}},
	}
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	require.True(t, snapshot.Truncated)
	require.Empty(t, snapshot.Nodes)
}

func TestService_ActionsReturnStableValidationAndCapabilityErrors(t *testing.T) {
	_, err := (*Service)(nil).ResolveOperations(context.Background(), ActionStatus, ActionRequest{})
	require.EqualError(t, err, "browser service is required")

	service, err := NewService(context.Background(), testBrowserConfig(t), allowChecker(), &fakeBackend{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorUnavailable, browserErr.Code)

	_, err = getActionTimeout(-time.Second)
	require.EqualError(t, err, "browser action timeout must be between zero and two minutes")
	_, err = getActionTimeout(maxActionTimeout + time.Millisecond)
	require.EqualError(t, err, "browser action timeout must be between zero and two minutes")
	require.Equal(t, defaultActionTimeout, mustGetActionTimeout(t, 0))
	require.Equal(t, time.Second, mustGetActionTimeout(t, time.Second))
}

func TestService_WaitReturnsStableTimeoutError(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.blockWait = true
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	_, err = service.Wait(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Condition: WaitLoad, Timeout: time.Millisecond,
	})
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorTimeout, browserErr.Code)
	require.True(t, browserErr.Retryable)
}

func TestService_OpenKeepsNetworkAuthorizationUntilThePageSettles(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	_, err = service.Open(ctx, ActionRequest{SessionID: session.ID, URL: testPageURL + "/open"})
	require.NoError(t, err)

	backendSession.mu.Lock()
	defer backendSession.mu.Unlock()
	require.Equal(t, []string{"opened-tab"}, backendSession.networkSettles)
}

func TestService_OpenReturnsLoadedTabWhenOptionalNetworkSettlementTimesOut(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.networkSettleErr = errOptionalNetworkSettlementTimedOut
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	tab, err := service.Open(ctx, ActionRequest{SessionID: session.ID, URL: testPageURL + "/open"})
	require.NoError(t, err)
	require.Equal(t, "opened-tab", tab.ID)
	require.Equal(t, testPageURL+"/open", tab.URL)
}

func TestService_NavigateReturnsLoadedTabWhenOptionalNetworkSettlementTimesOut(t *testing.T) {
	backendSession := newInteractiveBackendSession()
	backendSession.networkSettleErr = errOptionalNetworkSettlementTimedOut
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		&interactiveBackend{session: backendSession},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)

	tab, err := service.Navigate(ctx, ActionRequest{
		SessionID: session.ID,
		TabID:     "tab-1",
		URL:       testPageURL + "/next",
	})
	require.NoError(t, err)
	require.Equal(t, "tab-1", tab.ID)
	require.Equal(t, testPageURL+"/next", tab.URL)
}

func TestService_CheckOperationsRequiresExactAdmissionEvidence(t *testing.T) {
	operation := permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate,
		Effects: []permissions.Effect{permissions.EffectWrite}, Target: "profile=default",
	}
	checker := checkerFunc(func(context.Context, permissions.EvaluationInput) (permissions.Evaluation, error) {
		evaluation := permissions.Evaluation{Decision: permissions.DecisionDeny, Reason: "changed operation"}
		return evaluation, &permissions.DecisionError{
			Code: permissions.ErrorCodeDenied, Evaluation: evaluation,
		}
	})
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker,
		&interactiveBackend{session: newInteractiveBackendSession()},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	changed := operation
	changed.Effects = []permissions.Effect{permissions.EffectWrite, permissions.EffectExternalSystem}
	ctx := permissions.WithAuthorizedOperations(context.Background(), []permissions.Operation{operation})

	err = service.checkOperations(ctx, []permissions.Operation{changed})
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeDenied, decisionErr.Code)
}

func TestService_InteractiveBackendFailuresReturnStableErrors(t *testing.T) {
	tests := []struct {
		action Action
		run    func(context.Context, *Service, string, string) error
	}{
		{action: ActionTabs, run: func(ctx context.Context, service *Service, sessionID, _ string) error {
			_, err := service.Tabs(ctx, sessionID)
			return err
		}},
		{action: ActionOpen, run: func(ctx context.Context, service *Service, sessionID, _ string) error {
			_, err := service.Open(ctx, ActionRequest{SessionID: sessionID, URL: testPageURL + "/open"})
			return err
		}},
		{action: ActionFocus, run: runFailedTabAction((*Service).Focus)},
		{action: ActionClose, run: runFailedTabAction((*Service).CloseTab)},
		{action: ActionNavigate, run: func(ctx context.Context, service *Service, sessionID, _ string) error {
			_, err := service.Navigate(ctx, ActionRequest{
				SessionID: sessionID, TabID: "tab-1", URL: testPageURL + "/next",
			})
			return err
		}},
		{action: ActionBack, run: runFailedTabAction((*Service).Back)},
		{action: ActionForward, run: runFailedTabAction((*Service).Forward)},
		{action: ActionReload, run: runFailedTabAction((*Service).Reload)},
		{action: ActionSnapshot, run: runFailedTabAction(func(
			service *Service, ctx context.Context, request ActionRequest,
		) (Tab, error) {
			_, err := service.Snapshot(ctx, request)
			return Tab{}, err
		})},
		{action: ActionClick, run: runFailedElementAction((*Service).Click)},
		{action: ActionType, run: runFailedElementAction((*Service).Type)},
		{action: ActionPress, run: runFailedTabAction((*Service).Press)},
		{action: ActionScroll, run: runFailedTabAction((*Service).Scroll)},
		{action: ActionSelect, run: runFailedElementAction((*Service).Select)},
		{action: ActionWait, run: runFailedTabAction((*Service).Wait)},
	}

	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			backendSession := newInteractiveBackendSession()
			service, err := NewService(
				context.Background(), testBrowserConfig(t), allowChecker(),
				&interactiveBackend{session: backendSession},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
			ctx := testBrowserContext("owner", "session")
			session, err := service.Start(ctx, StartRequest{})
			require.NoError(t, err)
			var ref string
			if test.action == ActionClick || test.action == ActionType || test.action == ActionSelect {
				snapshot, snapshotErr := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
				require.NoError(t, snapshotErr)
				ref = snapshot.Nodes[0].Ref
			}
			backendSession.failAction = test.action

			err = test.run(ctx, service, session.ID, ref)
			browserErr, ok := GetError(err)
			require.True(t, ok)
			require.Equal(t, ErrorUnavailable, browserErr.Code)
			require.Equal(t, test.action, browserErr.Operation)
			require.True(t, browserErr.Retryable)
		})
	}
}

func TestService_RichActionsPersistArtifactsStageUploadsAndHandleDialogs(t *testing.T) {
	backend := newInteractiveBackendSession()
	backendSnapshot := backend.snapshots["tab-1"]
	backendSnapshot.Nodes[3].Value = ""
	backendSnapshot.Nodes[3].Properties = nil
	backend.snapshots["tab-1"] = backendSnapshot
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &interactiveBackend{session: backend},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)
	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	refs := make(map[string]string)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}

	screenshot, err := service.Screenshot(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", FullPage: true,
	})
	require.NoError(t, err)
	require.Equal(t, ArtifactScreenshot, screenshot.Kind)
	require.NotEmpty(t, screenshot.Handle)
	require.Equal(t, testPageURL, screenshot.Source)
	require.True(t, screenshot.Sensitive)
	require.Contains(t, screenshot.Effects, permissions.EffectCredentialBearing)
	content, err := service.ReadArtifact(ctx, screenshot.Handle)
	require.NoError(t, err)
	require.Equal(t, []byte("png"), content.Data)
	exportRoot, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	exportPath := filepath.Join(exportRoot, "screenshot.png")
	exported, err := service.ExportArtifact(ctx, ArtifactExportRequest{
		Handle: screenshot.Handle, Path: exportPath, FileTarget: filepath.ToSlash(exportPath),
		TargetScope: permissions.TargetScopeExternal,
	})
	require.NoError(t, err)
	require.Equal(t, screenshot.Handle, exported.Handle)
	exportedData, err := os.ReadFile(exportPath)
	require.NoError(t, err)
	require.Equal(t, []byte("png"), exportedData)
	_, err = service.ExportArtifact(ctx, ArtifactExportRequest{
		Handle: screenshot.Handle, Path: exportPath, FileTarget: filepath.ToSlash(exportPath),
		TargetScope: permissions.TargetScopeExternal,
	})
	require.ErrorIs(t, err, os.ErrExist)
	_, err = service.ReadArtifact(testBrowserContext("other", "session"), screenshot.Handle)
	require.EqualError(t, err, "browser artifact belongs to another owner")

	pdf, err := service.PDF(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	require.Equal(t, ArtifactPDF, pdf.Kind)
	messages, err := service.Console(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, backend.console, messages)

	source := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(source, []byte("approved bytes"), 0o600))
	uploadedTab, err := service.Upload(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Upload"], Path: source,
		FileTarget: filepath.ToSlash(source), TargetScope: permissions.TargetScopeExternal,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(3), uploadedTab.Generation)
	require.Equal(t, []byte("approved bytes"), backend.uploaded)
	_, err = os.Stat(backend.uploadPath)
	require.ErrorIs(t, err, os.ErrNotExist)

	snapshot, err = service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	refs = make(map[string]string)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}
	download, err := service.Download(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Download"],
	})
	require.NoError(t, err)
	require.Equal(t, ArtifactDownload, download.Kind)
	downloadContent, err := service.ReadArtifact(ctx, download.Handle)
	require.NoError(t, err)
	require.Equal(t, []byte("download"), downloadContent.Data)

	accepted, err := service.AcceptDialog(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Submit"], Text: "confirmed",
	})
	require.NoError(t, err)
	require.Greater(t, accepted.Generation, snapshot.Generation)
	snapshot, err = service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}
	_, err = service.DismissDialog(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Submit"],
	})
	require.NoError(t, err)
	require.Equal(t, []bool{true, false}, backend.dialogAccepted)
	require.Equal(t, []string{"confirmed", ""}, backend.dialogPrompts)

	downloadRoot := service.sessions[session.ID].downloadRoot
	require.True(t, strings.HasPrefix(downloadRoot, filepath.Join(service.cfg.Artifacts.Root, ".downloads")))
	require.DirExists(t, downloadRoot)
	_, err = service.Stop(ctx, session.ID)
	require.NoError(t, err)
	require.NoDirExists(t, downloadRoot)
}

func TestService_UploadDenialPreventsStagingAndBackendAccess(t *testing.T) {
	backend := newInteractiveBackendSession()
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		if input.Operation.Resource == permissions.ResourceFile {
			return permissions.Evaluation{Decision: permissions.DecisionDeny}, &permissions.DecisionError{
				Code:       permissions.ErrorCodeDenied,
				Evaluation: permissions.Evaluation{Decision: permissions.DecisionDeny, Reason: "file denied"},
			}
		}
		return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
	})
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker, &interactiveBackend{session: backend},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	_, err = service.Tabs(ctx, session.ID)
	require.NoError(t, err)
	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	var uploadRef string
	for _, node := range snapshot.Nodes {
		if node.Name == "Upload" {
			uploadRef = node.Ref
		}
	}
	source := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(source, []byte("never read"), 0o600))
	_, err = service.Upload(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: uploadRef, Path: source,
		FileTarget: filepath.ToSlash(source), TargetScope: permissions.TargetScopeExternal,
	})
	require.Error(t, err)
	require.Empty(t, backend.uploaded)
	require.NoDirExists(t, filepath.Join(service.cfg.TemporaryRoot, "uploads", session.ID))

	artifact, err := service.Screenshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	exportRoot, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	exportPath := filepath.Join(exportRoot, "denied.png")
	_, err = service.ExportArtifact(ctx, ArtifactExportRequest{
		Handle: artifact.Handle, Path: exportPath, FileTarget: filepath.ToSlash(exportPath),
		TargetScope: permissions.TargetScopeExternal,
	})
	require.Error(t, err)
	require.NoFileExists(t, exportPath)
}

func TestService_FileTargetMismatchPreventsFilesystemSideEffects(t *testing.T) {
	backend := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &interactiveBackend{session: backend},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	refs := make(map[string]string)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}
	source := filepath.Join(t.TempDir(), "source.txt")
	require.NoError(t, os.WriteFile(source, []byte("private"), 0o600))
	_, err = service.Upload(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Upload"], Path: source,
		FileTarget: "/different/file.txt", TargetScope: permissions.TargetScopeExternal,
	})
	require.EqualError(t, err, "browser file target does not match the filesystem path")
	require.Empty(t, backend.uploaded)

	artifact, err := service.Screenshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	exportPath := filepath.Join(t.TempDir(), "export.png")
	_, err = service.ExportArtifact(ctx, ArtifactExportRequest{
		Handle: artifact.Handle, Path: exportPath, FileTarget: "/different/export.png",
		TargetScope: permissions.TargetScopeExternal,
	})
	require.EqualError(t, err, "browser artifact export target is invalid")
	require.NoFileExists(t, exportPath)
}

func TestService_FileTransfersRejectRemoteBrowserProfiles(t *testing.T) {
	backend := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(), &interactiveBackend{session: backend},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	refs := make(map[string]string)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}
	service.sessions[session.ID].ProfileMode = config.BrowserProfileRemoteCDP

	source := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(source, []byte("not transferred"), 0o600))
	_, err = service.Upload(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Upload"], Path: source,
		FileTarget: filepath.ToSlash(source), TargetScope: permissions.TargetScopeExternal,
	})
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorUnavailable, browserErr.Code)
	require.Empty(t, backend.uploaded)

	_, err = service.Download(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Download"],
	})
	browserErr, ok = GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorUnavailable, browserErr.Code)
}

func TestService_RemoteNetworkActionsRequireFullAccess(t *testing.T) {
	backend := newInteractiveBackendSession()
	cfg := testBrowserConfig(t)
	cfg.Network.Strict = new(false)
	cfg.Profiles = []config.BrowserProfileConfig{{
		Name: "remote", Mode: config.BrowserProfileRemoteCDP, CDPEndpoint: "http://127.0.0.1:9222",
		AttachmentScope: config.BrowserAttachmentBrowser, AcknowledgeUnmanagedEgress: true,
	}}
	cfg.DefaultProfile = "remote"
	service, err := NewService(
		context.Background(), cfg, allowChecker(), &interactiveBackend{session: backend},
		WithAttachmentIdentityKey(testAttachmentIdentityKey),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	_, err = service.Open(ctx, ActionRequest{SessionID: session.ID, URL: "https://example.com"})
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorUnavailable, browserErr.Code)
	require.EqualError(
		t, err, "remote browser network actions require full_access",
	)
	require.Len(t, backend.tabs, 1)

	fullAccess := permissions.WithPreset(ctx, permissions.PresetFullAccess)
	_, err = service.Open(fullAccess, ActionRequest{SessionID: session.ID, URL: "https://example.com"})
	require.NoError(t, err)
	require.Len(t, backend.tabs, 2)
}

func TestService_ExistingSessionOperationsRemainCredentialBearing(t *testing.T) {
	backend := newInteractiveBackendSession()
	cfg := testBrowserConfig(t)
	cfg.Network.Strict = new(false)
	cfg.Profiles = []config.BrowserProfileConfig{{
		Name: "personal", Mode: config.BrowserProfileExistingSession,
		CDPEndpoint: "http://127.0.0.1:9222", DataIdentity: "daily-profile",
		AttachmentScope: config.BrowserAttachmentBrowser, AcknowledgeUnmanagedEgress: true,
	}}
	cfg.DefaultProfile = "personal"
	service, err := NewService(
		context.Background(), cfg,
		permissions.NewEngine(permissions.Policy{Preset: permissions.PresetFullAccess}),
		&interactiveBackend{session: backend}, WithAttachmentIdentityKey(testAttachmentIdentityKey),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := permissions.WithPreset(testBrowserContext("owner", "session"), permissions.PresetFullAccess)
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	operations, err := service.ResolveOperations(
		ctx, ActionSnapshot, ActionRequest{SessionID: session.ID, TabID: "tab-1"},
	)
	require.NoError(t, err)
	require.Len(t, operations, 1)
	require.Contains(t, operations[0].Effects, permissions.EffectCredentialBearing)
	require.Contains(t, operations[0].Target, "attachment_id=")
}

func TestService_BackgroundConnectRequiresExactConfiguredRule(t *testing.T) {
	authorization := permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Surface: permissions.SurfaceTUI, Profile: "default", SessionID: "session-1", RunID: "run-1",
	}
	ctx := permissions.WithContext(context.Background(), authorization)
	var observed permissions.Evaluation
	ctx = permissions.WithDecisionObserver(ctx, func(
		_ context.Context,
		_ permissions.Operation,
		evaluation permissions.Evaluation,
	) {
		observed = evaluation
	})
	target := permissions.NetworkTarget{
		Scheme: "https", Host: "background.example", Port: 443, Path: "/", Method: "CONNECT",
		RequestClass: permissions.NetworkRequestSubresource,
	}
	policy := permissions.Policy{Preset: permissions.PresetCustom, Rules: []permissions.Rule{{
		Name:      "allow exact browser background connection",
		Resources: []permissions.Resource{permissions.ResourceNetwork},
		Actions:   []permissions.Action{permissions.ActionConnect},
		Network: []permissions.NetworkSelector{{
			Host: "background.example", Port: 443, Method: "CONNECT",
			RequestClass: permissions.NetworkRequestBackground,
		}},
		Decision: permissions.DecisionAllow,
	}}}
	ledger := newTestTransportPermitLedger(t, time.Now)
	resolveCalls := 0
	proxy := &egressProxy{permits: ledger, policy: NetworkPolicy{
		ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
			resolveCalls++
			return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
		},
	}}
	runtime := &managedSession{
		Session: Session{ID: "browser-1", Owner: Owner{Actor: authorization.Actor}},
		permits: ledger, proxy: proxy,
	}
	service := &Service{checker: permissions.NewEngine(policy), now: time.Now}
	_, err := ledger.beginGeneration(ctx)
	require.NoError(t, err)
	lease, err := service.authorizeBackgroundConnect(ctx, runtime, target)
	require.NoError(t, err)
	lease.Release()
	require.Equal(t, permissions.DecisionAllow, observed.Decision)
	require.Equal(t, "allow exact browser background connection", observed.Rule)
	require.True(t, observed.MatchedConfiguredRule)
	require.Equal(t, 1, resolveCalls)

	fullAccess := permissions.WithPreset(permissions.WithFullAccess(ctx), permissions.PresetFullAccess)
	otherLedger := newTestTransportPermitLedger(t, time.Now)
	runtime.permits = otherLedger
	runtime.proxy.permits = otherLedger
	_, err = otherLedger.beginGeneration(fullAccess)
	require.NoError(t, err)
	lease, err = service.authorizeBackgroundConnect(fullAccess, runtime, target)
	require.NoError(t, err)
	lease.Release()
	require.Equal(t, 2, resolveCalls)

	idleLedger := newTestTransportPermitLedger(t, time.Now)
	runtime.permits = idleLedger
	runtime.proxy.permits = idleLedger
	_, err = service.authorizeBackgroundConnect(fullAccess, runtime, target)
	require.ErrorIs(t, err, errBackgroundAuthorityUnavailable)
	require.Equal(t, 2, resolveCalls)

	var denied permissions.Evaluation
	deniedCtx := permissions.WithDecisionObserver(fullAccess, func(
		_ context.Context,
		_ permissions.Operation,
		evaluation permissions.Evaluation,
	) {
		denied = evaluation
	})
	deniedLedger := newTestTransportPermitLedger(t, time.Now)
	runtime.permits = deniedLedger
	runtime.proxy.permits = deniedLedger
	_, err = deniedLedger.beginGeneration(deniedCtx)
	require.NoError(t, err)
	deniedService := &Service{
		checker: permissions.NewEngine(permissions.Policy{Preset: permissions.PresetFullAccess}), now: time.Now,
	}
	_, err = deniedService.authorizeBackgroundConnect(deniedCtx, runtime, target)
	require.ErrorIs(t, err, errBackgroundRuleRequired)
	require.Equal(t, permissions.DecisionDeny, denied.Decision)
	require.Equal(t, permissions.ReasonBackgroundRule, denied.ReasonCode)
	require.Equal(t, 2, resolveCalls)
}

func TestService_NetworkPolicyDenialIsObservedBeforePermitInstallation(t *testing.T) {
	authorization := permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Surface: permissions.SurfaceTUI, Profile: "default", SessionID: "session-1", RunID: "run-1",
	}
	ctx := permissions.WithContext(context.Background(), authorization)
	var observed permissions.Evaluation
	ctx = permissions.WithDecisionObserver(ctx, func(
		_ context.Context,
		_ permissions.Operation,
		evaluation permissions.Evaluation,
	) {
		observed = evaluation
	})
	resolveCalls := 0
	ledger := newTestTransportPermitLedger(t, time.Now)
	proxy := &egressProxy{permits: ledger, policy: NetworkPolicy{
		Strict: true,
		ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
			resolveCalls++
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
	}}
	runtime := &managedSession{
		Session: Session{ID: "browser-1", Profile: "default", Owner: Owner{Actor: authorization.Actor}},
		permits: ledger, proxy: proxy,
	}
	service := &Service{
		checker: permissions.NewEngine(permissions.Policy{Default: permissions.DecisionAllow}), now: time.Now,
	}
	generation, err := ledger.beginGeneration(ctx)
	require.NoError(t, err)
	target := permissions.NetworkTarget{
		Scheme: "https", Host: "public.example", Port: 443, Path: "/", Method: "GET",
		RequestClass: permissions.NetworkRequestNavigation,
	}

	err = service.authorizeNetworkTargets(ctx, runtime, ActionNavigate, generation, []networkAuthorizationTarget{{
		Target: target, Count: 1,
	}})
	decision, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ReasonHardDeny, decision.Evaluation.ReasonCode)
	require.Equal(t, permissions.DecisionDeny, observed.Decision)
	require.Equal(t, permissions.ReasonHardDeny, observed.ReasonCode)
	require.Equal(t, 1, resolveCalls)
	require.Empty(t, ledger.permits)
}

func TestService_WebSocketAuthorizationUsesExactConnectOperation(t *testing.T) {
	authorization := permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Surface: permissions.SurfaceTUI, Profile: "default", SessionID: "session-1", RunID: "run-1",
	}
	ctx := permissions.WithContext(context.Background(), authorization)
	policy := permissions.Policy{
		Preset:              permissions.PresetCustom,
		Default:             permissions.DecisionDeny,
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{},
		SurfaceDefaults:     map[permissions.Surface]permissions.Decision{},
		Rules: []permissions.Rule{{
			Name:      "allow exact browser websocket",
			Resources: []permissions.Resource{permissions.ResourceNetwork},
			Actions:   []permissions.Action{permissions.ActionConnect},
			Network: []permissions.NetworkSelector{{
				Scheme: "ws", Host: "socket.example", Port: 80, PathPrefix: "/events",
				Method: http.MethodGet, RequestClass: permissions.NetworkRequestWebSocket,
			}},
			Decision: permissions.DecisionAllow,
		}}}
	ledger := newTestTransportPermitLedger(t, time.Now)
	proxy := &egressProxy{permits: ledger, policy: NetworkPolicy{
		ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
		},
	}}
	runtime := &managedSession{
		Session: Session{ID: "browser-1", Profile: "default", Owner: Owner{Actor: authorization.Actor}},
		permits: ledger, proxy: proxy,
	}
	service := &Service{checker: permissions.NewEngine(policy), now: time.Now}
	generation, err := ledger.beginGeneration(ctx)
	require.NoError(t, err)
	target, err := permissions.NetworkTargetFromURL(
		"ws://socket.example/events", http.MethodGet, permissions.NetworkRequestWebSocket,
	)
	require.NoError(t, err)

	require.NoError(t, service.authorizeNetworkTargets(
		ctx,
		runtime,
		ActionNavigate,
		generation,
		[]networkAuthorizationTarget{{Target: target, Count: 1}},
	))
	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	lease.Release()
	otherPath := target
	otherPath.Path = "/other"
	err = service.authorizeNetworkTargets(
		ctx,
		runtime,
		ActionNavigate,
		generation,
		[]networkAuthorizationTarget{{Target: otherPath, Count: 1}},
	)
	decision, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.DecisionDeny, decision.Evaluation.Decision)
	require.Len(t, ledger.permits, 1)
}

func TestIsBackendTabAllowed_EnforcesAttachmentScope(t *testing.T) {
	tab := BackendTab{ID: "target-1", BrowserContextID: "context-1"}
	runtime := &managedSession{}
	require.True(t, isBackendTabAllowed(runtime, tab))

	runtime.attachment.scope = config.BrowserAttachmentContext
	runtime.attachment.contextID = "context-1"
	require.True(t, isBackendTabAllowed(runtime, tab))
	runtime.attachment.contextID = "context-2"
	require.False(t, isBackendTabAllowed(runtime, tab))

	runtime.attachment.scope = config.BrowserAttachmentTargets
	runtime.attachment.targetIDs = map[string]struct{}{"target-1": {}}
	require.True(t, isBackendTabAllowed(runtime, tab))
	runtime.attachment.targetIDs = map[string]struct{}{}
	require.False(t, isBackendTabAllowed(runtime, tab))

	runtime.attachment.scope = "invalid"
	require.False(t, isBackendTabAllowed(runtime, tab))
}

func TestService_RichActionsRejectBackendsWithoutRichCapabilities(t *testing.T) {
	backend := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), allowChecker(),
		backendFunc(func(context.Context, LaunchOptions) (BackendSession, error) {
			return &interactiveOnlySession{BackendSession: backend, InteractiveBackendSession: backend}, nil
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	_, err = service.Screenshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	browserErr, ok := GetError(err)
	require.True(t, ok)
	require.Equal(t, ErrorUnavailable, browserErr.Code)
	require.Equal(t, ActionScreenshot, browserErr.Operation)
}

func TestService_ArtifactReadAndDownloadDenialsHaveNoDataSideEffects(t *testing.T) {
	denyArtifactRead := false
	denyDownload := false
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		isArtifactRead := input.Operation.Resource == permissions.ResourceBrowser &&
			input.Operation.Action == permissions.ActionRead && strings.HasPrefix(input.Operation.Target, "artifact:")
		isDownload := input.Operation.Network != nil &&
			input.Operation.Network.RequestClass == permissions.NetworkRequestDownload
		if (denyArtifactRead && isArtifactRead) || (denyDownload && isDownload) {
			evaluation := permissions.Evaluation{Decision: permissions.DecisionDeny, Reason: "denied"}
			return evaluation, &permissions.DecisionError{Code: permissions.ErrorCodeDenied, Evaluation: evaluation}
		}
		return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
	})
	backend := newInteractiveBackendSession()
	service, err := NewService(
		context.Background(), testBrowserConfig(t), checker, &interactiveBackend{session: backend},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	snapshot, err := service.Snapshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	refs := make(map[string]string)
	for _, node := range snapshot.Nodes {
		refs[node.Name] = node.Ref
	}
	artifact, err := service.Screenshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)

	denyArtifactRead = true
	_, err = service.ReadArtifact(ctx, artifact.Handle)
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeDenied, decisionErr.Code)
	require.FileExists(t, service.artifacts.dataPath(artifact.Handle))

	denyArtifactRead = false
	denyDownload = true
	_, err = service.Download(ctx, ActionRequest{
		SessionID: session.ID, TabID: "tab-1", Ref: refs["Download"],
	})
	decisionErr, ok = permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeDenied, decisionErr.Code)
	entries, err := os.ReadDir(service.cfg.Artifacts.Root)
	require.NoError(t, err)
	binaryArtifacts := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bin") {
			binaryArtifacts++
		}
	}
	require.Equal(t, 1, binaryArtifacts)
}

func TestService_ActiveArtifactSurvivesRetentionCleanupUntilSessionStops(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	cfg := testBrowserConfig(t)
	cfg.Artifacts.Retention = time.Minute
	service, err := NewService(
		context.Background(), cfg, allowChecker(), &interactiveBackend{session: newInteractiveBackendSession()},
		WithNow(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)
	artifact, err := service.Screenshot(ctx, ActionRequest{SessionID: session.ID, TabID: "tab-1"})
	require.NoError(t, err)
	now = now.Add(2 * time.Minute)

	require.NoError(t, service.artifacts.cleanup(service.isArtifactOwnerActive))
	require.FileExists(t, service.artifacts.dataPath(artifact.Handle))
	_, err = service.Stop(ctx, session.ID)
	require.NoError(t, err)
	require.NoError(t, service.artifacts.cleanup(service.isArtifactOwnerActive))
	require.NoFileExists(t, service.artifacts.dataPath(artifact.Handle))
}

func runFailedTabAction(
	run func(*Service, context.Context, ActionRequest) (Tab, error),
) func(context.Context, *Service, string, string) error {
	return func(ctx context.Context, service *Service, sessionID, _ string) error {
		_, err := run(service, ctx, ActionRequest{
			SessionID: sessionID, TabID: "tab-1", Key: "Enter", X: 1, Y: 2,
			Condition: WaitLoad,
		})
		return err
	}
}

func runFailedElementAction(
	run func(*Service, context.Context, ActionRequest) (Tab, error),
) func(context.Context, *Service, string, string) error {
	return func(ctx context.Context, service *Service, sessionID, ref string) error {
		_, err := run(service, ctx, ActionRequest{
			SessionID: sessionID, TabID: "tab-1", Ref: ref, Text: "value", Value: "value",
		})
		return err
	}
}

func mustGetActionTimeout(t *testing.T, timeout time.Duration) time.Duration {
	t.Helper()
	value, err := getActionTimeout(timeout)
	require.NoError(t, err)
	return value
}

func TestIsActionableRole_ClassifiesSupportedRoles(t *testing.T) {
	for _, role := range []string{
		"button", "checkbox", "combobox", "link", "listbox", "menuitem", "option", "radio",
		"searchbox", "slider", "spinbutton", "switch", "tab", "textbox",
	} {
		require.True(t, isActionableRole(role), role)
	}
	require.False(t, isActionableRole("paragraph"))
}
