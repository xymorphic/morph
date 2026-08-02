package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/security"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"golang.org/x/net/websocket"
)

func TestChromiumBackend_RejectsInvalidLaunchConfiguration(t *testing.T) {
	backend := ChromiumBackend{}
	_, err := backend.Start(context.Background(), LaunchOptions{})
	require.EqualError(t, err, "browser startup timeout must be greater than zero")
	_, err = backend.Start(context.Background(), LaunchOptions{Timeout: time.Second, Mode: "bad"})
	require.EqualError(t, err, "browser profile mode is invalid")
	_, err = backend.Start(context.Background(), LaunchOptions{
		Timeout: time.Second, Mode: config.BrowserProfileManagedEphemeral,
	})
	require.EqualError(t, err, "browser executable is required")
	_, err = backend.Start(context.Background(), LaunchOptions{
		Timeout: time.Second, Mode: config.BrowserProfileRemoteCDP,
	})
	require.EqualError(t, err, "browser CDP endpoint is required")
	_, err = backend.Start(context.Background(), LaunchOptions{
		Timeout: time.Second, ProxyURL: "http://127.0.0.1:1234",
	})
	require.EqualError(t, err, "browser proxy credentials are required")
	_, err = backend.Start(context.Background(), LaunchOptions{
		Timeout: time.Second, ProxyUser: "morph", ProxySecret: "secret",
	})
	require.EqualError(t, err, "browser proxy URL is required for proxy credentials")
}

func TestChromiumSession_AttachmentScopeRestrictsTargets(t *testing.T) {
	first := &target.Info{TargetID: "target-1", BrowserContextID: "context-1"}
	second := &target.Info{TargetID: "target-2", BrowserContextID: "context-2"}

	session := &chromiumSession{
		attachmentScope:   config.BrowserAttachmentTargets,
		attachmentTargets: map[string]struct{}{"target-1": {}},
	}
	require.True(t, session.isTargetAllowed(first))
	require.False(t, session.isTargetAllowed(second))

	session.attachmentScope = config.BrowserAttachmentContext
	session.browserContextID = "context-2"
	require.False(t, session.isTargetAllowed(first))
	require.True(t, session.isTargetAllowed(second))

	session.attachmentScope = config.BrowserAttachmentBrowser
	require.True(t, session.isTargetAllowed(first))
	require.True(t, session.isTargetAllowed(second))
}

func TestChromiumSession_AttachedTargetsAreQuarantinedWithoutClosingThem(t *testing.T) {
	session := &chromiumSession{
		attached: true, attachmentScope: config.BrowserAttachmentBrowser,
		quarantinedTargets: make(map[string]struct{}), openingTabIDs: make(map[string]struct{}),
	}
	info := &target.Info{TargetID: "human-tab", Type: "page"}

	session.getPageTargetLifecycleListener()(&target.EventTargetCreated{TargetInfo: info})

	require.Contains(t, session.quarantinedTargets, "human-tab")
	require.False(t, session.isTargetAllowed(info))

	session.getPageTargetLifecycleListener()(&target.EventTargetDestroyed{TargetID: info.TargetID})
	require.NotContains(t, session.quarantinedTargets, "human-tab")

	session.openingTabIDs["morph-tab"] = struct{}{}
	session.getPageTargetLifecycleListener()(&target.EventTargetCreated{
		TargetInfo: &target.Info{TargetID: "morph-tab", Type: "page"},
	})
	require.NotContains(t, session.quarantinedTargets, "morph-tab")
}

func TestChromiumSession_OpeningTargetReadySignalPersistsUntilWaiter(t *testing.T) {
	session := &chromiumSession{
		openingTargets:  1,
		openingTabIDs:   make(map[string]struct{}),
		openingTabReady: make(map[string]chan struct{}),
	}

	session.mu.Lock()
	require.True(t, session.claimOpeningTargetLocked("tab-1"))
	session.mu.Unlock()
	session.signalOpeningTargetReady("tab-1")
	session.signalOpeningTargetReady("tab-1")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, session.waitForOpeningTargetReady(ctx, "tab-1"))
}

func TestChromiumSession_OpeningTargetReadinessRejectsUnarmedAndCancelledWaits(t *testing.T) {
	session := &chromiumSession{
		openingTabIDs:   make(map[string]struct{}),
		openingTabReady: make(map[string]chan struct{}),
	}

	session.mu.Lock()
	require.False(t, session.claimOpeningTargetLocked("unarmed"))
	session.openingTargets = 1
	require.True(t, session.claimOpeningTargetLocked("tab-1"))
	require.True(t, session.claimOpeningTargetLocked("tab-1"))
	session.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, session.waitForOpeningTargetReady(ctx, "tab-1"), context.Canceled)
}

func TestChromiumSession_PageTargetLifecycleListenerClassifiesManagedTargets(t *testing.T) {
	session := &chromiumSession{
		rootTabID:          "root",
		tabContexts:        map[string]context.Context{"known": context.Background()},
		quarantinedTargets: make(map[string]struct{}),
		openingTabIDs:      make(map[string]struct{}),
		openingTabReady:    make(map[string]chan struct{}),
	}
	listener := session.getPageTargetLifecycleListener()

	listener(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "worker", Type: "worker"}})
	listener(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "root", Type: "page"}})
	listener(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "known", Type: "page"}})
	require.Empty(t, session.quarantinedTargets)

	session.openingTargets = 1
	listener(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "opened", Type: "page"}})
	require.Contains(t, session.openingTabIDs, "opened")
	require.NotContains(t, session.quarantinedTargets, "opened")

	listener(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "popup", Type: "page"}})
	require.Contains(t, session.quarantinedTargets, "popup")
}

func TestChromiumSession_RelatedTargetCleanupCancelsOwnedSessions(t *testing.T) {
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	firstID := target.SessionID("session-1")
	secondID := target.SessionID("session-2")
	session := &chromiumSession{
		tabContexts:     map[string]context.Context{"target-1": firstCtx},
		tabCancels:      map[string]context.CancelFunc{"target-1": cancelFirst},
		relatedSessions: map[target.SessionID]string{firstID: "target-1", secondID: "target-1"},
		relatedTargets: map[string]*relatedTarget{"target-1": {
			targetType: "page",
			sessions: map[target.SessionID]*relatedTargetSession{
				firstID:  {ctx: firstCtx, cancel: cancelFirst},
				secondID: {ctx: secondCtx, cancel: cancelSecond},
			},
		}},
	}

	session.removeRelatedTargetSession(firstID)
	require.ErrorIs(t, firstCtx.Err(), context.Canceled)
	require.Contains(t, session.relatedTargets, "target-1")
	require.Contains(t, session.tabContexts, "target-1")

	session.removeRelatedTarget("target-1")
	require.ErrorIs(t, secondCtx.Err(), context.Canceled)
	require.Empty(t, session.relatedTargets)
	require.Empty(t, session.relatedSessions)
	require.Empty(t, session.tabContexts)
	require.Empty(t, session.tabCancels)

	session.removeRelatedTarget("missing")
	session.removeRelatedTargetSession("")
}

func TestChromiumSession_GetEffectiveTabIDUsesRootOnlyWhenUnspecified(t *testing.T) {
	session := &chromiumSession{rootTabID: "root"}

	require.Equal(t, "tab-1", session.getEffectiveTabID("tab-1"))
	require.Equal(t, "root", session.getEffectiveTabID(""))
}

func TestChromiumSession_SetRootTargetContextRegistersRootBeforeTargetSupervision(t *testing.T) {
	ctx, cancel := chromedp.NewContext(context.Background())
	t.Cleanup(cancel)
	chromiumCtx := chromedp.FromContext(ctx)
	chromiumCtx.Target = &chromedp.Target{TargetID: "root"}
	session := &chromiumSession{
		tabContexts:     make(map[string]context.Context),
		consoleMessages: make(map[string][]ConsoleMessage),
	}

	require.NoError(t, session.setRootTargetContext(ctx))

	session.mu.Lock()
	defer session.mu.Unlock()
	require.Equal(t, "root", session.rootTabID)
	require.True(t, ctx == session.tabContexts["root"])
}

