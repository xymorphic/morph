package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	agentapi "github.com/wandxy/morph/internal/agent"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	appcredential "github.com/wandxy/morph/internal/credential"
	modelcatalog "github.com/wandxy/morph/internal/model"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	provider_ollama "github.com/wandxy/morph/internal/model/provider_ollama"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/trace"
	"github.com/wandxy/morph/internal/tui/render"
	agent "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/str"
)

func TestMain(m *testing.M) {
	original := promptHistoryPath
	originalTheme := defaultTUITheme
	originalProfile := profile.Active()
	testProfileHome, _ := os.MkdirTemp("", "morph-tui-profile-*")
	_ = original()
	promptHistoryPath = func() string {
		return ""
	}
	if testProfileHome != "" {
		_ = os.WriteFile(
			filepath.Join(testProfileHome, userNameFilename),
			[]byte("{\"name\":\"Kennedy\"}\n"),
			0o600,
		)
		_ = os.WriteFile(
			filepath.Join(testProfileHome, "config.yaml"),
			[]byte(`
name: test-agent
models:
    main:
        provider: openrouter
        name: openai/gpt-4o-mini
search:
    vector:
        enabled: false
`),
			0o600,
		)
		profile.SetActive(profile.Profile{Name: profile.DefaultName, HomeDir: testProfileHome})
	} else {
		profile.SetActive(profile.Profile{})
	}
	defaultTUITheme = render.DefaultTheme
	code := m.Run()
	promptHistoryPath = original
	defaultTUITheme = originalTheme
	profile.SetActive(originalProfile)
	if testProfileHome != "" {
		_ = os.RemoveAll(testProfileHome)
	}
	os.Exit(code)
}

func TestModel_ViewRendersShellAreas(t *testing.T) {
	model := newModel()
	view := model.View()
	content := stripANSI(view.Content)

	require.True(t, view.AltScreen)
	require.Equal(t, tea.MouseModeCellMotion, view.MouseMode)
	require.Contains(t, view.Content, "48;5;235")
	require.Contains(t, content, "██████")
	require.Contains(t, content, "/changelog")
	require.Contains(t, content, "Hi, Kennedy")
	require.Contains(t, content, emptyUserPromptQuestion)
	require.Contains(t, content, inputPrompt+"Ask Morph...")
	require.Contains(t, content, "Ask Morph...")
	require.NotContains(t, content, "minimax-m2.7")
	require.Contains(t, content, "enter to send")
}

func TestModel_ViewShowsCancelHintDuringActiveResponse(t *testing.T) {
	runModel := newModel()
	runModel.responding = true

	content := stripANSI(runModel.View().Content)

	require.Contains(t, content, "esc to stop")
	require.NotContains(t, content, "enter to send")
}

func TestModel_InputCursorsDoNotBlink(t *testing.T) {
	runModel := newModel()

	require.False(t, runModel.input.Styles().Cursor.Blink)
	require.False(t, runModel.apiKeyInput.Styles().Cursor.Blink)
	require.False(t, runModel.baseURLInput.Styles().Cursor.Blink)
	require.False(t, runModel.modelFilterInput.Styles().Cursor.Blink)
	require.False(t, runModel.nameInput.Styles().Cursor.Blink)
	require.False(t, runModel.renameInput.Styles().Cursor.Blink)
}

func TestModel_InitFocusesInput(t *testing.T) {
	runModel := newModel()

	cmd := runModel.Init()

	require.NotNil(t, cmd)
}

func TestNewModelWithClientContextDefaultsNilContext(t *testing.T) {
	var ctx context.Context
	runModel := newModelWithClientContext(ctx, nil)

	require.NotNil(t, runModel.chatCtx)
}

func TestNewModel_ShowsNamePromptForEmptyProfile(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)

	runModel := newModel()
	content := stripANSI(runModel.View().Content)

	require.True(t, runModel.shouldShowNamePrompt())
	require.Contains(t, content, "████████")
	require.Contains(t, content, namePromptTitle)
	require.Contains(t, content, namePromptPlaceholder)
	require.Contains(t, content, namePromptSubmitHint)
	require.NotContains(t, content, inputPrompt+"Ask Morph")
	require.NotContains(t, content, "Welcome to Morph TUI")
}

func TestNewModel_LoadsSavedProfileName(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: openrouter
        name: openai/gpt-4o-mini
search:
    vector:
        enabled: false
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, userNameFilename),
		[]byte("{\"name\":\"Nedy\"}\n"),
		0o600,
	))

	runModel := newModel()

	require.False(t, runModel.shouldShowNamePrompt())
	require.Equal(t, "Nedy", runModel.userName)
	require.Contains(t, stripANSI(runModel.renderHeader()), "Welcome, Nedy")
}

func TestNewModel_StartsModelSetupForSavedNameWhenModelMissing(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
search:
    vector:
        enabled: false
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, userNameFilename),
		[]byte("{\"name\":\"Nedy\"}\n"),
		0o600,
	))

	runModel := newModel()
	content := stripANSI(runModel.View().Content)

	require.True(t, runModel.shouldShowNamePrompt())
	require.False(t, runModel.shouldShowProfileModelSetup())
	require.True(t, runModel.setupNamePromptActive)
	require.Equal(t, "Nedy", runModel.nameInput.Value())
	require.Contains(t, content, namePromptTitle)
	require.Contains(t, content, "Nedy")
	require.NotContains(t, content, emptyUserPromptQuestion)
	require.NotContains(t, content, inputPrompt+"Ask Morph")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.shouldShowNamePrompt())
	require.True(t, runModel.shouldShowProfileModelSetup())
	require.Equal(t, setupModelStepAuthMethod, runModel.setupModelStep)
	require.Contains(t, stripANSI(runModel.View().Content), "Select login method")
	require.Contains(t, stripANSI(runModel.View().Content), "esc to go back")

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.shouldShowNamePrompt())
	require.False(t, runModel.shouldShowProfileModelSetup())
	require.True(t, runModel.setupNamePromptActive)
	require.False(t, runModel.setupDismissible)
	require.Equal(t, "Nedy", runModel.nameInput.Value())
	require.Contains(t, stripANSI(runModel.View().Content), namePromptTitle)
}

func TestNewModel_ShowsEmptyPromptForSavedProfileName(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: openrouter
        name: openai/gpt-4o-mini
search:
    vector:
        enabled: false
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, userNameFilename),
		[]byte("{\"name\":\"Nedy\"}\n"),
		0o600,
	))

	runModel := newModel()
	content := stripANSI(runModel.View().Content)

	require.True(t, runModel.shouldShowEmptyUserPrompt())
	require.Contains(t, content, "██████")
	require.Contains(t, content, "/changelog")
	require.Contains(t, content, "Hi, Nedy")
	require.Contains(t, content, emptyUserPromptQuestion)
	require.Contains(t, content, inputPrompt+"Ask Morph")
	require.NotContains(t, content, "Welcome to Morph TUI")
}

func TestModel_SubmitsNamePrompt(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	runModel := newModel()
	runModel.nameInput.SetValue("  Nedy-Okpala  ")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	data, err := os.ReadFile(filepath.Join(home, userNameFilename))
	require.NoError(t, err)
	require.False(t, runModel.shouldShowNamePrompt())
	require.Equal(t, "Nedy-Okpala", runModel.userName)
	require.JSONEq(t, `{"name":"Nedy-Okpala"}`, string(data))
	require.Contains(t, stripANSI(runModel.renderHeader()), "Welcome, Nedy-Okpala")
}

func TestModel_SubmitsNamePromptStartsModelSetupWhenMissing(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
search:
    vector:
        enabled: false
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowNamePrompt())
	require.False(t, runModel.commandView.Visible)
	require.Equal(t, setupModelStepAuthMethod, runModel.setupModelStep)
	require.Empty(t, runModel.setupProviders)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "████")
	require.Contains(t, content, "Select login method")
	require.Contains(t, content, "enter to select")
	require.Contains(t, content, "Use a subscription")
	require.Contains(t, content, "Use an API Key")
	require.Contains(t, content, "Use local providers")
}

func TestModel_SetupAuthMethodSelectionShowsFilteredProviders(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodSubscription)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.commandView.Visible)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	require.NotEmpty(t, runModel.setupProviders)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Select model provider")
	require.Contains(t, content, "enter to select")
	require.Contains(t, content, "esc to go back")
	require.Contains(t, getLineContaining(content, "Select model provider"), "login type: subscription")
	require.Equal(
		t,
		lipgloss.Width(getLineContaining(content, "╭")),
		lipgloss.Width(getLineContaining(content, "Select model provider")),
	)
	require.Contains(t, content, "Anthropic")
	require.Contains(t, content, "Use your Anthropic subscription")
	require.Contains(t, content, "OpenAI Codex")
	require.Contains(t, content, "Use your OpenAI account")
	require.Contains(t, content, "GitHub Copilot")
	require.NotContains(t, content, "Ollama")
	require.NotContains(t, content, "OpenRouter")
}

func TestModel_SetupAPIKeyAuthMethodSelectionShowsAPIKeyProviders(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodAPIKey)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Anthropic")
	require.Contains(t, content, "Use your Anthropic API key")
	require.Contains(t, content, "OpenAI")
	require.Contains(t, content, "Use your OpenAI API key")
	require.Contains(t, content, "OpenRouter")
	require.Contains(t, content, "Use your OpenRouter API key")
	require.NotContains(t, content, "Ollama")
	require.NotContains(t, content, "OpenAI Codex")
	require.NotContains(t, content, "GitHub Copilot")
}

func TestModel_SetupLocalAuthMethodSelectionShowsLocalProviders(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodLocal)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, getLineContaining(content, "Select model provider"), "login type: local")
	require.Contains(t, content, "Ollama")
	require.Contains(t, content, "Local provider · local")
	require.NotContains(t, content, "OpenAI")
	require.NotContains(t, content, "Anthropic")
	require.NotContains(t, content, "OpenRouter")
}

func TestModel_SetupModelBackReturnsToProviderSelector(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("router-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	require.Empty(t, runModel.setupModelProvider)
	require.NotEmpty(t, runModel.setupProviders)
	require.Equal(t, "openrouter", runModel.setupProviders[runModel.setupItemSelected].ID)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Select model provider")
}

func TestModel_SetupProviderClickShowsLocalModels(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	row := getVisibleSetupProviderRow(t, &runModel, "openrouter")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      runModel.getProfileModelSetupListFirstRow() + row,
	}))
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "openrouter", runModel.setupModelProvider)
	require.NotEmpty(t, runModel.setupModels)
}

func TestModel_SetupProviderDescriptionClickShowsLocalModels(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	row := getVisibleSetupProviderRow(t, &runModel, "openrouter")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      runModel.getProfileModelSetupListFirstRow() + row + 1,
	}))
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "openrouter", runModel.setupModelProvider)
	require.NotEmpty(t, runModel.setupModels)
}

func TestRenderProfileModelSetupProviderRowKeepsSelectedEntryFullWidth(t *testing.T) {
	row := renderProfileModelSetupProviderRow(
		rpcclient.ProviderOption{ID: "anthropic", Name: "Anthropic"},
		setupAuthMethodSubscription,
		48,
		true,
	)
	lines := strings.Split(row, "\n")

	require.Len(t, lines, 2)
	require.Contains(t, stripANSI(lines[0]), "Anthropic")
	require.Contains(t, stripANSI(lines[1]), "Use your Anthropic subscription")
	require.NotEqual(t, lines[0], lines[1])
	for _, line := range lines {
		require.Equal(t, 48, lipgloss.Width(stripANSI(line)))
		require.Contains(t, line, "\x1b[")
	}
}

func TestModel_SetupModelSelectionPersistsMainAndSummary(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("router-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupModel(t, &runModel, "openai/gpt-4o-mini")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Summary.Provider)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Summary.Name)
	require.Equal(t, "openrouter", cfg.Models.Embedding.Provider)
	require.Equal(t, "text-embedding-3-small", cfg.Models.Embedding.Name)
}

func TestModel_SetupModelClickPersistsMainAndSummary(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("router-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	row := getVisibleSetupModelRow(t, &runModel, "openai/gpt-4o-mini")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      runModel.getProfileModelSetupListFirstRow() + row,
	}))
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Summary.Provider)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Summary.Name)
	require.Equal(t, "openrouter", cfg.Models.Embedding.Provider)
	require.Equal(t, "text-embedding-3-small", cfg.Models.Embedding.Name)
}

func TestModel_SetupMissingAPIKeyShowsInput(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "openrouter", runModel.setupModelProvider)
	require.Empty(t, runModel.setupPendingModelID)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, getLineContaining(content, "Enter API key for OpenRouter"), "login type: api key")
	require.Equal(
		t,
		lipgloss.Width(getLineContaining(content, "╭")),
		lipgloss.Width(getLineContaining(content, "Enter API key for OpenRouter")),
	)
	require.Contains(t, content, "████")
	require.Contains(t, content, "Enter API key for OpenRouter")
	require.Contains(t, content, "Enter key")
	require.Contains(t, content, "enter to save")
	require.Contains(t, content, "esc to go back")
}

func TestModel_SetupAPIKeyPromptPrefillsCurrentProviderKey(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    providers:
        openrouter:
            apiKey: existing-router-key
search:
    vector:
        enabled: false
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "openrouter")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "existing-router-key", runModel.apiKeyInput.Value())
	require.Contains(t, stripANSI(runModel.View().Content), "existing-router-key")
}

func TestModel_SetupModelAPIKeyPromptPrefillsCurrentProviderKey(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    providers:
        openrouter:
            apiKey: existing-router-key
search:
    vector:
        enabled: false
`)
	runModel := newModel()
	updated, cmd := runModel.showSetupProviderAPIKeyPrompt(rpcclient.ModelOption{
		ID:       "openai/gpt-4o-mini",
		Provider: "openrouter",
	})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "openai/gpt-4o-mini", runModel.setupPendingModelID)
	require.Equal(t, "existing-router-key", runModel.apiKeyInput.Value())
}

func TestModel_SetupAPIKeyBackspaceEditsInput(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	runModel.apiKeyInput.SetValue("router-key")
	runModel.apiKeyInput.CursorEnd()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "openrouter", runModel.setupModelProvider)
	require.Equal(t, "router-ke", runModel.apiKeyInput.Value())
}

func TestModel_SetupAPIKeyLeftStaysInInput(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	runModel.apiKeyInput.SetValue("router-key")
	runModel.apiKeyInput.CursorEnd()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "openrouter", runModel.setupModelProvider)
	require.Equal(t, "router-key", runModel.apiKeyInput.Value())
}

func TestModel_SetupAPIKeyEscapeReturnsToProviderSelector(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Contains(t, stripANSI(runModel.View().Content), "esc to go back")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	require.Empty(t, runModel.setupModelProvider)
	require.Contains(t, stripANSI(runModel.View().Content), "Select model provider")
}

func TestModel_SetupAPIKeyEscapeReturnsToProviderSelectorInSetupMode(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupDismissible = true
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	require.True(t, runModel.setupDismissible)
	require.Contains(t, stripANSI(runModel.View().Content), "Select model provider")
}

func TestModel_SetupAPIKeySubmitPersistsProviderKey(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	runModel.apiKeyInput.SetValue("router-key")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.True(t, runModel.shouldShowProfileModelSetup())
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Equal(t, "router-key", runModel.setupProviderAPIKey)
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Empty(t, cfg.Models.Main.Provider)
	require.Empty(t, cfg.Models.Main.Name)
	require.Empty(t, cfg.Models.Providers["openrouter"].APIKey)
}

func TestModel_SetupModelSelectionFiltersModels(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("router-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Greater(t, len(runModel.setupModels), 1)

	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Select model from")
	require.Contains(t, content, "Filter models")
	initialHeight := runModel.getProfileModelSetupRenderedListHeight()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, "mini", runModel.modelFilterInput.Value())
	require.NotEmpty(t, runModel.filteredSetupModels())
	for _, option := range runModel.filteredSetupModels() {
		require.Contains(t, strings.ToLower(option.ID+" "+option.Name), "mini")
	}
	content = stripANSI(runModel.View().Content)
	require.Contains(t, content, "mini")
	require.Equal(t, initialHeight, runModel.getProfileModelSetupRenderedListHeight())

	updated, cmd = runModel.Update(tea.PasteMsg{Content: "missing"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.filteredSetupModels())
	require.Equal(t, initialHeight, runModel.getProfileModelSetupRenderedListHeight())
	require.Contains(t, stripANSI(runModel.View().Content), " No matching models.")
}

func TestModel_SetupOllamaUsesLocalAwareModelOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"local:latest"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion","tools"],"model_info":{"llama.context_length":4096}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    providers:
        ollama:
            baseUrl: `+server.URL+`
search:
    vector:
        enabled: false
`)
	runModel.setupModelStep = setupModelStepProvider
	runModel.setupAuthMethod = ""
	runModel.setupProviders = []rpcclient.ProviderOption{{ID: constants.ModelProviderOllama, Local: true, Type: "local"}}
	runModel.setupItemSelected = 0

	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	require.Equal(t, server.URL, runModel.baseURLInput.Value())

	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)

	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Contains(t, getSetupModelIDs(runModel.setupModels), "local:latest")
	require.Contains(t, getSetupModelIDs(runModel.setupModels), constants.DefaultOllamaModel)
	local := getSetupModelOption(t, runModel.setupModels, "local:latest")
	require.False(t, local.LocalMissing)
	require.Equal(t, modelcatalog.OptionSourceDiscovery, local.Source)
	require.Equal(t, server.URL, local.BaseURL)
	require.True(t, local.SupportsTools)
	require.Equal(t, 4096, local.ContextWindow)
	require.True(t, getSetupModelOption(t, runModel.setupModels, constants.DefaultOllamaModel).LocalMissing)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, server.URL)
	require.Contains(t, getLineContaining(content, "local"), "installed")
	require.Contains(t, content, "not installed")
}

func TestModel_SetupOllamaBaseURLStepValidatesAndLoadsModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"local:latest"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	runModel.setupModelStep = setupModelStepProvider
	runModel.setupProviders = []rpcclient.ProviderOption{{ID: constants.ModelProviderOllama}}

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	require.Equal(t, constants.DefaultOllamaBaseURL, runModel.baseURLInput.Value())
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Set base URL for Ollama")
	require.Contains(t, content, constants.DefaultOllamaBaseURL)
	require.Contains(t, content, "enter to continue")

	runModel.baseURLInput.SetValue("")
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	require.Equal(t, "base URL required", runModel.status.Text())

	runModel.baseURLInput.SetValue("not a url")
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	require.Equal(t, "base URL invalid", runModel.status.Text())

	runModel.baseURLInput.SetValue("")
	updated, cmd = runModel.Update(tea.PasteMsg{Content: server.URL + "/"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Equal(t, server.URL, runModel.setupModelBaseURL)
	require.Contains(t, getSetupModelIDs(runModel.setupModels), "local:latest")

	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	require.Equal(t, server.URL, runModel.baseURLInput.Value())
}

func TestModel_SetupOllamaBaseURLStepHandlesUnavailableModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusInternalServerError)
	}))
	defer server.Close()

	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepBaseURL
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.baseURLInput = newSetupBaseURLInput()
	runModel.baseURLInput.SetValue(server.URL)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.Equal(t, setupNoticeActionLocalUnavailable, runModel.setupNoticeAction)
	require.Equal(t, "ollama not reachable", runModel.status.Text())
	require.Contains(t, runModel.setupNoticeMessage, "Could not connect to Ollama")
	require.NotContains(t, runModel.setupNoticeMessage, "Error:")
	require.Empty(t, runModel.setupModels)

	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	require.Equal(t, server.URL, runModel.baseURLInput.Value())
}

func TestModel_SetupOllamaBaseURLStepShowsSuggestedModelsWhenNoneInstalled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepBaseURL
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.baseURLInput = newSetupBaseURLInput()
	runModel.baseURLInput.SetValue(server.URL)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.NotEmpty(t, runModel.setupModels)
	require.True(t, getSetupModelOption(t, runModel.setupModels, constants.DefaultOllamaModel).LocalMissing)
	require.Contains(t, stripANSI(runModel.View().Content), "not installed")
}

func TestModel_SetupOllamaModelClickPersistsWithoutAuthPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"local:latest"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion","tools"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	runModel.setupModelStep = setupModelStepProvider
	runModel.setupProviders = []rpcclient.ProviderOption{{ID: constants.ModelProviderOllama, Local: true, Type: "local"}}

	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.baseURLInput.SetValue(server.URL)
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	row := getVisibleSetupModelRow(t, &runModel, "local:latest")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      runModel.getProfileModelSetupListFirstRow() + row,
	}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, "local:latest", cfg.Models.Main.Name)
	require.Equal(t, server.URL, cfg.Models.Main.BaseURL)
}

func TestModel_SetupOllamaModelWithoutToolsWarnsBeforePersisting(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	runModel.setupModelStep = setupModelStepModel
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:       "chat-only:latest",
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
	}}

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.Equal(t, setupNoticeActionToolWarning, runModel.setupNoticeAction)
	require.Equal(t, "chat-only:latest", runModel.setupPendingModelID)
	require.Contains(t, runModel.setupNoticeMessage, "does not advertise tool support")
	require.Contains(t, runModel.setupNoticeHint, "enter to save anyway")

	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Empty(t, cfg.Models.Main.Name)

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err = config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, "chat-only:latest", cfg.Models.Main.Name)
}

func TestModel_SetupMissingOllamaModelPromptsBeforePersisting(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	runModel.setupModelStep = setupModelStepModel
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "missing:latest",
		Provider:      constants.ModelProviderOllama,
		API:           modelprovider.APIOllamaNative,
		LocalMissing:  true,
		SupportsTools: true,
	}}

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.Equal(t, setupNoticeActionMissingModelPull, runModel.setupNoticeAction)
	require.Equal(t, "missing:latest", runModel.setupPendingModelID)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Install missing:latest?")
	require.Contains(t, content, "This Ollama model is not installed locally.")
	require.Contains(t, content, "Pull it now before saving.")
	require.Contains(t, content, "Or skip to save without installing.")
	require.Contains(t, content, "enter to pull")
	require.Contains(t, content, "s to skip")

	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Empty(t, cfg.Models.Main.Name)

	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Equal(t, "missing:latest", runModel.setupModels[runModel.setupItemSelected].ID)
}

func TestModel_SetupMissingOllamaModelSkipPersistsSelection(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupPendingModelID = "missing:latest"
	runModel.setupNoticeAction = setupNoticeActionMissingModelPull
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "missing:latest",
		Provider:      constants.ModelProviderOllama,
		API:           modelprovider.APIOllamaNative,
		LocalMissing:  true,
		SupportsTools: true,
	}}

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, "missing:latest", cfg.Models.Main.Name)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)
}

func TestModel_SetupMissingOllamaModelPullsBeforePersisting(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupPendingModelID = "missing:latest"
	runModel.setupNoticeAction = setupNoticeActionMissingModelPull
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "missing:latest",
		Provider:      constants.ModelProviderOllama,
		API:           modelprovider.APIOllamaNative,
		LocalMissing:  true,
		SupportsTools: true,
	}}
	var pulledBaseURL, pulledModel string
	restore := stubSetupOllamaPull(t, func(
		_ context.Context,
		baseURL string,
		model string,
		_ map[string]string,
		onProgress func(provider_ollama.PullProgress),
	) error {
		pulledBaseURL = baseURL
		pulledModel = model
		onProgress(provider_ollama.PullProgress{Status: "pulling manifest"})
		onProgress(provider_ollama.PullProgress{Status: "pulling manifest"})
		onProgress(provider_ollama.PullProgress{Status: "success"})
		return nil
	})
	defer restore()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupNoticeActionPullingModel, runModel.setupNoticeAction)

	pullMessages := runSetupModelPullBatch(t, cmd)
	require.Len(t, pullMessages.progress, 2)
	require.Equal(t, []string{"Ollama pull: pulling manifest"}, pullMessages.progress[0].lines)
	require.Equal(t, []string{"Ollama pull: pulling manifest", "Ollama pull: success"}, pullMessages.progress[1].lines)
	updated, cmd = runModel.Update(pullMessages.progress[1])
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Contains(t, stripANSI(runModel.View().Content), "Ollama pull: success")

	updated, cmd = runModel.Update(pullMessages.completed)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, "http://127.0.0.1:11434", pulledBaseURL)
	require.Equal(t, "missing:latest", pulledModel)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, "missing:latest", cfg.Models.Main.Name)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)
}

func TestModel_SetupMissingOllamaModelPullFailureKeepsSelection(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupPendingModelID = "missing:latest"
	runModel.setupNoticeAction = setupNoticeActionMissingModelPull
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:           "missing:latest",
		Provider:     constants.ModelProviderOllama,
		API:          modelprovider.APIOllamaNative,
		LocalMissing: true,
	}}
	restore := stubSetupOllamaPull(t, func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		return errors.New("pull failed")
	})
	defer restore()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	pullMessages := runSetupModelPullBatch(t, cmd)
	updated, cmd = runModel.Update(pullMessages.completed)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.Equal(t, setupNoticeActionMissingModelPull, runModel.setupNoticeAction)
	require.Equal(t, "missing:latest", runModel.setupPendingModelID)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Ollama pull failed: pull failed")
	require.Contains(t, content, "enter to retry")

	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Equal(t, "missing:latest", runModel.setupModels[runModel.setupItemSelected].ID)
}

