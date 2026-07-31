package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/constants"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
)

func TestModel_StartModelsCommandLoadsModels(t *testing.T) {
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("/models")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	msg := modelsLoadedMessageFromBatch(t, cmd)
	updated, cmd = updated.(model).Update(msg)

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, client.listModelCalls)
	require.Equal(t, commandViewKindModels, runModel.commandView.Kind)
	require.Equal(t, "Models", runModel.commandView.TitleLeft)
	require.Equal(t, "enter to select · left to providers · esc to close", runModel.commandView.TitleRight)
	require.Equal(t, "OpenRouter", runModel.commandView.TitleSubtext)
	require.Equal(t, "openrouter", runModel.commandView.ModelProvider)
	require.NotEmpty(t, runModel.commandView.Models)
	require.Zero(t, runModel.commandViewItemSelected)
	require.Equal(t, "openai/gpt-4o-mini", runModel.commandView.Models[0].ID)
	require.True(t, runModel.commandView.Models[0].Current)
	defaultModel := findCommandModelOption(t, runModel.commandView.Models, constants.DefaultProfileModel)
	require.True(t, defaultModel.DisplayDefault)
}

func TestLoadModelsCmdUsesBackgroundContextWhenMissing(t *testing.T) {
	msg := loadModelsCmd("openai", "gpt-5.4")()

	require.Equal(t, "openai", msg.(modelsLoadedMsg).List.Provider)
	require.Equal(t, "gpt-5.5", msg.(modelsLoadedMsg).List.Models[0].ID)
	require.True(t, msg.(modelsLoadedMsg).List.Models[0].DisplayDefault)
	current := findCommandModelOption(t, msg.(modelsLoadedMsg).List.Models, "gpt-5.4")
	require.True(t, current.Current)
}

func TestLoadProvidersCmdUsesBackgroundContextWhenMissing(t *testing.T) {
	msg := loadProvidersCmd("openai")()

	require.Equal(t, "openai", msg.(providersLoadedMsg).List.Providers[0].ID)
	require.True(t, msg.(providersLoadedMsg).List.Providers[0].Current)
}

func TestModel_StartProvidersCommandLoadsProviders(t *testing.T) {
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("/providers")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	msg := providersLoadedMessageFromBatch(t, cmd)
	updated, cmd = updated.(model).Update(msg)

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, client.listProviderCalls)
	require.Equal(t, commandViewKindProviders, runModel.commandView.Kind)
	require.Equal(t, "Providers", runModel.commandView.TitleLeft)
	require.NotEmpty(t, runModel.commandView.Providers)
	require.Equal(t, "openai", runModel.commandView.Providers[0].ID)
	openrouter := findCommandProviderOption(t, runModel.commandView.Providers, "openrouter")
	require.True(t, openrouter.Current)
}

func TestModel_SelectProviderLoadsProviderModels(t *testing.T) {
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.showCommandView(commandViewPayload{
		Kind: commandViewKindProviders,
		Providers: []rpcclient.ProviderOption{
			{ID: "openai", Name: "OpenAI"},
			{ID: "openrouter", Name: "OpenRouter"},
		},
	})
	runModel.commandViewItemSelected = 1

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	msg := modelsLoadedMessageFromBatch(t, cmd)
	updated, cmd = updated.(model).Update(msg)

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, client.listModelCalls)
	require.Equal(t, commandViewKindModels, runModel.commandView.Kind)
	require.Equal(t, "openrouter", runModel.commandView.ModelProvider)
	require.NotEmpty(t, runModel.commandView.Models)
}

func TestModel_RenderProvidersCommandViewHighlightsCurrentAndFallbackDetails(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind: commandViewKindProviders,
		Providers: []rpcclient.ProviderOption{
			{ID: "openai", Name: "OpenAI", ModelCount: 2, AuthType: "api-key", Current: true},
			{ID: "custom", Type: "none"},
		},
	})

	content := stripANSI(runModel.renderProvidersCommandViewContent(commandViewContent{Width: 48, Height: 2}))
	require.Contains(t, content, "OpenAI")
	require.Contains(t, content, "current")
	require.Contains(t, content, "api-key")
	require.Contains(t, content, "2 models")
	require.Contains(t, content, "custom")
	require.Contains(t, content, "none")

	require.Equal(t, "No providers available.", newModel().renderProvidersCommandViewContent(commandViewContent{}))
	require.Contains(t, stripANSI(renderProvidersCommandRow(rpcclient.ProviderOption{ID: "openai", Type: "api-key"}, 32, false)), "OpenAI")
	require.Empty(t, stripANSI(renderProvidersCommandRow(rpcclient.ProviderOption{ID: "openai", Type: "api-key"}, 1, false)))
}