func TestChromiumSession_SetRootTargetContextRejectsUnavailableTarget(t *testing.T) {
	session := &chromiumSession{tabContexts: make(map[string]context.Context)}

	require.EqualError(
		t,
		session.setRootTargetContext(context.Background()),
		"browser root target is unavailable",
	)
}

func TestGetAttachmentTarget_SelectsOnlyEligiblePage(t *testing.T) {
	infos := []*target.Info{
		nil,
		{
			TargetID:         "worker",
			Type:             "worker",
			BrowserContextID: "context-1",
		},
		{
			TargetID:         "subtype",
			Type:             "page",
			Subtype:          "prerender",
			BrowserContextID: "context-1",
		},
		{
			TargetID:         "other",
			Type:             "page",
			URL:              "https://example.com",
			BrowserContextID: "context-2",
		},
		{
			TargetID:         "selected",
			Type:             "page",
			URL:              "https://example.com",
			BrowserContextID: "context-1",
		},
		{
			TargetID:         "blank",
			Type:             "page",
			URL:              "about:blank",
			BrowserContextID: "context-1",
		},
	}

	require.Equal(
		t, target.ID("blank"),
		getAttachmentTarget(infos, config.BrowserAttachmentContext, "context-1"),
	)
	require.Empty(t, getAttachmentTarget(infos, config.BrowserAttachmentContext, "missing"))
	require.Equal(t, target.ID("blank"), getAttachmentTarget(infos, config.BrowserAttachmentBrowser, ""))
	require.Equal(t, target.ID("a"), getAttachmentTarget([]*target.Info{
		{
			TargetID: "z",
			Type:     "page",
			URL:      "https://example.com/z",
		},
		{
			TargetID: "a",
			Type:     "page",
			URL:      "https://example.com/a",
		},
	}, config.BrowserAttachmentBrowser, ""))
}

func TestGetAttachedContextCancel_PreservesExistingTarget(t *testing.T) {
	ctx, cancel := chromedp.NewContext(context.Background())
	chromiumCtx := chromedp.FromContext(ctx)
	chromiumCtx.Target = &chromedp.Target{}

	getAttachedContextCancel(ctx, cancel)()

	require.Nil(t, chromiumCtx.Target)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestChromiumSession_PreservesAllAttachedTargetsOnShutdown(t *testing.T) {
	rootCtx, cancelRoot := chromedp.NewContext(context.Background())
	tabCtx, cancelTab := chromedp.NewContext(context.Background())
	t.Cleanup(cancelRoot)
	t.Cleanup(cancelTab)
	chromedp.FromContext(rootCtx).Target = &chromedp.Target{}
	chromedp.FromContext(tabCtx).Target = &chromedp.Target{}
	session := &chromiumSession{
		attached: true, ctx: rootCtx,
		tabContexts: map[string]context.Context{"tab": tabCtx},
	}

	session.preserveAttachedTargets()

	require.Nil(t, chromedp.FromContext(rootCtx).Target)
	require.Nil(t, chromedp.FromContext(tabCtx).Target)
}

func TestPrepareInitialBrowserContext_ValidatesTargetScopeWithoutConnecting(t *testing.T) {
	_, _, _, err := prepareInitialBrowserContext(
		context.Background(), context.Background(),
		LaunchOptions{AttachmentScope: config.BrowserAttachmentTargets},
	)
	require.EqualError(t, err, "target-scoped browser attachment requires a target ID")

	allocatorCtx, cancelAllocator := chromedp.NewRemoteAllocator(
		context.Background(), "ws://127.0.0.1:1/devtools/browser/unreachable",
	)
	defer cancelAllocator()
	browserCtx, cancel, cancelBootstrap, err := prepareInitialBrowserContext(
		context.Background(), allocatorCtx,
		LaunchOptions{AttachmentScope: config.BrowserAttachmentTargets, TargetIDs: []string{"target-1"}},
	)
	require.NoError(t, err)
	require.NotNil(t, browserCtx)
	require.NotNil(t, cancel)
	require.Nil(t, cancelBootstrap)
	cancel()

	browserCtx, cancel, cancelBootstrap, err = prepareInitialBrowserContext(
		context.Background(), allocatorCtx, LaunchOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, browserCtx)
	require.NotNil(t, cancel)
	require.Nil(t, cancelBootstrap)
	cancel()

	_, _, _, err = prepareInitialBrowserContext(
		context.Background(), allocatorCtx,
		LaunchOptions{
			AttachmentScope:  config.BrowserAttachmentContext,
			BrowserContextID: "context-1",
		},
	)
	require.Error(t, err)
}

func TestPrepareInitialBrowserContext_SelectsConfiguredBrowserContext(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	allocatorOptions := append(
		append([]chromedp.ExecAllocatorOption(nil), chromedp.DefaultExecAllocatorOptions[:]...),
		chromedp.ExecPath(executable),
		chromedp.UserDataDir(t.TempDir()),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
	)
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(cancelAllocator)
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(cancelBrowser)
	require.NoError(t, chromedp.Run(browserCtx))
	chromiumCtx := chromedp.FromContext(browserCtx)
	require.NotNil(t, chromiumCtx)
	require.NotNil(t, chromiumCtx.Browser)
	browserExecutor := cdp.WithExecutor(context.Background(), chromiumCtx.Browser)
	browserContextID, err := target.CreateBrowserContext().Do(browserExecutor)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, target.DisposeBrowserContext(browserContextID).Do(browserExecutor))
	})
	targetID, err := target.CreateTarget("about:blank").
		WithBrowserContextID(browserContextID).
		WithNewWindow(true).
		Do(browserExecutor)
	require.NoError(t, err)

	selectedCtx, cancelSelected, cancelBootstrap, err := prepareInitialBrowserContext(
		context.Background(), browserCtx,
		LaunchOptions{
			AttachmentScope:  config.BrowserAttachmentContext,
			BrowserContextID: string(browserContextID),
		},
	)
	require.NoError(t, err)
	require.NotNil(t, cancelBootstrap)
	t.Cleanup(cancelBootstrap)
	var selected *target.Info
	require.NoError(t, chromedp.Run(selectedCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		selected, err = target.GetTargetInfo().Do(ctx)
		return err
	})))
	require.Equal(t, targetID, selected.TargetID)
	require.Equal(t, browserContextID, selected.BrowserContextID)
	getAttachedContextCancel(selectedCtx, cancelSelected)()
	retained, err := target.GetTargetInfo().WithTargetID(targetID).Do(browserExecutor)
	require.NoError(t, err)
	require.Equal(t, targetID, retained.TargetID)

	_, _, _, err = prepareInitialBrowserContext(
		context.Background(), browserCtx,
		LaunchOptions{
			AttachmentScope:  config.BrowserAttachmentContext,
			BrowserContextID: "missing-context",
		},
	)
	require.EqualError(t, err, "configured browser attachment has no page target")
}

func TestChromiumBackend_RedactsRemoteRelaySecretFromConnectionError(t *testing.T) {
	_, err := (ChromiumBackend{}).Start(context.Background(), LaunchOptions{
		Mode:        config.BrowserProfileRemoteCDP,
		CDPEndpoint: "ws://127.0.0.1:1/devtools/browser/missing?_morph_browser_token=secret",
		Timeout:     time.Second,
	})
	require.EqualError(t, err, "browser CDP connection failed")
	require.NotContains(t, err.Error(), "secret")
}

func TestChromiumBackend_UsesAuthenticatedProxyAndCannotBypassStrictPolicy(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	var permissiveRequests atomic.Int64
	var originReceivedAuthorization atomic.Bool
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		permissiveRequests.Add(1)
		if request.URL.Path == "/auth" {
			if request.Header.Get("Authorization") != "" {
				originReceivedAuthorization.Store(true)
			}
			writer.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(writer, "<html><body>fixture</body></html>")
	}))
	defer fixture.Close()

	permissive, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	session := startChromiumSession(t, executable, permissive)
	restoreAuthorization := allowBackendNetworkRequests(t, session, permissive)
	chromium := session.(*chromiumSession)
	require.NoError(t, chromedp.Run(chromium.ctx, chromedp.Navigate(fixture.URL)))
	require.Positive(t, permissiveRequests.Load())
	_ = chromedp.Run(chromium.ctx, chromedp.Navigate(fixture.URL+"/auth"))
	require.False(t, originReceivedAuthorization.Load())
	restoreAuthorization()
	require.NoError(t, session.Close(context.Background()))
	require.NoError(t, permissive.Close(context.Background()))

	var strictRequests atomic.Int64
	strictFixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		strictRequests.Add(1)
		_, _ = io.WriteString(writer, "<html><body>blocked</body></html>")
	}))
	defer strictFixture.Close()
	strict, err := startEgressProxy(NetworkPolicy{Strict: true})
	require.NoError(t, err)
	session = startChromiumSession(t, executable, strict)
	chromium = session.(*chromiumSession)
	_ = chromedp.Run(chromium.ctx, chromedp.Navigate(strictFixture.URL))
	require.Zero(t, strictRequests.Load())
	restoreAuthorization()
	require.NoError(t, session.Close(context.Background()))
	require.NoError(t, strict.Close(context.Background()))
}

