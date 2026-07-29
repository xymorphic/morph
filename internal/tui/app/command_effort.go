package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

type reasoningEffortSetMsg struct {
	SessionID string
	Settings  agentsession.ReasoningSettings
	Err       error
}

func (m *model) handleEffortCommand(argument string) tea.Cmd {
	requested := strings.TrimSpace(argument)
	if requested == "" {
		return m.startEffortCommand()
	}
	if m.effortSavingSessionID == m.getCurrentSessionID() {
		return m.setStatus("reasoning effort is already saving")
	}
	reset := strings.EqualFold(requested, "default") || strings.EqualFold(requested, "reset")
	if reset {
		return m.setEffort(
			m.getCurrentSessionID(),
			m.reasoning.Model,
			"",
			true,
		)
	}
	if !m.reasoning.Reasoning {
		return m.setStatus("reasoning effort is not applicable")
	}
	if !m.reasoning.Adjustable {
		return m.setStatus("reasoning effort control is unavailable")
	}

	effort, ok := getCanonicalReasoningEffort(requested, m.reasoning.SupportedEfforts)
	if !ok {
		return m.setStatus("unsupported reasoning effort: " + requested)
	}
	return m.setEffort(
		m.getCurrentSessionID(),
		m.reasoning.Model,
		effort,
		false,
	)
}

func (m *model) startEffortCommand() tea.Cmd {
	efforts := append([]agentsession.ReasoningEffort(nil), m.reasoning.SupportedEfforts...)
	m.showCommandView(commandViewPayload{
		TitleLeft:        "Reasoning effort",
		TitleSubtext:     getEffortCommandSummary(m.reasoning),
		TitleRight:       "enter or click to select · esc to close",
		TitleRightColor:  defaultTUITheme.MutedText,
		Kind:             commandViewKindEffort,
		EffortSessionID:  m.getCurrentSessionID(),
		EffortModel:      m.reasoning.Model,
		Efforts:          efforts,
		EffortReasoning:  m.reasoning.Reasoning,
		EffortAdjustable: m.reasoning.Adjustable,
	})
	m.commandViewItemSelected = getCurrentEffortOptionIndex(m.reasoning, efforts)
	m.commandViewOffset = 0
	return nil
}

func (m model) isEffortCommandView() bool {
	return m.commandView.Visible && m.commandView.Kind == commandViewKindEffort
}

func (m model) renderEffortCommandViewContent(content commandViewContent) string {
	if !m.commandView.EffortReasoning {
		return getUnavailableEffortCommandDetail(
			"Reasoning effort is not applicable to this model.",
			m.reasoning,
		)
	}
	if !m.commandView.EffortAdjustable {
		return getUnavailableEffortCommandDetail(
			"Reasoning effort control is unavailable for this model.",
			m.reasoning,
		)
	}
	count := len(m.commandView.Efforts) + 1
	offset := min(max(content.Offset, 0), max(count-1, 0))
	height := max(content.Height, 1)
	end := min(offset+height, count)
	rows := make([]string, 0, height)
	for index := offset; index < end; index++ {
		name := "default"
		detail := getDefaultEffortOptionDetail(m.reasoning)
		if index > 0 {
			effort := m.commandView.Efforts[index-1]
			name = string(effort)
			detail = getEffortOptionDetail(m.reasoning, effort)
		}
		rows = append(rows, renderCommandListEntryRow(
			name,
			detail,
			content.Width,
			max(content.Width-2, 1),
			index == m.commandViewItemSelected,
		))
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}

func (m *model) updateEffortCommandView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.commandView.EffortReasoning || !m.commandView.EffortAdjustable {
		return *m, nil
	}
	count := len(m.commandView.Efforts) + 1
	selection := m.commandViewItemSelected
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Key().Code {
		case tea.KeyUp:
			selection--
		case tea.KeyDown:
			selection++
		case tea.KeyHome:
			selection = 0
		case tea.KeyEnd:
			selection = count - 1
		case tea.KeyEnter:
			return m.selectEffortOption()
		default:
			return *m, nil
		}
	case tea.MouseWheelMsg:
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			selection--
		case tea.MouseWheelDown:
			selection++
		default:
			return *m, nil
		}
	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button != tea.MouseLeft || !m.isMouseInCommandViewContent(mouse) {
			return *m, nil
		}
		selection = m.commandViewOffset + mouse.Y - m.getCommandViewContentTop()
		if selection < 0 || selection >= count {
			return *m, nil
		}
		m.commandViewItemSelected = selection
		return m.selectEffortOption()
	default:
		return *m, nil
	}

	m.commandViewItemSelected = min(max(selection, 0), count-1)
	m.commandViewOffset = getChatsCommandViewOffsetForSelection(
		m.commandViewItemSelected,
		m.commandViewOffset,
		m.getCommandViewContentHeight(),
		count,
	)
	m.clearCommandViewSelection()
	return *m, nil
}

