package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/wandxy/morph/internal/config"
)

func (s *chromiumSession) ListTabs(ctx context.Context) ([]BackendTab, error) {
	if s == nil || s.ctx == nil {
		return nil, errors.New("browser session is unavailable")
	}
	actionCtx, done := s.newActionContext(ctx)
	defer done()
	infos, err := chromedp.Targets(actionCtx)
	if err != nil {
		return nil, err
	}
	result := make([]BackendTab, 0, len(infos))
	for _, info := range infos {
		if info.Type != "page" || info.Subtype != "" || !s.isTargetAllowed(info) {
			continue
		}
		s.mu.Lock()
		if s.activeTabID == "" {
			s.activeTabID = string(info.TargetID)
		}
		activeTabID := s.activeTabID
		s.mu.Unlock()
		result = append(result, backendTabFromTarget(info, activeTabID))
	}
	return result, nil
}

func (s *chromiumSession) OpenTab(ctx context.Context, rawURL string) (BackendTab, error) {
	s.mu.Lock()
	s.openingTargets++
	s.mu.Unlock()
	defer s.disarmTargetCreation()
	actionCtx, done := s.newActionContext(ctx)
	defer done()
	browserCtx, err := s.getBrowserExecutorContext(actionCtx)
	if err != nil {
		return BackendTab{}, err
	}
	create := target.CreateTarget("about:blank")
	switch s.attachmentScope {
	case config.BrowserAttachmentTargets:
		return BackendTab{}, errors.New("target-scoped browser attachment cannot create tabs")
	case config.BrowserAttachmentContext:
		create = create.WithBrowserContextID(cdp.BrowserContextID(s.browserContextID))
	}
	id, err := create.Do(browserCtx)
	if err != nil {
		return BackendTab{}, errors.New("browser tab could not be created")
	}
	s.mu.Lock()
	delete(s.quarantinedTargets, string(id))
	s.openingTabIDs[string(id)] = struct{}{}
	s.setTabNetworkAuthorizationFromOpeningLocked(string(id))
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.openingTabIDs, string(id))
		delete(s.openingTabReady, string(id))
		s.mu.Unlock()
	}()
	if err := s.waitForOpeningTargetReady(actionCtx, string(id)); err != nil {
		return BackendTab{}, errors.New("browser tab supervision was not ready")
	}
	if err := waitForBrowserTarget(actionCtx, id); err != nil {
		return BackendTab{}, errors.New("browser tab was not ready")
	}
	if err := target.ActivateTarget(id).Do(browserCtx); err != nil {
		return BackendTab{}, errors.New("browser tab could not be activated")
	}
	s.setActiveTab(string(id))
	tab, err := s.Navigate(ctx, string(id), rawURL)
	if err != nil {
		return BackendTab{}, errors.Join(errors.New("browser tab could not navigate"), err)
	}
	return tab, nil
}

func (s *chromiumSession) disarmTargetCreation() {
	s.mu.Lock()
	if s.openingTargets > 0 {
		s.openingTargets--
	}
	s.mu.Unlock()
}

