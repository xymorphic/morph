package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/trace"
)

func TestModel_PermissionApprovalPromptResolvesFromKeyboard(t *testing.T) {
	client := &permissionAPIStub{}
	runModel := newModel()
	runModel.permissionClient = client
	message := permissionApprovalMsg{
		RequestID: "approval_1", Status: string(permissions.ApprovalPending),
		Summary: "run_command · execute process", Reason: "command policy requires approval",
		Effects: []string{"execution"}, ExpiresAt: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
	}

	updated, _ := runModel.handleAppEvent(applyTUIMessageEvent{Message: message})
	require.Equal(t, "approval_1", updated.pendingApprovalID)
	require.Len(t, updated.messages, 1)
	require.True(t, updated.isPermissionApprovalCommandView())
	view := stripANSI(updated.renderCommandView())
	require.Contains(t, view, "Permission approval")
	require.Contains(t, view, "run_command · execute process")
	require.Contains(t, view, "Allow once")
	require.Contains(t, view, "approve this request only")
	require.Contains(t, view, "Allow for session")
	require.Contains(t, view, "remember this approval for this session")
	require.Contains(t, view, "Deny")
	require.Contains(t, view, "deny this request only")
	require.NotContains(t, updated.messages[0].PlainText(), "[y] allow once")
	require.NotContains(t, updated.messages[0].PlainText(), "printf secret")

	modelValue, cmd, handled := updated.handleKeyPressMsg(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.True(t, handled)
	require.NotNil(t, cmd)
	next := modelValue.(model)
	result := cmd().(permissionResolutionCompletedMsg)
	require.NoError(t, result.Err)
	require.Equal(t, permissions.GrantOnce, client.scope)

	modelValue, _, handled = next.handleAsyncMsg(result)
	require.True(t, handled)
	next = modelValue.(model)
	require.Empty(t, next.pendingApprovalID)
	require.Contains(t, next.messages[0].PlainText(), "approved")
}

func TestModel_PermissionApprovalCommandViewNavigatesAndConfirms(t *testing.T) {
	client := &permissionAPIStub{}
	runModel := newModel()
	runModel.permissionClient = client
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval", Status: string(permissions.ApprovalPending), Summary: "read network",
		Effects: []string{string(permissions.EffectNetwork)},
	})

	updated, _, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, handled)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.NotNil(t, cmd)
	result := cmd().(permissionResolutionCompletedMsg)
	require.NoError(t, result.Err)
	require.True(t, client.approved)
	require.Equal(t, permissions.GrantSession, client.scope)
	require.True(t, updated.(model).isPermissionApprovalCommandView())
}

func TestModel_PermissionApprovalCommandViewResolvesMouseSelection(t *testing.T) {
	client := &permissionAPIStub{}
	runModel := newModel()
	runModel.permissionClient = client
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval", Status: string(permissions.ApprovalPending), Summary: "read network",
		Effects: []string{string(permissions.EffectNetwork)},
	})

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		X:      runModel.getCommandViewContentLeft(),
		Y:      runModel.getCommandViewContentTop() + 2,
		Button: tea.MouseLeft,
	}))
	require.NotNil(t, cmd)
	result := cmd().(permissionResolutionCompletedMsg)
	require.NoError(t, result.Err)
	require.False(t, client.approved)
	require.Empty(t, client.scope)
	require.Equal(t, 2, updated.(model).commandViewItemSelected)
}

func TestModel_PermissionApprovalCommandViewCannotBeDismissed(t *testing.T) {
	runModel := newModel()
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval", Status: string(permissions.ApprovalPending), Summary: "operation",
	})

	updated, cmd, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.True(t, handled)
	require.NotNil(t, cmd)
	require.True(t, updated.(model).isPermissionApprovalCommandView())
	require.Contains(t, updated.(model).status.Text(), "press n to deny")
}

func TestPermissionApprovalText_DisplaysExpiryTimeToGo(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	currentTime = func() time.Time { return now }
	t.Cleanup(func() { currentTime = originalCurrentTime })

	text := permissionApprovalText(permissionApprovalMsg{
		Status:    string(permissions.ApprovalPending),
		Summary:   "web_extract · read network",
		ExpiresAt: now.Add(2*time.Minute + time.Second),
	})

	require.Contains(t, text, "Expires: 3m")
}

func TestPermissionApprovalText_DisplaysPersonalBrowserWarning(t *testing.T) {
	text := permissionApprovalText(permissionApprovalMsg{
		Status:  string(permissions.ApprovalPending),
		Summary: "Attach to signed-in browser profile personal",
		Reason: "Attached browsers do not use Morph's managed egress proxy. " +
			"Personal browser attachment exposes signed-in sessions, cookies, and page data.",
		Effects: []string{
			string(permissions.EffectNetwork),
			string(permissions.EffectCredentialBearing),
			string(permissions.EffectExternalSystem),
		},
	})

	require.Contains(t, text, "Reason: Attached browsers do not use Morph's managed egress proxy.")
	require.Contains(t, text, "Personal browser attachment exposes signed-in sessions, cookies, and page data.")
	require.NotContains(t, text, "[a] always")
}

