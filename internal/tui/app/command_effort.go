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
		TitleRight:       "enter or click to select · esc to close",
		TitleRightColor:  defaultTUITheme.MutedText,
		Kind:             commandViewKindEffort,
		EffortSessionID:  m.getCurrentSessionID(),
		EffortModel:      m.reasoning.Model,
		Efforts:          efforts,
		EffortReasoning:  m.reasoning.Reasoning,
		EffortAdjustable: m.reasoning.Adjustable,
	})
	m.commandViewItemSelected = getEffectiveEffortOptionIndex(m.reasoning, efforts)
	m.commandViewOffset = getChatsCommandViewOffsetForSelection(
		m.commandViewItemSelected,
		0,
		m.getCommandViewContentHeight(),
		len(efforts),
	)
	return nil
}

func (m model) isEffortCommandView() bool {
	return m.commandView.Visible && m.commandView.Kind == commandViewKindEffort
}

func (m model) renderEffortCommandViewContent(content commandViewContent) string {
	if detail := getEffortCommandUnavailableDetail(m.reasoning); detail != "" {
		return detail
	}
	count := len(m.commandView.Efforts)
	if count == 0 {
		return "No reasoning effort options available."
	}
	offset := min(max(content.Offset, 0), max(count-1, 0))
	height := max(content.Height, 1)
	end := min(offset+height, count)
	rows := make([]string, 0, height)
	for index := offset; index < end; index++ {
		rows = append(rows, renderCommandListEntryRow(
			string(m.commandView.Efforts[index]),
			"",
			content.Width,
			max(content.Width-2, 1),
			index == m.commandViewItemSelected,
		))
	}
	return strings.Join(rows, "\n")
}

func (m *model) updateEffortCommandView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.commandView.EffortReasoning || !m.commandView.EffortAdjustable {
		return *m, nil
	}
	count := len(m.commandView.Efforts)
	if count == 0 {
		return *m, nil
	}
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
	if len(m.commandView.Efforts) == 0 {
		return *m, nil
	}
	index := min(max(m.commandViewItemSelected, 0), len(m.commandView.Efforts)-1)
	effort := m.commandView.Efforts[index]
	if m.commandView.EffortSessionID == m.getCurrentSessionID() &&
		reasoningModelTuplesEqual(m.commandView.EffortModel, m.reasoning.Model) &&
		effort == m.reasoning.EffectiveEffort {
		return m.hideCommandView(), nil
	}
	cmd := m.setEffort(
		m.commandView.EffortSessionID,
		m.commandView.EffortModel,
		effort,
		false,
	)
	return m.hideCommandView(), cmd
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

	previousHeight := m.getCommandViewHeight()
	m.applyAction(setSessionReasoningAction{Settings: msg.Settings})
	if m.isEffortCommandView() {
		m.commandView.EffortModel = msg.Settings.Model
		m.commandView.Efforts = append(
			[]agentsession.ReasoningEffort(nil),
			msg.Settings.SupportedEfforts...,
		)
		m.commandView.EffortReasoning = msg.Settings.Reasoning
		m.commandView.EffortAdjustable = msg.Settings.Adjustable
		m.commandViewItemSelected = getEffectiveEffortOptionIndex(
			msg.Settings,
			m.commandView.Efforts,
		)
		m.commandViewOffset = getChatsCommandViewOffsetForSelection(
			m.commandViewItemSelected,
			m.commandViewOffset,
			m.getCommandViewContentHeight(),
			len(m.commandView.Efforts),
		)
		m.resizeCommandViewIfHeightChanged(previousHeight)
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

func getEffectiveEffortOptionIndex(
	settings agentsession.ReasoningSettings,
	efforts []agentsession.ReasoningEffort,
) int {
	for index, effort := range efforts {
		if effort == settings.EffectiveEffort {
			return index
		}
	}
	return 0
}

func getEffortCommandUnavailableDetail(settings agentsession.ReasoningSettings) string {
	if !settings.Reasoning {
		return getUnavailableEffortCommandDetail(
			"Reasoning effort is not applicable to this model.",
			settings,
		)
	}
	if !settings.Adjustable {
		return getUnavailableEffortCommandDetail(
			"Reasoning effort control is unavailable for this model.",
			settings,
		)
	}

	return ""
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