func waitForBrowserTarget(ctx context.Context, id target.ID) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		infos, err := chromedp.Targets(ctx)
		if err != nil {
			return err
		}
		for _, info := range infos {
			if info.TargetID == id {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *chromiumSession) FocusTab(ctx context.Context, tabID string) error {
	actionCtx, done := s.newActionContext(ctx)
	defer done()
	browserCtx, err := s.getBrowserExecutorContext(actionCtx)
	if err != nil {
		return err
	}
	if err := target.ActivateTarget(target.ID(tabID)).Do(browserCtx); err != nil {
		return err
	}
	s.setActiveTab(tabID)
	return nil
}

func (s *chromiumSession) CloseTab(ctx context.Context, tabID string) error {
	actionCtx, done := s.newActionContext(ctx)
	defer done()
	browserCtx, err := s.getBrowserExecutorContext(actionCtx)
	if err != nil {
		return err
	}
	if err := target.CloseTarget(target.ID(tabID)).Do(browserCtx); err != nil {
		return err
	}
	s.mu.Lock()
	if s.activeTabID == tabID {
		s.activeTabID = ""
	}
	cancel := s.tabCancels[tabID]
	delete(s.tabCancels, tabID)
	delete(s.tabContexts, tabID)
	delete(s.consoleMessages, tabID)
	delete(s.dialogResponses, tabID)
	delete(s.networkActivity, tabID)
	delete(s.popupEvents, tabID)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *chromiumSession) Navigate(ctx context.Context, tabID, rawURL string) (BackendTab, error) {
	if err := s.runInTab(
		ctx,
		tabID,
		navigateToDOMContentLoaded(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return BackendTab{}, err
	}
	return s.getBackendTab(ctx, tabID)
}

func navigateToDOMContentLoaded(rawURL string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		listenerCtx, cancelListener := context.WithCancel(ctx)
		defer cancelListener()
		lifecycleEvents := make(chan *page.EventLifecycleEvent, 128)
		chromedp.ListenTarget(listenerCtx, func(event any) {
			lifecycleEvent, ok := event.(*page.EventLifecycleEvent)
			if !ok || lifecycleEvent.Name != "DOMContentLoaded" {
				return
			}
			select {
			case lifecycleEvents <- lifecycleEvent:
			default:
			}
		})

		frameID, loaderID, errorText, isDownload, err := page.Navigate(rawURL).Do(ctx)
		if err != nil {
			return err
		}
		if errorText != "" {
			return fmt.Errorf("page load error %s", errorText)
		}
		if isDownload {
			return errors.New("browser navigation became a download")
		}
		if loaderID == "" {
			return nil
		}

		for {
			select {
			case event := <-lifecycleEvents:
				if event.FrameID == frameID && event.LoaderID == loaderID {
					return nil
				}
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	})
}

func (s *chromiumSession) Back(ctx context.Context, tabID string) (BackendTab, error) {
	return s.navigateHistory(ctx, tabID, -1)
}

func (s *chromiumSession) Forward(ctx context.Context, tabID string) (BackendTab, error) {
	return s.navigateHistory(ctx, tabID, 1)
}

func (s *chromiumSession) navigateHistory(ctx context.Context, tabID string, offset int) (BackendTab, error) {
	actionCtx, done := s.newActionContext(ctx)
	defer done()

	var expectedURL string
	err := s.runInTab(actionCtx, tabID, chromedp.ActionFunc(func(tabCtx context.Context) error {
		current, entries, err := page.GetNavigationHistory().Do(tabCtx)
		if err != nil {
			return err
		}
		index := int(current) + offset
		if index < 0 || index >= len(entries) {
			if offset < 0 {
				return errors.New("browser tab has no previous history entry")
			}
			return errors.New("browser tab has no next history entry")
		}
		expectedURL = entries[index].URL
		return page.NavigateToHistoryEntry(entries[index].ID).Do(tabCtx)
	}))
	if err != nil {
		return BackendTab{}, fmt.Errorf("navigate browser history: %w", err)
	}
	if err := s.waitForValue(actionCtx, tabID, WaitURL, expectedURL); err != nil {
		return BackendTab{}, fmt.Errorf("wait for browser history URL: %w", err)
	}
	if err := s.runInTab(actionCtx, tabID, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return BackendTab{}, fmt.Errorf("wait for browser history document: %w", err)
	}
	tab, err := s.getBackendTab(actionCtx, tabID)
	if err != nil {
		return BackendTab{}, fmt.Errorf("read browser history result: %w", err)
	}
	return tab, nil
}

func (s *chromiumSession) Reload(ctx context.Context, tabID string) (BackendTab, error) {
	if err := s.runInTab(ctx, tabID, page.Reload(), chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return BackendTab{}, err
	}
	return s.getBackendTab(ctx, tabID)
}

func (s *chromiumSession) Snapshot(ctx context.Context, tabID string) (BackendSnapshot, error) {
	var tree rawAccessibilityTree
	if err := s.runInTab(ctx, tabID, chromedp.ActionFunc(func(actionCtx context.Context) error {
		return cdp.Execute(actionCtx, "Accessibility.getFullAXTree", nil, &tree)
	})); err != nil {
		return BackendSnapshot{}, err
	}
	tab, err := s.getBackendTab(ctx, tabID)
	if err != nil {
		return BackendSnapshot{}, err
	}
	result := BackendSnapshot{
		URL:   tab.URL,
		Title: tab.Title,
		Nodes: make([]BackendSnapshotNode, 0, len(tree.Nodes)),
	}
	for _, node := range tree.Nodes {
		if node.Ignored {
			continue
		}
		value := BackendSnapshotNode{
			BackendNodeID: node.BackendDOMNodeID, Role: getAXValue(node.Role), Name: getAXValue(node.Name),
			Value: getAXValue(node.Value), Description: getAXValue(node.Description),
		}
		for _, property := range node.Properties {
			propertyValue := getAXValue(property.Value)
			if property.Name == "disabled" && propertyValue == "true" {
				value.Disabled = true
			}
			if propertyValue != "" {
				if value.Properties == nil {
					value.Properties = make(map[string]string)
				}
				value.Properties[string(property.Name)] = propertyValue
			}
		}
		if value.Role != "" || value.Name != "" || value.Value != "" {
			result.Nodes = append(result.Nodes, value)
		}
	}
	if err := s.markSensitiveSnapshotNodes(ctx, tabID, result.Nodes); err != nil {
		return BackendSnapshot{}, err
	}
	return result, nil
}

func (s *chromiumSession) markSensitiveSnapshotNodes(
	ctx context.Context,
	tabID string,
	nodes []BackendSnapshotNode,
) error {
	return s.runInTab(ctx, tabID, chromedp.ActionFunc(func(actionCtx context.Context) error {
		for index := range nodes {
			if nodes[index].BackendNodeID <= 0 ||
				(nodes[index].Role != "textbox" && nodes[index].Role != "searchbox") {
				continue
			}
			node, err := dom.DescribeNode().
				WithBackendNodeID(cdp.BackendNodeID(nodes[index].BackendNodeID)).
				Do(actionCtx)
			if err != nil {
				return err
			}
			nodes[index].Sensitive = isSensitiveDOMNode(node)
		}
		return nil
	}))
}

func isSensitiveDOMNode(node *cdp.Node) bool {
	if node == nil {
		return false
	}
	for index := 0; index+1 < len(node.Attributes); index += 2 {
		name := strings.ToLower(node.Attributes[index])
		value := strings.ToLower(node.Attributes[index+1])
		if name == "type" && value == "password" {
			return true
		}
		if name == "autocomplete" && (value == "current-password" || value == "new-password") {
			return true
		}
	}
	return false
}

func (s *chromiumSession) Click(ctx context.Context, tabID string, backendNodeID int64) error {
	popup := s.getPopupEvent(tabID)

drainPopupEvents:
	for {
		select {
		case <-popup:
		default:
			break drainPopupEvents
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	clickCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- s.runOnNode(clickCtx, tabID, backendNodeID, func(nodeIDs []cdp.NodeID) chromedp.Action {
			return chromedp.Click(nodeIDs, chromedp.ByNodeID)
		})
	}()
	select {
	case err := <-result:
		return err
	case <-popup:
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("browser popup could not be quarantined")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chromiumSession) Type(ctx context.Context, tabID string, backendNodeID int64, text string, replace bool) error {
	return s.runOnNode(ctx, tabID, backendNodeID, func(nodeIDs []cdp.NodeID) chromedp.Action {
		actions := []chromedp.Action{chromedp.Focus(nodeIDs, chromedp.ByNodeID)}
		if replace {
			actions = append(actions, chromedp.ActionFunc(clearFocusedText))
		}
		actions = append(actions, chromedp.SendKeys(nodeIDs, text, chromedp.ByNodeID))
		return chromedp.Tasks(actions)
	})
}

func clearFocusedText(ctx context.Context) error {
	if err := input.DispatchKeyEvent(input.KeyRawDown).WithCommands([]string{"selectAll"}).Do(ctx); err != nil {
		return err
	}
	return input.DispatchKeyEvent(input.KeyRawDown).WithCommands([]string{"deleteBackward"}).Do(ctx)
}

func (s *chromiumSession) Press(ctx context.Context, tabID, key string) error {
	return s.runInTab(ctx, tabID, chromedp.KeyEvent(getKeyInput(key)))
}

func getKeyInput(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "enter":
		return kb.Enter
	case "tab":
		return kb.Tab
	case "escape", "esc":
		return kb.Escape
	case "backspace":
		return kb.Backspace
	case "delete":
		return kb.Delete
	case "arrowup":
		return kb.ArrowUp
	case "arrowdown":
		return kb.ArrowDown
	case "arrowleft":
		return kb.ArrowLeft
	case "arrowright":
		return kb.ArrowRight
	case "home":
		return kb.Home
	case "end":
		return kb.End
	case "pageup":
		return kb.PageUp
	case "pagedown":
		return kb.PageDown
	case "space":
		return " "
	default:
		return strings.TrimSpace(key)
	}
}

func (s *chromiumSession) Scroll(ctx context.Context, tabID string, x, y int64) error {
	return s.runInTab(ctx, tabID, chromedp.ActionFunc(func(actionCtx context.Context) error {
		_, visualViewport, _, _, cssVisualViewport, _, err := page.GetLayoutMetrics().Do(actionCtx)
		if err != nil {
			return err
		}
		if cssVisualViewport != nil {
			visualViewport = cssVisualViewport
		}
		if visualViewport == nil {
			return errors.New("browser viewport is unavailable")
		}
		return input.DispatchMouseEvent(
			input.MouseWheel, visualViewport.ClientWidth/2, visualViewport.ClientHeight/2,
		).WithDeltaX(float64(x)).WithDeltaY(float64(y)).Do(actionCtx)
	}))
}

func (s *chromiumSession) Select(ctx context.Context, tabID string, backendNodeID int64, value string) error {
	return s.runOnNode(ctx, tabID, backendNodeID, func(nodeIDs []cdp.NodeID) chromedp.Action {
		return chromedp.ActionFunc(func(actionCtx context.Context) error {
			object, err := dom.ResolveNode().WithNodeID(nodeIDs[0]).Do(actionCtx)
			if err != nil {
				return err
			}
			var selected bool
			err = chromedp.CallFunctionOn(`function(value) {
				if (this.tagName !== "SELECT") return false;
				const option = Array.from(this.options).find(item => item.value === value);
				if (!option) return false;
				this.value = value;
				this.dispatchEvent(new Event("input", {bubbles: true}));
				this.dispatchEvent(new Event("change", {bubbles: true}));
				return true;
			}`, &selected, func(params *cdpruntime.CallFunctionOnParams) *cdpruntime.CallFunctionOnParams {
				return params.WithObjectID(object.ObjectID)
			}, value).Do(actionCtx)
			if err != nil {
				return err
			}
			if !selected {
				return errors.New("browser select option was not found")
			}
			return nil
		})
	})
}

func (s *chromiumSession) Wait(
	ctx context.Context,
	tabID string,
	condition WaitCondition,
	value string,
	backendNodeID int64,
) error {
	switch condition {
	case WaitLoad:
		return s.runInTab(ctx, tabID, chromedp.WaitReady("body", chromedp.ByQuery))
	case WaitVisible:
		return s.runOnNode(ctx, tabID, backendNodeID, func(nodeIDs []cdp.NodeID) chromedp.Action {
			return chromedp.WaitVisible(nodeIDs, chromedp.ByNodeID)
		})
	case WaitText, WaitURL:
		return s.waitForValue(ctx, tabID, condition, value)
	default:
		return errors.New("browser wait condition is invalid")
	}
}

func (s *chromiumSession) runOnNode(
	ctx context.Context,
	tabID string,
	backendNodeID int64,
	getAction func([]cdp.NodeID) chromedp.Action,
) error {
	if backendNodeID <= 0 {
		return errors.New("browser backend node is invalid")
	}
	return s.runInTab(ctx, tabID, chromedp.ActionFunc(func(actionCtx context.Context) error {
		nodeIDs, err := dom.PushNodesByBackendIDsToFrontend([]cdp.BackendNodeID{cdp.BackendNodeID(backendNodeID)}).Do(actionCtx)
		if err != nil {
			return err
		}
		if len(nodeIDs) != 1 || nodeIDs[0] == cdp.EmptyNodeID {
			return errors.New("browser element is no longer available")
		}
		return getAction(nodeIDs).Do(actionCtx)
	}))
}

func (s *chromiumSession) waitForValue(ctx context.Context, tabID string, condition WaitCondition, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("browser wait value is required")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var current string
		var action chromedp.Action
		if condition == WaitURL {
			action = chromedp.Location(&current)
		} else {
			action = chromedp.Text("body", &current, chromedp.ByQuery)
		}
		if err := s.runInTab(ctx, tabID, action); err != nil {
			return err
		}
		if strings.Contains(current, value) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *chromiumSession) runInTab(ctx context.Context, tabID string, actions ...chromedp.Action) error {
	if s == nil || s.ctx == nil {
		return errors.New("browser session is unavailable")
	}
	tabCtx, initialize := s.getTabContext(tabID)
	if initialize {
		if err := chromedp.Run(tabCtx); err != nil {
			return err
		}
	}
	if initialize {
		setup := []chromedp.Action{
			network.Enable(),
			page.Enable(),
			fetch.Enable().WithHandleAuthRequests(s.proxyUser != ""),
			page.SetInterceptFileChooserDialog(true).WithCancel(true),
			cdpruntime.Enable(),
		}
		if !s.attached {
			setup = append(
				setup,
				target.SetAutoAttach(true, true).WithFlatten(true).WithFilter(getRelatedTargetFilter()),
			)
		}
		actions = append(setup, actions...)
	}
	actionCtx, done := newBoundedActionContext(tabCtx, ctx)
	defer done()
	runErr := chromedp.Run(actionCtx, actions...)
	if runErr != nil {
		if cause := context.Cause(actionCtx); cause != nil &&
			(errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) {
			runErr = cause
		}
	}
	if networkErr := s.consumeNetworkError(tabID); networkErr != nil {
		return networkErr
	}
	return runErr
}

func (s *chromiumSession) getTabContext(tabID string) (context.Context, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tabCtx := s.tabContexts[tabID]; tabCtx != nil {
		return tabCtx, false
	}
	tabCtx, cancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(target.ID(tabID)))
	chromedp.ListenTarget(tabCtx, s.getRequestListener(tabCtx, tabID))
	if !s.attached {
		chromedp.ListenTarget(tabCtx, s.getRelatedTargetListener(tabID))
	}
	chromedp.ListenTarget(tabCtx, s.getPageEffectListener(tabCtx, tabID))
	chromedp.ListenTarget(tabCtx, s.getConsoleListener(tabID))
	s.tabContexts[tabID] = tabCtx
	s.tabCancels[tabID] = cancel
	return tabCtx, true
}

func (s *chromiumSession) getPageEffectListener(ctx context.Context, tabID string) func(any) {
	return func(event any) {
		if _, ok := event.(*page.EventWindowOpen); ok {
			select {
			case s.getPopupEvent(tabID) <- struct{}{}:
			default:
			}
			return
		}
		opening, ok := event.(*page.EventJavascriptDialogOpening)
		if !ok {
			return
		}
		s.mu.Lock()
		response, armed := s.dialogResponses[tabID]
		if armed {
			delete(s.dialogResponses, tabID)
		}
		s.mu.Unlock()
		s.recordConsoleMessage(tabID, getDialogConsoleMessage(opening, armed, response.accept))
		go func() {
			if !armed {
				_ = chromedp.Run(ctx, page.HandleJavaScriptDialog(false))
				return
			}
			err := chromedp.Run(ctx, page.HandleJavaScriptDialog(response.accept).WithPromptText(response.promptText))
			select {
			case response.result <- err:
			default:
			}
		}()
	}
}

func (s *chromiumSession) getPopupEvent(tabID string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.popupEvents == nil {
		s.popupEvents = make(map[string]chan struct{})
	}
	if s.popupEvents[tabID] == nil {
		s.popupEvents[tabID] = make(chan struct{}, 1)
	}
	return s.popupEvents[tabID]
}

func getDialogConsoleMessage(event *page.EventJavascriptDialogOpening, armed, accepted bool) ConsoleMessage {
	resolution := "automatically dismissed"
	if armed && accepted {
		resolution = "accepted"
	} else if armed {
		resolution = "dismissed"
	}
	message := "Browser " + resolution + " " + string(event.Type) + " dialog: " + event.Message
	if event.DefaultPrompt != "" {
		message += " (default: " + event.DefaultPrompt + ")"
	}
	return ConsoleMessage{
		Level:     ConsoleWarn,
		Text:      sanitizeConsoleText(message),
		Timestamp: time.Now().UTC(),
	}
}

func (s *chromiumSession) getBackendTab(ctx context.Context, tabID string) (BackendTab, error) {
	actionCtx, done := s.newActionContext(ctx)
	defer done()
	browserCtx, err := s.getBrowserExecutorContext(actionCtx)
	if err != nil {
		return BackendTab{}, err
	}
	info, err := target.GetTargetInfo().WithTargetID(target.ID(tabID)).Do(browserCtx)
	if err != nil {
		return BackendTab{}, err
	}
	if !s.isTargetAllowed(info) {
		return BackendTab{}, errors.New("browser target is outside the configured attachment scope")
	}
	s.mu.Lock()
	active := s.activeTabID
	s.mu.Unlock()
	return backendTabFromTarget(info, active), nil
}

func (s *chromiumSession) newActionContext(ctx context.Context) (context.Context, func()) {
	if s == nil || s.ctx == nil {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		return cancelled, func() {}
	}
	return newBoundedActionContext(s.ctx, ctx)
}

func newBoundedActionContext(parent, caller context.Context) (context.Context, func()) {
	actionCtx, cancel := context.WithCancelCause(parent)
	var stopCaller func() bool
	if caller != nil {
		stopCaller = context.AfterFunc(caller, func() { cancel(context.Cause(caller)) })
	}
	var timeout *time.Timer
	if actionBudgetFromContext(caller) == nil {
		if caller == nil {
			timeout = time.AfterFunc(defaultActionTimeout, func() { cancel(errBrowserActionTimedOut) })
		} else if _, hasDeadline := caller.Deadline(); !hasDeadline {
			timeout = time.AfterFunc(defaultActionTimeout, func() { cancel(errBrowserActionTimedOut) })
		}
	}
	return actionCtx, func() {
		if stopCaller != nil {
			stopCaller()
		}
		if timeout != nil {
			timeout.Stop()
		}
		cancel(context.Canceled)
	}
}

func (s *chromiumSession) setActiveTab(tabID string) {
	s.mu.Lock()
	s.activeTabID = tabID
	s.mu.Unlock()
}

func (s *chromiumSession) getBrowserExecutorContext(ctx context.Context) (context.Context, error) {
	chromiumCtx := chromedp.FromContext(s.ctx)
	if chromiumCtx == nil || chromiumCtx.Browser == nil {
		return nil, errors.New("browser connection is unavailable")
	}
	return cdp.WithExecutor(ctx, chromiumCtx.Browser), nil
}

func backendTabFromTarget(info *target.Info, activeTabID string) BackendTab {
	if info == nil {
		return BackendTab{}
	}
	return BackendTab{
		ID: string(info.TargetID), BrowserContextID: string(info.BrowserContextID),
		Title: info.Title, URL: info.URL, Active: string(info.TargetID) == activeTabID,
	}
}

func (s *chromiumSession) isTargetAllowed(info *target.Info) bool {
	if info == nil {
		return false
	}
	s.mu.Lock()
	_, quarantined := s.quarantinedTargets[string(info.TargetID)]
	s.mu.Unlock()
	if quarantined {
		return false
	}
	switch s.attachmentScope {
	case "", config.BrowserAttachmentBrowser:
		return true
	case config.BrowserAttachmentContext:
		return string(info.BrowserContextID) == s.browserContextID
	case config.BrowserAttachmentTargets:
		_, ok := s.attachmentTargets[string(info.TargetID)]
		return ok
	default:
		return false
	}
}

type rawAccessibilityTree struct {
	Nodes []rawAccessibilityNode `json:"nodes"`
}

type rawAccessibilityNode struct {
	Ignored          bool                       `json:"ignored"`
	BackendDOMNodeID int64                      `json:"backendDOMNodeId"`
	Role             *rawAccessibilityValue     `json:"role"`
	Name             *rawAccessibilityValue     `json:"name"`
	Value            *rawAccessibilityValue     `json:"value"`
	Description      *rawAccessibilityValue     `json:"description"`
	Properties       []rawAccessibilityProperty `json:"properties"`
}

type rawAccessibilityProperty struct {
	Name  string                 `json:"name"`
	Value *rawAccessibilityValue `json:"value"`
}

type rawAccessibilityValue struct {
	Value json.RawMessage `json:"value"`
}

func getAXValue(value *rawAccessibilityValue) string {
	if value == nil || len(value.Value) == 0 {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(value.Value, &decoded); err != nil {
		return ""
	}
	switch typed := decoded.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		raw, _ := json.Marshal(typed)
		return string(raw)
	}
}