func TestGetProviderDisplayName(t *testing.T) {
	require.Equal(t, "OpenRouter", getProviderDisplayName("openrouter"))
	require.Equal(t, "OpenAI Codex", getProviderDisplayName("openai-codex"))
	require.Equal(t, "custom-provider", getProviderDisplayName("custom-provider"))
}

func TestModel_UpdateProvidersCommandViewNavigatesSelection(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind: commandViewKindProviders,
		Providers: []rpcclient.ProviderOption{
			{ID: "openai"},
			{ID: "openrouter"},
			{ID: "anthropic"},
		},
	})

	updated, cmd := runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Greater(t, runModel.commandViewItemSelected, 0)

	updated, cmd = runModel.updateProvidersCommandView(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseLeft}))
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.updateProvidersCommandView(struct{}{})
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	runModel.showCommandView(commandViewPayload{Kind: commandViewKindProviders})
	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)

	runModel = newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		Kind:      commandViewKindProviders,
		Providers: []rpcclient.ProviderOption{{ID: "openai"}},
	})
	updated, cmd = runModel.updateProvidersCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	require.Equal(t, "loading models", updated.(model).status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:      commandViewKindProviders,
		Providers: []rpcclient.ProviderOption{{ID: "openai"}},
	})
	updated, cmd = runModel.updateProvidersCommandView(modelsLoadedMsg{
		List: rpcclient.ModelList{Provider: "openai", Models: []rpcclient.ModelOption{{ID: "gpt-4o"}}},
	})
	require.Nil(t, cmd)
	require.Equal(t, commandViewKindModels, updated.(model).commandView.Kind)
	require.Equal(t, "openai", updated.(model).commandView.ModelProvider)

	updated, cmd = runModel.updateProvidersCommandView(modelsLoadedMsg{Err: errors.New("load failed")})
	require.NotNil(t, cmd)
	require.Equal(t, "models unavailable", updated.(model).status.Text())
}

func TestModel_RenderModelsCommandViewHighlightsCurrentAndSelection(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind: commandViewKindModels,
		Models: []rpcclient.ModelOption{
			{ID: "gpt-5.4-mini", Name: "GPT 5.4 Mini", Current: true, Reasoning: true, ContextWindow: 272000},
			{ID: "gpt-4o", Name: "GPT 4o", ContextWindow: 128000},
		},
	})

	content := stripANSI(runModel.renderModelsCommandViewContent(commandViewContent{Width: 48, Height: 5}))

	require.Contains(t, content, "GPT 5.4 Mini")
	require.Contains(t, content, "272k")
	require.Contains(t, content, "reasoning")
	require.Contains(t, content, "GPT 4o")
	require.Contains(t, content, "128k")
	require.Contains(t, content, "(current)")
}