func TestChromiumBackend_PreservesNativeFeaturesWhileDenyingUnarmedWebSockets(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	var socketRequests atomic.Int64
	var socketUpgrade atomic.Value
	var socketURL string
	var secureSocketURL string
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/socket" {
			socketRequests.Add(1)
			socketUpgrade.Store(request.Header.Get("Upgrade"))
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprintf(
			writer,
			`<html><body><script>
window.nativeFeatures = [
  typeof WebSocket,
  typeof Worker,
  typeof SharedWorker,
  typeof navigator.serviceWorker?.register
];
window.blockedSockets = 0;
window.workerSocketBlocked = false;
for (const url of [%q, %q]) {
  const socket = new WebSocket(url);
  socket.onerror = () => { window.blockedSockets++; };
}
const workerSource = %q;
const worker = new Worker(URL.createObjectURL(new Blob([workerSource], {type: "text/javascript"})));
worker.onmessage = (event) => { window.workerSocketBlocked = event.data === "blocked"; };
</script></body></html>`,
			socketURL,
			secureSocketURL,
			fmt.Sprintf(`
try {
  const socket = new WebSocket(%q);
  socket.onopen = () => postMessage("open");
  socket.onerror = () => postMessage("blocked");
} catch (_) {
  postMessage("blocked");
}`, socketURL),
		)
	}))
	defer fixture.Close()
	socketURL = "ws" + strings.TrimPrefix(fixture.URL, "http") + "/socket"
	secureSocketURL = "wss" + strings.TrimPrefix(fixture.URL, "http") + "/socket"

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	generation, err := proxy.permits.beginGeneration(context.Background())
	require.NoError(t, err)
	restoreAuthorization := session.(NetworkAuthorizingBackendSession).SetNetworkAuthorizer(
		"*",
		func(ctx context.Context, target permissions.NetworkTarget) error {
			if target.RequestClass == permissions.NetworkRequestWebSocket {
				return errors.New("websocket denied")
			}
			addresses, resolveErr := proxy.getPolicy().Resolve(ctx, target)
			if resolveErr != nil {
				return resolveErr
			}
			return proxy.permits.install(generation, []transportPermitInput{{
				Target: target, Addresses: addresses, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
			}})
		},
	)
	chromium := session.(*chromiumSession)
	var blockedSockets int
	var socketsBlocked bool
	var workerSocketBlocked bool
	var nativeFeatures []string
	require.NoError(t, chromedp.Run(
		chromium.ctx,
		chromedp.Navigate(fixture.URL),
		chromedp.Poll("window.blockedSockets === 2", &socketsBlocked),
		chromedp.Poll("window.workerSocketBlocked === true", &workerSocketBlocked),
		chromedp.Evaluate("window.blockedSockets", &blockedSockets),
		chromedp.Evaluate("window.nativeFeatures", &nativeFeatures),
	))
	require.Equal(t, []string{"function", "function", "function", "function"}, nativeFeatures)
	require.Equal(t, 2, blockedSockets)
	require.True(t, workerSocketBlocked)
	require.Zero(t, socketRequests.Load(), "upgrade=%q", socketUpgrade.Load())
	restoreAuthorization()
	require.NoError(t, proxy.permits.revokeGeneration(generation))
}

func TestChromiumBackend_RetainsTransportContainmentWithoutFeatureSuppression(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy).(*chromiumSession)
	session.process.mu.Lock()
	arguments := slices.Clone(session.process.cmd.Args)
	session.process.mu.Unlock()
	require.Contains(t, arguments, "--disable-quic")
	require.Contains(t, arguments, "--deny-permission-prompts")
	require.Contains(t, arguments, "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
	require.Contains(t, arguments, "--webrtc-ip-handling-policy=disable_non_proxied_udp")
	require.NotContains(t, arguments, "--disable-background-networking")
	require.NotContains(t, arguments, "--disable-service-worker")
}

func TestChromiumBackend_PreservesNativeTransportAPIsWithoutDirectUDP(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	defer func() { _ = udp.Close() }()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = tcp.Close() }()
	udpPort := udp.LocalAddr().(*net.UDPAddr).Port
	tcpPort := tcp.Addr().(*net.TCPAddr).Port

	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(writer, `<!doctype html><script>
window.transportTypes = [typeof RTCPeerConnection, typeof WebTransport];
window.transportDone = false;
(async () => {
  const peer = new RTCPeerConnection({iceServers: [
    {urls: "stun:127.0.0.1:%d"},
    {urls: "turn:127.0.0.1:%d?transport=tcp", username: "morph", credential: "test"}
  ]});
  peer.createDataChannel("morph");
  await peer.setLocalDescription(await peer.createOffer());
  if (typeof WebTransport === "function") {
    try {
      const transport = new WebTransport("https://127.0.0.1:%d/");
      transport.closed.catch(() => {});
    } catch (_) {}
  }
  setTimeout(() => { peer.close(); window.transportDone = true; }, 750);
})().catch(() => { window.transportDone = true; });
</script>`, udpPort, tcpPort, udpPort)
	}))
	defer fixture.Close()

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	restoreAuthorization := allowBackendNetworkRequests(t, session, proxy)
	defer restoreAuthorization()

	var types []string
	require.NoError(t, chromedp.Run(
		session.(*chromiumSession).ctx,
		chromedp.Navigate(fixture.URL),
		chromedp.Poll("window.transportDone === true", nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate("window.transportTypes", &types),
	))
	require.Equal(t, []string{"function", "function"}, types)

	require.NoError(t, udp.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	packet := make([]byte, 2048)
	count, _, udpErr := udp.ReadFromUDP(packet)
	require.Error(t, udpErr, "unexpected direct UDP packet: %x", packet[:count])
	require.True(t, errors.Is(udpErr, os.ErrDeadlineExceeded))

	require.NoError(t, tcp.(*net.TCPListener).SetDeadline(time.Now().Add(500*time.Millisecond)))
	connection, tcpErr := tcp.Accept()
	if connection != nil {
		_ = connection.Close()
	}
	require.Error(t, tcpErr)
	require.True(t, errors.Is(tcpErr, os.ErrDeadlineExceeded))
}

func TestChromiumBackend_AllowsPermissionBoundWebSocket(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	connected := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/socket", websocket.Handler(func(connection *websocket.Conn) {
		select {
		case connected <- struct{}{}:
		default:
		}
		var message string
		if receiveErr := websocket.Message.Receive(connection, &message); receiveErr != nil {
			return
		}
		_ = websocket.Message.Send(connection, "echo:"+message)
	}))
	var socketURL string
	mux.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(writer, `<!doctype html><script>
window.socketResult = "";
const socket = new WebSocket(%q);
socket.onopen = () => socket.send("morph");
socket.onmessage = (event) => { window.socketResult = event.data; socket.close(); };
socket.onerror = () => { window.socketResult = "error"; };
</script>`, socketURL)
	})
	fixture := httptest.NewServer(mux)
	defer fixture.Close()
	socketURL = "ws" + strings.TrimPrefix(fixture.URL, "http") + "/socket"

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	generation, err := proxy.permits.beginGeneration(context.Background())
	require.NoError(t, err)
	var websocketAuthorizations atomic.Int64
	observedWebSocket := make(chan permissions.NetworkTarget, 1)
	restoreAuthorization := session.(NetworkAuthorizingBackendSession).SetNetworkAuthorizer(
		"*",
		func(ctx context.Context, target permissions.NetworkTarget) error {
			if target.RequestClass == permissions.NetworkRequestWebSocket {
				websocketAuthorizations.Add(1)
				observedWebSocket <- target
			}
			addresses, resolveErr := proxy.getPolicy().Resolve(ctx, target)
			if resolveErr != nil {
				return resolveErr
			}
			return proxy.permits.install(generation, []transportPermitInput{{
				Target: target, Addresses: addresses, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
			}})
		},
	)
	defer func() {
		restoreAuthorization()
		require.NoError(t, proxy.permits.revokeGeneration(generation))
	}()

	var result string
	require.NoError(t, chromedp.Run(
		session.(*chromiumSession).ctx,
		chromedp.Navigate(fixture.URL),
		chromedp.Poll(`window.socketResult !== ""`, nil),
		chromedp.Evaluate("window.socketResult", &result),
	))
	require.Equal(t, "echo:morph", result)
	require.EqualValues(t, 1, websocketAuthorizations.Load())
	observed := <-observedWebSocket
	require.Equal(t, "ws", observed.Scheme)
	require.Equal(t, http.MethodGet, observed.Method)
	require.Equal(t, "/socket", observed.Path)
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("WebSocket upstream was not reached")
	}
}

