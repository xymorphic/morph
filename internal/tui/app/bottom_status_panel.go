package tui

import "github.com/xymorphic/morph/internal/permissions"

// renderBottomStatusPanel renders the compact bottom status panel below the composer.
func (m model) renderBottomStatusPanel() string {
	availableWidth := getInputBoxWidth(m.getMainPaneWidth())
	return defaultBottomStatusPanelRenderer.Render(getBottomStatusPanel(availableWidth, m))
}

type bottomStatusPanel struct {
	Width                 int
	HorizontalPadding     int
	ContentWidth          int
	ModelName             string
	Status                string
	Context               string
	Thinking              bool
	ThinkingFrame         int
	ExitConfirmation      bool
	InterruptConfirmation bool
	FullAccess            bool
	PermissionPreset      permissions.Preset
	PermissionLabel       string
}

func getBottomStatusPanel(width int, m model) bottomStatusPanel {
	return bottomStatusPanel{
		Width:                 max(width, 1),
		HorizontalPadding:     getPanelHorizontalPadding(width),
		ContentWidth:          getPanelContentWidth(width),
		ModelName:             m.getModelLabel(),
		Status:                m.bottomStatusText(),
		Context:               m.context,
		Thinking:              m.isModelThinking(),
		ThinkingFrame:         m.thinkingComposerFrame,
		ExitConfirmation:      m.hasPendingExitConfirmation(),
		InterruptConfirmation: !m.interruptAt.IsZero(),
		FullAccess:            m.fullAccess,
		PermissionPreset:      m.permissionPreset,
		PermissionLabel:       m.permissionPolicy.Label(),
	}
}