func TestModel_RenderModelsCommandViewHandlesEmptyAndContextLength(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{Kind: commandViewKindModels})
	require.Equal(t, "No models available.", runModel.renderModelsCommandViewContent(commandViewContent{}))

	row := stripANSI(renderModelsCommandRow(rpcclient.ModelOption{ID: "model-a", API: "openai-responses"}, 1, false))
	require.Empty(t, row)

	rendered := renderModelsCommandRow(
		rpcclient.ModelOption{ID: "model-a", API: "openai-responses", ContextWindow: 128000},
		32,
		false,
	)
	require.Contains(t, rendered, "\x1b[")
	row = stripANSI(rendered)
	require.Contains(t, row, "model-a")
	require.Contains(t, row, "128k")
	require.NotContains(t, row, "openai-responses")
	require.Equal(t, "             128k", getModelOptionMutedDetail(rpcclient.ModelOption{ContextWindow: 128000}))
	require.Equal(t, "reasoning ·  128k", getModelOptionMutedDetail(rpcclient.ModelOption{
		Reasoning:     true,
		ContextWindow: 128000,
	}))
	require.Equal(t, "reasoning · 1000k", getModelOptionMutedDetail(rpcclient.ModelOption{
		Reasoning:     true,
		ContextWindow: 1000000,
	}))
	require.Equal(t, "(current)", getModelOptionMutedDetail(rpcclient.ModelOption{Current: true}))
	require.Equal(t, "(current) · reasoning · 128k", getModelOptionMutedDetail(rpcclient.ModelOption{
		Current:       true,
		Reasoning:     true,
		ContextWindow: 128000,
	}))
	require.Equal(t, "OAuth", getModelOptionMutedDetail(rpcclient.ModelOption{SupportsOAuth: true}))
	require.Equal(t, "OAuth · reasoning · 1000k", getModelOptionMutedDetail(rpcclient.ModelOption{
		SupportsOAuth: true,
		Reasoning:     true,
		ContextWindow: 1000000,
	}))
}

func TestModel_UpdateModelsCommandViewFiltersModels(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind: commandViewKindModels,
		Models: []rpcclient.ModelOption{
			{ID: "gpt-5.4-mini", Name: "GPT 5.4 Mini"},
			{ID: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5"},
		},
	})
	runModel.commandViewItemSelected = 1

	content := stripANSI(runModel.renderModelsCommandViewContent(commandViewContent{Width: 48, Height: 5}))
	require.Contains(t, content, "Filter models")

	updated, cmd := runModel.updateModelsCommandView(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, "s", runModel.modelFilterInput.Value())
	require.Zero(t, runModel.commandViewItemSelected)
	require.Len(t, runModel.filteredCommandModels(), 1)
	require.Equal(t, "claude-sonnet-4.5", runModel.filteredCommandModels()[0].ID)

	content = stripANSI(runModel.renderModelsCommandViewContent(commandViewContent{Width: 48, Height: 5}))
	require.Contains(t, content, "Claude Sonnet 4.5")
	require.NotContains(t, content, "GPT 5.4 Mini")
	require.Equal(t, 5, lipgloss.Height(content))

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.modelFilterInput.Value())
	require.Len(t, runModel.filteredCommandModels(), 2)

	updated, cmd = runModel.updateModelsCommandView(tea.PasteMsg{Content: "missing"})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	content = stripANSI(runModel.renderModelsCommandViewContent(commandViewContent{Width: 48, Height: 5}))
	require.Contains(t, content, "No matching models.")
	require.Contains(t, content, "\n No matching models.")
	require.Equal(t, 5, lipgloss.Height(content))
}

func TestFilterModelOptionsIgnoresProviderMetadata(t *testing.T) {
	models := []rpcclient.ModelOption{
		{ID: "gpt-5.5", Name: "GPT-5.5", Provider: "openai-codex"},
		{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark", Provider: "openai-codex"},
	}

	filtered := filterModelOptions(models, "codex")

	require.Equal(t, []rpcclient.ModelOption{models[1]}, filtered)
}

func TestRenderModelsCommandRowStylesSelectedLabelOnly(t *testing.T) {
	rendered := renderModelsCommandRow(
		rpcclient.ModelOption{ID: "model-a", ContextWindow: 128000},
		32,
		true,
	)

	require.Contains(t, rendered, "\x1b[")
	require.Contains(t, rendered, "\x1b[90;")
	require.Contains(t, rendered, "48;")
	require.Contains(t, stripANSI(rendered), "model-a")
	require.Contains(t, stripANSI(rendered), "128k")
}

func TestModel_SelectModelCallsClientHidesCommandViewAndUpdatesChrome(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	client := &fakeTUIChatClient{
		selectedModel: rpcclient.ModelOption{ID: "gpt-4o", Provider: "openai", Current: true},
	}
	runModel := newModelWithClient(client)
	runModel.width = 180
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindModels,
		ModelProvider: "openai",
		ModelAuthType: "api-key",
		Models: []rpcclient.ModelOption{
			{ID: "gpt-5.4-mini", Current: true},
			{ID: "gpt-4o"},
		},
	})
	runModel.setTranscriptContent()
	runModel.commandViewItemSelected = 1

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.commandView.Visible)
	require.Equal(t, "gpt-4o", runModel.runtimeInfo.Model)
	require.Equal(t, "gpt-4o", runModel.runtimeInfo.SummaryModel)
	require.Equal(t, "openai", runModel.runtimeInfo.Provider)
	require.Contains(t, stripANSI(runModel.renderHeaderInfoPanel()), "model: gpt-4o")
	require.Contains(t, stripANSI(runModel.renderHeaderInfoPanel()), "summary: gpt-4o")
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "model: gpt-4o")
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "summary: gpt-4o")

	msg := modelSelectedMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, client.selectModelCalls)
	require.Equal(t, "gpt-4o", client.selectedModelID)
	require.Equal(t, "openai", client.selectedModelProvider)
	require.False(t, runModel.commandView.Visible)
	require.Equal(t, "gpt-4o", runModel.runtimeInfo.Model)
	require.Equal(t, "gpt-4o", runModel.runtimeInfo.SummaryModel)
	require.Equal(t, "openai", runModel.runtimeInfo.Provider)
	require.Equal(t, "model selected; daemon restarting", runModel.status.Text())
}