func TestPermissionApprovalText_SeparatesBatchOperations(t *testing.T) {
	text := permissionApprovalText(permissionApprovalMsg{
		Status:  string(permissions.ApprovalPending),
		Summary: "browser · approve 2 operations",
		Reason: "This browser action may load content from or send data to a website. Approve all 2 operations: " +
			"browser · update browser; browser · read network GET http://localhost:8089/",
	})

	require.Equal(t, strings.Join([]string{
		"Permission approval required",
		"Operation: browser · approve 2 operations",
		"Reason: This browser action may load content from or send data to a website.",
		"Operations:",
		"1. browser · update browser",
		"2. browser · read network GET http://localhost:8089/",
	}, "\n"), text)
}

func TestTranscriptRenderer_FormatsPermissionApprovalAsScannableBlock(t *testing.T) {
	cell := tuiMessageToTranscriptCell(permissionApprovalMsg{
		Status:  string(permissions.ApprovalPending),
		Summary: "browser · approve 2 operations",
		Effects: []string{"external_system", "network", "read", "write"},
		Reason: "This browser action may load content from or send data to a website. Approve all 2 operations: " +
			"browser · update browser; browser · read network GET http://localhost:8089/",
		ExpiresAt: time.Date(2026, 7, 20, 21, 5, 42, 0, time.UTC),
	})

	_, ok := cell.(permissionApprovalTranscriptCell)
	require.True(t, ok)
	rendered := stripANSI(defaultTranscriptRenderer.RenderCell(cell, transcriptRenderContext{
		Width: 120,
		Now:   time.Date(2026, 7, 20, 21, 2, 42, 0, time.UTC),
	}))
	require.Contains(t, rendered, permissionStatusIcon+" Permission approval required\n  Operation")
	require.Contains(t, rendered, "\n  Operation  browser · approve 2 operations\n")
	require.Contains(t, rendered, "\n  Effects    external_system, network, read, write\n")
	require.Contains(t, rendered, "\n  Reason     This browser action may load content from or send data to a website.\n")
	require.Contains(t, rendered, "\n  Operations\n    1. browser · update browser\n")
	require.Contains(t, rendered, "\n    2. browser · read network GET http://localhost:8089/\n")
	require.Contains(t, rendered, "\n  Expires    3m")
	require.NotContains(t, rendered, "required browser · approve")

	narrow := stripANSI(defaultTranscriptRenderer.RenderCell(cell, transcriptRenderContext{
		Width: 40,
		Now:   time.Date(2026, 7, 20, 21, 2, 42, 0, time.UTC),
	}))
	require.Contains(t, narrow, "  Reason     This browser action may\n             load content from or send\n             data to a website.\n")
}

func TestTranscriptRenderer_EmphasizesDeniedPermission(t *testing.T) {
	cell := tuiMessageToTranscriptCell(permissionApprovalMsg{
		Status:  string(permissions.ApprovalDenied),
		Summary: "browser · start browser",
	})

	rendered := defaultTranscriptRenderer.RenderCell(cell, transcriptRenderContext{Width: 80})
	deniedIcon := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(defaultTUITheme.ToolDeletion)).
		Render(permissionStatusIcon)
	deniedTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render("Permission denied")

	require.Contains(t, rendered, deniedIcon+" "+deniedTitle)
	require.Contains(t, stripANSI(rendered), permissionStatusIcon+" Permission denied")
	require.Contains(t, stripANSI(rendered), "Operation  browser · start browser")
}

func TestTranscriptRenderer_EmphasizesApprovedPermission(t *testing.T) {
	cell := tuiMessageToTranscriptCell(permissionApprovalMsg{
		Status:  string(permissions.ApprovalApproved),
		Scope:   string(permissions.GrantOnce),
		Summary: "browser · start browser",
	})

	rendered := defaultTranscriptRenderer.RenderCell(cell, transcriptRenderContext{Width: 80})
	approvedIcon := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(defaultTUITheme.ToolCompletedDot)).
		Render(permissionStatusIcon)
	approvedTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render("Permission approved (once)")

	require.Contains(t, rendered, approvedIcon+" "+approvedTitle)
	require.Contains(t, stripANSI(rendered), permissionStatusIcon+" Permission approved (once)")
	require.Contains(t, stripANSI(rendered), "Operation  browser · start browser")
}