func (m *model) selectEffortOption() (tea.Model, tea.Cmd) {
	if m.effortSavingSessionID == m.commandView.EffortSessionID {
		return *m, m.setStatus("reasoning effort is already saving")
	}
	index := min(max(m.commandViewItemSelected, 0), len(m.commandView.Efforts))
	reset := index == 0
	effort := agentsession.ReasoningEffort("")
	if !reset {
		effort = m.commandView.Efforts[index-1]
	}
	cmd := m.setEffort(
		m.commandView.EffortSessionID,
		m.commandView.EffortModel,
		effort,
		reset,
	)
	return *m, cmd
}

func (m *model) setEffort(
	sessionID string,
	tuple agentsession.ReasoningModelTuple,
	effort agentsession.ReasoningEffort,
	reset bool,
) tea.Cmd {
	if m.sessionClient == nil {
		return m.setStatus("reasoning effort control is unavailable")
	}
	m.effortSavingSessionID = sessionID
	return tea.Batch(
		m.setStatus("saving reasoning effort"),
		setReasoningEffortCmd(m.chatCtx, m.sessionClient, sessionID, tuple, effort, reset),
	)
}

func setReasoningEffortCmd(
	ctx context.Context,
	client rpcclient.SessionAPI,
	sessionID string,
	tuple agentsession.ReasoningModelTuple,
	effort agentsession.ReasoningEffort,
	reset bool,
) tea.Cmd {
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		settings, err := client.SetReasoningEffort(ctx, rpcclient.SetReasoningEffortOptions{
			SessionID:        sessionID,
			ExpectedProvider: tuple.Provider,
			ExpectedAPI:      tuple.API,
			ExpectedModel:    tuple.Model,
			Effort:           string(effort),
			Reset:            reset,
		})
		return reasoningEffortSetMsg{
			SessionID: sessionID,
			Settings:  settings,
			Err:       err,
		}
	}
}

func (m *model) completeReasoningEffortSet(msg reasoningEffortSetMsg) tea.Cmd {
	if msg.SessionID != m.effortSavingSessionID {
		return nil
	}
	m.effortSavingSessionID = ""
	if msg.SessionID != m.getCurrentSessionID() {
		return nil
	}
	if msg.Err != nil {
		if isStaleReasoningEffortError(msg.Err) {
			return tea.Batch(
				m.setStatus("model changed; refreshing reasoning effort"),
				loadSessionRuntimeStateCmd(
					m.chatCtx,
					m.chatClient,
					m.modelClient,
					m.getCurrentSessionID(),
				),
			)
		}
		return m.setStatus("reasoning effort unchanged: " + getUserFacingErrorMessage(msg.Err.Error()))
	}
	if m.modelRestartPending ||
		!reasoningModelTuplesEqual(m.reasoning.Model, msg.Settings.Model) ||
		!m.reasoningMatchesCurrentModel() {
		m.applyAction(setSessionReasoningAction{})
		return tea.Batch(
			m.setStatus("model changed; refreshing reasoning effort"),
			loadSessionRuntimeStateCmd(
				m.chatCtx,
				m.chatClient,
				m.modelClient,
				m.getCurrentSessionID(),
			),
		)
	}

	m.applyAction(setSessionReasoningAction{Settings: msg.Settings})
	if m.isEffortCommandView() {
		m.commandView.EffortModel = msg.Settings.Model
		m.commandView.Efforts = append(
			[]agentsession.ReasoningEffort(nil),
			msg.Settings.SupportedEfforts...,
		)
		m.commandView.EffortReasoning = msg.Settings.Reasoning
		m.commandView.EffortAdjustable = msg.Settings.Adjustable
		m.commandViewItemSelected = getCurrentEffortOptionIndex(
			msg.Settings,
			m.commandView.Efforts,
		)
		m.commandView.TitleSubtext = getEffortCommandSummary(msg.Settings)
	}
	m.setTranscriptContentForActiveTurn()
	return m.setStatus(getEffortChangeStatus(msg.Settings))
}