func TestModel_UpdateModelsCommandViewNavigatesSelection(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind: commandViewKindModels,
		Models: []rpcclient.ModelOption{
			{ID: "model-a"},
			{ID: "model-b"},
			{ID: "model-c"},
		},
	})

	updated, cmd := runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Greater(t, runModel.commandViewItemSelected, 0)

	updated, cmd = runModel.updateModelsCommandView(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseLeft}))
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.updateModelsCommandView(struct{}{})
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)
}

func TestModel_UpdateModelsCommandViewBacksToProviders(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindModels,
		ModelProvider: "openrouter",
		Models:        []rpcclient.ModelOption{{ID: "openai/gpt-4o"}},
	})

	updated, cmd := runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	require.Nil(t, cmd)

	runModel = updated.(model)
	require.Equal(t, commandViewKindProviders, runModel.commandView.Kind)
	require.Equal(t, "Providers", runModel.commandView.TitleLeft)
	require.NotEmpty(t, runModel.commandView.Providers)
	require.Equal(t, "openai", runModel.commandView.Providers[0].ID)
	openrouter := findCommandProviderOption(t, runModel.commandView.Providers, "openrouter")
	require.True(t, openrouter.Current)
}

func TestModel_UpdateModelsCommandViewHandlesEmptyListAndSelectionMessage(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{Kind: commandViewKindModels})

	updated, cmd := runModel.updateModelsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)

	runModel.showCommandView(commandViewPayload{
		Kind:   commandViewKindModels,
		Models: []rpcclient.ModelOption{{ID: "gpt-4o"}},
	})
	updated, cmd = runModel.updateModelsCommandView(modelSelectedMsg{
		Model: rpcclient.ModelOption{ID: "gpt-4o", Provider: "openai"},
	})
	require.NotNil(t, cmd)
	require.False(t, updated.(model).commandView.Visible)
	require.Equal(t, "gpt-4o", updated.(model).runtimeInfo.Model)
	require.Equal(t, "openai", updated.(model).runtimeInfo.Provider)
}

func TestModel_ModelsCommandReportsUnavailableStates(t *testing.T) {
	runModel := newModel()
	msg := loadModelsCmd("", "")()
	cmd := runModel.completeModelsCommand(msg.(modelsLoadedMsg))
	require.NotNil(t, cmd)
	require.Equal(t, "models unavailable", runModel.status.Text())

	msg = modelsLoadedMsg{Err: errors.New("load failed")}
	cmd = runModel.completeModelsCommand(msg.(modelsLoadedMsg))
	require.NotNil(t, cmd)
	require.Equal(t, "models unavailable", runModel.status.Text())

	client := &fakeTUIChatClient{selectModelErr: errors.New("select failed")}
	runModel = newModelWithClient(client)
	runModel.showCommandView(commandViewPayload{
		Kind:   commandViewKindModels,
		Models: []rpcclient.ModelOption{{ID: "gpt-4o"}},
	})
	msg = selectModelCmd(context.Background(), client, "openai", "openai-responses", "gpt-4o")()
	require.Equal(t, "openai-responses", client.selectedModelAPI)
	updated, cmd := runModel.updateModelsCommandView(msg)
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel = newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		Kind:      commandViewKindProviders,
		Providers: []rpcclient.ProviderOption{{}},
	})
	updated, cmd = runModel.selectCurrentProviderOption()
	require.NotNil(t, cmd)
	require.Equal(t, "provider selection unavailable", updated.(model).status.Text())
}