func TestChromiumBackend_AllowsPermissionBoundSecureWebSocket(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	connected := make(chan struct{}, 1)
	socketServer := httptest.NewTLSServer(websocket.Handler(func(connection *websocket.Conn) {
		select {
		case connected <- struct{}{}:
		default:
		}
		var message string
		if receiveErr := websocket.Message.Receive(connection, &message); receiveErr != nil {
			return
		}
		_ = websocket.Message.Send(connection, "secure:"+message)
	}))
	defer socketServer.Close()
	socketURL := "wss" + strings.TrimPrefix(socketServer.URL, "https")
	page := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(writer, `<!doctype html><script>
window.socketResult = "";
const socket = new WebSocket(%q);
socket.onopen = () => socket.send("morph");
socket.onmessage = (event) => { window.socketResult = event.data; socket.close(); };
socket.onerror = () => { window.socketResult = "error"; };
</script>`, socketURL)
	}))
	defer page.Close()

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	generation, err := proxy.permits.beginGeneration(context.Background())
	require.NoError(t, err)
	var websocketAuthorizations atomic.Int64
	observedWebSocket := make(chan permissions.NetworkTarget, 1)
	restoreAuthorization := session.(NetworkAuthorizingBackendSession).SetNetworkAuthorizer(
		"*",
		func(ctx context.Context, target permissions.NetworkTarget) error {
			if target.RequestClass == permissions.NetworkRequestWebSocket {
				websocketAuthorizations.Add(1)
				observedWebSocket <- target
			}
			addresses, resolveErr := proxy.getPolicy().Resolve(ctx, target)
			if resolveErr != nil {
				return resolveErr
			}
			return proxy.permits.install(generation, []transportPermitInput{{
				Target: target, Addresses: addresses, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
			}})
		},
	)
	defer func() {
		restoreAuthorization()
		require.NoError(t, proxy.permits.revokeGeneration(generation))
	}()

	var result string
	require.NoError(t, chromedp.Run(
		session.(*chromiumSession).ctx,
		security.SetIgnoreCertificateErrors(true),
		chromedp.Navigate(page.URL),
		chromedp.Poll(`window.socketResult !== ""`, nil),
		chromedp.Evaluate("window.socketResult", &result),
	))
	require.Equal(t, "secure:morph", result)
	require.EqualValues(t, 1, websocketAuthorizations.Load())
	observed := <-observedWebSocket
	require.Equal(t, "wss", observed.Scheme)
	require.Equal(t, http.MethodConnect, observed.Method)
	require.Equal(t, "/", observed.Path)
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("secure WebSocket upstream was not reached")
	}
}

func TestChromiumBackend_SupervisesNativeWorkerAndFrameTraffic(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	var iframeRequests atomic.Int64
	var workerRequests atomic.Int64
	var sharedWorkerRequests atomic.Int64
	var serviceWorkerRequests atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/iframe.html":
			_, _ = io.WriteString(writer, `<script>
fetch("/iframe-data").then((response) => response.text()).then((value) => parent.postMessage(value, "*"));
</script>`)
		case "/worker.js":
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `
fetch("/worker-data").then((response) => response.text()).then(postMessage);
self.onmessage = () => {};
`)
		case "/shared-worker.js":
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `
self.onconnect = (event) => {
  const port = event.ports[0];
  fetch("/shared-worker-data").then((response) => response.text()).then((value) => port.postMessage(value));
};
`)
		case "/service-worker.js":
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `
self.addEventListener("install", (event) => {
  event.waitUntil(fetch("/service-worker-data").then(() => self.skipWaiting()));
});
`)
		case "/iframe-data":
			iframeRequests.Add(1)
			_, _ = io.WriteString(writer, "iframe-ok")
		case "/worker-data":
			workerRequests.Add(1)
			_, _ = io.WriteString(writer, "worker-ok")
		case "/shared-worker-data":
			sharedWorkerRequests.Add(1)
			_, _ = io.WriteString(writer, "shared-worker-ok")
		case "/service-worker-data":
			serviceWorkerRequests.Add(1)
			_, _ = io.WriteString(writer, "service-worker-ok")
		default:
			_, _ = io.WriteString(writer, `<!doctype html>
<iframe src="/iframe.html"></iframe>
<script>
window.nativeResults = {iframe: "", worker: "", sharedWorker: "", serviceWorker: false};
window.addEventListener("message", (event) => {
  if (event.data === "iframe-ok") window.nativeResults.iframe = event.data;
});
const worker = new Worker("/worker.js");
worker.onmessage = (event) => { window.nativeResults.worker = event.data; };
const sharedWorker = new SharedWorker("/shared-worker.js");
sharedWorker.port.onmessage = (event) => { window.nativeResults.sharedWorker = event.data; };
sharedWorker.port.start();
navigator.serviceWorker.register("/service-worker.js").then(() => {
  window.nativeResults.serviceWorker = true;
});
</script>`)
		}
	}))
	defer fixture.Close()

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	var authorizedPaths sync.Map
	restoreAuthorization := allowBackendNetworkRequestsWithObserver(
		t,
		session,
		proxy,
		func(target permissions.NetworkTarget) {
			authorizedPaths.Store(target.Path, struct{}{})
		},
	)
	defer restoreAuthorization()
	restoreBackground := allowBackendBackgroundOrigin(t, proxy, fixture.URL)
	background := proxy.background
	var backgroundRequests atomic.Int64
	proxy.background = func(
		ctx context.Context,
		target permissions.NetworkTarget,
	) (*transportPermitLease, error) {
		backgroundRequests.Add(1)
		return background(ctx, target)
	}
	defer func() {
		proxy.background = background
		restoreBackground()
	}()

	var ready bool
	err = chromedp.Run(
		session.(*chromiumSession).ctx,
		chromedp.Navigate(fixture.URL),
		chromedp.Poll(`
window.nativeResults.iframe === "iframe-ok" &&
window.nativeResults.worker === "worker-ok" &&
window.nativeResults.sharedWorker === "shared-worker-ok" &&
window.nativeResults.serviceWorker === true
`, &ready, chromedp.WithPollingTimeout(10*time.Second)),
	)
	if err != nil {
		var results map[string]any
		_ = chromedp.Run(
			session.(*chromiumSession).ctx,
			chromedp.Evaluate("window.nativeResults", &results),
		)
		t.Fatalf(
			"native worker traffic did not settle: %v; results=%v; requests=iframe:%d worker:%d shared:%d service:%d",
			err,
			results,
			iframeRequests.Load(),
			workerRequests.Load(),
			sharedWorkerRequests.Load(),
			serviceWorkerRequests.Load(),
		)
	}
	require.Eventually(t, func() bool {
		return iframeRequests.Load() > 0 &&
			workerRequests.Load() > 0 &&
			sharedWorkerRequests.Load() > 0 &&
			serviceWorkerRequests.Load() > 0
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		session := session.(*chromiumSession)
		session.mu.Lock()
		defer session.mu.Unlock()
		types := make(map[string]bool)
		for _, related := range session.relatedTargets {
			types[related.targetType] = true
		}
		return types["worker"] && types["shared_worker"] && types["service_worker"]
	}, 5*time.Second, 20*time.Millisecond)
	_, workerDataAuthorized := authorizedPaths.Load("/worker-data")
	require.True(t, workerDataAuthorized)
	require.Positive(t, backgroundRequests.Load())
	chromium := session.(*chromiumSession)
	chromium.mu.Lock()
	sharedClientCount := -1
	for _, related := range chromium.relatedTargets {
		if related.targetType == "shared_worker" {
			sharedClientCount = len(related.clientTabIDs)
			break
		}
	}
	chromium.mu.Unlock()
	require.Zero(t, sharedClientCount)
}