func TestModel_SetupMissingOllamaModelPullReachabilityFailureIsClean(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11435"
	runModel.setupPendingModelID = "missing:latest"
	runModel.setupNoticeAction = setupNoticeActionMissingModelPull
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "missing:latest",
		Provider:      constants.ModelProviderOllama,
		API:           modelprovider.APIOllamaNative,
		LocalMissing:  true,
		SupportsTools: true,
	}}
	restore := stubSetupOllamaPull(t, func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		return errors.New(`ollama is not reachable at http://127.0.0.1:11435; start Ollama or update the base URL: Get "http://127.0.0.1:11435/api/tags": dial tcp 127.0.0.1:11435: connect: connection refused`)
	})
	defer restore()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	pullMessages := runSetupModelPullBatch(t, cmd)
	updated, cmd = runModel.Update(pullMessages.completed)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.Equal(t, setupNoticeActionMissingModelPull, runModel.setupNoticeAction)
	require.Contains(t, runModel.setupNoticeMessage, "Could not connect to Ollama")
	require.Contains(t, runModel.setupNoticeMessage, "http://127.0.0.1:11435")
	require.NotContains(t, runModel.setupNoticeMessage, "dial tcp")
	require.NotContains(t, runModel.setupNoticeMessage, "Error:")
}

func TestModel_SetupMissingOllamaModelPullCanBeCancelled(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupAuthMethod = setupAuthMethodLocal
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupPendingModelID = "missing:latest"
	runModel.setupNoticeAction = setupNoticeActionMissingModelPull
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:           "missing:latest",
		Provider:     constants.ModelProviderOllama,
		API:          modelprovider.APIOllamaNative,
		LocalMissing: true,
	}}
	pullStarted := make(chan struct{})
	restore := stubSetupOllamaPull(t, func(
		ctx context.Context,
		_ string,
		_ string,
		_ map[string]string,
		_ func(provider_ollama.PullProgress),
	) error {
		close(pullStarted)
		<-ctx.Done()
		return ctx.Err()
	})
	defer restore()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	completed := make(chan tea.Msg, 1)
	go func() {
		completed <- batch[0]()
	}()
	select {
	case <-pullStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pull")
	}

	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Nil(t, runModel.setupPullCancel)

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancelled pull")
	}
}

func TestProfileModelSetupPullProgressHelpers(t *testing.T) {
	require.True(t, sameSetupPullProgressLines(nil, nil))
	require.True(t, sameSetupPullProgressLines([]string{"a"}, []string{"a"}))
	require.False(t, sameSetupPullProgressLines([]string{"a"}, []string{"a", "b"}))
	require.False(t, sameSetupPullProgressLines([]string{"a"}, []string{"b"}))

	events := make(chan tea.Msg, 1)
	require.True(t, sendSetupModelPullEvent(context.Background(), events, setupModelPullClosedMsg{}))
	require.IsType(t, setupModelPullClosedMsg{}, <-events)
	require.False(t, sendSetupModelPullEvent(context.Background(), nil, setupModelPullClosedMsg{}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, sendSetupModelPullEvent(ctx, nil, setupModelPullClosedMsg{}))
	closedEvents := make(chan tea.Msg)
	close(closedEvents)
	require.IsType(t, setupModelPullClosedMsg{}, waitForSetupModelPullEvent(closedEvents)())
	require.Nil(t, waitForSetupModelPullEventFromState(nil))
	emptyModel := model{}
	require.Nil(t, waitForSetupModelPullEventFromState(&emptyModel))
}

func TestProfileModelSetupPullIgnoresStaleProgress(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupNoticeAction = setupNoticeActionPullingModel
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupPendingModelID = "missing:latest"
	updated, cmd := runModel.Update(setupModelPullProgressMsg{
		provider: constants.ModelProviderOpenAI,
		model:    "missing:latest",
		lines:    []string{"ignored"},
	})
	require.Nil(t, cmd)
	require.Empty(t, updated.(model).setupNoticeMessage)

	events := make(chan tea.Msg, 1)
	runModel.setupPullEvents = events
	updated, cmd = runModel.Update(setupModelPullProgressMsg{
		provider: constants.ModelProviderOllama,
		model:    "missing:latest",
	})
	require.NotNil(t, cmd)
	require.Empty(t, updated.(model).setupNoticeMessage)
}

func TestProfileModelSetupPullGuardPaths(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	updated, cmd := runModel.showMissingSetupModelPullPrompt(rpcclient.ModelOption{})
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	updated, cmd = runModel.startSetupModelPull()
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupPendingModelID = "missing:latest"
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "missing:latest",
		Provider:      constants.ModelProviderOllama,
		API:           modelprovider.APIOllamaNative,
		SupportsTools: true,
	}}
	updated, cmd = runModel.startSetupModelPull()
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel.setupPendingModelID = "missing:latest"
	runModel.setupModels = []rpcclient.ModelOption{{ID: "other:latest"}}
	_, ok := runModel.getPendingSetupModelOption()
	require.False(t, ok)

	updated, cmd = runModel.skipMissingSetupModelPull()
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel.setupPendingModelID = "missing:latest"
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = constants.DefaultOllamaBaseURL
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "missing:latest",
		Provider:      constants.ModelProviderOllama,
		API:           modelprovider.APIOllamaNative,
		SupportsTools: true,
	}}
	runModel.configPath = ""
	updated, cmd = runModel.skipMissingSetupModelPull()
	require.NotNil(t, cmd)
	require.Contains(t, stripANSI(updated.(model).View().Content), "config path unavailable")
}

func TestProfileModelSetupPullIgnoresStaleCompletion(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupNoticeAction = setupNoticeActionPullingModel
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupPendingModelID = "missing:latest"
	updated, cmd := runModel.Update(setupModelPullCompletedMsg{
		provider: constants.ModelProviderOpenAI,
		model:    "missing:latest",
	})
	require.Nil(t, cmd)
	require.Equal(t, setupNoticeActionPullingModel, updated.(model).setupNoticeAction)
}

func TestProfileModelSetupPullPersistFailureShowsNotice(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupNoticeAction = setupNoticeActionPullingModel
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupPendingModelID = "missing:latest"
	runModel.configPath = ""
	updated, cmd := runModel.Update(setupModelPullCompletedMsg{
		provider: constants.ModelProviderOllama,
		model:    "missing:latest",
		option: rpcclient.ModelOption{
			ID:            "missing:latest",
			Provider:      constants.ModelProviderOllama,
			API:           modelprovider.APIOllamaNative,
			SupportsTools: true,
		},
	})
	require.NotNil(t, cmd)
	require.Contains(t, stripANSI(updated.(model).View().Content), "config path unavailable")
}

func TestModel_StartProfileModelSetupClearsNoticeState(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupNoticeTitle = "Old title"
	runModel.setupNoticeMessage = "Old message"
	runModel.setupNoticeHint = "Old hint"
	runModel.setupNoticeAction = setupNoticeActionPullingModel
	cmd := runModel.startProfileModelSetup()
	require.NotNil(t, cmd)
	require.Empty(t, runModel.setupNoticeTitle)
	require.Empty(t, runModel.setupNoticeMessage)
	require.Empty(t, runModel.setupNoticeHint)
	require.Empty(t, runModel.setupNoticeAction)
}

func TestProfileModelSetupLocalModelDetails(t *testing.T) {
	require.Equal(t, "not installed", getSetupModelOptionMutedDetail(rpcclient.ModelOption{LocalMissing: true}))
	detail := getSetupModelOptionMutedDetail(rpcclient.ModelOption{LocalMissing: true, Reasoning: true})
	require.Contains(t, detail, "not installed")
	require.Contains(t, detail, "reasoning")
	require.Equal(
		t,
		"installed",
		getSetupModelOptionMutedDetail(rpcclient.ModelOption{Source: modelcatalog.OptionSourceDiscovery}),
	)
	detail = getSetupModelOptionMutedDetail(rpcclient.ModelOption{Source: modelcatalog.OptionSourceDiscovery, Reasoning: true})
	require.Contains(t, detail, "installed")
	require.Contains(t, detail, "reasoning")
	require.Equal(t, "", getSetupModelOptionMutedDetail(rpcclient.ModelOption{}))
}

func TestProfileModelSetupLocalProviderHelpers(t *testing.T) {
	providers := []modelcatalog.ProviderOption{
		{ID: constants.ModelProviderOpenAI, SupportsAPIKey: true},
		{ID: constants.ModelProviderOpenAICodex, SupportsAPIKey: true, SupportsOAuth: true},
		{ID: constants.ModelProviderOllama},
	}

	require.Equal(
		t,
		[]string{constants.ModelProviderOpenAI},
		getSetupProviderIDs(filterSetupProvidersForAuthMethod(providers, setupAuthMethodAPIKey)),
	)
	require.Equal(
		t,
		[]string{constants.ModelProviderOpenAICodex},
		getSetupProviderIDs(filterSetupProvidersForAuthMethod(providers, setupAuthMethodSubscription)),
	)
	require.Equal(
		t,
		[]string{constants.ModelProviderOllama},
		getSetupProviderIDs(filterSetupProvidersForAuthMethod(providers, setupAuthMethodLocal)),
	)
	require.Equal(
		t,
		[]string{},
		getSetupProviderIDs(filterSetupProvidersForAuthMethod(providers, "unknown")),
	)
	require.Equal(
		t,
		"Local provider · local",
		getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: constants.ModelProviderOllama}, ""),
	)

	runModel := newModel()
	runModel.setupModelProvider = constants.ModelProviderOpenAI
	require.Empty(t, runModel.renderProfileModelSetupModelDetail(40))
	runModel.setupModelProvider = constants.ModelProviderOllama
	require.Contains(t, stripANSI(runModel.renderProfileModelSetupModelDetail(40)), constants.DefaultOllamaBaseURL)

	updated, cmd := runModel.showSetupBaseURLPrompt("", "")
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "provider selection unavailable", runModel.status.Text())

	runModel.setupModelStep = setupModelStepBaseURL
	runModel.setupModelProvider = ""
	runModel.baseURLInput = newSetupBaseURLInput()
	runModel.baseURLInput.SetValue(constants.DefaultOllamaBaseURL)
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "provider selection unavailable", runModel.status.Text())
}

func TestModel_PersistSetupModelSelectionUpdatesLocalBaseURL(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: openai
        name: gpt-5.5
        api: openai-responses
        baseUrl: https://stale.example/v1
    summary:
        provider: openai
        name: gpt-5.5
        api: openai-responses
        baseUrl: https://stale.example/v1
search:
    vector:
        enabled: true
`)
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"

	err := runModel.persistSetupModelSelection(rpcclient.ModelOption{
		ID:       "lfm2.5-thinking:latest",
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
	}, "")

	require.NoError(t, err)
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, "lfm2.5-thinking:latest", cfg.Models.Main.Name)
	require.Equal(t, modelprovider.APIOllamaNative, cfg.Models.Main.API)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Summary.Provider)
	require.Equal(t, "lfm2.5-thinking:latest", cfg.Models.Summary.Name)
	require.Equal(t, modelprovider.APIOllamaNative, cfg.Models.Summary.API)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Summary.BaseURL)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Embedding.Provider)
	require.Equal(t, constants.DefaultOllamaEmbeddingModel, cfg.Models.Embedding.Name)
	require.Equal(t, modelprovider.APIOllamaEmbeddings, cfg.Models.Embedding.API)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Embedding.BaseURL)
	rawConfig, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(rawConfig), "baseURL:")
	require.Contains(t, string(rawConfig), "baseUrl: http://127.0.0.1:11434")
}

func TestModel_CompleteSetupModelSelectionRefreshesRuntimeModelClient(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	client := &fakeTUIChatClient{selectedModel: rpcclient.ModelOption{
		ID:       "lfm2.5-thinking:latest",
		Provider: constants.ModelProviderOllama,
	}}
	runModel.modelClient = client
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"

	option := rpcclient.ModelOption{
		ID:       "lfm2.5-thinking:latest",
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
	}
	require.NoError(t, runModel.persistSetupModelSelection(option, ""))

	updated, cmd := runModel.completeSetupModelSelection(option)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	require.Equal(t, "model setup saved", runModel.status.Text())
	require.Equal(t, constants.ModelProviderOllama, runModel.runtimeInfo.Provider)
	require.Equal(t, "lfm2.5-thinking:latest", runModel.runtimeInfo.Model)
	require.Equal(t, constants.ModelProviderOllama, runModel.runtimeInfo.SummaryProvider)
	require.Equal(t, "lfm2.5-thinking:latest", runModel.runtimeInfo.SummaryModel)

	msg := setupModelRuntimeSelectedMessageFromBatch(t, cmd)
	require.NoError(t, msg.Err)
	require.Equal(t, 1, client.selectModelCalls)
	require.Equal(t, constants.ModelProviderOllama, client.selectedModelProvider)
	require.Equal(t, "lfm2.5-thinking:latest", client.selectedModelID)

	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "model setup saved; daemon restarting", runModel.status.Text())
	require.Equal(t, constants.ModelProviderOllama, runModel.runtimeInfo.Provider)
	require.Equal(t, "lfm2.5-thinking:latest", runModel.runtimeInfo.Model)
}

func TestModel_CompleteSetupModelSelectionKeepsSavedConfigWhenRuntimeRefreshFails(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	client := &fakeTUIChatClient{selectModelErr: errors.New("daemon unavailable")}
	runModel.modelClient = client
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"

	option := rpcclient.ModelOption{
		ID:       "lfm2.5-thinking:latest",
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
	}
	require.NoError(t, runModel.persistSetupModelSelection(option, ""))

	updated, cmd := runModel.completeSetupModelSelection(option)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	msg := setupModelRuntimeSelectedMessageFromBatch(t, cmd)
	require.EqualError(t, msg.Err, "daemon unavailable")

	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "model setup saved; daemon refresh unavailable", runModel.status.Text())

	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, "lfm2.5-thinking:latest", cfg.Models.Main.Name)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)
}

func TestModel_PersistSetupModelSelectionClearsHostedBaseURL(t *testing.T) {
	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ollama
        name: lfm2.5-thinking:latest
        api: ollama-native
        baseUrl: http://127.0.0.1:11434
    summary:
        provider: ollama
        name: lfm2.5-thinking:latest
        api: ollama-native
        baseUrl: http://127.0.0.1:11434
search:
    vector:
        enabled: false
`)
	runModel.setupModelProvider = constants.ModelProviderOpenAI
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"

	err := runModel.persistSetupModelSelection(rpcclient.ModelOption{
		ID:       "gpt-5.5",
		Provider: constants.ModelProviderOpenAI,
		API:      modelprovider.APIOpenAIResponses,
	}, "")

	require.NoError(t, err)
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Main.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.Models.Main.API)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Summary.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Summary.Name)
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.Models.Summary.API)
	rawConfig, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(rawConfig), "http://127.0.0.1:11434")
}

func TestModel_SetupOllamaRefreshesLocalModelOptionsAndPreservesSelection(t *testing.T) {
	modelNames := []string{"first:latest", "selected:latest"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			names := make([]string, 0, len(modelNames))
			for _, name := range modelNames {
				names = append(names, `{"name":"`+name+`"}`)
			}
			_, _ = w.Write([]byte(`{"models":[` + strings.Join(names, ",") + `]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion"],"model_info":{"llama.context_length":4096}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	runModel := newSetupModelSelectionTestModelWithHome(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    providers:
        ollama:
            baseUrl: `+server.URL+`
search:
    vector:
        enabled: false
`)
	runModel.setupModelStep = setupModelStepProvider
	runModel.setupAuthMethod = ""
	runModel.setupProviders = []rpcclient.ProviderOption{{ID: constants.ModelProviderOllama, Local: true, Type: "local"}}

	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepBaseURL, runModel.setupModelStep)
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Equal(t, server.URL, runModel.setupModelBaseURL)
	selectSetupModel(t, &runModel, "selected:latest")

	modelNames = []string{"new:latest", "selected:latest"}
	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "refreshing models", runModel.status.Text())

	loaded := runSetupModelOptionsRefreshBatch(t, cmd)
	updated, cmd = runModel.Update(loaded)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, "models refreshed", runModel.status.Text())
	require.Contains(t, getSetupModelIDs(runModel.setupModels), "new:latest")
	require.NotContains(t, getSetupModelIDs(runModel.setupModels), "first:latest")
	require.Equal(t, "selected:latest", runModel.currentSetupModelID())
	require.Equal(t, server.URL, runModel.setupModelBaseURL)
}

func TestModel_SetupOllamaRefreshFailurePreservesCurrentModels(t *testing.T) {
	runModel := newModel()
	runModel.setupModelStep = setupModelStepModel
	runModel.setupModelProvider = constants.ModelProviderOllama
	runModel.setupModelBaseURL = "http://127.0.0.1:11434"
	runModel.setupModels = []rpcclient.ModelOption{{ID: "installed:latest"}}

	updated, cmd := runModel.Update(setupModelOptionsLoadedMsg{
		provider: constants.ModelProviderOllama,
		err:      errors.New("ollama offline"),
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, []string{"installed:latest"}, getSetupModelIDs(runModel.setupModels))
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.Equal(t, setupNoticeActionLocalUnavailable, runModel.setupNoticeAction)
	require.Equal(t, "ollama not reachable", runModel.status.Text())
	require.Contains(t, runModel.setupNoticeMessage, "Could not connect to Ollama")
	require.NotContains(t, runModel.setupNoticeMessage, "Error:")
}

func TestModel_SetupModelFilterTitleUsesFixedInputWidth(t *testing.T) {
	runModel := newModel()
	runModel.setupModelProvider = "openai-codex"

	require.Equal(t, 18, getSetupModelFilterTitleInputWidth(t, runModel.renderProfileModelSetupModelTitle(42)))
	require.Equal(t, 18, getSetupModelFilterTitleInputWidth(t, runModel.renderProfileModelSetupModelTitle(72)))

	runModel.modelFilterInput.SetValue(strings.Repeat("k", 40))
	title := runModel.renderProfileModelSetupModelTitle(42)
	require.Equal(t, 42, lipgloss.Width(stripANSI(title)))
	require.Equal(t, 18, getSetupModelFilterTitleInputWidth(t, title))
}

func getSetupModelFilterTitleInputWidth(t *testing.T, title string) int {
	t.Helper()

	title = stripANSI(title)
	if start := strings.Index(title, "Filter models"); start >= 0 {
		return lipgloss.Width(title[start:])
	}

	trimmed := strings.TrimRight(title, " ")
	start := strings.LastIndex(title, " ")
	require.GreaterOrEqual(t, start, 0)

	return lipgloss.Width(trimmed[start+1:])
}

func TestModel_SetupAPIKeySubmitRejectsEmptyKey(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Equal(t, "provider API key required", runModel.status.Text())
}

func TestModel_SetupAPIKeyAcceptsPaste(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)

	updated, cmd := runModel.Update(tea.PasteMsg{Content: " pasted-router-key\n"})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Empty(t, runModel.input.Value())
	require.Equal(t, "pasted-router-key", runModel.apiKeyInput.Value())
	require.Contains(t, stripANSI(runModel.View().Content), "pasted-router-key")
}

func TestModel_SetupMissingAPIKeyShowsInputBeforeEmbeddingValidation(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    embedding:
        provider: openrouter
        name: text-embedding-3-small
search:
    vector:
        enabled: true
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "openrouter")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Enter API key")
	require.NotContains(t, content, "Embedding setup required")
}

func TestModel_SetupMissingOAuthStartsLoginAndAdvancesToModels(t *testing.T) {
	store := newFakeSetupCredentialStore()
	restore := stubSetupOAuth(t, store, fakeSetupSubscriptionProvider{
		output:     "Open this URL to authenticate Anthropic:\nhttps://auth.test/login?client_id=very-long-client&state=very-long-state&redirect_uri=http%3A%2F%2Flocalhost%2Fcallback\n",
		credential: appcredential.StoredCredential{Type: appcredential.TypeOAuth, Token: "oauth-token"},
	})
	defer restore()

	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodSubscription)
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "anthropic")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.True(t, runModel.setupOAuthPending)
	require.Equal(t, "anthropic", runModel.setupOAuthProvider)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Connect Anthropic")
	require.Contains(t, content, "Opening browser to connect Anthropic.")
	require.Contains(t, content, "Complete login in your browser")
	require.Contains(t, content, "here.")
	require.Contains(t, content, "esc to go back")
	oauthCmd := cmd

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.setupOAuthPending)

	outputMsg, completedMsg := runSetupOAuthBatch(t, oauthCmd)
	updated, cmd = runModel.Update(outputMsg)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	content = stripANSI(runModel.View().Content)
	require.Contains(t, content, "Open this URL to authenticate Anthropic:")
	require.Contains(t, content, "URL: https://auth.test/login...")
	require.NotContains(t, content, "client_id=very-long-client")

	updated, cmd = runModel.Update(completedMsg)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.setupOAuthPending)
	require.Empty(t, runModel.setupOAuthProvider)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.Equal(t, "oauth-token", store.credentials["anthropic"].Token)
	require.Contains(t, stripANSI(runModel.View().Content), "Select model from Anthropic")
}

func TestModel_SetupSubscriptionProviderSkipsLoginWithExistingOAuthCredential(t *testing.T) {
	store := newFakeSetupCredentialStore()
	restore := stubSetupOAuth(t, store, fakeSetupSubscriptionProvider{
		err: errors.New("login should not run"),
	})
	defer restore()

	runModel := newSetupModelSelectionTestModel(t)
	require.NoError(t, appcredential.NewFileStore("").Set("anthropic", appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: "existing-token",
	}))
	selectSetupAuthMethod(t, &runModel, setupAuthMethodSubscription)
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "anthropic")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	require.False(t, runModel.setupOAuthPending)
	require.Contains(t, stripANSI(runModel.View().Content), "Select model from Anthropic")
}

func TestModel_SetupOAuthFailureShowsRetryNotice(t *testing.T) {
	store := newFakeSetupCredentialStore()
	restore := stubSetupOAuth(t, store, fakeSetupSubscriptionProvider{
		output: "Open this URL to authenticate Anthropic:\nhttps://auth.test/login\n",
		err:    errors.New("oauth failed"),
	})
	defer restore()

	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodSubscription)
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "anthropic")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	_, completedMsg := runSetupOAuthBatch(t, cmd)
	updated, cmd = runModel.Update(completedMsg)
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.False(t, runModel.setupOAuthPending)
	require.Equal(t, "anthropic", runModel.setupOAuthProvider)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Authentication failed")
	require.Contains(t, content, "oauth failed")
	require.Contains(t, content, "enter to retry")

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.setupOAuthPending)
	require.Equal(t, "anthropic", runModel.setupOAuthProvider)
	require.Contains(t, stripANSI(runModel.View().Content), "Opening browser to connect Anthropic.")
}

func TestModel_SetupOAuthModelAuthErrorShowsNoticeInsteadOfRestartingLogin(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	require.NoError(t, appcredential.NewFileStore("").Set(constants.ModelProviderOpenAICodex, appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: "not-a-jwt",
	}))
	runModel.setupModelStep = setupModelStepModel
	runModel.setupAuthMethod = setupAuthMethodSubscription
	runModel.setupModelProvider = constants.ModelProviderOpenAICodex
	runModel.setupModels = []rpcclient.ModelOption{{
		ID:            "gpt-5.5",
		Provider:      constants.ModelProviderOpenAICodex,
		SupportsOAuth: true,
	}}

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.False(t, runModel.setupOAuthPending)
	require.Empty(t, runModel.setupOAuthProvider)
	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Model setup unavailable")
	require.Contains(t, content, "OpenAI subscription token must be a JWT with")
	require.Contains(t, content, "account metadata")
	require.NotContains(t, content, "Opening browser to connect OpenAI Codex")
}