func TestModel_CompleteModelsCommandHighlightsCurrentModelFirst(t *testing.T) {
	runModel := newModel()
	runModel.commandViewItemSelected = 2
	runModel.commandViewOffset = 2

	cmd := runModel.completeModelsCommand(modelsLoadedMsg{
		List: rpcclient.ModelList{
			Provider: "openai",
			Models: []rpcclient.ModelOption{
				{ID: "default", DisplayDefault: true},
				{ID: "other"},
				{ID: "current", Current: true},
			},
		},
	})

	require.Nil(t, cmd)
	require.Zero(t, runModel.commandViewItemSelected)
	require.Zero(t, runModel.commandViewOffset)
	require.Equal(t, "current", runModel.commandView.Models[0].ID)
	require.True(t, runModel.commandView.Models[0].Current)
}

func TestModel_ModelSelectionEdgeCases(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:   commandViewKindModels,
		Models: []rpcclient.ModelOption{{ID: "gpt-4o", Current: true}},
	})
	updated, cmd := runModel.selectCurrentModelOption()
	require.NotNil(t, cmd)
	require.Equal(t, "model already selected", updated.(model).status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:   commandViewKindModels,
		Models: []rpcclient.ModelOption{{}},
	})
	updated, cmd = runModel.selectCurrentModelOption()
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel.showCommandView(commandViewPayload{
		Kind:   commandViewKindModels,
		Models: []rpcclient.ModelOption{{ID: "gpt-4o"}},
	})
	updated, cmd = runModel.selectCurrentModelOption()
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	require.Nil(t, selectModelCmd(context.Background(), nil, "openai", "", "gpt-4o"))
	client := &fakeTUIChatClient{selectedModel: rpcclient.ModelOption{ID: "gpt-4o"}}
	var nilContext context.Context
	msg := selectModelCmd(nilContext, client, "openai", "", "gpt-4o")()
	require.NoError(t, msg.(modelSelectedMsg).Err)
	require.Equal(t, "gpt-4o", msg.(modelSelectedMsg).Model.ID)
	require.Equal(t, "openai", client.selectedModelProvider)

	msg = selectModelCmd(context.Background(), &fakeTUIChatClient{}, "openai", "", "")()
	require.EqualError(t, msg.(modelSelectedMsg).Err, "model id is required")

	updated, cmd = runModel.completeSelectModel(modelSelectedMsg{})
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	require.Equal(t, []rpcclient.ModelOption{{ID: "a"}}, orderModelsCommandOptions([]rpcclient.ModelOption{{ID: "a"}}))
}

func TestModel_ModelSelectionPromptsForMissingProviderAPIKeyAndRetries(t *testing.T) {
	client := &fakeTUIChatClient{
		selectedModel: rpcclient.ModelOption{ID: "openai/gpt-4o", Provider: "openrouter", Current: true},
	}
	runModel := newModelWithClient(client)
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindModels,
		ModelProvider: "openrouter",
		ModelAuthType: "none",
		Models: []rpcclient.ModelOption{
			{ID: "openai/gpt-4o", Provider: "openrouter"},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, commandViewKindProviderAPIKey, runModel.commandView.Kind)
	require.Equal(t, "provider API key required", runModel.status.Text())
	require.Equal(t, "API key for OpenRouter", runModel.apiKeyInput.Placeholder)

	runModel.apiKeyInput.SetValue("router-key")
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	msg := providerAPIKeySetMessageFromBatch(t, cmd)
	updated, cmd = updated.(model).Update(msg)

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, client.setProviderKeyCalls)
	require.Equal(t, "openrouter", client.providerAPIKeyID)
	require.Equal(t, "router-key", client.providerAPIKey)
	require.False(t, runModel.commandView.Visible)

	selected := modelSelectedMessageFromBatch(t, cmd)
	require.NoError(t, selected.Err)
	require.Equal(t, "openai/gpt-4o", client.selectedModelID)
	require.Equal(t, "openrouter", client.selectedModelProvider)
}

