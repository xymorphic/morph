package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
)

func TestPermissionsCommand_OpensWithCurrentPresetSelected(t *testing.T) {
	runModel := newModel()
	runModel.permissionPreset = permissions.PresetApproveForMe

	cmd := runModel.startPermissionsCommand()

	require.Nil(t, cmd)
	require.True(t, runModel.isPermissionsCommandView())
	require.Equal(t, getPermissionPresetIndex(permissions.PresetApproveForMe), runModel.commandViewItemSelected)
	content := stripANSI(runModel.renderCommandView())
	require.Contains(t, content, "Ask for approval")
	require.Contains(t, content, "Approve for me")
	require.Contains(t, content, "Full access")
	require.Contains(t, content, "Custom")
}

func TestPermissionsCommand_SelectsPresetForCurrentTUISession(t *testing.T) {
	runModel := newModel()
	runModel.startPermissionsCommand()
	runModel.commandViewItemSelected = getPermissionPresetIndex(permissions.PresetAskForApproval)

	updated, cmd := runModel.updatePermissionsCommandView(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, permissions.PresetAskForApproval, runModel.permissionPreset)
	require.False(t, runModel.fullAccess)
	md, ok := metadata.FromOutgoingContext(runModel.chatCtx)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), md)
	selectedPreset, ok := rpcmeta.PermissionPresetFromIncomingContext(incoming)
	require.True(t, ok)
	require.Equal(t, permissions.PresetAskForApproval, selectedPreset)
	require.Equal(t, permissions.SurfaceTUI, rpcmeta.PermissionSurfaceFromIncomingContext(incoming))
	require.Contains(
		t,
		stripANSI(runModel.renderBottomStatusPanel()),
		permissionStatusIcon+" Ask for approval",
	)
}

func TestPermissionsCommand_PreservesConfiguredRuleCustomizationAcrossPresetSelection(t *testing.T) {
	runModel := newModelWithClientContextAndConfig(context.Background(), nil, &config.Config{
		Permissions: permissions.Policy{
			Preset: permissions.PresetApproveForMe,
			Rules:  []permissions.Rule{{Name: "allow clock", Decision: permissions.DecisionAllow}},
		},
	})
	runModel.width = 160
	runModel.startPermissionsCommand()
	content := stripANSI(runModel.renderCommandView())
	require.Contains(t, content, "Ask for approval (customized)")
	require.Contains(t, content, "Approve for me (customized)")
	require.NotContains(t, content, "Full access (customized)")

	runModel.commandViewItemSelected = getPermissionPresetIndex(permissions.PresetAskForApproval)
	updated, cmd := runModel.updatePermissionsCommandView(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Contains(
		t,
		stripANSI(runModel.renderBottomStatusPanel()),
		permissionStatusIcon+" Ask for approval (customized)",
	)
}

func TestPermissionsCommand_RequiresSecondConfirmationForFullAccess(t *testing.T) {
	runModel := newModel()
	runModel.startPermissionsCommand()
	runModel.commandViewItemSelected = getPermissionPresetIndex(permissions.PresetFullAccess)

	updated, cmd := runModel.updatePermissionsCommandView(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.isPermissionsCommandView())
	require.True(t, runModel.permissionPresetConfirm)
	require.NotEqual(t, permissions.PresetFullAccess, runModel.permissionPreset)
	require.Contains(t, runModel.commandView.TitleRight, "enter again to confirm")

	updated, cmd = runModel.updatePermissionsCommandView(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, permissions.PresetFullAccess, runModel.permissionPreset)
	require.True(t, runModel.fullAccess)
	require.Contains(
		t,
		stripANSI(runModel.renderBottomStatusPanel()),
		permissionStatusIcon+" Full access (unsafe)",
	)
}

func TestPermissionsCommand_ChangingSelectionClearsFullAccessConfirmation(t *testing.T) {
	runModel := newModel()
	runModel.startPermissionsCommand()
	runModel.commandViewItemSelected = getPermissionPresetIndex(permissions.PresetFullAccess)
	runModel.permissionPresetConfirm = true

	updated, cmd := runModel.updatePermissionsCommandView(tea.KeyPressMsg{Code: tea.KeyUp})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.permissionPresetConfirm)
	require.Equal(t, getPermissionPresetIndex(permissions.PresetApproveForMe), runModel.commandViewItemSelected)
}