func getCanonicalReasoningEffort(
	value string,
	efforts []agentsession.ReasoningEffort,
) (agentsession.ReasoningEffort, bool) {
	for _, effort := range efforts {
		if strings.EqualFold(strings.TrimSpace(value), string(effort)) {
			return effort, true
		}
	}
	return "", false
}

func getCurrentEffortOptionIndex(
	settings agentsession.ReasoningSettings,
	efforts []agentsession.ReasoningEffort,
) int {
	if settings.SessionOverride == "" {
		return 0
	}
	for index, effort := range efforts {
		if effort == settings.SessionOverride {
			return index + 1
		}
	}
	return 0
}

func getEffortOptionDetail(
	settings agentsession.ReasoningSettings,
	effort agentsession.ReasoningEffort,
) string {
	parts := make([]string, 0, 3)
	if effort == settings.SessionOverride {
		parts = append(parts, "override")
	}
	if effort == settings.EffectiveEffort {
		parts = append(parts, "next turn")
	}
	if settings.ActiveRunSnapshot != nil && effort == settings.ActiveRunSnapshot.Effort {
		parts = append(parts, "current turn")
	}
	if len(parts) == 0 {
		return "supported"
	}
	return strings.Join(parts, " · ")
}

func getDefaultEffortOptionDetail(settings agentsession.ReasoningSettings) string {
	parts := []string{"inherit"}
	if settings.SessionOverride == "" {
		parts = append(parts, "selected")
	}
	if settings.Source != "" {
		parts = append(parts, "source "+string(settings.Source))
	}
	if settings.Fallback != "" {
		parts = append(parts, "fallback "+string(settings.Fallback))
	}
	if settings.DormantEffort != "" {
		label := "dormant profile default "
		if settings.SessionOverride != "" {
			label = "dormant override "
		}
		parts = append(parts, label+string(settings.DormantEffort))
	}
	return strings.Join(parts, " · ")
}

func getEffortCommandSummary(settings agentsession.ReasoningSettings) string {
	next := string(settings.EffectiveEffort)
	if next == "" {
		next = "none"
	}
	if settings.ActiveRunSnapshot != nil {
		current := string(settings.ActiveRunSnapshot.Effort)
		if current == "" {
			current = "none"
		}
		return fmt.Sprintf("current turn %s · next turn %s", current, next)
	}
	return "effective " + next
}

func getUnavailableEffortCommandDetail(
	detail string,
	settings agentsession.ReasoningSettings,
) string {
	if settings.SessionOverride != "" {
		detail += "\nStored override: " + string(settings.SessionOverride) +
			" (dormant; clear with /effort reset)."
	} else if settings.DormantEffort != "" {
		detail += "\nDormant profile default: " + string(settings.DormantEffort) + "."
	}
	if settings.Fallback != "" {
		detail += "\nFallback: " + string(settings.Fallback)
	}
	return detail
}

func getEffortChangeStatus(settings agentsession.ReasoningSettings) string {
	next := string(settings.EffectiveEffort)
	if next == "" {
		next = "default"
	}
	if settings.ActiveRunSnapshot != nil &&
		settings.ActiveRunSnapshot.Effort != settings.EffectiveEffort {
		current := string(settings.ActiveRunSnapshot.Effort)
		if current == "" {
			current = "none"
		}
		return fmt.Sprintf(
			"current turn remains %s; %s applies next turn",
			current,
			next,
		)
	}
	return "reasoning effort: " + next
}

func isStaleReasoningEffortError(err error) bool {
	if errors.Is(err, agentsession.ErrReasoningStaleTuple) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "stale")
}

func reasoningModelTuplesEqual(
	left agentsession.ReasoningModelTuple,
	right agentsession.ReasoningModelTuple,
) bool {
	return strings.EqualFold(strings.TrimSpace(left.Provider), strings.TrimSpace(right.Provider)) &&
		strings.EqualFold(strings.TrimSpace(left.API), strings.TrimSpace(right.API)) &&
		strings.EqualFold(strings.TrimSpace(left.Model), strings.TrimSpace(right.Model))
}