func TestModel_ProviderAPIKeyPromptAcceptsPaste(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:           commandViewKindProviderAPIKey,
		ModelProvider:  "openrouter",
		PendingModelID: "openai/gpt-4o",
	})

	updated, cmd := runModel.Update(tea.PasteMsg{Content: " pasted-router-key\n"})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.input.Value())
	require.Equal(t, "pasted-router-key", runModel.apiKeyInput.Value())
	require.Contains(
		t,
		stripANSI(runModel.renderProviderAPIKeyCommandViewContent(commandViewContent{Width: 80})),
		"pasted-router-key",
	)
}

func TestModel_RenderProviderAPIKeyCommandViewHidesBottomBorder(t *testing.T) {
	runModel := newModel()
	runModel.height = 16
	runModel.width = 80
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindProviderAPIKey,
		TitleLeft:     "Provider API Key",
		TitleSubtext:  "openrouter",
		ModelProvider: "openrouter",
		Height:        commandViewMinHeight,
	})

	lines := strings.Split(stripANSI(runModel.View().Content), "\n")
	if len(lines) > runModel.height {
		lines = lines[:runModel.height]
	}

	require.NotEmpty(t, lines)
	require.NotContains(t, lines[len(lines)-1], "╰")
	require.NotContains(t, lines[len(lines)-1], "╯")
}

func TestNormalizeProviderAPIKeyPasteTrimsWhitespace(t *testing.T) {
	require.Equal(t, "sk-or-v1-key", normalizeProviderAPIKeyPaste(" sk-or-v1-key \n"))
	require.Equal(t, "key with space", normalizeProviderAPIKeyPaste(" key with space \n"))
}

func TestNewProviderAPIKeyInputUsesLegibleTextColor(t *testing.T) {
	input := newProviderAPIKeyInput("API key for openrouter")

	require.Equal(t, lipgloss.Color("15"), input.Styles().Focused.Text.GetForeground())
	require.Equal(t, 4096, input.CharLimit)
	require.Equal(t, "API key for openrouter", input.Placeholder)

	input = newProviderAPIKeyInput(" ")
	require.Equal(t, "API key", input.Placeholder)
}