func TestChromiumBackend_DeniesUnattributedWorkerTrafficWithoutSuppressingWorkers(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	var sharedRequests atomic.Int64
	var serviceRequests atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/shared-worker.js":
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `
self.onconnect = (event) => {
  fetch("/shared-data").catch(() => {});
  event.ports[0].postMessage("started");
};
`)
		case "/service-worker.js":
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `
self.addEventListener("install", (event) => {
  event.waitUntil(fetch("/service-data").catch(() => {}));
});
`)
		case "/shared-data":
			sharedRequests.Add(1)
			_, _ = io.WriteString(writer, "unexpected")
		case "/service-data":
			serviceRequests.Add(1)
			_, _ = io.WriteString(writer, "unexpected")
		default:
			_, _ = io.WriteString(writer, `<!doctype html><script>
window.workerTypes = [typeof SharedWorker, typeof navigator.serviceWorker?.register];
window.workersStarted = false;
const shared = new SharedWorker("/shared-worker.js");
shared.port.onmessage = () => { window.workersStarted = true; };
shared.port.start();
navigator.serviceWorker.register("/service-worker.js").catch(() => {});
</script>`)
		}
	}))
	defer fixture.Close()

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	restoreAuthorization := allowBackendNetworkRequests(t, session, proxy)
	defer restoreAuthorization()

	var types []string
	require.NoError(t, chromedp.Run(
		session.(*chromiumSession).ctx,
		chromedp.Navigate(fixture.URL),
		chromedp.Poll("window.workersStarted === true", nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate("window.workerTypes", &types),
	))
	require.Equal(t, []string{"function", "function"}, types)
	require.Never(t, func() bool {
		return sharedRequests.Load() > 0 || serviceRequests.Load() > 0
	}, 500*time.Millisecond, 20*time.Millisecond)
}

func startChromiumSession(t *testing.T, executable string, proxy *egressProxy) BackendSession {
	t.Helper()
	username, password := proxy.authorization.credentials()
	session, err := (ChromiumBackend{}).Start(context.Background(), LaunchOptions{
		Executable:       executable,
		Mode:             config.BrowserProfileManagedEphemeral,
		DataDir:          t.TempDir(),
		DownloadRoot:     t.TempDir(),
		ProxyURL:         proxy.chromiumURL(),
		ProxyUser:        username,
		ProxySecret:      password,
		Timeout:          15 * time.Second,
		transportPermits: proxy.permits,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close(context.Background())) })

	return session
}

func TestChromiumBackend_StartsAvailableChromium(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	backend := ChromiumBackend{}
	session, err := backend.Start(context.Background(), LaunchOptions{
		Executable: executable,
		Mode:       config.BrowserProfileManagedEphemeral,
		DataDir:    t.TempDir(),
		Timeout:    15 * time.Second,
	})
	require.NoError(t, err)
	require.NoError(t, session.Health(context.Background()))
	require.NoError(t, session.Close(context.Background()))
	require.NoError(t, session.Close(context.Background()))
}

func TestChromedpFork_AdoptsPausedWorkerTargetSession(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/worker.js" {
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `fetch("/worker-data")`)
			return
		}
		if request.URL.Path == "/worker-data" {
			_, _ = io.WriteString(writer, `worker data`)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(writer, `<!doctype html><title>Worker fixture</title>`)
	}))
	defer fixture.Close()

	allocatorOptions := append(
		append([]chromedp.ExecAllocatorOption(nil), chromedp.DefaultExecAllocatorOptions[:]...),
		chromedp.ExecPath(executable),
		chromedp.UserDataDir(t.TempDir()),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
	)
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()
	require.NoError(t, chromedp.Run(browserCtx))

	attached := make(chan *target.EventAttachedToTarget, 1)
	listenForWorker := func(event any) {
		if event, ok := event.(*target.EventAttachedToTarget); ok {
			if event.TargetInfo.Type == "worker" {
				select {
				case attached <- event:
				default:
				}
			}
		}
	}
	chromedp.ListenBrowser(browserCtx, listenForWorker)
	chromedp.ListenTarget(browserCtx, listenForWorker)
	require.NoError(t, chromedp.Run(browserCtx, chromedp.Navigate(fixture.URL)))
	paused := make(chan *fetch.EventRequestPaused, 1)
	chromedp.ListenTarget(browserCtx, func(event any) {
		request, ok := event.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		if request.Request.URL == fixture.URL+"/worker-data" {
			select {
			case paused <- request:
			default:
			}
		}
		go func() {
			_ = chromedp.Run(browserCtx, fetch.ContinueRequest(request.RequestID))
		}()
	})
	require.NoError(t, chromedp.Run(
		browserCtx,
		fetch.Enable(),
		target.SetAutoAttach(true, true).WithFlatten(true),
		chromedp.Evaluate(`new Worker("/worker.js")`, nil),
	))

	var event *target.EventAttachedToTarget
	select {
	case event = <-attached:
	case <-time.After(5 * time.Second):
		t.Fatal("worker target was not attached")
	}
	require.True(t, event.WaitingForDebugger)

	childCtx, cancelChild := chromedp.NewContext(
		browserCtx,
		chromedp.WithExistingTargetSession(event.TargetInfo.TargetID, event.SessionID),
		chromedp.WithExistingTargetSessionType(event.TargetInfo.Type),
		chromedp.WithExistingTargetSessionWaitingForDebugger(event.WaitingForDebugger),
	)
	defer cancelChild()

	var className string
	require.NoError(t, chromedp.Run(
		childCtx,
		target.SetAutoAttach(true, true).WithFlatten(true),
		chromedp.Evaluate("self.constructor.name", &className),
	))
	require.Equal(t, "DedicatedWorkerGlobalScope", className)
	select {
	case request := <-paused:
		require.Equal(t, fixture.URL+"/worker-data", request.Request.URL)
	case <-time.After(5 * time.Second):
		t.Fatal("worker request was not intercepted")
	}
}