func TestModel_SetupOAuthModelSelectionIgnoresMissingGatewayToken(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
gateway:
    enabled: true
    telegram:
        enabled: true
        mode: polling
        botToken: ""
search:
    vector:
        enabled: false
`)
	require.NoError(t, appcredential.NewFileStore("").Set(constants.ModelProviderOpenAICodex, appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: makeOpenAITestJWTForSetup(t),
	}))
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodSubscription)
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, constants.ModelProviderOpenAICodex)
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Equal(t, setupModelStepModel, runModel.setupModelStep)
	selectSetupModel(t, &runModel, "gpt-5.5")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAICodex, cfg.Models.Main.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Equal(t, constants.ModelProviderOpenAICodex, cfg.Models.Summary.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Summary.Name)
	require.NotContains(t, stripANSI(runModel.View().Content), "gateway telegram bot token")
}

func TestModel_SetupOAuthBackCancelsPendingLogin(t *testing.T) {
	store := newFakeSetupCredentialStore()
	restore := stubSetupOAuth(t, store, fakeSetupSubscriptionProvider{waitForCancel: true})
	defer restore()

	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodSubscription)
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "anthropic")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.setupOAuthPending)
	require.NotNil(t, runModel.setupOAuthCancel)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	completedCh := make(chan setupOAuthCompletedMsg, 1)
	go func() {
		msg, _ := batch[0]().(setupOAuthCompletedMsg)
		completedCh <- msg
	}()

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	require.False(t, runModel.setupOAuthPending)
	require.Nil(t, runModel.setupOAuthCancel)

	select {
	case msg := <-completedCh:
		require.ErrorIs(t, msg.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for setup oauth cancellation")
	}
}

func TestModel_StartSetupOAuthLoginHandlesUnavailableProvider(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)

	updated, cmd := runModel.startSetupOAuthLogin("")
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "provider selection unavailable", runModel.status.Text())

	restore := stubSetupOAuth(t, newFakeSetupCredentialStore(), nil)
	defer restore()

	updated, cmd = runModel.startSetupOAuthLogin("anthropic")
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.False(t, runModel.setupOAuthPending)
	require.Contains(t, stripANSI(runModel.View().Content), "Subscription login is not available")
	require.Contains(t, stripANSI(runModel.View().Content), "for Anthropic.")
}

func TestModel_SetupOAuthOutputHandlesStaleEmptyAndErrorMessages(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupOAuthPending = true
	runModel.setupOAuthProvider = "anthropic"
	runModel.setupNoticeMessage = "Waiting"

	updated, cmd := runModel.Update(setupOAuthOutputMsg{provider: "openai", line: "ignored"})
	require.Nil(t, cmd)
	require.Equal(t, "Waiting", updated.(model).setupNoticeMessage)

	updated, cmd = runModel.Update(setupOAuthOutputMsg{provider: "anthropic"})
	require.Nil(t, cmd)
	require.Equal(t, "Waiting", updated.(model).setupNoticeMessage)

	updated, cmd = runModel.Update(setupOAuthOutputMsg{provider: "anthropic", err: errors.New("pipe failed")})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.False(t, runModel.setupOAuthPending)
	require.Contains(t, stripANSI(runModel.View().Content), "pipe failed")
}

func TestShortenSetupOAuthOutputLine(t *testing.T) {
	require.Equal(
		t,
		"Open this URL\nURL: https://auth.test/login...",
		shortenSetupOAuthOutput("Open this URL\nhttps://auth.test/login?client_id=long&state=secret\n"),
	)
	require.Equal(t, "", shortenSetupOAuthOutputLine(" "))
	require.Equal(t, "Enter code: ABCD-EFGH", shortenSetupOAuthOutputLine("Enter code: ABCD-EFGH"))
	require.Equal(
		t,
		"URL: https://auth.openai.com/oauth/authorize...",
		shortenSetupOAuthOutputLine(
			"https://auth.openai.com/oauth/authorize?client_id=long&state=secret&redirect_uri=http%3A%2F%2Flocalhost%2Fcallback",
		),
	)
	require.Equal(
		t,
		"URL: https://github.com/login/device",
		shortenSetupOAuthOutputLine("https://github.com/login/device"),
	)
}

func TestReadSetupOAuthOutputCommandHandlesClosedPipe(t *testing.T) {
	reader, writer := io.Pipe()
	require.NoError(t, writer.Close())

	msg := readSetupOAuthOutputCommand("anthropic", reader)()

	outputMsg, ok := msg.(setupOAuthOutputMsg)
	require.True(t, ok)
	require.Equal(t, "anthropic", outputMsg.provider)
	require.Empty(t, outputMsg.line)
	require.NoError(t, outputMsg.err)

	reader, writer = io.Pipe()
	expectedErr := errors.New("pipe failed")
	require.NoError(t, writer.CloseWithError(expectedErr))

	msg = readSetupOAuthOutputCommand("anthropic", reader)()

	outputMsg, ok = msg.(setupOAuthOutputMsg)
	require.True(t, ok)
	require.ErrorIs(t, outputMsg.err, expectedErr)
}

func TestRunSetupOAuthLoginCommandReturnsStoreFailure(t *testing.T) {
	store := newFakeSetupCredentialStore()
	store.err = errors.New("store failed")
	restore := stubSetupOAuth(t, store, fakeSetupSubscriptionProvider{})
	defer restore()
	reader, writer := io.Pipe()
	require.NoError(t, reader.Close())

	msg := runSetupOAuthLoginCommand(
		context.Background(),
		"anthropic",
		fakeSetupSubscriptionProvider{},
		writer,
	)()

	completedMsg, ok := msg.(setupOAuthCompletedMsg)
	require.True(t, ok)
	require.Equal(t, "anthropic", completedMsg.provider)
	require.ErrorContains(t, completedMsg.err, "store failed")
	require.Empty(t, store.credentials)
}

func TestModel_CompleteSetupOAuthLoginIgnoresStaleMessage(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupModelStep = setupModelStepNotice
	runModel.setupOAuthPending = true
	runModel.setupOAuthProvider = "anthropic"

	updated, cmd := runModel.Update(setupOAuthCompletedMsg{provider: "openai"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.setupOAuthPending)
	require.Equal(t, "anthropic", runModel.setupOAuthProvider)
}

func TestRenderProfileModelSetupNoticeMessageStylesProviderCommand(t *testing.T) {
	rendered := renderProfileModelSetupNoticeMessage("run morph provider login anthropic in a new terminal", 80)

	require.Contains(
		t,
		rendered,
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.MarkdownLinkForeground)).
			Render("morph provider login anthropic"),
	)
	require.Contains(t, stripANSI(rendered), "run morph provider login anthropic in a new terminal")
	require.Equal(
		t,
		"first line\nsecond line",
		stripANSI(renderProfileModelSetupNoticeMessage("first line\nsecond line", 80)),
	)
	require.Empty(t, renderProfileModelSetupNoticeMessage("", 80))
	require.Empty(t, getProfileModelSetupNoticeProviderCommand("run something else"))
}

func TestModel_SetupOpenRouterSelectionSetsEmbeddingModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    embedding:
        provider: openrouter
        name: ""
search:
    vector:
        enabled: true
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "openrouter")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("router-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupModel(t, &runModel, "openai/gpt-4o-mini")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.True(t, cfg.Search.Vector.Enabled)
	require.Equal(t, "openrouter", cfg.Models.Embedding.Provider)
	require.Equal(t, "text-embedding-3-small", cfg.Models.Embedding.Name)
	require.Equal(t, "openrouter", runModel.runtimeInfo.EmbeddingProvider)
	require.Equal(t, "text-embedding-3-small", runModel.runtimeInfo.EmbeddingModel)
	runModel.width = 180
	require.Contains(t, stripANSI(runModel.renderHeaderInfoPanel()), "embedding: text-embedding-3-small")
}

func TestModel_SetupOpenAISelectionSetsEffectiveEmbeddingModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    embedding:
        provider: ""
        name: ""
search:
    vector:
        enabled: true
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "openai")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("openai-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupModel(t, &runModel, "gpt-5.5")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.True(t, cfg.Search.Vector.Enabled)
	require.Equal(t, "openai", cfg.Models.Embedding.Provider)
	require.Equal(t, "text-embedding-3-small", cfg.Models.Embedding.Name)
	require.Equal(t, "openai", runModel.runtimeInfo.EmbeddingProvider)
	require.Equal(t, "text-embedding-3-small", runModel.runtimeInfo.EmbeddingModel)
	runModel.width = 180
	require.Contains(t, stripANSI(runModel.renderHeaderInfoPanel()), "embedding: text-embedding-3-small")
}

func TestModel_SetupOtherProviderSelectionDisablesVector(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
    embedding:
        provider: openrouter
        name: text-embedding-3-small
search:
    vector:
        enabled: true
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupProvider(t, &runModel, "anthropic")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	runModel.apiKeyInput.SetValue("anthropic-key")
	updated, _ = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	selectSetupModel(t, &runModel, "claude-sonnet-4-6")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.shouldShowProfileModelSetup())
	cfg, err := config.Load("", filepath.Join(home, "config.yaml"))
	require.NoError(t, err)
	require.False(t, cfg.Search.Vector.Enabled)
}

func TestModel_SetupProviderWheelMovesSelection(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	selectSetupAuthMethod(t, &runModel, setupAuthMethodAPIKey)
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)
	require.Greater(t, len(runModel.setupProviders), 1)
	require.Equal(t, 0, runModel.setupItemSelected)

	updated, cmd := runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	require.Nil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, 1, runModel.setupItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	require.Nil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, 0, runModel.setupItemSelected)
}

func TestGetProfileModelSetupProviderDescription(t *testing.T) {
	require.Equal(t, "Use your Anthropic subscription", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: "anthropic"}, setupAuthMethodSubscription))
	require.Equal(t, "Use your Anthropic API key", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: "anthropic"}, setupAuthMethodAPIKey))
	require.Equal(t, "Use your OpenAI account", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: "openai-codex"}, setupAuthMethodSubscription))
	require.Equal(t, "Use your GitHub Copilot subscription", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: "github-copilot"}, setupAuthMethodSubscription))
	require.Equal(t, "Use your OpenAI API key", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: "openai"}, setupAuthMethodAPIKey))
	require.Equal(t, "Use your OpenRouter API key", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{ID: "openrouter"}, setupAuthMethodAPIKey))
	require.Equal(t, "Use your Custom account", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{
		ID:            "custom",
		Name:          "Custom",
		SupportsOAuth: true,
	}, setupAuthMethodSubscription))
	require.Equal(t, "Use your Custom API key", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{
		ID:             "custom",
		Name:           "Custom",
		SupportsAPIKey: true,
	}, setupAuthMethodAPIKey))
	require.Equal(t, "manual setup", getProfileModelSetupProviderDescription(rpcclient.ProviderOption{
		ID:       "custom",
		Name:     "Custom",
		AuthType: "manual setup",
	}, ""))
}

func TestProfileModelSetupHelpers(t *testing.T) {
	require.Equal(t, "option-provider", getSetupModelProvider("", rpcclient.ModelOption{Provider: "option-provider"}))
	require.False(t, isEmbeddingSetupError(nil))
	require.False(t, isEmbeddingSetupError(errors.New("model API key is required")))
	require.True(t, isEmbeddingSetupError(errors.New("embedding model is required")))
	require.True(t, isEmbeddingSetupError(errors.New("embedding API key is required")))
	require.Contains(t, getEmbeddingSetupInstruction(), "search.vector.enabled false")
	require.Empty(t, renderProfileModelSetupPaddedLabel("ABC", 1))
	require.Equal(t, "\n", renderProfileModelSetupProviderRow(rpcclient.ProviderOption{Name: "ABC", AuthType: "DEF"}, "", 1, false))
}

func TestModel_SubmitsNamePromptSkipsModelSetupWhenConfigured(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: openrouter
        name: openai/gpt-4o-mini
search:
    vector:
        enabled: false
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.False(t, runModel.commandView.Visible)
	require.False(t, runModel.shouldShowProfileModelSetup())
}

func TestModel_SubmitNamePromptRejectsInvalidName(t *testing.T) {
	now := time.Date(2026, 5, 28, 20, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	currentTime = func() time.Time {
		return now
	}
	home := t.TempDir()
	setActiveTestProfile(t, home)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy Okpala!")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	_, err := os.Stat(filepath.Join(home, userNameFilename))
	require.True(t, os.IsNotExist(err))
	require.True(t, runModel.shouldShowNamePrompt())
	require.Empty(t, runModel.userName)
	require.Equal(t, defaultStatus, runModel.status.Text())
	require.Contains(t, stripANSI(runModel.View().Content), namePromptInvalidHint)

	require.Equal(t, namePromptErrorExpiredMsg{startedAt: now}, cmd())
	updated, cmd = runModel.Update(namePromptErrorExpiredMsg{startedAt: now})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.namePromptError)
	require.Contains(t, stripANSI(runModel.View().Content), namePromptSubmitHint)
	require.NotContains(t, stripANSI(runModel.View().Content), namePromptInvalidHint)
}

func TestModel_NamePromptAllowsCtrlCExit(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	runModel := newModel()

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, "Press Ctrl-C again to exit", runModel.status.Text())
	require.True(t, runModel.shouldShowNamePrompt())
}

func TestModel_InitLoadsExistingSessionTimeline(t *testing.T) {
	client := &fakeTUIChatClient{
		timeline: rpcclient.SessionTimeline{
			SessionID: "default",
			Messages: []agentapi.SessionTimelineMessage{{
				Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "older prompt"},
			}},
		},
	}
	runModel := newModelWithClient(client)

	cmd := runModel.Init()

	require.NotNil(t, cmd)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	loaded, ok := batch[1]().(sessionTimelineLoadedMsg)
	require.True(t, ok)

	require.Equal(t, "default", loaded.Timeline.SessionID)
	require.Len(t, loaded.Timeline.Messages, 1)
	require.Equal(t, 1, client.timelineCalls)
	require.Equal(t, defaultSessionID, client.usedSessionID)
	require.Equal(t, defaultSessionID, client.timelineSessionID)
}

func TestModel_InitLoadsSessionContextUsage(t *testing.T) {
	client := &fakeTUIChatClient{
		timeline: rpcclient.SessionTimeline{SessionID: "default"},
		contextStatus: rpcclient.ContextStatus{
			SessionID: "default",
			Length:    128000,
			Used:      64000,
			UsedPct:   0.5,
		},
	}
	runModel := newModelWithClient(client)

	cmd := runModel.Init()

	require.NotNil(t, cmd)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	timelineMsg, ok := batch[1]().(sessionTimelineLoadedMsg)
	require.True(t, ok)
	updated, cmd := runModel.Update(timelineMsg)
	require.NotNil(t, cmd)

	loadedBatch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	loaded, ok := loadedBatch[0]().(sessionContextLoadedMsg)
	require.True(t, ok)
	runModel = updated.(model)

	require.Equal(t, "default", client.contextSessionID)
	require.Equal(t, 1, client.contextCalls)
	require.Equal(t, 64000, loaded.Status.Used)
	require.Equal(t, defaultSessionID, runModel.sessionID)
}

func TestModel_InitRestoresRememberedActiveSession(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, saveLastSessionID("session-saved"))
	client := &fakeTUIChatClient{
		sessions: []storage.Session{
			{ID: defaultSessionID},
			{ID: "session-saved", Title: "Saved Chat"},
		},
		timeline:      rpcclient.SessionTimeline{SessionID: "session-saved", Title: "Saved Chat"},
		contextStatus: rpcclient.ContextStatus{SessionID: "session-saved"},
	}
	runModel := newModelWithClient(client)

	cmd := runModel.Init()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	timelineMsg, ok := batch[1]().(sessionTimelineLoadedMsg)
	require.True(t, ok)
	updated, contextCmd := runModel.Update(timelineMsg)
	runModel = updated.(model)

	require.NotNil(t, contextCmd)
	require.Equal(t, "session-saved", client.usedSessionID)
	require.Equal(t, "session-saved", client.timelineSessionID)
	require.Equal(t, "session-saved", runModel.sessionID)
	require.Equal(t, "Saved Chat", runModel.sessionTitle)

	rememberedID, err := loadLastSessionID()
	require.NoError(t, err)
	require.Equal(t, "session-saved", rememberedID)
}

func TestModel_InitFallsBackToDefaultWhenRememberedSessionIsNotActive(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, saveLastSessionID("session-archived"))
	client := &fakeTUIChatClient{
		sessions: []storage.Session{{ID: defaultSessionID}},
		timeline: rpcclient.SessionTimeline{SessionID: defaultSessionID},
	}
	runModel := newModelWithClient(client)

	cmd := runModel.Init()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	timelineMsg, ok := batch[1]().(sessionTimelineLoadedMsg)
	require.True(t, ok)
	updated, _ := runModel.Update(timelineMsg)
	runModel = updated.(model)

	require.Equal(t, 1, client.listSessionCalls)
	require.Equal(t, defaultSessionID, client.usedSessionID)
	require.Equal(t, defaultSessionID, client.timelineSessionID)
	require.Equal(t, defaultSessionID, runModel.sessionID)

	rememberedID, err := loadLastSessionID()
	require.NoError(t, err)
	require.Equal(t, defaultSessionID, rememberedID)
}

func TestLoadSessionTimelineCmdReturnsLoadFailure(t *testing.T) {
	expectedErr := errors.New("timeline unavailable")
	client := &fakeTUIChatClient{timelineErr: expectedErr}

	cmd := loadSessionTimelineCmd(context.Background(), client, "session-a")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadFailedMsg{Err: expectedErr}, cmd())
	require.Equal(t, "session-a", client.timelineSessionID)
}

func TestFormatSessionContextUsageUsesStatusValues(t *testing.T) {
	status := rpcclient.ContextStatus{
		Length:  128000,
		Used:    64000,
		UsedPct: 0.5,
	}

	require.Equal(t, "64,000 used · 50%", formatSessionContextUsage(status))
}

func TestFormatSessionContextUsageComputesMissingPercent(t *testing.T) {
	status := rpcclient.ContextStatus{
		Length: 200000,
		Used:   130000,
	}

	require.Equal(t, "130,000 used · 65%", formatSessionContextUsage(status))
}

func TestFormatSessionContextUsageLabelsEstimatedUsage(t *testing.T) {
	status := rpcclient.ContextStatus{
		Length:      128000,
		Used:        32000,
		UsedPct:     0.25,
		UsageSource: "estimated",
	}

	require.Equal(t, "~32,000 used · ~25%", formatSessionContextUsage(status))
}

func TestLoadSessionTitleCmdReturnsLoadedTitle(t *testing.T) {
	client := &fakeTUIChatClient{
		currentSession: storage.Session{
			ID:    "default",
			Title: "Daily Planning",
		},
	}

	cmd := loadSessionTitleCmd(context.Background(), client)

	require.NotNil(t, cmd)
	require.Equal(t, sessionTitleLoadedMsg{Session: client.currentSession}, cmd())
	require.Equal(t, 1, client.currentSessionCalls)
}

func TestModel_UpdateHydratesLoadedSessionTimeline(t *testing.T) {
	runModel := newModel()
	oldCell := assistantTranscriptCell{text: "stale answer"}
	runModel.messages = []transcriptCell{oldCell}
	runModel.setTranscriptContent()
	oldCacheKey := getTranscriptCellRenderCacheKeyForModel(&runModel, oldCell)
	_, oldCellCached := runModel.transcriptCache.get(oldCacheKey)
	require.True(t, oldCellCached)
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	originalTime := currentTime
	currentTime = func() time.Time { return now }
	t.Cleanup(func() { currentTime = originalTime })

	updated, cmd := runModel.Update(sessionTimelineLoadedMsg{
		Timeline: rpcclient.SessionTimeline{
			SessionID: "default",
			Title:     "Daily Planning",
			Messages: []agentapi.SessionTimelineMessage{{
				Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "older answer"},
			}},
			TraceEvents: []agentapi.SessionTimelineTraceEvent{{
				Event: agentsession.TraceEvent{
					Type:      trace.EvtContextCompactionSucceeded,
					Timestamp: now,
					Payload: trace.CompactionEventPayload{
						SessionID: "default",
						Status:    string(storage.CompactionStatusSucceeded),
						Auto:      true,
					},
				},
			}},
		},
	})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, []string{"Automatic compaction completed", "Morph: older answer"}, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.transcript.View()), "older answer")
	require.Equal(t, defaultSessionID, runModel.sessionID)
	require.Equal(t, "Daily Planning (default)", runModel.sessionTitle)
	require.Contains(t, transcriptCellPlainTexts(runModel.messages), "Automatic compaction completed")
	require.Contains(t, stripANSI(runModel.View().Content), "Automatic Compaction")
	_, oldCellCached = runModel.transcriptCache.get(oldCacheKey)
	require.False(t, oldCellCached)
	require.Equal(t, defaultStatus, runModel.status.Text())
}

func TestModel_UpdateHydratesOnlyTailWindowForLongSession(t *testing.T) {
	runModel := newModel()
	runModel.height = 20
	runModel.resize()
	messages := make([]agentapi.SessionTimelineMessage, 2_000)
	for index := range messages {
		messages[index] = agentapi.SessionTimelineMessage{Message: morphmsg.Message{
			Role: morphmsg.RoleAssistant, Content: fmt.Sprintf("history-%04d", index),
		}}
	}

	updated, cmd := runModel.Update(sessionTimelineLoadedMsg{Timeline: rpcclient.SessionTimeline{
		SessionID: "default",
		Messages:  messages,
	}})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Len(t, runModel.messages, 2_000)
	require.Less(t, runModel.transcriptCache.len(), 100)
	require.Contains(t, stripANSI(runModel.transcript.View()), "history-1999")
	require.NotContains(t, stripANSI(runModel.transcript.GetContent()), "history-0000")
}

func TestModel_ApplyTUIMessageRendersLiveAutoCompactionTrace(t *testing.T) {
	runModel := newModel()

	cmd := runModel.applyTUIMessage(manualCompactionMsg{
		State: manualCompactionState{Status: "succeeded", Label: autoCompactionLabel},
	})

	require.Nil(t, cmd)
	require.Equal(t, []string{"Automatic compaction completed"}, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.View().Content), "Automatic Compaction")
}

func TestModel_ApplyTUIMessageRefreshesContextAfterCompaction(t *testing.T) {
	client := &fakeTUIChatClient{contextStatus: rpcclient.ContextStatus{
		SessionID:   "default",
		Used:        77509,
		Length:      128000,
		UsedPct:     float64(77509) / 128000,
		UsageSource: "estimated",
	}}
	runModel := newModelWithClient(client)
	runModel.sessionID = "default"

	cmd := runModel.applyTUIMessage(manualCompactionMsg{
		State: manualCompactionState{Status: "succeeded", Label: autoCompactionLabel},
	})

	require.NotNil(t, cmd)
	message, ok := cmd().(sessionContextLoadedMsg)
	require.True(t, ok)
	updated, _ := runModel.Update(message)
	runModel = updated.(model)
	require.Equal(t, 1, client.contextCalls)
	require.Equal(t, "~77,509 used · ~61%", runModel.context)
}

func TestModel_UpdateReportsTimelineLoadFailure(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.Update(sessionTimelineLoadFailedMsg{Err: errors.New("timeline unavailable")})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "session timeline unavailable", runModel.status.Text())
	require.Contains(t, stripANSI(runModel.View().Content), emptyUserPromptQuestion)
}

func TestModel_InitSchedulesLoadedTransientStatusExpiration(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	currentTime = func() time.Time {
		return now
	}

	runModel := newModel()
	setStatusTransient(&runModel.status, "loaded")
	cmd := runModel.statusExpireCmd()

	require.NotNil(t, cmd)
}

func TestModel_StatusExpireCmdFallsBackToDefaultWindow(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	currentTime = func() time.Time {
		return now
	}

	runModel := newModel()
	runModel.status.SetHideAfter(0)
	setStatusTransient(&runModel.status, "loaded")
	cmd := runModel.statusExpireCmd()

	require.NotNil(t, cmd)
}

func TestModel_StatusExpireCmdReturnsExpirationMessage(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	currentTime = func() time.Time {
		return now
	}

	runModel := newModel()
	runModel.status.SetHideAfter(time.Nanosecond)
	setStatusTransient(&runModel.status, "loaded")
	cmd := runModel.statusExpireCmd()

	require.NotNil(t, cmd)
	require.Equal(t, statusExpiredMsg{startedAt: now}, cmd())
}

func TestModel_ViewRendersHeaderInfoPanelWhenWide(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Models.Main.Provider = "openai"
	cfg.Models.Main.Name = "openai/gpt-4o-mini"
	cfg.Models.Summary.Provider = "openrouter"
	cfg.Models.Summary.Name = "openai/gpt-4o"
	cfg.Models.Embedding.Provider = "openai"
	cfg.Models.Embedding.Name = "openai/text-embedding-3-large"
	cfg.Storage.Backend = "memory"
	runModel := newModelWithClientContextAndConfig(context.Background(), nil, cfg)
	runModel.runtimeInfo.Profile = "work"
	runModel.width = 180
	runModel.resize()
	content := stripANSI(runModel.renderHeader())

	require.Contains(t, content, "Welcome, Kennedy")
	require.Contains(t, content, "Use /changelog to see what changed")
	require.Contains(t, content, "version: dev")
	require.Contains(t, content, "commit: unknown")
	require.Contains(t, content, "profile: work")
	require.Contains(t, content, "session: default")
	require.Contains(t, content, "provider: openai")
	require.Contains(t, content, "model: gpt-4o-mini")
	require.Contains(t, content, "summary: gpt-4o")
	require.Contains(t, content, "embedding: text-embedding-3-large")
	require.Contains(t, content, "storage: memory")
	require.Contains(t, content, "streaming: on")
	require.NotContains(t, content, "summary: openai/gpt-4o")
}

func TestModel_RenderNoticeBarFillsRow(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	lines := strings.Split(stripANSI(runModel.renderNoticeBar()), "\n")

	require.Len(t, lines, noticeBarHeight)
	require.Contains(t, lines[0], "Welcome, Kennedy")
	require.Contains(t, lines[0], "Use /changelog to see what changed")
	require.Equal(t, 80, lipgloss.Width(lines[0]))
}

func TestModel_RenderNoticeBarUsesConfiguredColors(t *testing.T) {
	content := newModel().renderNoticeBar()

	require.Contains(t, content, "48;5;235")
	require.Contains(t, renderNoticeBarLeft(), "38;5;246")
	require.Contains(t, renderNoticeBarLeft(), "97")
	require.Contains(t, renderNoticeBarRight(), "38;5;246")
	require.Contains(t, renderNoticeBarRight(), "97")
}

func TestRenderNoticeBarContent_HidesRightTextWhenTooNarrow(t *testing.T) {
	content := stripANSI(renderNoticeBarContent("Welcome", "Use /changelog", 8))

	require.Equal(t, "Welcome", content)
}

func TestRenderNoticeBarContent_HidesRightTextWhenMissing(t *testing.T) {
	content := stripANSI(renderNoticeBarContent("Welcome", " ", 80))

	require.Equal(t, "Welcome", content)
}

func TestRenderNoticeBarContent_FillsWidthWithSpacer(t *testing.T) {
	content := stripANSI(renderNoticeBarContent("Welcome", "Use /changelog", 30))

	require.Equal(t, "Welcome         Use /changelog", content)
	require.Equal(t, 30, lipgloss.Width(content))
}

func TestModel_ViewAlignsHeaderInfoKeys(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.resize()
	lines := strings.Split(stripANSI(runModel.renderHeaderInfoPanel()), "\n")
	rows := getHeaderInfoRows(runModel)
	columnWidth := getHeaderInfoColumnWidth(rows)
	leftColonIndex := headerInfoKeyWidth
	rightColonIndex := columnWidth + headerInfoColumnGap + headerInfoKeyWidth

	require.Len(t, lines, (len(rows)+1)/2)
	for _, line := range lines {
		lineValue := str.String(line)
		if lineValue.Trim() == "" {
			continue
		}
		require.Equal(t, leftColonIndex, strings.Index(line, ":"))
		if strings.Count(line, ":") > 1 {
			require.Equal(t, rightColonIndex, strings.LastIndex(line, ":"))
		}
	}
}

func TestModel_ViewPlacesProviderAboveModelInfo(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.runtimeInfo.Provider = "openrouter"
	runModel.runtimeInfo.Streaming = "on"
	runModel.resize()

	lines := strings.Split(stripANSI(runModel.renderHeaderInfoPanel()), "\n")

	require.GreaterOrEqual(t, len(lines), 5)
	require.Contains(t, lines[0], "version:")
	require.Contains(t, lines[0], "provider: openrouter")
	require.Contains(t, lines[1], "commit:")
	require.Contains(t, lines[1], "model:")
	require.Contains(t, lines[4], "streaming: on")
	require.Contains(t, lines[4], "storage:")
}

func TestRenderHeaderInfoPanel_UsesOneColorForBothColumns(t *testing.T) {
	panel := getHeaderPanel(newModel(), 180)
	content := renderHeaderInfoPanel(panel)
	modelCell := renderBottomStatusMutedCell("model")

	require.Equal(t, lipgloss.Height(content), strings.Count(content, "\x1b[90m"))
	require.Contains(t, modelCell, "\x1b[90m")
	require.NotContains(t, content, "38;5;"+defaultTUITheme.ToolDetail)
	require.Contains(t, stripANSI(content), "version:")
	require.Contains(t, stripANSI(content), "model:")
}

func TestModel_ViewSizesHeaderInfoPanelToValues(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.resize()
	content := stripANSI(runModel.renderHeaderInfoPanel())
	columnWidth := headerInfoKeyWidth + 2 + lipgloss.Width("text-embedding-3-small")

	require.Equal(t, columnWidth*2+headerInfoColumnGap, lipgloss.Width(content))
}

func TestModel_ViewVerticallyCentersHeaderInfoPanel(t *testing.T) {
	panel := alignHeaderInfoPanel("one\ntwo", 4)
	lines := strings.Split(panel, "\n")

	require.Len(t, lines, 4)
	require.Equal(t, "", lines[0])
	require.Equal(t, "one", lines[1])
	require.Equal(t, "two", lines[2])
	require.Equal(t, "", lines[3])
}

func TestGetModelDisplayName_RemovesProviderPrefix(t *testing.T) {
	require.Equal(t, "gpt-4o-mini", getModelDisplayName("openai/gpt-4o-mini"))
	require.Equal(t, "GPT 5.5", getModelDisplayName(" GPT 5.5 "))
}

func TestGetRuntimeModelDisplayName_UsesCatalogName(t *testing.T) {
	require.Equal(t, "GPT-5.5", getRuntimeModelDisplayName("openai-codex", "gpt-5.5"))
	require.Equal(t, "OpenAI: GPT-5.5", getRuntimeModelDisplayName("openrouter", "openai/gpt-5.5"))
	require.Equal(t, "custom-model", getRuntimeModelDisplayName("custom", "owner/custom-model"))
}

func TestGetMorphBannerColor_UsesLastColorForOutOfRangeIndex(t *testing.T) {
	require.Equal(t, morphBannerColors[len(morphBannerColors)-1], getMorphBannerColor(-1))
	require.Equal(t, morphBannerColors[len(morphBannerColors)-1], getMorphBannerColor(len(morphBannerColors)))
}

func TestModel_ViewKeepsBannerFullWhenInfoPanelWouldClipIt(t *testing.T) {
	runModel := newModel()
	banner := getHeaderBrandText(runModel.runtimeInfo)
	runModel.width = lipgloss.Width(banner) + headerGapWidth + getHeaderInfoWidth(getHeaderInfoRows(runModel)) - 1
	runModel.resize()
	content := stripANSI(runModel.renderHeader())

	require.Contains(t, content, "Morph")
	require.Contains(t, content, "dev (unknown)")
	require.NotContains(t, content, "provider: openrouter")
}

func TestModel_ViewShowsHeaderMarkNextToFullBanner(t *testing.T) {
	runModel := newModel()
	banner := getHeaderBrandText(runModel.runtimeInfo)
	runModel.width = lipgloss.Width(morphHeaderMark) + headerGapWidth + lipgloss.Width(banner) + headerBodyPadding*2
	runModel.resize()
	content := stripANSI(runModel.renderHeader())

	require.Contains(t, content, "░████████  Morph")
	require.Contains(t, content, "dev (unknown)")
	require.Contains(t, content, "░█░█░█▀")
	require.Contains(t, content, "   █ █ █")
	require.Contains(t, content, "   ▀▀▀▀▀  ")
}

func TestModel_ViewHidesHeaderMarkWhenFullBannerWouldClip(t *testing.T) {
	runModel := newModel()
	banner := getHeaderBrandText(runModel.runtimeInfo)
	runModel.width = lipgloss.Width(morphHeaderMark) + headerGapWidth + lipgloss.Width(banner) + headerBodyPadding*2 - 1
	runModel.resize()
	content := stripANSI(runModel.renderHeader())

	require.Contains(t, content, "Morph")
	require.Contains(t, content, "dev (unknown)")
	require.NotContains(t, content, "░█░█░█▀")
}

func TestModel_ViewUsesCompactBannerWhenFullBannerDoesNotFit(t *testing.T) {
	runModel := newModel()
	banner := getHeaderBrandText(runModel.runtimeInfo)
	runModel.width = lipgloss.Width(banner) - 1
	runModel.resize()
	content := stripANSI(runModel.renderHeader())

	require.Contains(t, content, "Morph")
	require.NotContains(t, content, "dev (unknown)")
}

func TestRenderHeaderBody_FillsAvailableWidthWhenInfoIsVisible(t *testing.T) {
	runModel := newModel()
	panel := getHeaderPanel(runModel, 120)
	content := stripANSI(renderHeaderBody(panel))

	for _, line := range strings.Split(content, "\n") {
		lineValue2 := str.String(line)
		if lineValue2.Trim() == "" {
			continue
		}
		require.Equal(t, panel.Width, lipgloss.Width(line))
	}
}

func TestRenderHeaderBody_InsetsBannerAndInfo(t *testing.T) {
	runModel := newModel()
	panel := getHeaderPanel(runModel, 120)
	content := stripANSI(renderHeaderBody(panel))

	for _, line := range strings.Split(content, "\n") {
		lineValue3 := str.String(line)
		if lineValue3.Trim() == "" {
			continue
		}
		require.True(t, strings.HasPrefix(line, " "))
		require.True(t, strings.HasSuffix(line, " "))
	}
}

func TestModel_ViewRendersBottomStatusPanelBelowComposer(t *testing.T) {
	runModel := newModel()
	content := stripANSI(runModel.View().Content)
	inputIndex := strings.Index(content, inputPrompt+"Ask Morph...")
	infoIndex := strings.LastIndex(content, statusReadySuffix)

	require.NotEqual(t, -1, inputIndex)
	require.NotEqual(t, -1, infoIndex)
	require.Greater(t, infoIndex, inputIndex)
}

func TestModel_RenderBottomStatusPanelUsesCatalogModelName(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Models.Main.Provider = "openai-codex"
	cfg.Models.Main.Name = "gpt-5.5"
	runModel := newModelWithClientContextAndConfig(context.Background(), nil, cfg)

	require.Equal(t, "gpt-5.5", runModel.runtimeInfo.Model)
	require.Equal(t, "GPT-5.5", runModel.modelName)
	require.Contains(t, stripANSI(runModel.renderBottomStatusPanel()), "GPT-5.5")
}

func TestModel_RenderInputUsesCompleteComposerFrame(t *testing.T) {
	runModel := newModel()
	runModel.width = 40
	runModel.resize()

	lines := strings.Split(stripANSI(runModel.renderInput()), "\n")

	require.GreaterOrEqual(t, len(lines), 3)
	require.True(t, strings.HasPrefix(lines[0], "╭"))
	require.True(t, strings.HasSuffix(strings.TrimRight(lines[0], " "), "╮"))
	require.True(t, strings.HasPrefix(lines[1], "│"))
	require.True(t, strings.HasSuffix(strings.TrimRight(lines[1], " "), "│"))
	require.Contains(t, lines[1], inputPrompt+"Ask Morph...")
	require.True(t, strings.HasPrefix(lines[2], "╰"))
	require.True(t, strings.HasSuffix(strings.TrimRight(lines[2], " "), "╯"))
}

func TestRenderComposerInputPrompt_HasNoBackgroundColor(t *testing.T) {
	prompt := renderComposerInputPrompt()

	require.Contains(t, stripANSI(prompt), inputPrompt)
	require.NotContains(t, prompt, "[48;")
}

func TestModel_RenderBottomStatusPanelMovesContextToRight(t *testing.T) {
	runModel := newModel()
	runModel.modelName = "openai/gpt-4o-mini"
	runModel.context = "64,000 used · 50%"
	content := stripANSI(runModel.renderBottomStatusPanel())

	require.True(t, strings.HasPrefix(content, " "))
	require.Equal(t, runModel.width, lipgloss.Width(content))
	require.Contains(t, content, "gpt-4o-mini")
	require.NotContains(t, content, "default session")
	require.Contains(t, content, "64,000")
	require.Contains(t, content, "used · 50%")
	require.GreaterOrEqual(t, strings.Count(content, "  "), 1)
	require.Greater(t, strings.Index(content, "64,000"), strings.Index(content, "gpt-4o-mini"))
}

func TestModel_RenderBottomStatusPanelWarnsWhenFullAccessIsEnabled(t *testing.T) {
	runModel := newModelWithClientContextAndConfig(
		context.Background(),
		nil,
		&config.Config{Permissions: permissions.Policy{Preset: permissions.PresetFullAccess}},
	)

	content := stripANSI(runModel.renderBottomStatusPanel())

	require.True(t, runModel.fullAccess)
	require.Contains(t, content, permissionStatusIcon+" Full access (unsafe)")
}

func TestModel_RenderBottomStatusPanelShowsCustomPermissionPreset(t *testing.T) {
	runModel := newModelWithClientContextAndConfig(
		context.Background(),
		nil,
		&config.Config{Permissions: permissions.Policy{Preset: permissions.PresetCustom}},
	)

	content := stripANSI(runModel.renderBottomStatusPanel())

	require.Equal(t, permissions.PresetCustom, runModel.permissionPreset)
	require.Contains(t, content, permissionStatusIcon+" Custom")
}

func TestModel_RenderBottomStatusPanelShowsCustomizedPreset(t *testing.T) {
	runModel := newModelWithClientContextAndConfig(
		context.Background(),
		nil,
		&config.Config{Permissions: permissions.Policy{
			Preset: permissions.PresetApproveForMe,
			Rules:  []permissions.Rule{{Name: "allow clock", Decision: permissions.DecisionAllow}},
		}},
	)

	content := stripANSI(runModel.renderBottomStatusPanel())

	require.Contains(t, content, permissionStatusIcon+" Approve for me (customized)")
}

func TestModel_RenderBottomStatusPanelShowsThinkingBeforeModel(t *testing.T) {
	runModel := newModel()
	runModel.modelName = "openai/gpt-4o-mini"
	runModel.responding = true

	content := stripANSI(runModel.renderBottomStatusPanel())

	require.Contains(t, content, "Thinking")
	require.Contains(t, content, "gpt-4o-mini")
	require.Less(t, strings.Index(content, "Thinking"), strings.Index(content, "gpt-4o-mini"))
}

func TestModel_RenderBottomStatusPanelHidesThinkingWhenNotThinking(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.live = assistantTranscriptCell{text: "hello"}

	content := stripANSI(runModel.renderBottomStatusPanel())

	require.NotContains(t, content, "Thinking")
}

func TestModel_RenderBottomStatusPanelShowsThinkingWhenComposerAnimationDisabled(t *testing.T) {
	disabled := false
	runModel := newModelWithClientContextAndConfig(
		context.Background(),
		nil,
		&config.Config{TUI: config.TUIConfig{ThinkingComposer: &disabled}},
	)
	runModel.responding = true

	content := stripANSI(runModel.renderBottomStatusPanel())

	require.False(t, runModel.isThinkingComposerVisible())
	require.True(t, runModel.isModelThinking())
	require.Contains(t, content, "Thinking")
}

func TestModel_RenderBottomStatusPanelAnimatesThinkingStatus(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.thinkingComposerFrame = 0
	first := runModel.renderBottomStatusPanel()

	runModel.thinkingComposerFrame = 1
	second := runModel.renderBottomStatusPanel()

	require.Contains(t, stripANSI(first), "Thinking")
	require.Contains(t, stripANSI(second), "Thinking")
	require.NotEqual(t, first, second)
}

func TestGetThinkingStatusColor_UsesGrayBaseAndThreeCharacterShimmer(t *testing.T) {
	require.Equal(t, thinkingStatusShimmerColor, getThinkingStatusColor(0, 0, len("Thinking")))
	require.Equal(t, thinkingStatusEdgeColor, getThinkingStatusColor(1, 0, len("Thinking")))
	require.Equal(t, thinkingStatusEdgeColor, getThinkingStatusColor(len("Thinking")-1, 0, len("Thinking")))
	require.Equal(t, thinkingStatusBaseColor, getThinkingStatusColor(2, 0, len("Thinking")))
	require.Equal(t, thinkingStatusShimmerColor, getThinkingStatusColor(1, 1, len("Thinking")))
	require.Equal(t, thinkingStatusShimmerColor, getThinkingStatusColor(len("Thinking")-1, -1, len("Thinking")))
	require.Equal(t, thinkingStatusBaseColor, getThinkingStatusColor(0, 0, 0))
}

func TestModel_RenderBottomStatusPanelKeepsMutedCellsWhenThinking(t *testing.T) {
	runModel := newModel()
	runModel.modelName = "openai/gpt-4o-mini"
	runModel.responding = true

	content := runModel.renderBottomStatusPanel()

	require.Contains(t, content, renderBottomStatusMutedCell("openai/gpt-4o-mini"))
	require.Contains(t, content, renderBottomStatusMutedCell(statusCancelSuffix))
	require.NotContains(t, stripANSI(content), defaultSessionTitle)
}

func TestGetPanelHorizontalPadding_DisablesPaddingWhenNarrow(t *testing.T) {
	require.Equal(t, 0, getPanelHorizontalPadding(2))
	require.Equal(t, panelHorizontalPadding, getPanelHorizontalPadding(3))
}

func TestJoinBottomStatusPanelSegments_HandlesEmptySingleAndNarrowValues(t *testing.T) {
	require.Empty(t, joinBottomStatusPanelSegments([]string{" ", ""}, 20))
	require.Equal(t, "enter to send · ctrl+c to quit", joinBottomStatusPanelSegments([]string{"enter to send · ctrl+c to quit"}, 40))
	require.Equal(t, "model · status", joinBottomStatusPanelSegments([]string{"model", "status"}, 5))
}

func TestSpaceBetweenBottomStatusPanel_HandlesMissingAndNarrowSides(t *testing.T) {
	require.Equal(t, "right", spaceBetweenBottomStatusPanel("", "right", 20))
	require.Equal(t, "left · right", stripANSI(spaceBetweenBottomStatusPanel("left", "right", 1)))
}

func TestCompactTranscriptSelectionBlankLines_CollapsesVisualPaddingRuns(t *testing.T) {
	require.Equal(t,
		"❯ first\n\nMorph: second",
		compactTranscriptSelectionBlankLines("❯ first\n\n\nMorph: second"),
	)
	require.Equal(t,
		"❯ first\n\nMorph: second",
		compactTranscriptSelectionBlankLines("❯ first\n"+strings.Repeat("▄", 40)+"\n"+strings.Repeat("▀", 40)+"\n\nMorph: second"),
	)
}

func TestModel_UpdateResizesTranscriptAndInput(t *testing.T) {
	runModel := newModel()
	updated, cmd := runModel.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Nil(t, cmd)

	resized := updated.(model)
	mainWidth := resized.getMainPaneWidth()
	require.Equal(t, 100, resized.width)
	require.Equal(t, 30, resized.height)
	require.Equal(t, mainWidth, resized.transcript.Width())
	require.LessOrEqual(t, resized.input.Width(), mainWidth)
	require.GreaterOrEqual(t, resized.transcript.Height(), 1)
	require.Equal(t, 1, resized.input.Height())
	lines := strings.Split(stripANSI(resized.transcript.GetContent()), "\n")
	require.NotEmpty(t, lines)
	require.Equal(t, mainWidth, lipgloss.Width(lines[0]))
	require.Contains(t, stripANSI(resized.View().Content), emptyUserPromptQuestion)
}

func TestModel_UpdateScrollsTranscriptWithPagingKeys(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	bottomOffset := runModel.transcript.YOffset()
	bottomView := runModel.transcript.View()
	require.Greater(t, bottomOffset, 0)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.NotEqual(t, bottomView, runModel.transcript.View())

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Greater(t, runModel.transcript.YOffset(), 0)

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyHome})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.transcript.YOffset())

	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnd})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, bottomOffset, runModel.transcript.YOffset())
}

func TestModel_UpdateScrollsHeaderWithTranscript(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.renderTranscriptWindowIntoViewport(transcriptWindowHead)
	runModel.transcript.GotoTop()
	require.Contains(t, stripANSI(runModel.transcript.View()), "Welcome, Kennedy")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Welcome, Kennedy")
	require.Contains(t, stripANSI(runModel.renderTranscriptContent()), "Welcome, Kennedy")
}

func TestModel_RenderTranscriptContentPreservesMainPaneHeader(t *testing.T) {
	runModel := newModel()
	runModel.width = 120
	runModel.resize()
	runModel.messages = []transcriptCell{systemTranscriptCell{text: "ready"}}
	runModel.setTranscriptContent()
	lines := strings.Split(stripANSI(runModel.transcript.GetContent()), "\n")
	viewLines := strings.Split(stripANSI(runModel.View().Content), "\n")
	mainWidth := runModel.getMainPaneWidth()

	require.NotEmpty(t, lines)
	require.Equal(t, mainWidth, lipgloss.Width(lines[0]))
	require.True(t, strings.HasPrefix(lines[0], " Welcome, Kennedy"))
	require.NotEmpty(t, viewLines)
	require.Equal(t, runModel.width, lipgloss.Width(viewLines[0]))
	require.True(t, strings.HasPrefix(viewLines[0], " Welcome, Kennedy"))
}

func TestModel_RenderTranscriptContentKeepsFirstPromptCloseToHeader(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.messages = []transcriptCell{userTranscriptCell{text: "hello"}}
	runModel.resize()
	runModel.setTranscriptContent()

	lines := strings.Split(stripANSI(runModel.transcript.GetContent()), "\n")
	firstPromptRow := -1
	for index, line := range lines {
		if strings.Contains(line, "❯ hello") {
			firstPromptRow = index
			break
		}
	}

	require.Greater(t, firstPromptRow, 2)
	linesValue := str.String(lines[firstPromptRow-1])
	require.NotEmpty(t, linesValue.Trim())
	require.Contains(t, lines[firstPromptRow-1], "▄")
}

func TestModel_UpdateScrollsTranscriptWithMouseWheel(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	bottomOffset := runModel.transcript.YOffset()
	require.Greater(t, bottomOffset, 0)

	updated, cmd := runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Less(t, runModel.transcript.YOffset(), bottomOffset)
}

func TestModel_UpdateOpensTranscriptLinkWithClick(t *testing.T) {
	originalOpenExternalLink := openExternalLink
	t.Cleanup(func() {
		openExternalLink = originalOpenExternalLink
	})

	opened := ""
	openExternalLink = func(raw string) error {
		opened = raw
		return nil
	}

	runModel := newModel()
	runModel.width = 100
	runModel.height = 20
	runModel.resize()
	runModel.messages = []transcriptCell{
		assistantTranscriptCell{text: "Read [docs](https://example.com/docs) for details."},
	}
	runModel.setTranscriptContent()
	runModel.transcript.GotoTop()

	lines := strings.Split(stripANSI(runModel.transcript.View()), "\n")
	row := indexLineContaining(lines, "docs")
	require.NotEqual(t, -1, row)
	column := strings.Index(lines[row], "docs")
	require.NotEqual(t, -1, column)

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      column,
		Y:      runModel.getTranscriptTop() + row,
	}))

	require.Nil(t, cmd)
	require.Equal(t, "https://example.com/docs", opened)
	require.False(t, updated.(model).selection.active)
}

func TestModel_UpdateDoesNotOpenTranscriptLinkWithRightClick(t *testing.T) {
	originalOpenExternalLink := openExternalLink
	t.Cleanup(func() {
		openExternalLink = originalOpenExternalLink
	})

	openExternalLink = func(string) error {
		t.Fatal("right click should not open external link")
		return nil
	}

	runModel := newModel()
	runModel.width = 100
	runModel.height = 20
	runModel.resize()
	runModel.messages = []transcriptCell{
		assistantTranscriptCell{text: "Read [docs](https://example.com/docs) for details."},
	}
	runModel.setTranscriptContent()
	runModel.transcript.GotoTop()

	lines := strings.Split(stripANSI(runModel.transcript.View()), "\n")
	row := indexLineContaining(lines, "docs")
	require.NotEqual(t, -1, row)
	column := strings.Index(lines[row], "docs")
	require.NotEqual(t, -1, column)

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseRight,
		X:      column,
		Y:      runModel.getTranscriptTop() + row,
	}))

	require.Nil(t, cmd)
	require.False(t, updated.(model).selection.active)
}

func TestModel_UpdateTogglesCompletedThinkingSummaryWithClick(t *testing.T) {
	runModel := newModel()
	runModel.width = 100
	runModel.height = 20
	runModel.resize()
	runModel.messages = []transcriptCell{thinkingTranscriptCell{
		id:        "live-1",
		summary:   "Checked the queue and current run.",
		duration:  4 * time.Second,
		completed: true,
	}}
	runModel.setTranscriptContent()
	runModel.transcript.GotoTop()

	clickToggle := func(current model, label string) model {
		lines := strings.Split(stripANSI(current.transcript.View()), "\n")
		row := indexLineContaining(lines, label)
		require.NotEqual(t, -1, row)
		column := strings.Index(lines[row], label)
		require.NotEqual(t, -1, column)

		updated, cmd := current.Update(tea.MouseClickMsg(tea.Mouse{
			Button: tea.MouseLeft,
			X:      column,
			Y:      current.getTranscriptTop() + row,
		}))
		require.Nil(t, cmd)
		return updated.(model)
	}

	require.Contains(t, stripANSI(runModel.transcript.View()), "View thinking")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Checked the queue")

	runModel = clickToggle(runModel, "View thinking")
	require.Contains(t, stripANSI(runModel.transcript.View()), "Hide thinking")
	require.Contains(t, stripANSI(runModel.transcript.View()), "Checked the queue and current run.")

	runModel = clickToggle(runModel, "Hide thinking")
	require.Contains(t, stripANSI(runModel.transcript.View()), "View thinking")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Checked the queue")
	require.False(t, runModel.selection.active)
}

func TestModel_UpdateDoesNotScrollTranscriptWhenTypingComposerText(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	bottomOffset := runModel.transcript.YOffset()
	require.True(t, runModel.transcript.AtBottom())

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "k", runModel.input.Value())
	require.Equal(t, bottomOffset, runModel.transcript.YOffset())
	require.True(t, runModel.transcript.AtBottom())
}

func TestModel_ViewShowsJumpToBottomWhenTranscriptIsNotAtBottom(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	require.NotContains(t, stripANSI(runModel.View().Content), jumpToBottomLabel)

	runModel.transcript.GotoTop()

	require.False(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.View().Content), jumpToBottomLabel)
}

func TestModel_UpdateJumpsTranscriptToBottomFromIndicatorAndShortcut(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	bottomOffset := runModel.transcript.YOffset()
	runModel.transcript.GotoTop()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      runModel.width / 2,
		Y:      runModel.getJumpToBottomIndicatorRow(),
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, bottomOffset, runModel.transcript.YOffset())
	require.True(t, runModel.transcript.AtBottom())

	runModel.transcript.GotoTop()
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd, Mod: tea.ModCtrl}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, bottomOffset, runModel.transcript.YOffset())
	require.True(t, runModel.transcript.AtBottom())
}

func TestModel_HydrateSessionTimelineReplacesVisibleTranscript(t *testing.T) {
	runModel := newModel()
	runModel.height = 14
	runModel.resize()
	runModel.messages = []transcriptCell{systemTranscriptCell{text: "stale cell"}}
	runModel.transcript.SetContent("stale cell")

	messages := make([]agentapi.SessionTimelineMessage, 0, 20)
	for index := 0; index < 18; index++ {
		messages = append(messages, agentapi.SessionTimelineMessage{
			Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: fmt.Sprintf("older %02d", index)},
		})
	}
	messages = append(messages,
		agentapi.SessionTimelineMessage{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "hello"}},
		agentapi.SessionTimelineMessage{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "hi"}},
	)

	runModel.hydrateSessionTimeline(rpcclient.SessionTimeline{
		SessionID: "project-a",
		Title:     "Project Planning",
		Messages:  messages,
		TraceEvents: []agentapi.SessionTimelineTraceEvent{{
			Event: agentsession.TraceEvent{
				Type:    trace.EvtToolInvocationStarted,
				Payload: map[string]any{"id": "call_1", "name": "read_file"},
			},
		}},
	})

	content := stripANSI(runModel.transcript.View())
	require.Equal(t, "Project Planning", runModel.sessionTitle)
	require.Equal(t, defaultStatus, runModel.status.Text())
	require.Equal(t, "You: hello", transcriptCellPlainText(runModel.messages[len(runModel.messages)-3]))
	require.Equal(t, "Morph: hi", transcriptCellPlainText(runModel.messages[len(runModel.messages)-2]))
	require.Equal(t, transcriptCellPlainText(toolTranscriptTestCell("call_1", "read_file", "")), transcriptCellPlainText(runModel.messages[len(runModel.messages)-1]))
	require.Contains(t, content, "❯ hello")
	require.Contains(t, content, "hi")
	require.NotContains(t, content, "Morph: hi")
	require.Contains(t, content, "● Read")
	require.Contains(t, content, "└ read_file")
	require.NotContains(t, content, "older 00")
	require.NotContains(t, content, "stale cell")
	require.True(t, runModel.transcript.AtBottom())
}

func TestModel_HydrateSessionTimelineShowsEmptySession(t *testing.T) {
	runModel := newModel()

	runModel.hydrateSessionTimeline(rpcclient.SessionTimeline{SessionID: "empty"})

	require.Equal(t, "empty", runModel.sessionTitle)
	require.Equal(t, defaultStatus, runModel.status.Text())
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.NotContains(t, runModel.transcript.View(), "empty has no visible timeline yet.")
}

func TestModel_HydrateSessionTimelineShowsFallbackForMissingSessionID(t *testing.T) {
	runModel := newModel()

	runModel.hydrateSessionTimeline(rpcclient.SessionTimeline{})

	require.Equal(t, "session", runModel.sessionTitle)
	require.Equal(t, defaultStatus, runModel.status.Text())
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.NotContains(t, runModel.transcript.View(), "session has no visible timeline yet.")
}

func TestModel_UpdateIgnoresEsc(t *testing.T) {
	runModel := newModel()
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))

	require.Nil(t, cmd)
	require.Equal(t, runModel.status.Text(), updated.(model).status.Text())
}

func TestModel_UpdatePromptsOnFirstCtrlC(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	currentTime = func() time.Time {
		return time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	}

	runModel := newModel()
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))

	require.NotNil(t, cmd)
	require.Equal(t, "Press Ctrl-C again to exit", updated.(model).status.Text())
}

func TestModel_UpdateFirstCtrlCStoresExpirationTimestamp(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	now := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	currentTime = func() time.Time {
		return now
	}

	runModel := newModel()
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, now, runModel.exitAt)
	runModel = runModel.expireExitConfirmation(exitConfirmationExpiredMsg{startedAt: now}).(model)
	require.True(t, runModel.exitAt.IsZero())
	require.Equal(t, defaultStatus, runModel.status.Text())
}

func TestModel_RenderBottomStatusPanelShowsCtrlCNoticeOnRightOnly(t *testing.T) {
	runModel := newModel()
	runModel.exitAt = time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	setStatusTransient(&runModel.status, "Press Ctrl-C again to exit")
	content := stripANSI(runModel.renderBottomStatusPanel())

	require.Contains(t, content, "Press Ctrl-C again to exit")
	require.NotContains(t, content, "minimax-m2.7")
	require.NotContains(t, content, "60,000 used")
	require.Equal(t, 0, strings.Index(strings.TrimLeft(content, " "), "Press Ctrl-C again to exit"))
}

func TestModel_UpdateQuitsOnSecondQuickCtrlC(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	times := []time.Time{
		time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 16, 9, 0, 1, 0, time.UTC),
	}
	currentTime = func() time.Time {
		if len(times) == 0 {
			return time.Date(2026, 5, 16, 9, 0, 1, 0, time.UTC)
		}

		value := times[0]
		times = times[1:]
		return value
	}

	runModel := newModel()
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)

	_, cmd = updated.(model).Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

func TestModel_UpdateDoesNotQuitOnSlowSecondCtrlC(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() {
		currentTime = originalCurrentTime
	})
	times := []time.Time{
		time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 16, 9, 0, 3, 0, time.UTC),
	}
	currentTime = func() time.Time {
		if len(times) == 0 {
			return time.Date(2026, 5, 16, 9, 0, 3, 0, time.UTC)
		}

		value := times[0]
		times = times[1:]
		return value
	}

	runModel := newModel()
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)

	updated, cmd = updated.(model).Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	require.Equal(t, "Press Ctrl-C again to exit", updated.(model).status.Text())
}

func TestModel_UpdateClearsExpiredCtrlCNotice(t *testing.T) {
	startedAt := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	runModel := newModel()
	runModel.exitAt = startedAt
	runModel.status.SetTransient("Press Ctrl-C again to exit", startedAt)

	updated, cmd := runModel.Update(exitConfirmationExpiredMsg{startedAt: startedAt})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.exitAt.IsZero())
	require.Equal(t, defaultStatus, runModel.status.Text())
}

func TestModel_UpdateIgnoresStaleCtrlCNoticeTimeout(t *testing.T) {
	runModel := newModel()
	runModel.exitAt = time.Date(2026, 5, 16, 9, 0, 1, 0, time.UTC)
	runModel.status.SetTransient("Press Ctrl-C again to exit", runModel.exitAt)

	updated, cmd := runModel.Update(exitConfirmationExpiredMsg{
		startedAt: time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC),
	})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.exitAt.IsZero())
	require.Equal(t, "Press Ctrl-C again to exit", runModel.status.Text())
}

func TestModel_UpdateKeepsPrintableTextInPrompt(t *testing.T) {
	runModel := newModel()
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))

	if cmd != nil {
		_, isQuit := cmd().(tea.QuitMsg)
		require.False(t, isQuit)
	}
	require.Equal(t, "q", updated.(model).input.Value())
}

func TestModel_UpdateAppendsPromptOnEnter(t *testing.T) {
	runModel := newModel()
	runModel.context = "64,000 used · 50%"
	runModel.input.SetValue("Summarize tests")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.Nil(t, cmd)

	mainModel := updated.(model)
	require.Empty(t, mainModel.input.Value())
	require.Equal(t, []string{"You: Summarize tests"}, transcriptCellPlainTexts(mainModel.messages))

	content := stripANSI(mainModel.View().Content)
	require.Contains(t, content, "██████")
	require.Contains(t, content, "❯ Summarize tests")
	require.Contains(t, content, "64,000")
	require.Contains(t, content, "used · 50%")
}

func TestModel_UpdateHandlesClearCommand(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{userTranscriptCell{text: "stale"}, assistantTranscriptCell{text: "stale"}}
	runModel.live = assistantTranscriptCell{text: "live"}
	runModel.stream.Add("live")
	runModel.input.SetValue("/clear")
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.live)
	require.Empty(t, runModel.input.Value())
	require.Empty(t, runModel.stream.Render())
	require.Equal(t, "transcript cleared", runModel.status.Text())
	content := stripANSI(runModel.transcript.View())
	require.Contains(t, stripANSI(runModel.View().Content), emptyUserPromptQuestion)
	require.Contains(t, content, "Welcome, Kennedy")
	require.NotContains(t, content, "You: stale")
	require.NotContains(t, content, "Morph: live")

	updated, cmd = runModel.Update(statusExpiredMsg{startedAt: runModel.status.StartedAt()})
	require.Nil(t, cmd)
	require.Equal(t, defaultStatus, updated.(model).status.Text())
}

func TestModel_UpdateTreatsHelpCommandAsUnknown(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/help")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.input.Value())
	require.Equal(t, "unknown command: /help", runModel.status.Text())
}

func TestModel_UpdateHandlesSetupCommand(t *testing.T) {
	runModel := newModel()
	runModel.userName = "Nedy"
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "existing conversation"}}
	runModel.input.SetValue("/setup")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.shouldShowNamePrompt())
	require.True(t, runModel.setupNamePromptActive)
	require.True(t, runModel.setupDismissible)
	require.Equal(t, "Nedy", runModel.nameInput.Value())
	require.Empty(t, runModel.input.Value())
	require.Contains(t, stripANSI(runModel.View().Content), namePromptTitle)
	require.Contains(t, stripANSI(runModel.View().Content), "Nedy")
	require.Contains(t, stripANSI(runModel.View().Content), "ctrl+x to close")
	require.Equal(t, "enter your name", runModel.status.Text())

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.shouldShowNamePrompt())
	require.False(t, runModel.shouldShowProfileModelSetup())
	require.False(t, runModel.setupDismissible)
	require.Equal(t, "setup closed", runModel.status.Text())

	runModel.input.SetValue("/setup")
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.setupNamePromptActive)
	require.True(t, runModel.setupDismissible)
	require.Equal(t, setupModelStepAuthMethod, runModel.setupModelStep)
	require.Contains(t, stripANSI(runModel.View().Content), "Select login method")
	require.Contains(t, stripANSI(runModel.View().Content), "esc to go back")
	require.Contains(t, stripANSI(runModel.View().Content), "ctrl+x to close")
	requireSetupHintAlignedToBorder(t, stripANSI(runModel.View().Content))

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)
	require.Contains(t, stripANSI(runModel.View().Content), "esc to go back")
	require.Contains(t, stripANSI(runModel.View().Content), "ctrl+x to close")
	requireSetupHintAlignedToBorder(t, stripANSI(runModel.View().Content))

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepAuthMethod, runModel.setupModelStep)
	require.True(t, runModel.setupDismissible)
	require.Contains(t, stripANSI(runModel.View().Content), "Select login method")

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.shouldShowNamePrompt())
	require.True(t, runModel.setupNamePromptActive)
	require.True(t, runModel.setupDismissible)
	require.Equal(t, "Nedy", runModel.nameInput.Value())

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.shouldShowNamePrompt())
	require.False(t, runModel.shouldShowProfileModelSetup())
	require.False(t, runModel.setupDismissible)
	require.Equal(t, "setup closed", runModel.status.Text())
}

func TestModel_SetupCloseHotKeyClosesFromAnySetupStep(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.setupDismissible = true

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	closed := updated.(model)
	require.False(t, closed.shouldShowProfileModelSetup())
	require.False(t, closed.setupDismissible)
	require.Equal(t, "setup closed", closed.status.Text())

	runModel = newSetupModelSelectionTestModel(t)
	runModel.setupDismissible = true
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepProvider, runModel.setupModelStep)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	closed = updated.(model)
	require.False(t, closed.shouldShowProfileModelSetup())
	require.False(t, closed.setupDismissible)
	require.Equal(t, "setup closed", closed.status.Text())

	runModel = newSetupModelSelectionTestModel(t)
	runModel.setupDismissible = true
	selectSetupProvider(t, &runModel, "openrouter")
	updated, cmd = runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, setupModelStepAPIKey, runModel.setupModelStep)
	require.Contains(t, stripANSI(runModel.View().Content), "ctrl+x to close")
	requireSetupHintAlignedToBorder(t, stripANSI(runModel.View().Content))

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	require.NotNil(t, cmd)
	closed = updated.(model)
	require.False(t, closed.shouldShowProfileModelSetup())
	require.False(t, closed.setupDismissible)
	require.Equal(t, "setup closed", closed.status.Text())
}

func TestModel_UpdateSubmitsDefaultCommandMenuItemForBareSlash(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/")
	runModel.updateCommandMenuForInput("/")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.input.Value())
	require.Zero(t, runModel.commandMenuSelected)
	require.Zero(t, runModel.commandMenuOffset)
	require.Empty(t, runModel.commandMenuPrefix)
	require.True(t, runModel.isCommandViewVisible())
	require.Equal(t, "Changelog", runModel.commandView.TitleLeft)
}

func TestModel_UpdateEscapeClosesCommandView(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft:  "Changelog",
		TitleRight: "esc to close",
		Content:    "latest updates",
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.isCommandViewVisible())
	require.Contains(t, stripANSI(runModel.View().Content), inputPrompt+"Ask Morph")
}

func TestCommandViewFrame_UsesProvidedTitleColorsAndContent(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.showCommandView(commandViewPayload{
		TitleIcon:       "◉",
		TitleLeft:       "Release Notes",
		TitleSubtext:    "New things",
		TitleRight:      "esc",
		AccentColor:     "203",
		TitleRightColor: "83",
		Content:         "latest update",
	})

	content := runModel.renderCommandView()
	plain := stripANSI(content)

	require.Contains(t, plain, "◉ Release Notes")
	require.Contains(t, plain, "Release Notes")
	require.Contains(t, plain, " - New things")
	require.Contains(t, plain, "esc")
	require.Contains(t, plain, "latest update")
	require.Contains(t, content, "38;5;203")
	require.Contains(t, content, "38;5;83")
}

func TestCommandViewFrame_UsesDefaultTitleAndMutedSubtitleColors(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.showCommandView(commandViewPayload{
		TitleIcon:    "◉",
		TitleLeft:    "Release Notes",
		TitleSubtext: "New things",
		Content:      "latest update",
	})

	frame := runModel.getCommandViewFrame()
	title := lipgloss.NewStyle().
		Inline(true).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Render("◉ Release Notes")
	mutedSubtitle := lipgloss.NewStyle().
		Inline(true).
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(" - New things")

	require.Equal(t, defaultTUITheme.NoticeForeground, frame.AccentColor)
	require.Contains(t, frame.Title, title)
	require.Contains(t, frame.Title, mutedSubtitle)
}

func TestCommandViewFrame_UsesPayloadHeight(t *testing.T) {
	runModel := newModel()
	runModel.height = 30
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Content:   "latest update",
		Height:    10,
	})

	frame := runModel.getCommandViewFrame()

	require.Equal(t, 10, runModel.getCommandViewHeight())
	require.Equal(t, 10, frame.Height)

	runModel.height = 4
	require.Equal(t, 3, runModel.getCommandViewHeight())
}

func TestCommandViewFrame_AddsGapBetweenTitleAndContent(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Release Notes",
		Content:   "latest update",
	})

	lines := strings.Split(stripANSI(runModel.renderCommandView()), "\n")

	require.GreaterOrEqual(t, len(lines), 4)
	require.Contains(t, lines[1], "Release Notes")
	require.NotContains(t, lines[2], "Release Notes")
	require.NotContains(t, lines[2], "latest update")
	require.Contains(t, lines[3], "latest update")
}

func TestCommandViewFrame_ModelViewPadsFilterInput(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.showCommandView(commandViewPayload{
		Kind:      commandViewKindModels,
		TitleLeft: "Models",
		Models:    []rpcclient.ModelOption{{ID: "gpt-5.5"}},
	})

	lines := strings.Split(stripANSI(runModel.renderCommandView()), "\n")

	require.GreaterOrEqual(t, len(lines), 5)
	require.Contains(t, lines[1], "Models")
	require.Empty(t, strings.Trim(lines[2], " │"))
	require.Contains(t, lines[3], "Filter models")
	require.Empty(t, strings.Trim(lines[4], " │"))
}

func TestCommandViewFrame_UsesComposerBorderColor(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.showCommandView(commandViewPayload{
		TitleLeft:   "Release Notes",
		AccentColor: "203",
		Content:     "latest update",
	})

	frame := runModel.getCommandViewFrame()

	require.Equal(t, defaultTUITheme.InputFrameBorder, frame.BorderColor)
}

func TestCommandViewFrame_ScrollsContent(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.height = 18
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Long Output",
		Content:   strings.Join([]string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6"}, "\n"),
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewOffset)
	require.NotContains(t, stripANSI(runModel.renderCommandView()), "line 1")
	require.Contains(t, stripANSI(runModel.renderCommandView()), "line 2")
}

func TestModel_UpdateCopiesCommandViewContent(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Changelog",
		Content:   "latest update",
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Mod: tea.ModCtrl}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "latest update", copied)
	require.Equal(t, "command view copied", runModel.status.Text())
}

func TestModel_UpdateCopiesRenderedCommandViewMarkdown(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Notes",
		Content:   "## Latest\n\n- Added markdown rendering",
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Mod: tea.ModCtrl}))

	require.NotNil(t, cmd)
	_ = updated.(model)
	require.Contains(t, copied, "Latest")
	require.Contains(t, copied, "Added markdown rendering")
	require.NotContains(t, copied, "## Latest")
	require.NotContains(t, copied, "- Added")
}

func TestModel_UpdateSelectsCommandViewTextWithMouseAndCopiesOnRelease(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	runModel := newModel()
	runModel.width = 80
	runModel.height = 24
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Changelog",
		Content:   "alpha\nbeta",
	})
	row := runModel.getCommandViewContentTop()
	x := runModel.getCommandViewContentLeft()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      x,
		Y:      row,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.commandViewSelection.dragging)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      x + len("alpha"),
		Y:      row,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Contains(t, runModel.renderCommandView(), "\x1b[7m")

	updated, cmd = runModel.Update(tea.MouseReleaseMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      x + len("alpha"),
		Y:      row,
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.commandViewSelection.dragging)
	require.True(t, runModel.commandViewSelection.active)
	require.Contains(t, runModel.renderCommandView(), "\x1b[7m")
	require.Equal(t, "alpha", copied)
	require.Equal(t, "alpha", runModel.selectedCommandViewText())
}

func TestModel_CommandViewSelectionUsesRenderedChatRows(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.height = 24
	runModel.showCommandView(commandViewPayload{
		Kind:      commandViewKindChats,
		TitleLeft: "Chats",
		Chats: []storage.Session{{
			ID:    "chat-1",
			Title: "Latest CNN and BBC Headlines",
		}},
	})
	row := runModel.getCommandViewContentTop()
	x := runModel.getCommandViewContentLeft()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      x,
		Y:      row,
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.commandViewSelection.dragging)
	require.Contains(t, stripANSI(runModel.commandViewSelection.content), "Latest CNN and BBC Headlines")
	require.NotContains(t, stripANSI(runModel.renderCommandView()), "No content available.")
}

func TestModel_UpdateAutoScrollsCommandViewSelection(t *testing.T) {
	runModel := newModel()
	runModel.width = 80
	runModel.height = 18
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Long Output",
		Content: strings.Join([]string{
			"line 1",
			"line 2",
			"line 3",
			"line 4",
			"line 5",
			"line 6",
		}, "\n"),
	})
	top := runModel.getCommandViewContentTop()
	x := runModel.getCommandViewContentLeft()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      x,
		Y:      top,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      x + len("line 6"),
		Y:      top + runModel.getCommandViewContentHeight(),
	}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewOffset)
	require.Contains(t, runModel.selectedCommandViewText(), "line 4")

	updated, cmd = runModel.Update(commandViewSelectionAutoScrollTickMsg{})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewOffset)
	require.Contains(t, runModel.selectedCommandViewText(), "line 5")
}

func TestModel_UpdateSelectsTranscriptTextWithMouseAndCopiesOnRelease(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	runModel := newModel()
	runModel.height = 40
	runModel.resize()
	runModel.messages = transcriptWindowTestCells(300)
	runModel.messages = append(runModel.messages,
		userTranscriptCell{text: "first"},
		assistantTranscriptCell{text: "second"},
		toolTranscriptTestCell("", "read_file", ""),
	)
	runModel.setTranscriptContent()
	runModel.resize()
	firstRow := getTranscriptContentRow(t, runModel, "❯ first") - runModel.transcript.YOffset()
	secondRow := getTranscriptContentRow(t, runModel, "second") - runModel.transcript.YOffset()
	require.GreaterOrEqual(t, runModel.transcript.Height(), 3)

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      firstRow,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.dragging)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph) + len("second"),
		Y:      secondRow,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")
	require.Contains(t, runModel.transcript.View(), "48;5;235")

	updated, cmd = runModel.Update(tea.MouseReleaseMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph) + len("second"),
		Y:      secondRow,
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.selection.dragging)
	require.True(t, runModel.selection.active)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")
	require.Contains(t, runModel.transcript.View(), "48;5;235")
	require.Equal(t, strings.Join([]string{
		"❯ first",
		"",
		assistantTranscriptIndicatorGlyph + "second",
	}, "\n"), trimTrailingLineSpaces(copied))
	require.Equal(t, defaultStatus, runModel.status.Text())
}

func TestModel_UpdateSelectsTranscriptTextCharacterByCharacter(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	runModel := newModel()
	runModel.height = 40
	runModel.resize()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "second"}}
	runModel.setTranscriptContent()
	runModel.resize()
	runModel.transcript.GotoTop()
	row := getTranscriptContentRow(t, runModel, "second")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph),
		Y:      row,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph) + len("sec"),
		Y:      row,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseReleaseMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph) + len("sec"),
		Y:      row,
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.selection.dragging)
	require.True(t, runModel.selection.active)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")
	require.Equal(t, "sec", runModel.selectedTranscriptText())
	require.Equal(t, "sec", copied)
	require.Equal(t, defaultStatus, runModel.status.Text())
}

func TestModel_UpdateIgnoresNonLeftMouseSelectionStart(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "first"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseRight,
		Y:      runModel.getTranscriptTop(),
	}))

	require.Nil(t, cmd)
	require.False(t, updated.(model).selection.active)
}

func TestModel_UpdateIgnoresSelectionMotionAndReleaseWithoutDrag(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "first"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      runModel.getTranscriptTop(),
	}))

	require.Nil(t, cmd)
	require.False(t, updated.(model).selection.active)

	updated, cmd = updated.(model).Update(tea.MouseReleaseMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      runModel.getTranscriptTop(),
	}))

	require.Nil(t, cmd)
	require.False(t, updated.(model).selection.active)
}

func TestModel_UpdateKeepsSelectionWhenDraggingOutsideTranscript(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "first"}}
	runModel.setTranscriptContent()
	top := runModel.getTranscriptTop()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      len("Morph"),
		Y:      top,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	start := runModel.selection.start

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      len("Morph: first"),
		Y:      top + runModel.transcript.Height(),
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.dragging)
	require.Equal(t, start, runModel.selection.end)
}

func TestModel_UpdateKeepsSelectionDragDuringResponseUpdate(t *testing.T) {
	runModel := newModel()
	runModel.height = 40
	runModel.resize()
	runModel.messages = []transcriptCell{
		userTranscriptCell{text: "first"},
		assistantTranscriptCell{text: "second"},
	}
	runModel.setTranscriptContent()
	runModel.resize()
	runModel.transcript.GotoTop()
	firstRow := getTranscriptContentRow(t, runModel, "❯ first")
	secondRow := getTranscriptContentRow(t, runModel, "second")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      firstRow,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph) + len("second"),
		Y:      secondRow,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.dragging)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")

	runModel.responding = true
	runModel.responseID = 4
	updated, cmd = runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "new response text"},
	})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.dragging)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")
	require.Contains(t, runModel.selectedTranscriptText(), "first")
	require.Contains(t, runModel.selectedTranscriptText(), "second")
}

func TestModel_UpdateKeepsSelectionDragDuringToolUpdate(t *testing.T) {
	runModel := newModel()
	runModel.height = 40
	runModel.resize()
	runModel.messages = []transcriptCell{
		userTranscriptCell{text: "first"},
		assistantTranscriptCell{text: "second"},
	}
	runModel.setTranscriptContent()
	runModel.resize()
	runModel.transcript.GotoTop()
	firstRow := getTranscriptContentRow(t, runModel, "❯ first")
	secondRow := getTranscriptContentRow(t, runModel, "second")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      firstRow,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + lipgloss.Width(assistantTranscriptIndicatorGlyph) + len("second"),
		Y:      secondRow,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.dragging)

	updated, cmd = runModel.Update(toolInvocationCompletedMsg{Name: "read_file"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.dragging)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")
	require.Contains(t, runModel.selectedTranscriptText(), "first")
	require.Contains(t, runModel.selectedTranscriptText(), "second")
}

func TestModel_UpdateAutoScrollsTranscriptSelectionAtBottomEdge(t *testing.T) {
	runModel := newModel()
	runModel.width = 40
	runModel.height = 12
	runModel.resize()
	runModel.transcript.SetWidth(20)
	runModel.transcript.SetHeight(3)
	runModel.transcript.SetContent(strings.Join([]string{
		"line 00",
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
	}, "\n"))
	runModel.transcript.GotoTop()
	top := runModel.getTranscriptTop()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width),
		Y:      top,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width) + len("line 03"),
		Y:      top + runModel.transcript.Height(),
	}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.transcript.YOffset())
	require.Contains(t, runModel.selectedTranscriptText(), "line 03")

	updated, cmd = runModel.Update(transcriptSelectionAutoScrollTickMsg{})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.transcript.YOffset())
	require.Contains(t, runModel.selectedTranscriptText(), "line 04")
}

func TestModel_UpdateAutoScrollsTranscriptSelectionAtTopEdge(t *testing.T) {
	runModel := newModel()
	runModel.width = 40
	runModel.height = 12
	runModel.resize()
	runModel.transcript.SetWidth(20)
	runModel.transcript.SetHeight(3)
	runModel.transcript.SetContent(strings.Join([]string{
		"line 00",
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
	}, "\n"))
	runModel.transcript.SetYOffset(3)
	top := runModel.getTranscriptTop()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width),
		Y:      top + runModel.transcript.Height() - 1,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseMotionMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      getPanelHorizontalPadding(runModel.width),
		Y:      top - 1,
	}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.transcript.YOffset())
	require.Contains(t, runModel.selectedTranscriptText(), "line 02")

	updated, cmd = runModel.Update(transcriptSelectionAutoScrollTickMsg{})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.transcript.YOffset())
	require.Contains(t, runModel.selectedTranscriptText(), "line 01")
}

func TestModel_UpdateDoesNotCopyBlankMouseSelection(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	writeClipboard = func(string) error {
		t.Fatal("clipboard should not be called for blank selections")
		return nil
	}
	runModel := newModel()
	runModel.messages = []transcriptCell{systemTranscriptCell{text: "   "}}
	runModel.transcript.SetContent("   ")
	top := runModel.getTranscriptTop()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      top,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseReleaseMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      3,
		Y:      top,
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.selection.dragging)
	require.False(t, runModel.selection.active)
	require.NotContains(t, runModel.transcript.View(), "\x1b[7m")
}

func TestModel_UpdateReportsMouseSelectionCopyFailure(t *testing.T) {
	originalWriteClipboard := writeClipboard
	t.Cleanup(func() {
		writeClipboard = originalWriteClipboard
	})
	writeClipboard = func(string) error {
		return errors.New("clipboard unavailable")
	}
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "first"}}
	runModel.setTranscriptContent()
	runModel.resize()
	runModel.transcript.GotoTop()
	row := getTranscriptContentRow(t, runModel, "first")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      row,
	}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.MouseReleaseMsg(tea.Mouse{
		Button: tea.MouseLeft,
		X:      len("Morph"),
		Y:      row,
	}))

	require.NotNil(t, cmd)
	require.Equal(t, "copy failed", updated.(model).status.Text())
}

func TestModel_UpdateIgnoresMouseSelectionOutsideTranscript(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "first"}}
	runModel.setTranscriptContent()
	runModel.resize()
	belowTranscript := runModel.getTranscriptTop() + runModel.transcript.Height()

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft,
		Y:      belowTranscript,
	}))

	require.Nil(t, cmd)
	require.False(t, updated.(model).selection.active)
}

func TestModel_TranscriptSelectionPointFromVisualLineHandlesPlainLines(t *testing.T) {
	runModel := newModel()
	runModel.transcript.SoftWrap = false
	runModel.transcript.SetContent("one\ntwo")

	point, ok := runModel.transcriptSelectionPointFromVisualLine(1, 2)

	require.True(t, ok)
	require.Equal(t, transcriptSelectionPoint{line: 1, offset: len("one\n") + len("tw")}, point)

	_, ok = runModel.transcriptSelectionPointFromVisualLine(2, 0)
	require.False(t, ok)
}

func TestModel_TranscriptSelectionPointFromVisualLineRejectsInvalidRows(t *testing.T) {
	runModel := newModel()
	runModel.transcript.SetContent("one")

	_, ok := runModel.transcriptSelectionPointFromVisualLine(-1, 0)
	require.False(t, ok)

	_, ok = runModel.transcriptSelectionPointFromVisualLine(10, 0)
	require.False(t, ok)
}

func TestModel_TranscriptSelectionPointFromMouseMapsWrappedVisualRowsToContentLine(t *testing.T) {
	runModel := newModel()
	runModel.width = 24
	runModel.height = 40
	runModel.resize()
	first := "Morph: " + strings.Repeat("wrapped ", 6)
	runModel.transcript.SetContent(first + "\nYou: next")
	runModel.transcript.GotoTop()

	point, ok := runModel.transcriptSelectionPointFromMouse(tea.Mouse{
		X: 3,
		Y: runModel.getTranscriptTop() + 1,
	})

	require.True(t, ok)
	require.Equal(t, 0, point.line)
	require.Greater(t, point.offset, 0)
}

func TestModel_TranscriptSelectionPointFromMouseUsesWrappedVisualViewportOffset(t *testing.T) {
	runModel := newModel()
	runModel.transcript.SetWidth(10)
	runModel.transcript.SetHeight(1)
	firstLine := "abcdefghijklmno"
	runModel.transcript.SetContent(firstLine + "\ntarget line")
	width := max(runModel.transcript.Width()-runModel.transcript.Style.GetHorizontalFrameSize(), 1)
	runModel.transcript.SetYOffset(getWrappedTranscriptLineHeight(firstLine, width))

	point, ok := runModel.transcriptSelectionPointFromMouse(tea.Mouse{
		X: getPanelHorizontalPadding(runModel.width) + len("target"),
		Y: runModel.getTranscriptTop(),
	})

	require.True(t, ok)
	require.Equal(
		t,
		transcriptSelectionPoint{line: 1, offset: len("abcdefghijklmno\n") + len("target")},
		point,
	)
}

func TestModel_SelectedTranscriptTextHandlesOutOfRangeOffsets(t *testing.T) {
	runModel := newModel()
	runModel.transcript.SetContent("abc")
	runModel.selection = transcriptSelection{
		active: true,
		start:  transcriptSelectionPoint{offset: 2},
		end:    transcriptSelectionPoint{offset: 20},
	}

	require.Equal(t, "c", runModel.selectedTranscriptText())

	runModel.selection = transcriptSelection{
		active: true,
		start:  transcriptSelectionPoint{offset: 10},
		end:    transcriptSelectionPoint{offset: 10},
	}
	require.Empty(t, runModel.selectedTranscriptText())
}

func TestTranscriptSelectionOffsetBoundsOrdersReverseSelection(t *testing.T) {
	selection := transcriptSelection{
		start: transcriptSelectionPoint{offset: 8},
		end:   transcriptSelectionPoint{offset: 3},
	}

	start, end := selection.offsetBounds()

	require.Equal(t, 3, start)
	require.Equal(t, 8, end)
}

func TestGetTranscriptSelectionPointRejectsInvalidLineIndex(t *testing.T) {
	require.Equal(t, transcriptSelectionPoint{}, getTranscriptSelectionPoint([]string{"one"}, 2, 0, 0))
	require.Equal(t, transcriptSelectionPoint{}, getTranscriptSelectionPoint([]string{"one"}, -1, 0, 0))
}

func TestGetTranscriptLineOffsetReturnsEndOffsetForPastEndIndex(t *testing.T) {
	require.Equal(t, len("one\ntwo"), getTranscriptLineOffset([]string{"one", "two"}, 10))
}

func TestGetByteOffsetForDisplayColumnSkipsANSISequences(t *testing.T) {
	line := renderTranscriptTestCell(assistantTranscriptCell{text: "hello"})

	offset := getByteOffsetForDisplayColumn(line, lipgloss.Width(assistantTranscriptIndicatorGlyph)+len("hel"))

	require.Equal(t, strings.Index(line, "lo"), offset)
}

func TestHighlightTranscriptSelectionUsesDisplayColumnsForWideCharacters(t *testing.T) {
	line := renderTranscriptTestCell(assistantTranscriptCell{text: "👋 anything"})
	plain := stripANSI(line)
	start := strings.Index(plain, "anything")
	end := start + len("anything")

	highlighted := highlightTranscriptSelection(
		line,
		start,
		end,
		lipgloss.NewStyle().Reverse(true),
	)

	require.Contains(t, highlighted, "\x1b[7manything")
	require.NotContains(t, highlighted, "\x1b[7mything")
}

func TestGetDisplayColumnForByteOffsetHandlesWideCharacters(t *testing.T) {
	line := "Morph: 👋 anything"

	column := getDisplayColumnForByteOffset(line, strings.Index(line, "anything"))

	require.Equal(t, len("Morph: ")+2+1, column)
}

func TestModel_SetTranscriptContentClearsMouseSelection(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "stale"}}
	runModel.setTranscriptContent()
	runModel.selection = transcriptSelection{
		active: true,
		start:  transcriptSelectionPoint{offset: 0},
		end:    transcriptSelectionPoint{offset: len("Morph")},
	}
	runModel.applyTranscriptSelectionStyle()
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")

	runModel.applyAction(setTranscriptCellsAction{Cells: []transcriptCell{
		assistantTranscriptCell{text: "refreshed"},
	}})
	runModel.setTranscriptContent()

	require.False(t, runModel.selection.active)
	require.Empty(t, runModel.selectedTranscriptText())
	require.Contains(t, stripANSI(runModel.transcript.View()), "refreshed")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Morph: refreshed")
}

func TestModel_UpdateReportsUnknownCommand(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/missing now")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Equal(t, "unknown command: /missing", runModel.status.Text())
	require.Empty(t, runModel.input.Value())
}

func TestModel_HandleSlashCommandReportsEmptyCommand(t *testing.T) {
	runModel := newModel()

	cmd := runModel.handleSlashCommand(composerInput{Kind: composerInputCommand})

	require.NotNil(t, cmd)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Equal(t, "empty command", runModel.status.Text())
}

func TestModel_SubmitPromptStartsRPCResponse(t *testing.T) {
	client := &fakeTUIChatClient{reply: "hello back"}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("hello")

	cmd := runModel.submitPrompt()

	require.NotNil(t, cmd)
	require.True(t, runModel.responding)
	require.True(t, runModel.thinkingComposerActive)
	require.Equal(t, []string{"You: hello"}, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.input.Value())
	require.Equal(t, []string{"hello"}, runModel.history)
	require.Zero(t, client.calls)
	require.NotNil(t, runModel.responseCancel)
}

func TestModel_SubmitPromptSendsCurrentSessionID(t *testing.T) {
	client := &fakeTUIChatClient{reply: "hello back"}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current"})
	runModel.input.SetValue("hello")

	cmd := runModel.submitPrompt()

	require.NotNil(t, cmd)
	msg := responseMessageFromBatch(t, cmd)

	require.Equal(t, responseCompletedMsg{ResponseID: runModel.responseID}, msg)
	require.Equal(t, "ses_current", client.respondSessionID)
}

func TestModel_UpdateEnterStartsThinkingResponse(t *testing.T) {
	client := &fakeTUIChatClient{reply: "hello back"}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("hello")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.True(t, runModel.thinkingComposerActive)
	require.True(t, runModel.isModelThinking())
	require.Contains(t, stripANSI(runModel.renderBottomStatusPanel()), "Thinking")
	require.Equal(t, []string{"You: hello"}, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.input.Value())
	require.Zero(t, client.calls)
	require.NotNil(t, runModel.responseCancel)
}

func TestModel_UpdateEscapeCancelsActiveResponse(t *testing.T) {
	responseCtx, cancel := context.WithCancel(context.Background())
	runModel := newModelWithClientContext(responseCtx, &fakeTUIChatClient{})
	runModel.responding = true
	runModel.responseID = 4
	runModel.responseCancel = cancel
	runModel.responseTranscriptFollow = true
	runModel.thinkingComposerActive = true
	runModel.toolAnimationActive = true
	runModel.events = make(chan tea.Msg)
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id:        "call_1",
		action:    "Automation",
		detail:    "add:Reminder",
		startedAt: time.Now().Add(-time.Second),
	}}

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.False(t, runModel.responseTranscriptFollow)
	require.False(t, runModel.thinkingComposerActive)
	require.False(t, runModel.toolAnimationActive)
	require.Nil(t, runModel.responseCancel)
	require.Nil(t, runModel.events)
	require.Equal(t, "interrupt requested", runModel.status.Text())
	require.ErrorIs(t, responseCtx.Err(), context.Canceled)
	toolCell, ok := runModel.messages[0].(toolTranscriptCell)
	require.True(t, ok)
	require.Equal(t, toolTranscriptTerminalStatusInterrupted, toolCell.terminalStatus)
	require.False(t, toolCell.completedAt.IsZero())
	require.Contains(t, toolCell.PlainText(), "status: interrupted")
	require.Contains(t, stripANSI(renderTranscriptCells(runModel.messages)), "Interrupted Automation")
}

func TestModel_UpdateEscapeIgnoresStaleCancelledCompletion(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 4
	runModel.responseCancel = func() {}
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(responseCompletedMsg{ResponseID: 4, Err: context.Canceled})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Equal(t, "interrupt requested", runModel.status.Text())
}

func TestModel_SubmitPromptPreservesTranscriptOffsetWhenAwayFromBottom(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.transcript.GotoTop()
	offsetBefore := runModel.transcript.YOffset()
	runModel.input.SetValue("hello")

	cmd := runModel.submitPrompt()

	require.NotNil(t, cmd)
	require.Equal(t, offsetBefore, runModel.transcript.YOffset())
	require.False(t, runModel.transcript.AtBottom())
	require.False(t, runModel.responseTranscriptFollow)
	require.Contains(t, stripANSI(runModel.renderTranscriptContent()), "❯ hello")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "❯ hello")
}

func TestModel_SubmitPromptStartsResponseFollowFromSettledBottom(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.input.SetValue(strings.Join([]string{
		"first line",
		"second line",
		"third line",
		"fourth line",
	}, "\n"))

	cmd := runModel.submitPrompt()

	require.NotNil(t, cmd)
	require.True(t, runModel.responding)
	require.True(t, runModel.responseTranscriptFollow)
	require.False(t, runModel.responseTranscriptScrolled)
	require.True(t, runModel.transcript.AtBottom())

	updated, cmd := runModel.Update(responseCompletedMsg{ResponseID: runModel.responseID, Text: "final"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	updated, cmd = runModel.Update(responseEventsClosedMsg{ResponseID: runModel.responseID})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.transcript.View()), "final")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Morph: final")
}

func TestModel_SubmitPromptFollowsResponseAfterUserScrollsBackToBottom(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.transcript.GotoTop()
	require.False(t, runModel.transcript.AtBottom())
	runModel.transcript.GotoBottom()
	require.True(t, runModel.transcript.AtBottom())
	runModel.input.SetValue("hello")

	cmd := runModel.submitPrompt()

	require.NotNil(t, cmd)
	require.True(t, runModel.responseTranscriptFollow)
	require.True(t, runModel.transcript.AtBottom())

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: runModel.responseID,
		Message:    assistantTextDeltaMsg{Text: strings.Repeat("streamed ", 40)},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.transcript.View()), "streamed")
}

func TestModel_UpdateRefreshesSessionTitleAfterResponseCompletes(t *testing.T) {
	client := &fakeTUIChatClient{
		currentSession: storage.Session{
			ID:    "default",
			Title: "Daily Planning",
		},
	}
	runModel := newModelWithClient(client)
	runModel.sessionTitle = defaultSessionTitle
	runModel.responding = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseCompletedMsg{ResponseID: 4, Text: "final"})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, defaultSessionTitle, runModel.sessionTitle)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)

	var msg tea.Msg
	for _, child := range batch {
		if childMsg, ok := child().(sessionTitleLoadedMsg); ok {
			msg = childMsg
			break
		}
	}
	require.Equal(t, 1, client.currentSessionCalls)
	require.NotNil(t, msg)

	updated, cmd = runModel.Update(msg)

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, defaultSessionID, runModel.sessionID)
	require.Equal(t, "Daily Planning (default)", runModel.sessionTitle)
}

func TestRespondToPromptCmd_StreamsDeltasTraceEventsAndCompletion(t *testing.T) {
	client := &fakeTUIChatClient{
		reply: "hello world",
		events: []rpcclient.Event{
			{Kind: agent.EventKindTextDelta},
			{Kind: agent.EventKindTextDelta, Text: "hello "},
			{
				Kind: agent.EventKindTrace,
				TraceEvent: &trace.Event{
					Type:    trace.EvtToolInvocationStarted,
					Payload: map[string]any{"id": "call_1", "name": "read_file"},
				},
			},
			{
				Kind: agent.EventKindTrace,
				TraceEvent: &trace.Event{
					Type:    trace.EvtFinalAssistantResponse,
					Payload: map[string]any{"message": "hello world"},
				},
			},
		},
	}
	events := make(chan tea.Msg, 8)

	msg := respondToPromptCmd(
		client,
		7,
		context.Background(),
		"project-a",
		"hello",
		permissions.PresetAskForApproval,
		events,
	)()

	require.Equal(t, responseCompletedMsg{ResponseID: 7}, msg)
	require.Equal(t, "hello", client.message)
	require.Equal(t, "project-a", client.respondSessionID)
	outgoingMetadata, ok := metadata.FromOutgoingContext(client.respondContext)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), outgoingMetadata)
	require.Equal(t, permissions.SurfaceTUI, rpcmeta.PermissionSurfaceFromIncomingContext(incoming))
	preset, ok := rpcmeta.PermissionPresetFromIncomingContext(incoming)
	require.True(t, ok)
	require.Equal(t, permissions.PresetAskForApproval, preset)
	_, ok = <-events
	require.False(t, ok)
}

func TestRespondToPromptCmd_ReturnsErrorCompletion(t *testing.T) {
	expectedErr := errors.New("daemon unavailable")
	client := &fakeTUIChatClient{err: expectedErr}
	events := make(chan tea.Msg, 1)

	msg := respondToPromptCmd(
		client,
		3,
		nil,
		"project-a",
		"hello",
		permissions.PresetCustom,
		events,
	)()

	require.Equal(t, responseCompletedMsg{ResponseID: 3, Err: expectedErr}, msg)
	require.Equal(t, "hello", client.message)
	require.Equal(t, "project-a", client.respondSessionID)
	_, ok := <-events
	require.False(t, ok)
}

func TestModel_UpdateAppliesResponseEventsAndCompletion(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "hello"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "Morph: hello", transcriptCellPlainText(runModel.live))

	updated, cmd = runModel.Update(responseCompletedMsg{ResponseID: 4, Text: "hello final"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Nil(t, runModel.events)
	require.Empty(t, runModel.live)
	require.Equal(t, []string{"Morph: hello final"}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateRendersCompletedResponseDuration(t *testing.T) {
	startedAt := time.Date(2026, 6, 11, 1, 30, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	t.Cleanup(func() { currentTime = originalCurrentTime })
	currentTime = func() time.Time { return startedAt.Add(6 * time.Second) }
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 4
	runModel.responseStartedAt = startedAt
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseCompletedMsg{ResponseID: 4, Text: "Here's one for you."})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, []string{"Morph: Here's one for you.\nWorked for 6s"}, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), assistantTranscriptWorkGlyph+"Worked for 6s")
	require.True(t, runModel.responseStartedAt.IsZero())
}

func TestModel_UpdatePreservesTranscriptScrollDuringActiveResponse(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	bottomOffset := runModel.transcript.YOffset()
	require.Greater(t, bottomOffset, 0)
	runModel.transcript.GotoTop()
	offsetBefore := runModel.transcript.YOffset()
	runModel.responding = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, offsetBefore, runModel.transcript.YOffset())
	require.Contains(t, stripANSI(runModel.renderTranscriptContent()), "streamed")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "streamed")
}

func TestModel_UpdateFollowsBottomDuringActiveResponse(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "streamed")
	require.Contains(t, stripANSI(runModel.transcript.View()), "streamed")
}

func TestModel_UpdateFollowsBottomWhenToolCallGrowsTranscript(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message: toolInvocationStartedMsg{
			ID:     "call_1",
			Name:   "run_command",
			Detail: "printf " + strings.Repeat("long-output ", 40),
		},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.transcript.View()), "long-output")
}

func TestModel_UpdateKeepsFollowingBottomWhenResponseCompletesAfterStream(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())

	updated, cmd = runModel.Update(responseCompletedMsg{ResponseID: 4})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.transcript.View()), "streamed")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Morph: streamed")
}

func TestModel_UpdateScrollsToBottomWhenResponseCompletesWhileViewportIsAtBottom(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseCompletedMsg{ResponseID: 4, Text: "final"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.transcript.AtBottom())
	require.Contains(t, stripANSI(runModel.transcript.View()), "final")
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Morph: final")
}

func TestModel_UpdateDoesNotScrollToBottomWhenResponseCompletesAfterManualScroll(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responseTranscriptScrolled)
	offsetBefore := runModel.transcript.YOffset()

	updated, cmd = runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(responseCompletedMsg{ResponseID: 4})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, offsetBefore, runModel.transcript.YOffset())
	require.False(t, runModel.transcript.AtBottom())
	require.NotContains(t, stripANSI(runModel.transcript.View()), "streamed")
}

func TestModel_UpdateDisablesFollowModeOnWheelDuringActiveResponse(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(tea.MouseWheelMsg(tea.Mouse{
		Button: tea.MouseWheelUp,
		X:      getPanelHorizontalPadding(runModel.width),
		Y:      runModel.transcript.Height() - 1,
	}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responseTranscriptScrolled)
	require.False(t, runModel.responseTranscriptFollow)
	offsetBefore := runModel.transcript.YOffset()

	updated, cmd = runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, offsetBefore, runModel.transcript.YOffset())
	require.NotContains(t, stripANSI(runModel.transcript.View()), "streamed")
}

func TestModel_UpdateReenablesFollowModeWhenUserScrollsBackToBottom(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())
	runModel.responding = true
	runModel.responseTranscriptFollow = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.transcript.AtBottom())
	require.True(t, runModel.responseTranscriptScrolled)
	require.False(t, runModel.responseTranscriptFollow)

	for !runModel.isTranscriptAtAbsoluteBottom() {
		updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		require.Nil(t, cmd)
		runModel = updated.(model)
	}
	require.True(t, runModel.responseTranscriptFollow)
	require.False(t, runModel.responseTranscriptScrolled)

	updated, cmd = runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.isTranscriptAtAbsoluteBottom())
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "streamed")
	require.Contains(t, stripANSI(runModel.transcript.View()), "streamed")
}

func TestModel_UpdateDoesNotScrollToBottomWhenResponseArrivesAwayFromBottom(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	runModel.messages = make([]transcriptCell, 0, 30)
	for index := 0; index < 30; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.transcript.GotoTop()
	offsetBefore := runModel.transcript.YOffset()
	runModel.responding = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    assistantTextDeltaMsg{Text: "streamed"},
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(responseCompletedMsg{ResponseID: 4})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, offsetBefore, runModel.transcript.YOffset())
	require.False(t, runModel.transcript.AtBottom())
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Morph: streamed")
}

func TestModel_UpdateSurfacesRPCErrorInStatusAndTranscript(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 2
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseCompletedMsg{
		ResponseID: 2,
		Err:        errors.New("daemon unavailable"),
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Nil(t, runModel.events)
	require.Equal(t, "response failed", runModel.status.Text())
	require.Equal(t, []string{"Error: daemon unavailable"}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateDrainsToolCompletionBeforeResponseError(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 2
	runModel.responseEventStreamActive = true
	runModel.events = make(chan tea.Msg)
	detail := getToolInputDisplayDetail(
		"automation",
		`{"action":"add","job":{"name":"One-time current time update"}}`,
	)

	updated, _ := runModel.Update(responseEventMsg{
		ResponseID: 2,
		Message: toolInvocationStartedMsg{
			ID:     "call_1",
			Name:   "automation",
			Detail: detail,
		},
	})
	runModel = updated.(model)

	updated, cmd := runModel.Update(responseCompletedMsg{
		ResponseID: 2,
		Err:        errors.New("database is locked"),
	})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)

	updated, _ = runModel.Update(responseEventMsg{
		ResponseID: 2,
		Message: toolInvocationCompletedMsg{
			ID:     "call_1",
			Name:   "automation",
			Detail: detail,
		},
	})
	runModel = updated.(model)

	updated, cmd = runModel.Update(responseEventsClosedMsg{ResponseID: 2})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.False(t, runModel.toolAnimationActive)
	rendered := stripANSI(renderTranscriptCells(runModel.messages))
	require.Contains(t, rendered, "Added Automation")
	require.Contains(t, rendered, "Added automation One-time current time update")
	require.Equal(t, "Error: database is locked", runModel.messages[len(runModel.messages)-1].PlainText())
}

func TestModel_UpdateMarksRunningToolFailedWhenResponseFails(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 2
	runModel.responseStartMessageIndex = 99
	runModel.events = make(chan tea.Msg)
	detail := getToolInputDisplayDetail(
		"automation",
		`{"action":"add","job":{"name":"One-time current time update"}}`,
	)

	updated, _ := runModel.Update(responseEventMsg{
		ResponseID: 2,
		Message: toolInvocationStartedMsg{
			ID:        "call_1",
			Name:      "automation",
			Detail:    detail,
			StartedAt: time.Now().Add(-2 * time.Second),
		},
	})
	runModel = updated.(model)

	updated, cmd := runModel.Update(responseCompletedMsg{
		ResponseID: 2,
		Err:        errors.New("database is locked"),
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	toolCell, ok := runModel.messages[0].(toolTranscriptCell)
	require.True(t, ok)
	require.Equal(t, toolTranscriptTerminalStatusFailed, toolCell.terminalStatus)
	require.False(t, toolCell.completed)
	require.False(t, toolCell.completedAt.IsZero())
	require.False(t, runModel.toolAnimationActive)
	require.Contains(t, toolCell.PlainText(), "status: failed")
	rendered := stripANSI(renderTranscriptCells(runModel.messages))
	require.Contains(t, rendered, "Failed Automation")
	require.Contains(t, rendered, "Adding automation One-time current time update")
}

func TestModel_UpdateShowsResponseFailureOnRunningBrowserBranch(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 2
	runModel.events = make(chan tea.Msg)
	runModel.applyTUIMessage(toolInvocationStartedMsg{
		ID: "call_1", Name: "browser", Detail: "browser",
	})

	updated, cmd := runModel.Update(responseCompletedMsg{
		ResponseID: 2,
		Err:        errors.New("daemon unavailable"),
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	rendered := stripANSI(renderTranscriptCells(runModel.messages))
	require.Contains(t, rendered, "Browser Action Failed")
	require.Contains(t, rendered, "└ Daemon unavailable")
}

func TestModel_ToolErrorCompletionRendersFailedState(t *testing.T) {
	runModel := newModel()
	runModel.applyTUIMessage(toolInvocationStartedMsg{
		ID: "call_1", Name: "browser", Detail: "start:Profile default",
	})
	runModel.applyTUIMessage(toolInvocationCompletedMsg{
		ID: "call_1", Name: "browser", Failed: true,
	})

	toolCell, ok := runModel.messages[0].(toolTranscriptCell)
	require.True(t, ok)
	require.False(t, toolCell.completed)
	require.Equal(t, toolTranscriptTerminalStatusFailed, toolCell.terminalStatus)

	rendered := stripANSI(renderTranscriptCells(runModel.messages))
	require.Contains(t, rendered, "Failed to Start Browser")
	require.NotContains(t, rendered, "Completed Browser Action")
}

func TestModel_UpdateInterruptsUnfinishedToolWhenResponseCompletes(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 2
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id:        "call_1",
		action:    "Automation",
		detail:    "add:Reminder",
		startedAt: time.Now().Add(-time.Second),
	}}

	updated, _ := runModel.Update(responseCompletedMsg{ResponseID: 2, Text: "Done"})
	runModel = updated.(model)

	toolCell, ok := runModel.messages[0].(toolTranscriptCell)
	require.True(t, ok)
	require.Equal(t, toolTranscriptTerminalStatusInterrupted, toolCell.terminalStatus)
	require.False(t, toolCell.completedAt.IsZero())
	require.Contains(t, stripANSI(renderTranscriptCells(runModel.messages)), "Interrupted Automation")
	require.Equal(t, "Morph: Done", runModel.messages[1].PlainText())
}

func TestModel_TerminalizesOnlyToolsFromActiveResponse(t *testing.T) {
	for _, test := range []struct {
		name       string
		finalize   func(*model)
		wantStatus toolTranscriptTerminalStatus
	}{
		{name: "failed", finalize: func(value *model) {
			value.failRunningToolTranscriptCells(time.Now(), "")
		}, wantStatus: toolTranscriptTerminalStatusFailed},
		{name: "interrupted", finalize: func(value *model) { value.interruptRunningToolTranscriptCells(time.Now()) }, wantStatus: toolTranscriptTerminalStatusInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			runModel := newModel()
			runModel.messages = []transcriptCell{
				toolTranscriptCell{id: "old", action: "Automation", detail: "add:Old"},
				toolTranscriptCell{id: "current", action: "Automation", detail: "add:Current"},
			}
			runModel.responseStartMessageIndex = 1

			test.finalize(&runModel)

			historical := runModel.messages[0].(toolTranscriptCell)
			current := runModel.messages[1].(toolTranscriptCell)
			require.Empty(t, historical.terminalStatus)
			require.Equal(t, test.wantStatus, current.terminalStatus)
		})
	}
}

func TestModel_UpdateSurfacesProviderErrorAsFriendlyMessage(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 2
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseCompletedMsg{
		ResponseID: 2,
		Err: errors.New(
			`POST "https://api.anthropic.com/v1/messages": 400 Bad Request ` +
				`{"type":"error","error":{"type":"invalid_request_error","message":"tools.1.custom is not supported"}}`,
		),
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Equal(
		t,
		[]string{"Error: Model provider rejected the request: tools.1.custom is not supported"},
		transcriptCellPlainTexts(runModel.messages),
	)
}

func TestModel_UpdateSuppressesLiveTraceErrorDuringActiveResponse(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 4
	runModel.events = make(chan tea.Msg, 1)

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 4,
		Message:    sessionErrorMsg{Message: "provider failed"},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))

	updated, cmd = runModel.Update(responseCompletedMsg{
		ResponseID: 4,
		Err:        errors.New("provider failed"),
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Equal(t, []string{"Error: provider failed"}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateAppliesSessionErrorMessage(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.Update(sessionErrorMsg{Message: "daemon unavailable"})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "response failed", runModel.status.Text())
	require.Equal(t, []string{"Error: daemon unavailable"}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateIgnoresStaleResponseEvents(t *testing.T) {
	runModel := newModel()
	runModel.responding = false
	runModel.responseID = 3
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "final"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(responseEventMsg{
		ResponseID: 3,
		Message:    assistantTextDeltaMsg{Text: "late delta"},
	})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.live)
	require.Equal(t, []string{"Morph: final"}, transcriptCellPlainTexts(runModel.messages))
	require.NotContains(t, stripANSI(runModel.transcript.View()), "late delta")
}

func TestModel_UpdateHandlesResponseEventsClosed(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3

	updated, cmd := runModel.Update(responseEventsClosedMsg{ResponseID: 3})

	require.Nil(t, cmd)
	require.True(t, updated.(model).responding)

	updated, cmd = runModel.Update(responseEventsClosedMsg{ResponseID: 2})

	require.Nil(t, cmd)
	require.True(t, updated.(model).responding)
}

func TestModel_UpdateCompletesAfterResponseEventsCloseFirst(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3
	runModel.responseEventStreamActive = true
	runModel.events = make(chan tea.Msg)

	updated, cmd := runModel.Update(responseEventsClosedMsg{ResponseID: 3})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.False(t, runModel.responseEventStreamActive)
	require.Nil(t, runModel.events)

	updated, _ = runModel.Update(responseCompletedMsg{ResponseID: 3, Text: "done"})
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Equal(t, "Morph: done", runModel.messages[0].PlainText())
}

func TestModel_UpdateIgnoresStaleResponseCompletion(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 5

	updated, cmd := runModel.Update(responseCompletedMsg{ResponseID: 4, Text: "stale"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
}

func TestWaitForResponseEventReturnsQueuedAndClosedMessages(t *testing.T) {
	events := make(chan tea.Msg, 1)
	events <- sessionErrorMsg{Message: "failed"}

	msg := waitForResponseEvent(9, events)()

	require.Equal(t, responseEventBatchMsg{
		ResponseID: 9,
		Messages:   []tea.Msg{sessionErrorMsg{Message: "failed"}},
	}, msg)

	close(events)
	msg = waitForResponseEvent(9, events)()

	require.Equal(t, responseEventsClosedMsg{ResponseID: 9}, msg)
}

func TestModel_UpdateAddsTraceMessagesToTranscript(t *testing.T) {
	runModel := newModel()

	for index, msg := range []tea.Msg{
		toolInvocationStartedMsg{Name: "read_file"},
		toolInvocationCompletedMsg{Name: "read_file"},
		safetyEventMsg{Action: "blocked", FindingIDs: []string{"prompt_exfiltration"}},
	} {
		updated, cmd := runModel.Update(msg)
		if index == 0 {
			require.NotNil(t, cmd)
		} else {
			require.Nil(t, cmd)
		}
		runModel = updated.(model)
	}

	require.Equal(t, []string{
		transcriptCellPlainText(toolTranscriptTestCell("", "read_file", "")),
		transcriptCellPlainText(toolTranscriptTestCell("", "read_file", "", true)),
		"Safety: blocked: prompt_exfiltration",
	}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateMergesCompletedToolAfterInterleavedSafetyEvent(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseStartMessageIndex = len(runModel.messages)

	for index, msg := range []tea.Msg{
		toolInvocationStartedMsg{ID: "call_1", Name: "web_extract"},
		safetyEventMsg{Action: "blocked", FindingIDs: []string{"invisible_unicode"}},
		toolInvocationCompletedMsg{ID: "call_1", Name: "web_extract"},
	} {
		updated, cmd := runModel.Update(msg)
		switch index {
		case 0, 2:
			require.NotNil(t, cmd)
		default:
			require.Nil(t, cmd)
		}
		runModel = updated.(model)
	}

	require.Equal(t, []string{
		transcriptCellPlainText(toolTranscriptTestCell("call_1", "web_extract", "", true)),
		"Safety: blocked: invisible_unicode",
	}, transcriptCellPlainTexts(runModel.messages))
	require.NotContains(t, stripANSI(runModel.transcript.View()), "Extracting from web")
	require.Contains(t, stripANSI(runModel.transcript.View()), "Extraction finished")
}

func TestModel_UpdateDoesNotMergeCompletedToolBeforeCurrentResponse(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{
		userTranscriptCell{text: "first"},
		toolTranscriptTestCell("call_1", "web_extract", ""),
		assistantTranscriptCell{text: "first done"},
		userTranscriptCell{text: "second"},
	}
	runModel.responding = true
	runModel.responseStartMessageIndex = len(runModel.messages)
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(toolInvocationCompletedMsg{ID: "call_1", Name: "web_extract"})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, []string{
		"You: first",
		transcriptCellPlainText(toolTranscriptTestCell("call_1", "web_extract", "")),
		"Morph: first done",
		"You: second",
		transcriptCellPlainText(toolTranscriptTestCell("call_1", "web_extract", "", true)),
	}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateAnimatesRunningToolTranscriptDot(t *testing.T) {
	originalInterval := toolAnimationInterval
	t.Cleanup(func() {
		toolAnimationInterval = originalInterval
	})
	toolAnimationInterval = time.Nanosecond
	runModel := newModel()

	updated, cmd := runModel.Update(toolInvocationStartedMsg{ID: "call_1", Name: "web_search"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.toolAnimationActive)
	require.Contains(t, stripANSI(runModel.transcript.View()), "● Web Search")
	require.Equal(t, toolAnimationTickMsg{}, cmd())

	updated, cmd = runModel.Update(toolAnimationTickMsg{})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.toolAnimationFrame)
	require.Contains(t, stripANSI(runModel.transcript.View()), "◖ Web Search")

	updated, cmd = runModel.Update(toolInvocationCompletedMsg{ID: "call_1", Name: "web_search"})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Contains(t, stripANSI(runModel.transcript.View()), "● Searched")

	updated, cmd = runModel.Update(toolAnimationTickMsg{})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.toolAnimationActive)
	require.Contains(t, stripANSI(runModel.transcript.View()), "● Searched")
}

func TestModel_UpdateKeepsCompletedSelectionDuringToolAnimation(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id:        "call_1",
		action:    "Web Search",
		startedAt: time.Now(),
	}}
	runModel.setTranscriptContent()
	runModel.selection = transcriptSelection{
		active:  true,
		content: runModel.transcript.GetContent(),
		start:   transcriptSelectionPoint{offset: 0},
		end:     transcriptSelectionPoint{offset: 1},
	}
	runModel.applyTranscriptSelectionStyle()

	updated, cmd := runModel.Update(toolAnimationTickMsg{})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.selection.active)
	require.Contains(t, runModel.transcript.View(), "\x1b[7m")
}

func TestModel_UpdateAnimatesThinkingComposerBorder(t *testing.T) {
	originalInterval := thinkingComposerInterval
	t.Cleanup(func() {
		thinkingComposerInterval = originalInterval
	})
	thinkingComposerInterval = time.Nanosecond
	runModel := newModel()
	runModel.responding = true

	cmd := runModel.startThinkingComposer()
	require.NotNil(t, cmd)
	require.True(t, runModel.thinkingComposerActive)
	require.Equal(t, getThinkingComposerBorderColor(0), runModel.getInputFrameBorderColor())
	require.Equal(t, thinkingComposerTickMsg{}, cmd())

	updated, cmd := runModel.Update(thinkingComposerTickMsg{})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.thinkingComposerFrame)
	require.Equal(t, getThinkingComposerBorderColor(1), runModel.getInputFrameBorderColor())

	runModel.live = assistantTranscriptCell{text: "hello"}
	updated, cmd = runModel.Update(thinkingComposerTickMsg{})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.thinkingComposerActive)
	require.Equal(t, "8", runModel.getInputFrameBorderColor())
}

func TestModel_ThinkingComposerBorderWaitsForRunningTool(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseRunningToolCount = 1
	runModel.messages = []transcriptCell{toolTranscriptTestCell("call_1", "web_search", "")}

	require.False(t, runModel.isThinkingComposerVisible())
	require.Equal(t, "8", runModel.getInputFrameBorderColor())

	runModel.responseRunningToolCount = 0
	runModel.messages = []transcriptCell{toolTranscriptTestCell("call_1", "web_search", "", true)}
	require.True(t, runModel.isThinkingComposerVisible())
	require.Equal(t, getThinkingComposerBorderColor(0), runModel.getInputFrameBorderColor())
}

func TestModel_ThinkingComposerIgnoresStaleRunningToolCells(t *testing.T) {
	setActiveTestProfile(t, t.TempDir())
	client := &fakeTUIChatClient{reply: "hello back"}
	runModel := newModelWithClient(client)
	runModel.namePromptEnabled = false
	runModel.messages = []transcriptCell{toolTranscriptTestCell("old_call", "web_search", "")}
	runModel.setTranscriptContent()
	runModel.input.SetValue("hello")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.True(t, runModel.isModelThinking())
	require.True(t, runModel.isThinkingComposerVisible())
	require.Contains(t, stripANSI(runModel.renderBottomStatusPanel()), "Thinking")
}

func TestModel_ThinkingComposerBorderCanBeDisabled(t *testing.T) {
	disabled := false
	runModel := newModelWithClientContextAndConfig(
		context.Background(),
		nil,
		&config.Config{TUI: config.TUIConfig{ThinkingComposer: &disabled}},
	)
	runModel.responding = true

	require.False(t, runModel.thinkingComposerEnabled)
	require.False(t, runModel.isThinkingComposerVisible())
	require.NotNil(t, runModel.startThinkingComposer())
	require.True(t, runModel.thinkingComposerActive)
	require.Equal(t, "8", runModel.getInputFrameBorderColor())
}

func TestModel_UpdatePreventsOverlappingPromptSubmission(t *testing.T) {
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.responding = true
	runModel.input.SetValue("second prompt")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.input.Value())
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	_ = cmd()
	require.Equal(t, "second prompt", client.message)
}

func TestModel_UpdateKeepsCommandsLocalDuringActiveResponse(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.responding = true
	runModel.input.SetValue("/clear")
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "old"}}

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.input.Value())
	require.Equal(t, "transcript cleared", runModel.status.Text())
}

func TestModel_UpdatePastesLargeMultilineContent(t *testing.T) {
	runModel := newModel()
	paste := strings.Join([]string{
		"first",
		"second",
		strings.Repeat("x", getInputInnerWidth(runModel.width)+1),
	}, "\n")

	updated, cmd := runModel.Update(tea.PasteMsg{Content: paste})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, paste, runModel.input.Value())
	require.GreaterOrEqual(t, runModel.input.Height(), 3)
}

func TestModel_UpdateTrimsTrailingPasteLineBreaks(t *testing.T) {
	runModel := newModel()
	paste := "first\nsecond\n\n"

	updated, cmd := runModel.Update(tea.PasteMsg{Content: paste})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "first\nsecond", runModel.input.Value())
	require.Equal(t, 2, runModel.input.Height())
	require.Contains(t, stripANSI(runModel.input.View()), "second")
}

func TestModel_UpdateSizesPasteUsingTextareaWidth(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.height = 20
	runModel.resize()
	paste := strings.Join([]string{
		`office.\n[...]\nOn Monday Iran said it had responded to the latest US proposal and that exchanges with Washington were continuing through Pakistani mediators.`,
		`\n[...]\nTrump's message echoed his threat that a \"whole civilisation\" would die unless Iran agreed to a deal to end the war.`,
		`\n[...]\nIsraeli and US forces began massive air strikes on Iran on 28 February. The ceasefire meant to facilitate`,
	}, "")

	updated, cmd := runModel.Update(tea.PasteMsg{Content: paste})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Greater(t, runModel.input.Height(), 1)
	require.Zero(t, runModel.input.ScrollYOffset())
	require.Contains(t, stripANSI(runModel.input.View()), "office.")
}