func TestModel_ProviderAPIKeyPromptEdgeCases(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{Kind: commandViewKindProviderAPIKey})
	require.Contains(t, stripANSI(runModel.renderProviderAPIKeyCommandViewContent(commandViewContent{Width: 20})), "API key")
	require.NotContains(t, stripANSI(runModel.renderProviderAPIKeyCommandViewContent(commandViewContent{Width: 20})), "Enter API key")

	updated, cmd := runModel.updateProviderAPIKeyCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.NotNil(t, cmd)
	require.False(t, updated.(model).commandView.Visible)
	require.Equal(t, "provider API key cancelled", updated.(model).status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:           commandViewKindProviderAPIKey,
		ModelProvider:  "openrouter",
		PendingModelID: "openai/gpt-4o",
	})
	updated, cmd = runModel.submitProviderAPIKey()
	require.NotNil(t, cmd)
	require.Equal(t, "provider API key required", updated.(model).status.Text())

	runModel.apiKeyInput.SetValue("router-key")
	runModel.modelClient = nil
	updated, cmd = runModel.submitProviderAPIKey()
	require.NotNil(t, cmd)
	require.Equal(t, "provider API key unavailable", updated.(model).status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{Kind: commandViewKindProviderAPIKey})
	runModel.apiKeyInput.SetValue("router-key")
	updated, cmd = runModel.submitProviderAPIKey()
	require.NotNil(t, cmd)
	require.Equal(t, "provider API key unavailable", updated.(model).status.Text())

	require.Nil(t, setProviderAPIKeyCmd(context.Background(), nil, "openrouter", "", "openai/gpt-4o", "key"))

	var nilContext context.Context
	cmd = setProviderAPIKeyCmd(nilContext, &fakeTUIChatClient{}, "openrouter", "", "openai/gpt-4o", "key")
	require.NotNil(t, cmd)
	require.Equal(t, "openrouter", cmd().(providerAPIKeySetMsg).Provider)

	client := &fakeTUIChatClient{providerAPIKeyErr: errors.New("save failed")}
	runModel = newModelWithClient(client)
	updated, cmd = runModel.completeProviderAPIKeySet(providerAPIKeySetMsg{Err: errors.New("save failed")})
	require.NotNil(t, cmd)
	require.Equal(t, "provider API key unavailable", updated.(model).status.Text())

	runModel = newModel()
	updated, cmd = runModel.completeProviderAPIKeySet(providerAPIKeySetMsg{Provider: "openrouter", ModelID: "openai/gpt-4o"})
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel = newModel()
	updated, cmd = runModel.showProviderAPIKeyPrompt(rpcclient.ModelOption{})
	require.NotNil(t, cmd)
	require.Equal(t, "model selection unavailable", updated.(model).status.Text())

	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindModels,
		ModelProvider: "openrouter",
	})
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	require.False(t, runModel.shouldPromptForProviderAPIKey(rpcclient.ModelOption{ID: "openai/gpt-4o"}))
	require.False(t, runModel.shouldPromptForProviderAPIKey(rpcclient.ModelOption{ID: "gpt-5.4", SupportsOAuth: true}))

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindProviderAPIKey,
		ModelProvider: "openrouter",
	})
	updated, cmd = runModel.updateProviderAPIKeyCommandView(providerAPIKeySetMsg{Err: errors.New("save failed")})
	require.NotNil(t, cmd)
	require.Equal(t, "provider API key unavailable", updated.(model).status.Text())

	updated, cmd = runModel.updateProviderAPIKeyCommandView(struct{}{})
	require.Nil(t, cmd)
	require.Equal(t, commandViewKindProviderAPIKey, updated.(model).commandView.Kind)
}

func TestModel_ModelSelectionReportsOAuthLoginCommand(t *testing.T) {
	runModel := newModel()
	updated, cmd := runModel.completeSelectModel(modelSelectedMsg{
		Err: errors.New(`model API key is required for provider "openai-codex"; set a provider API key, provider env var, role apiKey, or run morph provider login openai-codex`),
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "model authentication required", runModel.status.Text())
	require.Equal(
		t,
		[]string{"Error: run morph provider login openai-codex in a new terminal"},
		transcriptCellPlainTexts(runModel.messages),
	)
	require.Contains(
		t,
		stripANSI(runModel.transcript.GetContent()),
		"Error - Model authentication is required.",
	)
	require.Contains(
		t,
		stripANSI(runModel.transcript.GetContent()),
		"morph provider login openai-codex",
	)
	require.Contains(
		t,
		stripANSI(runModel.transcript.GetContent()),
		"Run this command in a new terminal.",
	)
	require.NotContains(
		t,
		stripANSI(runModel.transcript.GetContent()),
		"run morph provider login openai-codex in a new terminal",
	)
	require.NotContains(
		t,
		stripANSI(runModel.transcript.GetContent()),
		"╭",
	)
	require.NotContains(
		t,
		stripANSI(runModel.transcript.GetContent()),
		"╰",
	)
	require.Empty(t, getModelSelectionLoginCommand(nil))
	require.Empty(t, getModelSelectionLoginCommand(errors.New("other error")))
}

func TestModel_SelectOAuthModelWithoutAuthStartsProviderSubscriptionSetup(t *testing.T) {
	runModel := newSetupModelSelectionTestModel(t)
	runModel.clearProfileModelSetup()
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindModels,
		ModelProvider: constants.ModelProviderAnthropic,
		Models: []rpcclient.ModelOption{{
			ID:            "claude-sonnet-4-6",
			Name:          "Claude Sonnet 4.6",
			Provider:      constants.ModelProviderAnthropic,
			SupportsOAuth: true,
		}},
	})

	updated, cmd := runModel.selectCurrentModelOption()
	require.NotNil(t, cmd)

	runModel = updated.(model)
	defer runModel.cancelSetupOAuthLogin()
	require.False(t, runModel.commandView.Visible)
	require.True(t, runModel.setupDismissible)
	require.Equal(t, setupAuthMethodSubscription, runModel.setupAuthMethod)
	require.Equal(t, setupModelStepNotice, runModel.setupModelStep)
	require.True(t, runModel.setupOAuthPending)
	require.Equal(t, constants.ModelProviderAnthropic, runModel.setupOAuthProvider)
	require.Equal(t, "claude-sonnet-4-6", runModel.currentSetupModelID())
	require.Empty(t, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.View().Content), "Opening browser to connect Anthropic.")
}