func TestChromiumBackend_InteractiveActionsCompleteLocalFixtureWorkflow(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	var popupRequests atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/popup" {
			popupRequests.Add(1)
		}
		_, _ = io.WriteString(writer, `<!doctype html>
<html>
  <head><title>Browser fixture</title></head>
  <body>
	<label>Name <input aria-label="Name" value="Old text" onkeydown="document.getElementById('result').textContent = event.key" /></label>
	<label>Password <input type="password" aria-label="Password" /></label>
	<label>Choice <select aria-label="Choice" onchange="document.getElementById('choice-result').textContent = 'Choice ' + this.value"><option value="choice-one">One</option><option value="choice-two">Two</option></select></label>
	<label>Upload <input type="file" aria-label="Upload" /></label>
    <button onclick="document.getElementById('result').textContent = 'Submitted'">Submit</button>
    <button onclick="alert('blocked')">Dialog</button>
    <button onclick="window.open('/popup')">Popup</button>
	<p id="result">Waiting</p>
	<p id="choice-result">Choice choice-one</p>
	<div style="height: 2000px"></div>
  </body>
</html>`)
	}))
	defer fixture.Close()
	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	t.Cleanup(func() { require.NoError(t, session.Close(context.Background())) })
	interactive := session.(InteractiveBackendSession)
	restoreAuthorization := allowBackendNetworkRequests(t, session, proxy)
	defer restoreAuthorization()

	tabs, err := interactive.ListTabs(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, tabs)
	tab, err := interactive.OpenTab(context.Background(), fixture.URL)
	require.NoError(t, err)
	require.NoError(t, interactive.FocusTab(context.Background(), tab.ID))
	focusedTabs, err := interactive.ListTabs(context.Background())
	require.NoError(t, err)
	require.True(t, getBackendTabByID(focusedTabs, tab.ID).Active)
	tab, err = interactive.Navigate(context.Background(), tab.ID, fixture.URL)
	require.NoError(t, err)
	require.Equal(t, "Browser fixture", tab.Title)

	snapshot, err := interactive.Snapshot(context.Background(), tab.ID)
	require.NoError(t, err)
	var textboxID, passwordID, buttonID, dialogID, popupID, selectID, uploadID int64
	for _, node := range snapshot.Nodes {
		switch {
		case node.Role == "textbox" && node.Name == "Name":
			textboxID = node.BackendNodeID
		case node.Role == "textbox" && node.Name == "Password":
			passwordID = node.BackendNodeID
			require.True(t, node.Sensitive)
		case node.Role == "button" && node.Name == "Submit":
			buttonID = node.BackendNodeID
		case node.Role == "button" && node.Name == "Dialog":
			dialogID = node.BackendNodeID
		case node.Role == "button" && node.Name == "Popup":
			popupID = node.BackendNodeID
		case node.Role == "combobox" && node.Name == "Choice":
			selectID = node.BackendNodeID
		case node.Name == "Upload":
			uploadID = node.BackendNodeID
		}
	}
	require.Positive(t, textboxID)
	require.Positive(t, passwordID)
	require.Positive(t, buttonID)
	require.Positive(t, dialogID)
	require.Positive(t, popupID)
	require.Positive(t, selectID)
	require.Positive(t, uploadID)
	require.NoError(t, interactive.Type(context.Background(), tab.ID, textboxID, "Morph", true))
	require.NoError(t, interactive.Type(context.Background(), tab.ID, textboxID, " browser", false))
	typedSnapshot, err := interactive.Snapshot(context.Background(), tab.ID)
	require.NoError(t, err)
	require.Equal(t, "Morph browser", getBackendSnapshotNode(typedSnapshot, "textbox", "Name").Value)
	require.NoError(t, interactive.Press(context.Background(), tab.ID, "Enter"))
	require.NoError(t, interactive.Wait(context.Background(), tab.ID, WaitText, "Enter", 0))
	require.NoError(t, interactive.Select(context.Background(), tab.ID, selectID, "choice-two"))
	require.NoError(t, interactive.Wait(context.Background(), tab.ID, WaitText, "Choice choice-two", 0))
	require.Error(t, interactive.Select(context.Background(), tab.ID, selectID, "missing"))
	require.NoError(t, interactive.Scroll(context.Background(), tab.ID, 0, 500))
	require.Eventually(t, func() bool {
		var scrollY float64
		evaluateErr := session.(*chromiumSession).runInTab(
			context.Background(), tab.ID, chromedp.Evaluate(`window.scrollY`, &scrollY),
		)
		return evaluateErr == nil && scrollY >= 500
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, interactive.Wait(context.Background(), tab.ID, WaitVisible, "", buttonID))
	require.NoError(t, interactive.Wait(context.Background(), tab.ID, WaitURL, fixture.URL, 0))
	require.NoError(t, interactive.Wait(context.Background(), tab.ID, WaitLoad, "", 0))
	require.NoError(t, interactive.Click(context.Background(), tab.ID, buttonID))
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, interactive.Wait(waitCtx, tab.ID, WaitText, "Submitted", 0))
	require.NoError(t, interactive.Click(context.Background(), tab.ID, dialogID))
	require.Eventually(t, func() bool {
		messages, consoleErr := session.(RichBackendSession).Console(context.Background(), tab.ID, 10)
		return consoleErr == nil && slices.ContainsFunc(messages, func(message ConsoleMessage) bool {
			return strings.Contains(message.Text, "automatically dismissed alert dialog: blocked")
		})
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, interactive.Click(context.Background(), tab.ID, uploadID))
	require.NoError(t, interactive.Click(context.Background(), tab.ID, popupID))
	require.Never(t, func() bool {
		current, listErr := interactive.ListTabs(context.Background())
		return listErr != nil || len(current) != len(tabs)+1
	}, 500*time.Millisecond, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		session := session.(*chromiumSession)
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.quarantinedTargets) == 0
	}, 3*time.Second, 20*time.Millisecond)
	require.Zero(t, popupRequests.Load())

	firstURL := fixture.URL + "/first"
	secondURL := fixture.URL + "/second"
	_, err = interactive.Navigate(context.Background(), tab.ID, firstURL)
	require.NoError(t, err)
	_, err = interactive.Navigate(context.Background(), tab.ID, secondURL)
	require.NoError(t, err)
	back, err := interactive.Back(context.Background(), tab.ID)
	require.NoError(t, err)
	require.Equal(t, firstURL, back.URL)
	forward, err := interactive.Forward(context.Background(), tab.ID)
	require.NoError(t, err)
	require.Equal(t, secondURL, forward.URL)
	reloaded, err := interactive.Reload(context.Background(), tab.ID)
	require.NoError(t, err)
	require.Equal(t, secondURL, reloaded.URL)

	temporary, err := interactive.OpenTab(context.Background(), fixture.URL+"/temporary")
	require.NoError(t, err)
	session.(*chromiumSession).recordConsoleMessage(temporary.ID, ConsoleMessage{Text: "temporary"})
	require.NoError(t, interactive.CloseTab(context.Background(), temporary.ID))
	session.(*chromiumSession).mu.Lock()
	_, retainedConsole := session.(*chromiumSession).consoleMessages[temporary.ID]
	session.(*chromiumSession).mu.Unlock()
	require.False(t, retainedConsole)
	require.NoError(t, interactive.FocusTab(context.Background(), tab.ID))
}

func TestChromiumSession_NavigateReturnsAfterDOMContentLoadedWithPendingSubresource(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	releaseImage := make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/start":
			http.Redirect(writer, request, "/page", http.StatusFound)
			return
		case "/pending.png":
			writer.Header().Set("Content-Type", "image/png")
			writer.WriteHeader(http.StatusOK)
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			<-releaseImage
			return
		}
		_, _ = io.WriteString(writer, `<!doctype html>
<html>
  <head><title>DOMContentLoaded fixture</title></head>
  <body><main>Ready</main><img src="/pending.png" alt="Pending" /></body>
</html>`)
	}))
	defer fixture.Close()
	defer close(releaseImage)
	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	t.Cleanup(func() { require.NoError(t, session.Close(context.Background())) })
	interactive := session.(InteractiveBackendSession)
	restoreAuthorization := allowBackendNetworkRequests(t, session, proxy)
	defer restoreAuthorization()
	tabs, err := interactive.ListTabs(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, tabs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tab, err := interactive.Navigate(ctx, tabs[0].ID, fixture.URL+"/start")
	require.NoError(t, err)
	require.Equal(t, fixture.URL+"/page", tab.URL)
	require.Equal(t, "DOMContentLoaded fixture", tab.Title)
}

func TestService_ChromiumNavigationAuthorizesSubresources(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	var allowedSubresources atomic.Int64
	var blockedSubresources atomic.Int64
	blocked := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		blockedSubresources.Add(1)
		_, _ = io.WriteString(writer, `window.blocked = true`)
	}))
	defer blocked.Close()
	blockedTarget, err := permissions.NetworkTargetFromURL(
		blocked.URL, http.MethodGet, permissions.NetworkRequestSubresource,
	)
	require.NoError(t, err)
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/style.css":
			allowedSubresources.Add(1)
			writer.Header().Set("Content-Type", "text/css")
			_, _ = io.WriteString(writer, `body { color: rgb(1, 2, 3); }`)
		case "/blocked":
			_, _ = io.WriteString(writer, `<html><head><script src="`+blocked.URL+`/blocked.js"></script></head><body>blocked</body></html>`)
		default:
			_, _ = io.WriteString(writer, `<html><head><link rel="stylesheet" href="/style.css"></head><body>allowed</body></html>`)
		}
	}))
	defer fixture.Close()

	cfg := testBrowserConfig(t)
	cfg.Executable = executable
	cfg.StartTimeout = 15 * time.Second
	strict := false
	cfg.Network.Strict = &strict
	checker := checkerFunc(func(_ context.Context, input permissions.EvaluationInput) (permissions.Evaluation, error) {
		if input.Operation.Network != nil && input.Operation.Network.Port == blockedTarget.Port {
			evaluation := permissions.Evaluation{Decision: permissions.DecisionDeny, Reason: "cross-origin blocked"}
			return evaluation, &permissions.DecisionError{
				Code: permissions.ErrorCodeDenied, Evaluation: evaluation,
			}
		}
		return permissions.Evaluation{Decision: permissions.DecisionAllow}, nil
	})
	service, err := NewService(context.Background(), cfg, checker, ChromiumBackend{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	ctx := testBrowserContext("owner", "session")
	session, err := service.Start(ctx, StartRequest{})
	require.NoError(t, err)

	_, err = service.Open(ctx, ActionRequest{SessionID: session.ID, URL: fixture.URL})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return allowedSubresources.Load() > 0 }, 3*time.Second, 20*time.Millisecond)
	_, err = service.Open(ctx, ActionRequest{SessionID: session.ID, URL: fixture.URL + "/blocked"})
	decisionErr, ok := permissions.GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, permissions.ErrorCodeDenied, decisionErr.Code)
	require.Zero(t, blockedSubresources.Load())
}