func TestModel_UpdateNavigatesPromptHistory(t *testing.T) {
	runModel := newModel()
	for _, prompt := range []string{"first prompt", "second prompt"} {
		runModel.input.SetValue(prompt)
		updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		require.Nil(t, cmd)
		runModel = updated.(model)
	}
	runModel.input.SetValue("draft")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "second prompt", runModel.input.Value())

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "first prompt", runModel.input.Value())

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "second prompt", runModel.input.Value())

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "draft", runModel.input.Value())
}

func TestModel_UpdateDeduplicatesConsecutivePromptHistory(t *testing.T) {
	runModel := newModel()
	for range 2 {
		runModel.input.SetValue("repeat")
		updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		require.Nil(t, cmd)
		runModel = updated.(model)
	}

	require.Equal(t, []string{"repeat"}, runModel.history)
	require.Equal(t, 1, runModel.historyAt)
}

func TestModel_AddPromptHistoryIgnoresBlankValues(t *testing.T) {
	runModel := newModel()

	runModel.addPromptHistory(" \n\t ")

	require.Empty(t, runModel.history)
	require.Zero(t, runModel.historyAt)
}

func TestModel_UpdateKeepsHistoryStableWhenEmpty(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("draft")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "draft", runModel.input.Value())
	require.Empty(t, runModel.history)
}

