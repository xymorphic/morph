package tui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"github.com/wandxy/morph/pkg/str"
)

var permissionPresetOptions = []permissions.Preset{
	permissions.PresetAskForApproval,
	permissions.PresetApproveForMe,
	permissions.PresetFullAccess,
	permissions.PresetCustom,
}

type permissionPresetPersistedMsg struct {
	Preset permissions.Preset
	Err    error
}

func (m *model) startPermissionsCommand() tea.Cmd {
	m.showCommandView(commandViewPayload{
		TitleLeft:       "Permissions",
		TitleRight:      "enter to select · esc to close",
		TitleRightColor: defaultTUITheme.MutedText,
		Kind:            commandViewKindPermissions,
	})
	m.commandViewItemSelected = getPermissionPresetIndex(m.permissionPreset)
	m.commandViewOffset = 0
	m.permissionPresetConfirm = false

	return nil
}

func (m model) isPermissionsCommandView() bool {
	return m.commandView.Visible && m.commandView.Kind == commandViewKindPermissions
}

func (m model) renderPermissionsCommandViewContent(content commandViewContent) string {
	offset := min(max(content.Offset, 0), max(len(permissionPresetOptions)-1, 0))
	height := max(content.Height, 1)
	end := min(offset+height, len(permissionPresetOptions))
	rows := make([]string, 0, height)
	for index := offset; index < end; index++ {
		preset := permissionPresetOptions[index]
		policy := m.permissionPolicy
		policy.Preset = preset
		detail := preset.Description()
		if preset == m.permissionPreset {
			detail = "current · " + detail
		}
		rows = append(rows, renderCommandListEntryRow(
			policy.Label(),
			detail,
			content.Width,
			max(content.Width-2, 1),
			index == m.commandViewItemSelected,
		))
	}
	return strings.Join(rows, "\n")
}

func (m *model) updatePermissionsCommandView(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			selection = len(permissionPresetOptions) - 1
		case tea.KeyEnter:
			return m.selectPermissionPreset()
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
	default:
		return *m, nil
	}

	m.commandViewItemSelected = min(max(selection, 0), len(permissionPresetOptions)-1)
	m.commandViewOffset = getChatsCommandViewOffsetForSelection(
		m.commandViewItemSelected,
		m.commandViewOffset,
		m.getCommandViewContentHeight(),
		len(permissionPresetOptions),
	)
	m.permissionPresetConfirm = false
	m.clearCommandViewSelection()

	return *m, nil
}

func (m *model) selectPermissionPreset() (tea.Model, tea.Cmd) {
	preset := permissionPresetOptions[min(max(m.commandViewItemSelected, 0), len(permissionPresetOptions)-1)]
	if preset == permissions.PresetFullAccess &&
		m.permissionPreset != permissions.PresetFullAccess &&
		!m.permissionPresetConfirm {
		m.permissionPresetConfirm = true
		m.commandView.TitleRight = "unsafe · enter again to confirm · esc to close"
		return *m, m.setStatus("full access is unsafe; press enter again to confirm")
	}

	m.permissionPreset = preset
	m.permissionPolicy.Preset = preset
	m.fullAccess = preset == permissions.PresetFullAccess
	m.chatCtx = rpcmeta.WithOutgoingPermissionPreset(m.chatCtx, preset)
	m.permissionPresetConfirm = false
	next := m.hideCommandView()

	return next, tea.Batch(
		next.setStatus("permission preset: "+next.permissionPolicy.Label()),
		persistPermissionPresetCmd(next.configEnvPath, next.configPath, preset),
	)
}

func getPermissionPresetIndex(preset permissions.Preset) int {
	for index, option := range permissionPresetOptions {
		if option == preset {
			return index
		}
	}

	return len(permissionPresetOptions) - 1
}

func persistPermissionPresetCmd(envPath string, configPath string, preset permissions.Preset) tea.Cmd {
	return func() tea.Msg {
		configPath = str.String(configPath).Trim()
		if configPath == "" {
			return permissionPresetPersistedMsg{
				Preset: preset,
				Err:    errors.New("config path unavailable"),
			}
		}

		updates := []config.ConfigUpdate{{
			Path:  "permissions.preset",
			Value: string(preset),
		}}
		if _, err := config.SetConfigValuesRelaxed(envPath, configPath, updates); err != nil {
			return permissionPresetPersistedMsg{Preset: preset, Err: err}
		}
		if cfg, err := config.Load(envPath, configPath); err == nil {
			config.Set(cfg)
		}

		return permissionPresetPersistedMsg{Preset: preset}
	}
}