func TestChromiumBackend_RichArtifactUploadDialogAndDownloadWorkflow(t *testing.T) {
	executable, err := discoverChromiumExecutable("")
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/download" {
			writer.Header().Set("Content-Type", "text/plain")
			writer.Header().Set("Content-Disposition", `attachment; filename="report.txt"`)
			_, _ = io.WriteString(writer, "downloaded")
			return
		}
		_, _ = io.WriteString(writer, `<html><body>
			<input type="file" aria-label="Upload">
			<button aria-label="Prompt" onclick="window.prompt('Name?')">Prompt</button>
			<a aria-label="Download" href="/download" download>Download</a>
			<script>console.warn('token=private-value')</script>
		</body></html>`)
	}))
	defer fixture.Close()
	strict := false
	proxy, err := startEgressProxy(NetworkPolicy{Strict: strict})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	session := startChromiumSession(t, executable, proxy)
	t.Cleanup(func() { require.NoError(t, session.Close(context.Background())) })
	restore := allowBackendNetworkRequests(t, session, proxy)
	defer restore()
	interactive := session.(InteractiveBackendSession)
	rich := session.(RichBackendSession)
	tab, err := interactive.OpenTab(context.Background(), fixture.URL)
	require.NoError(t, err)
	snapshot, err := interactive.Snapshot(context.Background(), tab.ID)
	require.NoError(t, err)
	upload := getBackendSnapshotNode(snapshot, "button", "Upload")
	prompt := getBackendSnapshotNode(snapshot, "button", "Prompt")
	download := getBackendSnapshotNode(snapshot, "link", "Download")
	require.NotZero(t, upload.BackendNodeID)
	require.NotZero(t, prompt.BackendNodeID)
	require.NotZero(t, download.BackendNodeID)

	screenshot, err := rich.Screenshot(context.Background(), tab.ID, true)
	require.NoError(t, err)
	require.Equal(t, ArtifactScreenshot, screenshot.Kind)
	require.True(t, len(screenshot.Data) > 8)
	require.Equal(t, []byte("\x89PNG\r\n\x1a\n"), screenshot.Data[:8])
	pdf, err := rich.PDF(context.Background(), tab.ID)
	require.NoError(t, err)
	require.Equal(t, ArtifactPDF, pdf.Kind)
	require.True(t, strings.HasPrefix(string(pdf.Data), "%PDF"))
	require.Eventually(t, func() bool {
		messages, consoleErr := rich.Console(context.Background(), tab.ID, 10)
		return consoleErr == nil && len(messages) > 0 && !strings.Contains(messages[0].Text, "private-value")
	}, 3*time.Second, 20*time.Millisecond)

	staged := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(staged, []byte("upload"), 0o600))
	require.NoError(t, rich.Upload(context.Background(), tab.ID, upload.BackendNodeID, staged))
	require.NoError(t, rich.RespondToDialog(context.Background(), tab.ID, prompt.BackendNodeID, true, "Morph"))
	messages, err := rich.Console(context.Background(), tab.ID, 10)
	require.NoError(t, err)
	require.Contains(t, messages[len(messages)-1].Text, "accepted prompt dialog: Name?")
	artifact, err := rich.Download(context.Background(), tab.ID, download.BackendNodeID, 1024)
	require.NoError(t, err)
	require.Equal(t, ArtifactDownload, artifact.Kind)
	require.Equal(t, "report.txt", artifact.Name)
	require.Equal(t, []byte("downloaded"), artifact.Data)
}

func getBackendTabByID(tabs []BackendTab, id string) BackendTab {
	for _, tab := range tabs {
		if tab.ID == id {
			return tab
		}
	}
	return BackendTab{}
}

func getBackendSnapshotNode(snapshot BackendSnapshot, role, name string) BackendSnapshotNode {
	for _, node := range snapshot.Nodes {
		if node.Role == role && node.Name == name {
			return node
		}
	}
	return BackendSnapshotNode{}
}

func allowBackendNetworkRequests(t *testing.T, session BackendSession, proxy *egressProxy) func() {
	return allowBackendNetworkRequestsWithObserver(t, session, proxy, nil)
}

func allowBackendNetworkRequestsWithObserver(
	t *testing.T,
	session BackendSession,
	proxy *egressProxy,
	observe func(permissions.NetworkTarget),
) func() {
	t.Helper()
	authorizing, ok := session.(NetworkAuthorizingBackendSession)
	if !ok {
		return func() {}
	}
	generation, err := proxy.permits.beginGeneration(context.Background())
	require.NoError(t, err)
	restore := authorizing.SetNetworkAuthorizer("*", func(ctx context.Context, target permissions.NetworkTarget) error {
		if observe != nil {
			observe(target)
		}
		addresses, err := proxy.getPolicy().Resolve(ctx, target)
		if err != nil {
			return err
		}
		return proxy.permits.install(generation, []transportPermitInput{{
			Target: target, Addresses: addresses, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
		}})
	})
	return func() {
		restore()
		require.NoError(t, proxy.permits.revokeGeneration(generation))
	}
}

func allowBackendBackgroundOrigin(t *testing.T, proxy *egressProxy, rawURL string) func() {
	t.Helper()
	expected, err := permissions.NetworkTargetFromURL(
		rawURL,
		http.MethodConnect,
		permissions.NetworkRequestBackground,
	)
	require.NoError(t, err)
	expected.Path = "/"
	generation, ok := proxy.permits.getActiveGeneration()
	require.True(t, ok)
	previous := proxy.background
	proxy.background = func(
		ctx context.Context,
		target permissions.NetworkTarget,
	) (*transportPermitLease, error) {
		if target.Scheme != expected.Scheme || target.Host != expected.Host || target.Port != expected.Port {
			return nil, errors.New("unexpected background origin")
		}
		addresses, resolveErr := proxy.getPolicy().Resolve(ctx, target)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if installErr := proxy.permits.install(generation.ID, []transportPermitInput{{
			Target: target, Addresses: addresses, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
		}}); installErr != nil {
			return nil, installErr
		}
		return proxy.permits.acquire(target)
	}
	return func() {
		proxy.background = previous
	}
}

func TestGetKeyInput_NormalizesNamedKeys(t *testing.T) {
	tests := map[string]string{
		"Enter": "\r", "Tab": "\t", "Escape": "\u001b", "Esc": "\u001b",
		"Backspace": "\b", "Delete": "\u007f", "ArrowUp": "\u0304",
		"ArrowDown": "\u0301", "ArrowLeft": "\u0302", "ArrowRight": "\u0303",
		"Home": "\u0306", "End": "\u0305", "PageUp": "\u0308", "PageDown": "\u0307",
		"Space": " ", " x ": "x",
	}
	for input, want := range tests {
		require.Equal(t, want, getKeyInput(input), input)
	}
}