func TestModel_UpdateKeepsHistoryStableAtNewestEntry(t *testing.T) {
	runModel := newModel()
	runModel.history = []string{"first"}
	runModel.historyAt = len(runModel.history)
	runModel.input.SetValue("draft")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "draft", runModel.input.Value())
	require.Equal(t, len(runModel.history), runModel.historyAt)
}

func TestModel_UpdateLetsMultilineInputUseArrowKeys(t *testing.T) {
	runModel := newModel()
	runModel.history = []string{"previous prompt"}
	runModel.historyAt = len(runModel.history)
	runModel.input.SetValue("first\nsecond")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))

	require.NotNil(t, cmd)
	require.Equal(t, "first\nsecond", updated.(model).input.Value())
}

func TestModel_UpdatePreservesLiveAssistantCellDuringStreaming(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{userTranscriptCell{text: "hello"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{Text: "first line\npartial"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, []string{"You: hello"}, transcriptCellPlainTexts(runModel.messages))
	require.Equal(t, "Morph: first line\npartial", transcriptCellPlainText(runModel.live))
	content := stripANSI(runModel.transcript.View())
	require.Contains(t, content, "❯ hello")
	require.Contains(t, content, "first line")
	require.NotContains(t, content, "Morph: first line")
	require.Contains(t, content, "partial")
}

func TestModel_UpdateConvertsLiveAssistantCellToHistoryAtCompletion(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{userTranscriptCell{text: "hello"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{Text: "first line\npartial"})
	require.Nil(t, cmd)
	updated, cmd = updated.(model).Update(assistantResponseCompletedMsg{})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.live)
	require.Equal(t, []string{"You: hello", "Morph: first line\npartial"}, transcriptCellPlainTexts(runModel.messages))
	require.Equal(t, "", runModel.stream.Render())
	content := stripANSI(runModel.transcript.View())
	require.Contains(t, content, "first line")
	require.NotContains(t, content, "Morph: first line")
	require.Contains(t, content, "partial")
}

func TestModel_UpdateRendersReasoningDeltasOutsideAssistantStream(t *testing.T) {
	now := time.Date(2026, 5, 20, 11, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	t.Cleanup(func() { currentTime = originalCurrentTime })
	currentTime = func() time.Time { return now }

	runModel := newModel()
	runModel.messages = []transcriptCell{userTranscriptCell{text: "hello"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{Channel: "reasoning", Text: "first "})
	require.Nil(t, cmd)
	updated, cmd = updated.(model).Update(assistantTextDeltaMsg{Channel: "reasoning", Text: "token"})
	require.Nil(t, cmd)
	now = now.Add(3 * time.Second)
	updated, cmd = updated.(model).Update(assistantTextDeltaMsg{Text: "answer"})
	require.Nil(t, cmd)
	updated, cmd = updated.(model).Update(assistantResponseCompletedMsg{})
	require.Nil(t, cmd)

	runModel = updated.(model)
	require.Empty(t, runModel.live)
	thinking, ok := runModel.messages[1].(thinkingTranscriptCell)
	require.True(t, ok)
	require.Equal(t, "first token", thinking.summary)
	require.True(t, thinking.completed)
	require.False(t, thinking.expanded)
	require.Equal(t, []string{
		"You: hello",
		"Thought: 3s",
		"Morph: answer",
	}, transcriptCellPlainTexts(runModel.messages))
	content := stripANSI(runModel.transcript.View())
	require.Contains(t, content, "Thought for 3s")
	require.Contains(t, content, "answer")
	require.NotContains(t, content, "Reasoning:")
	require.NotContains(t, content, "first token")
}

func TestModel_UpdateRendersReasoningSummaryChannel(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{
		Channel: string(modelcatalog.StreamChannelReasoningSummary),
		Text:    "Checked the active run.",
	})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Len(t, runModel.messages, 1)
	thinking, ok := runModel.messages[0].(thinkingTranscriptCell)
	require.True(t, ok)
	require.Equal(t, "Checked the active run.", thinking.summary)
	require.Contains(t, stripANSI(runModel.transcript.View()), "Checked the active run.")
}

func TestModel_UpdateReasoningCompletedCollapsesEarlierThinkingCell(t *testing.T) {
	now := time.Date(2026, 5, 20, 11, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	t.Cleanup(func() { currentTime = originalCurrentTime })
	currentTime = func() time.Time { return now }

	runModel := newModel()
	runModel.responding = true
	runModel.messages = []transcriptCell{userTranscriptCell{text: "hello"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{Channel: "reasoning", Text: "checking messages"})
	require.Nil(t, cmd)
	now = now.Add(5 * time.Second)
	updated, cmd = updated.(model).Update(toolInvocationStartedMsg{
		ID:   "call_1",
		Name: "session_messages",
	})
	require.NotNil(t, cmd)
	updated, cmd = updated.(model).Update(toolInvocationCompletedMsg{
		ID:   "call_1",
		Name: "session_messages",
	})
	require.NotNil(t, cmd)
	updated, cmd = updated.(model).Update(assistantTextDeltaMsg{Channel: "reasoning", Text: "checking again"})
	require.Nil(t, cmd)
	now = now.Add(17 * time.Second)
	updated, cmd = updated.(model).Update(reasoningCompletedMsg{Duration: 17 * time.Second})
	require.Nil(t, cmd)
	updated, cmd = updated.(model).Update(assistantResponseCompletedMsg{Text: "done"})
	require.Nil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, []string{
		"You: hello",
		"Thought: 5s",
		transcriptCellPlainText(toolTranscriptTestCell("call_1", "session_messages", "", true)),
		"Thought: 17s",
		"Morph: done",
	}, transcriptCellPlainTexts(runModel.messages))
	content := stripANSI(runModel.transcript.View())
	require.Contains(t, content, "Thought for 5s")
	require.Contains(t, content, "Thought for 17s")
	require.Contains(t, content, "Fetched Session Messages")
	require.NotContains(t, content, "Thinking")
	require.NotContains(t, content, "checking messages")
	require.NotContains(t, content, "checking again")
}

func TestModel_UpdateStreamedRenderMatchesCommittedAssistantText(t *testing.T) {
	runModel := newModel()
	deltas := []string{"# Title\n", "\n- one", "\n- two\n", "tail\n\n"}
	for _, delta := range deltas {
		updated, cmd := runModel.Update(assistantTextDeltaMsg{Text: delta})
		require.Nil(t, cmd)
		runModel = updated.(model)
	}
	live := runModel.live

	updated, cmd := runModel.Update(assistantResponseCompletedMsg{})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, []string{transcriptCellPlainText(live)}, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.live)
}

func TestModel_UpdateUsesFinalAssistantTextAtCompletion(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{userTranscriptCell{text: "hello"}}
	runModel.setTranscriptContent()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{Text: "draft"})
	require.Nil(t, cmd)
	updated, cmd = updated.(model).Update(assistantResponseCompletedMsg{Text: "final"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.live)
	require.Equal(t, []string{"You: hello", "Morph: final"}, transcriptCellPlainTexts(runModel.messages))
	require.NotContains(t, stripANSI(runModel.transcript.View()), "draft")
}

func TestModel_UpdatePreservesFinalAssistantWhitespace(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{Text: "draft"})
	require.Nil(t, cmd)
	updated, cmd = updated.(model).Update(assistantResponseCompletedMsg{Text: "final\n\n"})

	require.Nil(t, cmd)
	require.Equal(t, []string{"Morph: final\n\n"}, transcriptCellPlainTexts(updated.(model).messages))
}

func TestModel_UpdateIgnoresEmptyAssistantDelta(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.Update(assistantTextDeltaMsg{})

	require.Nil(t, cmd)
	require.Empty(t, updated.(model).live)
	require.Empty(t, transcriptCellPlainTexts(updated.(model).messages))
}

func TestModel_UpdateClearsEmptyAssistantCompletion(t *testing.T) {
	runModel := newModel()
	runModel.live = assistantTranscriptCell{text: "draft"}
	runModel.stream.Add("   ")

	updated, cmd := runModel.Update(assistantResponseCompletedMsg{})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.live)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Empty(t, runModel.stream.Render())
}

func TestAssistantTranscriptCell_IgnoresBlankText(t *testing.T) {
	require.True(t, assistantTranscriptCell{text: " \n\t "}.IsEmpty())
}

func TestModel_UpdateInsertsPromptNewlineOnShiftEnter(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("first line")

	updated, cmd := runModel.Update(tea.KeyPressMsg{
		Code: tea.KeyEnter,
		Mod:  tea.ModShift,
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "first line\n", runModel.input.Value())
	require.Equal(t, 2, runModel.input.Height())
	require.Zero(t, runModel.input.ScrollYOffset())
	require.Contains(t, stripANSI(runModel.input.View()), "first line")
	inputPromptValue := str.String(inputPrompt)
	require.Equal(t, 1, strings.Count(stripANSI(runModel.input.View()), inputPromptValue.Trim()))
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateInsertsPromptNewlineOnTerminalModifiedEnterFallbacks(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{
			name: "alt_enter",
			key: tea.KeyPressMsg{
				Code: tea.KeyEnter,
				Mod:  tea.ModAlt,
			},
		},
		{
			name: "ctrl_j",
			key: tea.KeyPressMsg{
				Code: 'j',
				Mod:  tea.ModCtrl,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runModel := newModel()
			runModel.input.SetValue("first line")

			updated, cmd := runModel.Update(tt.key)

			require.NotNil(t, cmd)
			runModel = updated.(model)
			require.Equal(t, "first line\n", runModel.input.Value())
			require.Equal(t, 2, runModel.input.Height())
			require.Empty(t, transcriptCellPlainTexts(runModel.messages))
		})
	}
}

func TestModel_UpdateDeletesCurrentPromptLineOnCommandDelete(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
	}{
		{name: "command_backspace", key: tea.Key{Code: tea.KeyBackspace, Mod: tea.ModSuper}},
		{name: "command_delete", key: tea.Key{Code: tea.KeyDelete, Mod: tea.ModSuper}},
		{name: "meta_backspace", key: tea.Key{Code: tea.KeyBackspace, Mod: tea.ModMeta}},
		{name: "ctrl_backspace", key: tea.Key{Code: tea.KeyBackspace, Mod: tea.ModCtrl}},
		{name: "ctrl_u", key: tea.Key{Code: 'u', Mod: tea.ModCtrl}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runModel := newModel()
			runModel.input.SetValue("first line\nsecond line")

			updated, cmd := runModel.Update(tea.KeyPressMsg(tt.key))
			require.NotNil(t, cmd)

			runModel = updated.(model)
			require.Equal(t, "first line\n", runModel.input.Value())
			require.Empty(t, transcriptCellPlainTexts(runModel.messages))
		})
	}
}