func TestModel_PermissionApprovalSupportsSessionAlwaysDenyAndFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		key      string
		approved bool
		scope    permissions.GrantScope
	}{
		{name: "session", key: "s", approved: true, scope: permissions.GrantSession},
		{name: "always", key: "a", approved: true, scope: permissions.GrantAlways},
		{name: "deny", key: "n", approved: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &permissionAPIStub{}
			runModel := newModel()
			runModel.permissionClient = client
			runModel.applyTUIMessage(permissionApprovalMsg{RequestID: "approval", Status: "pending", Summary: "operation"})
			_, cmd, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: rune(test.key[0]), Text: test.key})
			require.True(t, handled)
			result := cmd().(permissionResolutionCompletedMsg)
			require.NoError(t, result.Err)
			require.Equal(t, test.approved, client.approved)
			require.Equal(t, test.scope, client.scope)
		})
	}

	runModel := newModel()
	runModel.permissionClient = &permissionAPIStub{err: errors.New("store unavailable")}
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval", Status: string(permissions.ApprovalPending), Summary: "run command",
	})
	_, cmd, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.True(t, handled)
	result := cmd().(permissionResolutionCompletedMsg)
	modelValue, _, handled := runModel.handleAsyncMsg(result)
	require.True(t, handled)
	failedModel := modelValue.(model)
	require.Contains(t, failedModel.status.Text(), "approval failed")
	require.Empty(t, failedModel.pendingApprovalID)
	require.Contains(t, failedModel.messages[0].PlainText(), "Permission failed")
	require.Contains(t, failedModel.messages[0].PlainText(), "run command")
}

func TestModel_PermissionApprovalHidesAndRejectsUnsafeAlwaysChoice(t *testing.T) {
	runModel := newModel()
	runModel.permissionClient = &permissionAPIStub{}
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval", Status: "pending", Summary: "credential update",
		Effects: []string{string(permissions.EffectCredentialBearing)},
	})
	require.NotContains(t, stripANSI(runModel.renderCommandView()), "Always allow")
	require.NotContains(t, runModel.commandView.TitleRight, "a/")
	updated, cmd, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.True(t, handled)
	require.NotNil(t, cmd)
	require.Contains(t, updated.(model).status.Text(), "always approval is unavailable")
}

func TestModel_PermissionApprovalQueuesIndependentRequests(t *testing.T) {
	runModel := newModel()
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval_1", Status: string(permissions.ApprovalPending), Summary: "first",
	})
	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval_2", Status: string(permissions.ApprovalPending), Summary: "second",
	})
	require.Equal(t, "approval_1", runModel.pendingApprovalID)
	require.Equal(t, "first", runModel.commandView.TitleSubtext)
	require.Len(t, runModel.messages, 2)

	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval_1", Status: string(permissions.ApprovalApproved), Summary: "first",
	})
	require.Equal(t, "approval_2", runModel.pendingApprovalID)
	require.Equal(t, "second", runModel.commandView.TitleSubtext)

	runModel.applyTUIMessage(permissionApprovalMsg{
		RequestID: "approval_2", Status: string(permissions.ApprovalDenied), Summary: "second",
	})
	require.Empty(t, runModel.pendingApprovalID)
	require.False(t, runModel.isCommandViewVisible())
}

func TestTraceEventToTUIMessage_DecodesPermissionApprovalLifecycle(t *testing.T) {
	message, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: map[string]any{
			"request_id": "approval_1", "status": "pending", "operation_summary": "run_command · execute process",
			"effects": []string{"execution"}, "operations": []string{"run_command · execute process git"},
		},
	})
	require.True(t, ok)
	approval := message.(permissionApprovalMsg)
	require.Equal(t, "approval_1", approval.RequestID)
	require.Equal(t, []string{"run_command · execute process git"}, approval.Operations)
}

type permissionAPIStub struct {
	approved bool
	scope    permissions.GrantScope
	err      error
}

func (s *permissionAPIStub) ResolveApprovalRequest(
	_ context.Context,
	id string,
	approved bool,
	scope permissions.GrantScope,
) (permissions.ApprovalRequest, error) {
	s.approved = approved
	s.scope = scope
	if s.err != nil {
		return permissions.ApprovalRequest{}, s.err
	}
	status := permissions.ApprovalDenied
	if approved {
		status = permissions.ApprovalApproved
	}
	return permissions.ApprovalRequest{ID: id, Status: status, Scope: scope}, nil
}

func (*permissionAPIStub) ListApprovalRequests(context.Context, permissions.ApprovalQuery) ([]permissions.ApprovalRequest, error) {
	return nil, nil
}
func (*permissionAPIStub) GetApprovalRequest(context.Context, string) (permissions.ApprovalRequest, bool, error) {
	return permissions.ApprovalRequest{}, false, nil
}
func (*permissionAPIStub) ListApprovalGrants(context.Context, permissions.GrantQuery) ([]permissions.ApprovalGrant, error) {
	return nil, nil
}
func (*permissionAPIStub) RevokeApprovalGrant(context.Context, string) (permissions.ApprovalGrant, error) {
	return permissions.ApprovalGrant{}, nil
}
func (*permissionAPIStub) DeleteApprovalRecord(context.Context, string) (permissions.ApprovalDeleteResult, error) {
	return permissions.ApprovalDeleteResult{}, nil
}
func (*permissionAPIStub) PruneApprovals(context.Context, bool) (permissions.ApprovalPruneResult, error) {
	return permissions.ApprovalPruneResult{}, nil
}

var _ rpcclient.PermissionAPI = (*permissionAPIStub)(nil)