func TestPermissionsCommand_NavigatesPresetOptions(t *testing.T) {
	tests := []struct {
		name      string
		start     int
		message   tea.Msg
		selection int
	}{
		{name: "up", start: 2, message: tea.KeyPressMsg{Code: tea.KeyUp}, selection: 1},
		{name: "down", start: 1, message: tea.KeyPressMsg{Code: tea.KeyDown}, selection: 2},
		{name: "home", start: 2, message: tea.KeyPressMsg{Code: tea.KeyHome}, selection: 0},
		{name: "end", start: 1, message: tea.KeyPressMsg{Code: tea.KeyEnd}, selection: 3},
		{
			name: "mouse up", start: 2,
			message: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}), selection: 1,
		},
		{
			name: "mouse down", start: 1,
			message: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}), selection: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runModel := newModel()
			runModel.startPermissionsCommand()
			runModel.commandViewItemSelected = test.start
			runModel.permissionPresetConfirm = true

			updated, cmd := runModel.updatePermissionsCommandView(test.message)

			require.Nil(t, cmd)
			runModel = updated.(model)
			require.Equal(t, test.selection, runModel.commandViewItemSelected)
			require.False(t, runModel.permissionPresetConfirm)
		})
	}
}

func TestPermissionsCommand_IgnoresUnrelatedInput(t *testing.T) {
	runModel := newModel()
	runModel.startPermissionsCommand()
	runModel.commandViewItemSelected = 1

	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeySpace},
		tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseLeft}),
		"unrelated",
	} {
		updated, cmd := runModel.updatePermissionsCommandView(message)
		require.Nil(t, cmd)
		runModel = updated.(model)
		require.Equal(t, 1, runModel.commandViewItemSelected)
	}

	require.Equal(t, len(permissionPresetOptions)-1, getPermissionPresetIndex("invalid"))
}

func TestPermissionsCommand_PersistsPresetForFutureTUISessions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    provider: ollama
    name: test
    api: ollama-native
search:
  vector:
    enabled: false
storage:
  backend: memory
permissions:
  preset: ask
  rules:
    - name: allow automation clock
      actors: [automation]
      resources: [clock]
      actions: [read]
      decision: allow
`), 0o600))
	originalConfig := config.Get()
	t.Cleanup(func() { config.Set(originalConfig) })

	message := persistPermissionPresetCmd(
		"",
		configPath,
		permissions.PresetApproveForMe,
	)().(permissionPresetPersistedMsg)

	require.NoError(t, message.Err)
	require.Equal(t, permissions.PresetApproveForMe, message.Preset)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, permissions.PresetApproveForMe, cfg.Permissions.EffectivePreset())
	require.Len(t, cfg.Permissions.Rules, 1)
	require.Equal(t, "allow automation clock", cfg.Permissions.Rules[0].Name)
	var nilContext context.Context
	restarted := newModelWithClientContextAndConfig(nilContext, nil, cfg)
	require.Equal(t, permissions.PresetApproveForMe, restarted.permissionPreset)
	require.Equal(t, "Approve for me (customized)", restarted.permissionPolicy.Label())
}

func TestPermissionsCommand_ReportsPresetPersistenceFailure(t *testing.T) {
	message := persistPermissionPresetCmd(
		"",
		"",
		permissions.PresetAskForApproval,
	)().(permissionPresetPersistedMsg)
	require.EqualError(t, message.Err, "config path unavailable")

	runModel := newModel()
	updated, cmd, handled := runModel.handleAsyncMsg(message)

	require.True(t, handled)
	require.NotNil(t, cmd)
	require.Contains(t, updated.(model).status.Text(), "permission preset not saved")

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("permissions: ["), 0o600))
	message = persistPermissionPresetCmd(
		"",
		configPath,
		permissions.PresetApproveForMe,
	)().(permissionPresetPersistedMsg)
	require.Error(t, message.Err)

	message = persistPermissionPresetCmd(
		"",
		configPath,
		permissions.PresetCustom,
	)().(permissionPresetPersistedMsg)
	require.Error(t, message.Err)
}