func TestModel_UpdateGrowsPromptForWrappedText(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue(strings.Repeat("a", getInputInnerWidth(runModel.width)+1))
	runModel.resize()

	require.Equal(t, 2, runModel.input.Height())
}

func TestModel_UpdateKeepsTranscriptAtBottomWhenPromptWrapGrowsComposer(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 12; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.transcript.AtBottom())

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	for i := 0; i < getInputInnerWidth(runModel.width)+2; i++ {
		updated, cmd = runModel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
		require.NotNil(t, cmd)
		runModel = updated.(model)
	}

	require.Greater(t, runModel.input.Height(), 1)
	require.True(t, runModel.transcript.AtBottom())
	require.NotContains(t, stripANSI(runModel.View().Content), jumpToBottomLabel)
}

func TestModel_UpdateKeepsTranscriptAtBottomWhenNewlineGrowsComposer(t *testing.T) {
	runModel := newModel()
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 12; index++ {
		runModel.messages = append(runModel.messages, systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)})
	}
	runModel.setTranscriptContent()
	runModel.input.SetValue("first line")
	require.True(t, runModel.transcript.AtBottom())

	updated, cmd := runModel.Update(tea.KeyPressMsg{
		Code: tea.KeyEnter,
		Mod:  tea.ModShift,
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, 2, runModel.input.Height())
	require.True(t, runModel.transcript.AtBottom())
	require.NotContains(t, stripANSI(runModel.View().Content), jumpToBottomLabel)
}