func TestChromiumSession_NetworkAuthorizersAreScopedToTabs(t *testing.T) {
	session := &chromiumSession{
		networkAuthorizers: make(map[string]networkAuthorization),
		openingTabIDs:      make(map[string]struct{}),
		networkErrors:      make(map[string]error),
	}
	exact := func(context.Context, permissions.NetworkTarget) error { return errors.New("exact") }
	pending := func(context.Context, permissions.NetworkTarget) error { return errors.New("pending") }
	wildcard := func(context.Context, permissions.NetworkTarget) error { return errors.New("wildcard") }
	restoreExact := session.SetNetworkAuthorizer("tab-1", exact)
	restorePending := session.SetNetworkAuthorizer("", pending)
	restoreWildcard := session.SetNetworkAuthorizer("*", wildcard)
	session.openingTabIDs["tab-2"] = struct{}{}

	require.EqualError(t, authorizeNetworkRequest(session, "tab-1"), "exact")
	require.EqualError(t, authorizeNetworkRequest(session, "tab-2"), "pending")
	require.EqualError(t, authorizeNetworkRequest(session, "tab-3"), "wildcard")
	restoreReplacement := session.SetNetworkAuthorizer("tab-1", wildcard)
	replacement, ok := session.getNetworkAuthorization("tab-1")
	require.True(t, ok)
	require.EqualError(t, replacement.authorize(replacement.ctx, permissions.NetworkTarget{}), "wildcard")
	restoreReplacement()
	require.ErrorIs(t, replacement.ctx.Err(), context.Canceled)
	require.EqualError(t, authorizeNetworkRequest(session, "tab-1"), "exact")

	restoreExact()
	restorePending()
	restoreWildcard()
	_, ok = session.getNetworkAuthorization("tab-1")
	require.False(t, ok)
	session.networkErrors["tab-1"] = errors.New("first tab failed")
	session.networkErrors["tab-2"] = errors.New("second tab failed")
	require.EqualError(t, session.consumeNetworkError("tab-1"), "first tab failed")
	require.Nil(t, session.consumeNetworkError("tab-1"))
	require.EqualError(t, session.consumeNetworkError("tab-2"), "second tab failed")
}

func TestChromiumSession_OpeningAuthorizationRemainsBoundUntilRestored(t *testing.T) {
	session := &chromiumSession{
		networkAuthorizers: make(map[string]networkAuthorization),
		openingTabIDs:      make(map[string]struct{}),
		networkErrors:      make(map[string]error),
	}
	restore := session.SetNetworkAuthorizer("", func(context.Context, permissions.NetworkTarget) error {
		return errors.New("opening")
	})

	session.mu.Lock()
	session.openingTabIDs["tab-1"] = struct{}{}
	session.setTabNetworkAuthorizationFromOpeningLocked("tab-1")
	delete(session.openingTabIDs, "tab-1")
	session.mu.Unlock()

	require.EqualError(t, authorizeNetworkRequest(session, "tab-1"), "opening")
	restore()
	_, ok := session.getNetworkAuthorization("tab-1")
	require.False(t, ok)
}

func TestChromiumSession_RelatedTargetAuthorizationRequiresUnambiguousOwnership(t *testing.T) {
	session := &chromiumSession{
		networkAuthorizers: make(map[string]networkAuthorization),
		relatedTargets:     make(map[string]*relatedTarget),
	}
	restoreFirst := session.SetNetworkAuthorizer("tab-1", func(context.Context, permissions.NetworkTarget) error {
		return errors.New("first")
	})
	defer restoreFirst()
	restoreSecond := session.SetNetworkAuthorizer("tab-2", func(context.Context, permissions.NetworkTarget) error {
		return errors.New("second")
	})
	defer restoreSecond()

	session.relatedTargets["worker"] = &relatedTarget{
		targetType: "worker",
		ownerTabID: "tab-1",
	}
	authorization, tabID, ok := session.getRelatedNetworkAuthorization("worker")
	require.True(t, ok)
	require.Equal(t, "tab-1", tabID)
	require.EqualError(t, authorization.authorize(authorization.ctx, permissions.NetworkTarget{}), "first")

	session.relatedTargets["shared"] = &relatedTarget{
		targetType: "shared_worker",
		clientTabIDs: map[string]struct{}{
			"tab-1": {},
		},
	}
	authorization, tabID, ok = session.getRelatedNetworkAuthorization("shared")
	require.True(t, ok)
	require.Equal(t, "tab-1", tabID)
	require.EqualError(t, authorization.authorize(authorization.ctx, permissions.NetworkTarget{}), "first")

	session.relatedTargets["shared"].clientTabIDs["tab-2"] = struct{}{}
	_, _, ok = session.getRelatedNetworkAuthorization("shared")
	require.False(t, ok)

	session.relatedTargets["service"] = &relatedTarget{targetType: "service_worker"}
	_, _, ok = session.getRelatedNetworkAuthorization("service")
	require.False(t, ok)
}

func TestChromiumSession_NetworkErrorsRequireActiveAuthorization(t *testing.T) {
	session := &chromiumSession{
		networkAuthorizers: make(map[string]networkAuthorization),
		networkErrors:      make(map[string]error),
	}
	restore := session.SetNetworkAuthorizer("tab-1", func(context.Context, permissions.NetworkTarget) error {
		return nil
	})
	authorization, ok := session.getNetworkAuthorization("tab-1")
	require.True(t, ok)
	authorization.cancel()
	session.recordNetworkError("tab-1", authorization.id, context.Canceled)
	require.Nil(t, session.consumeNetworkError("tab-1"))
	restore()

	session.recordNetworkError("tab-1", authorization.id, context.Canceled)
	require.Nil(t, session.consumeNetworkError("tab-1"))

	restore = session.SetNetworkAuthorizer("tab-1", func(context.Context, permissions.NetworkTarget) error {
		return nil
	})
	authorization, ok = session.getNetworkAuthorization("tab-1")
	require.True(t, ok)
	restoreReplacement := session.SetNetworkAuthorizer("tab-1", func(context.Context, permissions.NetworkTarget) error {
		return nil
	})
	session.recordNetworkError("tab-1", authorization.id, errors.New("replaced request failed"))
	require.Nil(t, session.consumeNetworkError("tab-1"))
	authorization, ok = session.getNetworkAuthorization("tab-1")
	require.True(t, ok)
	session.recordNetworkError("tab-1", authorization.id, errors.New("active request failed"))
	require.EqualError(t, session.consumeNetworkError("tab-1"), "active request failed")
	restoreReplacement()
	restore()
}

func authorizeNetworkRequest(session *chromiumSession, tabID string) error {
	authorization, ok := session.getNetworkAuthorization(tabID)
	if !ok {
		return errors.New("network authorizer not found")
	}
	return authorization.authorize(authorization.ctx, permissions.NetworkTarget{})
}

func TestChromiumActionContextsRespectCallerCancellation(t *testing.T) {
	actionCtx, done := newBoundedActionContext(context.Background(), nil)
	done()
	require.ErrorIs(t, actionCtx.Err(), context.Canceled)

	caller, cancelCaller := context.WithCancel(context.Background())
	actionCtx, done = newBoundedActionContext(context.Background(), caller)
	cancelCaller()
	require.Eventually(t, func() bool { return errors.Is(actionCtx.Err(), context.Canceled) }, time.Second, time.Millisecond)
	done()

	caller, cancelCaller = context.WithTimeout(context.Background(), time.Second)
	actionCtx, done = newBoundedActionContext(context.Background(), caller)
	done()
	cancelCaller()
	require.ErrorIs(t, actionCtx.Err(), context.Canceled)

	var session *chromiumSession
	actionCtx, done = session.newActionContext(context.Background())
	defer done()
	require.ErrorIs(t, actionCtx.Err(), context.Canceled)
	require.EqualError(t, session.runInTab(context.Background(), "tab"), "browser session is unavailable")
}

func TestChromiumSession_WaitForNetworkIdleTracksPendingRequests(t *testing.T) {
	session := &chromiumSession{networkActivity: make(map[string]*networkActivity)}
	session.markNetworkRequestStarted("tab-1")
	result := make(chan error, 1)
	go func() {
		result <- session.WaitForNetworkIdle(context.Background(), "tab-1", 10*time.Millisecond)
	}()

	select {
	case err := <-result:
		t.Fatalf("network wait returned before the request finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	session.markNetworkRequestFinished("tab-1")
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("network wait did not finish after the request settled")
	}
}