func TestModel_ApplySelectedModelToRuntimeUsesCommandProviderFallback(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		Kind:          commandViewKindModels,
		ModelProvider: "openrouter",
	})

	runModel.applySelectedModelToRuntime(rpcclient.ModelOption{ID: "openai/gpt-4o"})
	require.Equal(t, "openai/gpt-4o", runModel.runtimeInfo.Model)
	require.Equal(t, "openrouter", runModel.runtimeInfo.Provider)

	runModel.applySelectedModelToRuntime(rpcclient.ModelOption{})
	require.Equal(t, "openai/gpt-4o", runModel.runtimeInfo.Model)
	require.Equal(t,
		[]rpcclient.ModelOption{{ID: "current", Current: true}, {ID: "other"}},
		orderModelsCommandOptions([]rpcclient.ModelOption{{ID: "current", Current: true}, {ID: "other"}}),
	)
	require.Equal(t,
		[]rpcclient.ModelOption{{ID: "current", Current: true}, {ID: "other"}},
		orderModelsCommandOptions([]rpcclient.ModelOption{{ID: "other"}, {ID: "current", Current: true}}),
	)
	require.Equal(t,
		[]rpcclient.ModelOption{{ID: "other"}, {ID: "plain"}},
		orderModelsCommandOptions([]rpcclient.ModelOption{{ID: "other"}, {ID: "plain"}}),
	)
}

func TestModel_ApplySelectedModelToRuntimeUsesHumanFacingName(t *testing.T) {
	runModel := newModel()
	runModel.applySelectedModelToRuntime(rpcclient.ModelOption{
		ID:       "gpt-5.5",
		Name:     "GPT-5.5",
		Provider: "openai-codex",
	})

	require.Equal(t, "GPT-5.5", runModel.modelName)
	require.Equal(t, "gpt-5.5", runModel.runtimeInfo.Model)
	require.Equal(t, "openai-codex", runModel.runtimeInfo.Provider)
	require.Contains(t, stripANSI(runModel.renderBottomStatusPanel()), "GPT-5.5")
}

func modelsLoadedMessageFromBatch(t *testing.T, cmd tea.Cmd) modelsLoadedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(modelsLoadedMsg)
	require.True(t, ok)

	return msg
}

func providersLoadedMessageFromBatch(t *testing.T, cmd tea.Cmd) providersLoadedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(providersLoadedMsg)
	require.True(t, ok)

	return msg
}

func modelSelectedMessageFromBatch(t *testing.T, cmd tea.Cmd) modelSelectedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(modelSelectedMsg)
	require.True(t, ok)

	return msg
}

func providerAPIKeySetMessageFromBatch(t *testing.T, cmd tea.Cmd) providerAPIKeySetMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(providerAPIKeySetMsg)
	require.True(t, ok)

	return msg
}

func findCommandProviderOption(t *testing.T, providers []rpcclient.ProviderOption, id string) rpcclient.ProviderOption {
	t.Helper()

	for _, provider := range providers {
		if provider.ID == id {
			return provider
		}
	}

	t.Fatalf("provider option %q not found", id)
	return rpcclient.ProviderOption{}
}

func findCommandModelOption(t *testing.T, models []rpcclient.ModelOption, id string) rpcclient.ModelOption {
	t.Helper()

	for _, model := range models {
		if model.ID == id {
			return model
		}
	}

	t.Fatalf("model option %q not found", id)
	return rpcclient.ModelOption{}
}