func TestModel_UpdateShowsAllPromptRowsWhenSpaceAllows(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue(strings.Join([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
	}, "\n"))
	runModel.resize()

	require.Equal(t, 5, runModel.input.Height())
}

func TestModel_UpdateLimitsPromptRowsToAvailableHeight(t *testing.T) {
	runModel := newModel()
	runModel.height = 6
	runModel.input.SetValue(strings.Join([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
	}, "\n"))
	runModel.resize()

	require.Equal(t, 1, runModel.input.Height())
	require.Equal(t, 1, runModel.transcript.Height())
}

func TestModel_UpdateIgnoresEmptyEnter(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.Nil(t, cmd)

	runModel = updated.(model)
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateClampsTinyWindowSize(t *testing.T) {
	runModel := newModel()
	updated, cmd := runModel.Update(tea.WindowSizeMsg{})
	require.Nil(t, cmd)

	resized := updated.(model)
	require.Equal(t, 1, resized.width)
	require.Equal(t, 1, resized.height)
	require.Equal(t, 1, resized.transcript.Width())
	require.GreaterOrEqual(t, resized.transcript.Height(), 1)
	require.GreaterOrEqual(t, resized.input.Height(), 1)
}

func stripANSI(value string) string {
	return ansi.Strip(value)
}

func setActiveTestProfile(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})

	profile.SetActive(profile.Profile{Name: profile.DefaultName, HomeDir: home})
}

func writeSetupProfileConfig(t *testing.T, home string, content string) {
	t.Helper()
	contentValue := str.String(content)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(contentValue.Trim()+"\n"), 0o600))
}

func newSetupModelSelectionTestModel(t *testing.T) model {
	t.Helper()

	return newSetupModelSelectionTestModelWithHome(t, t.TempDir())
}

func newSetupModelSelectionTestModelWithHome(t *testing.T, home string) model {
	t.Helper()

	setActiveTestProfile(t, home)
	writeSetupProfileConfig(t, home, `
name: test-agent
models:
    main:
        provider: ""
        name: ""
search:
    vector:
        enabled: false
`)
	runModel := newModel()
	runModel.nameInput.SetValue("Nedy")
	updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(model)
}

func selectSetupProvider(t *testing.T, runModel *model, providerID string) {
	t.Helper()

	if runModel.setupModelStep == setupModelStepAuthMethod {
		authMethod := setupAuthMethodAPIKey
		switch providerID {
		case constants.ModelProviderOpenAICodex, constants.ModelProviderGitHubCopilot:
			authMethod = setupAuthMethodSubscription
		case constants.ModelProviderOllama:
			authMethod = setupAuthMethodLocal
		}
		selectSetupAuthMethod(t, runModel, authMethod)
		updated, _ := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		*runModel = updated.(model)
	}

	for index, provider := range runModel.setupProviders {
		if provider.ID == providerID {
			runModel.setupItemSelected = index
			return
		}
	}

	t.Fatalf("setup provider %q not found", providerID)
}

func selectSetupAuthMethod(t *testing.T, runModel *model, authMethod string) {
	t.Helper()

	for index, option := range setupAuthMethodOptions {
		if option.ID == authMethod {
			runModel.setupItemSelected = index
			return
		}
	}

	t.Fatalf("setup auth method %q not found", authMethod)
}

func selectSetupModel(t *testing.T, runModel *model, modelID string) {
	t.Helper()

	for index, model := range runModel.setupModels {
		if model.ID == modelID {
			runModel.setupItemSelected = index
			return
		}
	}

	t.Fatalf("setup model %q not found", modelID)
}

func getSetupModelIDs(options []rpcclient.ModelOption) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}

	return ids
}

func getSetupProviderIDs(options []modelcatalog.ProviderOption) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}

	return ids
}

func getSetupModelOption(t *testing.T, options []rpcclient.ModelOption, modelID string) rpcclient.ModelOption {
	t.Helper()

	for _, option := range options {
		if option.ID == modelID {
			return option
		}
	}

	t.Fatalf("setup model %q not found", modelID)
	return rpcclient.ModelOption{}
}

func getVisibleSetupProviderRow(t *testing.T, runModel *model, providerID string) int {
	t.Helper()

	if runModel.setupModelStep == setupModelStepAuthMethod {
		selectSetupProvider(t, runModel, providerID)
	}

	for index, provider := range runModel.setupProviders {
		if provider.ID == providerID {
			runModel.setProfileModelSetupSelection(index, len(runModel.setupProviders))
			return (index - runModel.setupOffset) * 2
		}
	}

	t.Fatalf("setup provider %q not found", providerID)
	return 0
}

func getVisibleSetupModelRow(t *testing.T, runModel *model, modelID string) int {
	t.Helper()

	for index, model := range runModel.setupModels {
		if model.ID == modelID {
			runModel.setProfileModelSetupSelection(index, len(runModel.setupModels))
			return index - runModel.setupOffset
		}
	}

	t.Fatalf("setup model %q not found", modelID)
	return 0
}

func getLineContaining(content string, value string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, value) {
			return line
		}
	}

	return ""
}

func requireSetupHintAlignedToBorder(t *testing.T, content string) {
	t.Helper()

	borderLine := strings.TrimRight(getLineContaining(content, "╭"), " ")
	hintLine := getLineContaining(content, setupCloseHint)
	visibleHintLine := strings.TrimRight(hintLine, " ")
	require.NotEmpty(t, borderLine)
	require.NotEmpty(t, hintLine)
	require.Equal(t, lipgloss.Width(borderLine), lipgloss.Width(visibleHintLine)+1)
}

type fakeSetupSubscriptionProvider struct {
	output        string
	credential    appcredential.StoredCredential
	err           error
	waitForCancel bool
}

func (p fakeSetupSubscriptionProvider) Login(
	ctx context.Context,
	options appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	if options.Output != nil && p.output != "" {
		_, _ = io.WriteString(options.Output, p.output)
	}
	if p.waitForCancel {
		<-ctx.Done()
		return appcredential.StoredCredential{}, ctx.Err()
	}
	if p.err != nil {
		return appcredential.StoredCredential{}, p.err
	}
	trimmedValueValue := str.String(p.credential.Type)
	if trimmedValueValue.Trim() == "" {
		p.credential.Type = appcredential.TypeOAuth
	}
	tokenValue := str.String(p.credential.Token)
	if tokenValue.Trim() == "" {
		p.credential.Token = "oauth-token"
	}

	return p.credential, nil
}

func (p fakeSetupSubscriptionProvider) Refresh(
	context.Context,
	appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	return appcredential.StoredCredential{}, errors.New("refresh not implemented")
}

func (p fakeSetupSubscriptionProvider) AuthHeaders(
	context.Context,
	appcredential.StoredCredential,
) (map[string]string, error) {
	return nil, errors.New("auth headers not implemented")
}

type fakeSetupCredentialStore struct {
	credentials map[string]appcredential.StoredCredential
	err         error
}

func newFakeSetupCredentialStore() *fakeSetupCredentialStore {
	return &fakeSetupCredentialStore{credentials: make(map[string]appcredential.StoredCredential)}
}

func (s *fakeSetupCredentialStore) Set(provider string, credential appcredential.StoredCredential) error {
	if s.err != nil {
		return s.err
	}

	providerValue := str.String(provider)
	s.credentials[providerValue.Trim()] = credential
	return nil
}

func stubSetupOAuth(
	t *testing.T,
	store setupCredentialStore,
	provider appcredential.SubscriptionProvider,
) func() {
	t.Helper()

	originalProvider := getSetupSubscriptionProvider
	originalStore := newSetupCredentialStore
	getSetupSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return provider, provider != nil
	}
	newSetupCredentialStore = func() setupCredentialStore {
		return store
	}

	return func() {
		getSetupSubscriptionProvider = originalProvider
		newSetupCredentialStore = originalStore
	}
}

func stubSetupOllamaPull(
	t *testing.T,
	pull func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error,
) func() {
	t.Helper()

	original := pullSetupOllamaModel
	pullSetupOllamaModel = pull

	return func() {
		pullSetupOllamaModel = original
	}
}

type setupModelPullBatchMessages struct {
	progress  []setupModelPullProgressMsg
	completed setupModelPullCompletedMsg
}

func runSetupModelPullBatch(t *testing.T, cmd tea.Cmd) setupModelPullBatchMessages {
	t.Helper()

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(batch), 2)

	require.Nil(t, batch[0]())
	wait := batch[1]
	var messages setupModelPullBatchMessages
	for {
		msg = wait()
		switch msg := msg.(type) {
		case setupModelPullProgressMsg:
			messages.progress = append(messages.progress, msg)
		case setupModelPullCompletedMsg:
			messages.completed = msg
			return messages
		case setupModelPullClosedMsg:
			return messages
		default:
			t.Fatalf("unexpected setup pull message %T", msg)
		}
	}
}

func runSetupOAuthBatch(t *testing.T, cmd tea.Cmd) (setupOAuthOutputMsg, setupOAuthCompletedMsg) {
	t.Helper()

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(batch), 2)

	outputCh := make(chan tea.Msg, 1)
	go func() {
		outputCh <- batch[1]()
	}()

	completedMsg, ok := batch[0]().(setupOAuthCompletedMsg)
	require.True(t, ok)

	select {
	case msg := <-outputCh:
		outputMsg, ok := msg.(setupOAuthOutputMsg)
		require.True(t, ok)
		return outputMsg, completedMsg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for setup oauth output")
		return setupOAuthOutputMsg{}, setupOAuthCompletedMsg{}
	}
}

func runSetupModelOptionsRefreshBatch(t *testing.T, cmd tea.Cmd) setupModelOptionsLoadedMsg {
	t.Helper()

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(batch), 1)

	for _, item := range batch {
		if item == nil {
			continue
		}
		if loaded, ok := item().(setupModelOptionsLoadedMsg); ok {
			return loaded
		}
	}

	t.Fatal("setup model options refresh message not found")
	return setupModelOptionsLoadedMsg{}
}

func setupModelRuntimeSelectedMessageFromBatch(t *testing.T, cmd tea.Cmd) setupModelRuntimeSelectedMsg {
	t.Helper()

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(batch), 1)

	selected, ok := batch[len(batch)-1]().(setupModelRuntimeSelectedMsg)
	require.True(t, ok)

	return selected
}

func makeOpenAITestJWTForSetup(t *testing.T) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-test",
		},
	})
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(body)

	return strings.Join([]string{header, payload, "signature"}, ".")
}

func getTranscriptContentRow(t *testing.T, runModel model, needle string) int {
	t.Helper()

	lines := strings.Split(stripANSI(runModel.transcript.GetContent()), "\n")
	for index, line := range lines {
		if strings.Contains(line, needle) {
			return index
		}
	}

	t.Fatalf("transcript row containing %q not found in %q", needle, runModel.transcript.GetContent())
	return 0
}

func trimTrailingLineSpaces(value string) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}

	return strings.Join(lines, "\n")
}

func responseMessageFromBatch(t *testing.T, cmd tea.Cmd) responseCompletedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(batch), 2)

	msg, ok := batch[1]().(responseCompletedMsg)
	require.True(t, ok)

	return msg
}

type fakeTUIChatClient struct {
	events                []rpcclient.Event
	reply                 string
	err                   error
	compactResult         rpcclient.CompactSessionResult
	compactErr            error
	compactID             string
	respondSessionID      string
	createdSession        storage.Session
	createSessionErr      error
	createSessionID       string
	sessions              []storage.Session
	archivedSessions      []storage.Session
	listSessionsErr       error
	listArchivedErr       error
	useSessionErr         error
	usedSessionID         string
	archiveSessionErr     error
	archivedSessionID     string
	unarchiveSessionErr   error
	unarchivedSession     storage.Session
	unarchivedSessionID   string
	renamedSession        storage.Session
	renameSessionErr      error
	renamedSessionID      string
	renamedSessionTitle   string
	timeline              rpcclient.SessionTimeline
	timelineErr           error
	timelineSessionID     string
	currentSession        storage.Session
	currentSessionErr     error
	providerList          rpcclient.ProviderList
	providerListErr       error
	modelList             rpcclient.ModelList
	modelListErr          error
	modelListProvider     string
	selectedModel         rpcclient.ModelOption
	selectModelErr        error
	selectedModelID       string
	selectedModelProvider string
	providerAPIKey        string
	providerAPIKeyID      string
	providerAPIKeyErr     error
	contextStatus         rpcclient.ContextStatus
	contextErr            error
	contextSessionID      string
	message               string
	respondContext        context.Context
	calls                 int
	compactCalls          int
	createSessionCalls    int
	createSessionOptions  rpcclient.CreateSessionOptions
	listSessionCalls      int
	listArchivedCalls     int
	useSessionCalls       int
	archiveSessionCalls   int
	unarchiveCalls        int
	renameSessionCalls    int
	timelineCalls         int
	currentSessionCalls   int
	listProviderCalls     int
	listModelCalls        int
	selectModelCalls      int
	setProviderKeyCalls   int
	contextCalls          int
	closed                bool
}

func (c *fakeTUIChatClient) SubmitMessage(
	ctx context.Context,
	opts rpcclient.SubmitMessageOptions,
) (rpcclient.SessionQueueEntry, error) {
	c.calls++
	c.respondContext = ctx
	c.message = opts.Message
	c.respondSessionID = opts.SessionID
	if c.err != nil {
		return rpcclient.SessionQueueEntry{}, c.err
	}
	return rpcclient.SessionQueueEntry{
		ID:                    "que_test",
		SessionID:             opts.SessionID,
		Content:               opts.Message,
		RequestedDeliveryMode: opts.DeliveryMode,
		DeliveryMode:          opts.DeliveryMode,
		Status:                agentsession.QueueStatusCompleted,
	}, nil
}

func (c *fakeTUIChatClient) State(
	context.Context,
	string,
) (rpcclient.SessionExecutionState, error) {
	return rpcclient.SessionExecutionState{
		SessionID: c.respondSessionID,
		Queue: []rpcclient.SessionQueueEntry{{
			ID:     "que_test",
			Status: agentsession.QueueStatusCompleted,
		}},
	}, c.err
}

func (c *fakeTUIChatClient) Observe(
	context.Context,
	string,
	int64,
	func(rpcclient.SessionEvent) error,
) error {
	return c.err
}

func (c *fakeTUIChatClient) EditQueuedMessage(
	context.Context,
	string,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, c.err
}

func (c *fakeTUIChatClient) RemoveQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, c.err
}

func (c *fakeTUIChatClient) PromoteQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, c.err
}

func (c *fakeTUIChatClient) SteerQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, c.err
}

func (c *fakeTUIChatClient) InterruptRun(
	context.Context,
	string,
) (rpcclient.SessionActiveRun, bool, error) {
	return rpcclient.SessionActiveRun{}, true, c.err
}

func (c *fakeTUIChatClient) SessionAPI() rpcclient.SessionAPI {
	return c
}

func (c *fakeTUIChatClient) ModelAPI() rpcclient.ModelAPI {
	return c
}

func (c *fakeTUIChatClient) ListProviders(context.Context) (rpcclient.ProviderList, error) {
	c.listProviderCalls++
	return c.providerList, c.providerListErr
}

func (c *fakeTUIChatClient) RuntimeModel(context.Context) (rpcclient.ModelRuntime, error) {
	return rpcclient.ModelRuntime{}, nil
}

func (c *fakeTUIChatClient) ListModels(_ context.Context, opts ...rpcclient.ModelListOptions) (rpcclient.ModelList, error) {
	c.listModelCalls++
	if len(opts) > 0 {
		c.modelListProvider = opts[0].Provider
	}
	return c.modelList, c.modelListErr
}

func (c *fakeTUIChatClient) SelectModel(_ context.Context, id string, opts ...rpcclient.ModelSelectOptions) (rpcclient.ModelOption, error) {
	c.selectModelCalls++
	c.selectedModelID = id
	if len(opts) > 0 {
		c.selectedModelProvider = opts[0].Provider
	}
	iDValue := str.String(c.selectedModel.ID)
	if iDValue.Trim() != "" {
		return c.selectedModel, c.selectModelErr
	}

	return rpcclient.ModelOption{ID: id, Current: true}, c.selectModelErr
}

func (c *fakeTUIChatClient) SetProviderAPIKey(_ context.Context, provider string, apiKey string) error {
	c.setProviderKeyCalls++
	c.providerAPIKeyID = provider
	c.providerAPIKey = apiKey

	return c.providerAPIKeyErr
}

func (c *fakeTUIChatClient) Compact(_ context.Context, id string) (rpcclient.CompactSessionResult, error) {
	c.compactCalls++
	c.compactID = id
	return c.compactResult, c.compactErr
}

func (c *fakeTUIChatClient) Repair(
	context.Context,
	rpcclient.RepairSessionOptions,
) (rpcclient.RepairSessionResult, error) {
	return rpcclient.RepairSessionResult{}, nil
}

func (c *fakeTUIChatClient) Create(_ context.Context, id string) (storage.Session, error) {
	c.createSessionCalls++
	c.createSessionID = id
	return c.createdSession, c.createSessionErr
}

func (c *fakeTUIChatClient) CreateWithOptions(
	_ context.Context,
	opts rpcclient.CreateSessionOptions,
) (storage.Session, error) {
	c.createSessionCalls++
	c.createSessionOptions = opts
	c.createSessionID = opts.ID
	return c.createdSession, c.createSessionErr
}

func (c *fakeTUIChatClient) List(_ context.Context, opts ...rpcclient.SessionListOptions) ([]storage.Session, error) {
	if len(opts) > 0 && opts[0].Archived != nil && *opts[0].Archived {
		c.listArchivedCalls++
		return c.archivedSessions, c.listArchivedErr
	}

	c.listSessionCalls++
	return c.sessions, c.listSessionsErr
}

func (c *fakeTUIChatClient) Use(_ context.Context, id string) error {
	c.useSessionCalls++
	c.usedSessionID = id
	return c.useSessionErr
}

func (c *fakeTUIChatClient) Archive(_ context.Context, id string) error {
	c.archiveSessionCalls++
	c.archivedSessionID = id
	return c.archiveSessionErr
}

func (c *fakeTUIChatClient) Unarchive(_ context.Context, id string) (storage.Session, error) {
	c.unarchiveCalls++
	c.unarchivedSessionID = id
	iDValue2 := str.String(c.unarchivedSession.ID)
	if iDValue2.Trim() != "" {
		return c.unarchivedSession, c.unarchiveSessionErr
	}

	return storage.Session{ID: id}, c.unarchiveSessionErr
}

func (c *fakeTUIChatClient) Rename(_ context.Context, id string, title string) (storage.Session, error) {
	c.renameSessionCalls++
	c.renamedSessionID = id
	c.renamedSessionTitle = title
	iDValue3 := str.String(c.renamedSession.ID)
	if iDValue3.Trim() != "" {
		return c.renamedSession, c.renameSessionErr
	}

	return storage.Session{
		ID:          id,
		Title:       title,
		TitleSource: storage.SessionTitleSourceManual,
	}, c.renameSessionErr
}

func (c *fakeTUIChatClient) Timeline(
	_ context.Context,
	opts rpcclient.SessionTimelineOptions,
) (rpcclient.SessionTimeline, error) {
	c.timelineCalls++
	c.timelineSessionID = opts.SessionID
	return c.timeline, c.timelineErr
}

func (c *fakeTUIChatClient) Current(context.Context) (storage.Session, error) {
	c.currentSessionCalls++
	return c.currentSession, c.currentSessionErr
}

func (c *fakeTUIChatClient) Status(_ context.Context, id string) (rpcclient.ContextStatus, error) {
	c.contextCalls++
	c.contextSessionID = id
	return c.contextStatus, c.contextErr
}

func (c *fakeTUIChatClient) Close() error {
	c.closed = true
	return nil
}
