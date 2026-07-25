package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"
	"gopkg.in/yaml.v3"

	clibase "github.com/wandxy/morph/internal/cli"
	clisetup "github.com/wandxy/morph/internal/cli/setup"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	appcredential "github.com/wandxy/morph/internal/credential"
	modelcatalog "github.com/wandxy/morph/internal/model"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	_ "github.com/wandxy/morph/internal/model/provider_anthropic"
	_ "github.com/wandxy/morph/internal/model/provider_copilot"
	provider_ollama "github.com/wandxy/morph/internal/model/provider_ollama"
	_ "github.com/wandxy/morph/internal/model/provider_openai"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/pkg/str"
)

const (
	userNameFilename        = "user.json"
	namePromptTitle         = "Hi, there ☺"
	namePromptPlaceholder   = "What can I call you?"
	namePromptSubmitHint    = "Enter to send →"
	namePromptInvalidHint   = "Use letters, numbers, and hyphen only"
	emptyUserPromptQuestion = "What can I do for you?"
	namePromptMaxWidth      = 52
	namePromptInputMinWidth = 28
	namePromptErrorWindow   = 2 * time.Second

	setupModelStepAuthMethod = "auth-method"
	setupModelStepProvider   = "provider"
	setupModelStepBaseURL    = "base-url"
	setupModelStepModel      = "model"
	setupModelStepAPIKey     = "api-key"
	setupModelStepNotice     = "notice"

	setupAuthMethodSubscription = "subscription"
	setupAuthMethodAPIKey       = "api-key"
	setupAuthMethodLocal        = "local"

	setupNoticeActionMissingModelPull = "missing-model-pull"
	setupNoticeActionPullingModel     = "pulling-model"
	setupNoticeActionLocalUnavailable = "local-unavailable"
	setupNoticeActionToolWarning      = "tool-warning"

	setupModelMaxWidth      = 72
	setupModelMinWidth      = 34
	setupModelLoginMinWidth = 52
	setupModelMaxListHeight = 8
	setupModelFilterWidth   = 18
	setupCloseHint          = "ctrl+x to close"
)

var validUserName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

var (
	getSetupSubscriptionProvider = appcredential.GetSubscriptionProvider
	newSetupCredentialStore      = func() setupCredentialStore {
		return appcredential.NewFileStore("")
	}
	pullSetupOllamaModel = provider_ollama.EnsureModel
)

var setupAuthMethodOptions = []setupAuthMethodOption{
	{
		ID:          setupAuthMethodSubscription,
		Label:       "Use a subscription",
		Description: "Connect with your Anthropic, OpenAI or Github account",
	},
	{
		ID:          setupAuthMethodAPIKey,
		Label:       "Use an API Key",
		Description: "Connect with your own Anthropic, OpenAI, OpenRouter API Keys (BYOK)",
	},
	{
		ID:          setupAuthMethodLocal,
		Label:       "Use local providers",
		Description: "Connect to Ollama or another local provider",
	},
}

type namePromptErrorExpiredMsg struct {
	startedAt time.Time
}

type setupAuthMethodOption struct {
	ID          string
	Label       string
	Description string
}

type profileUser struct {
	Name string `json:"name"`
}

type setupCredentialStore interface {
	Set(string, appcredential.StoredCredential) error
}

type setupOAuthOutputMsg struct {
	provider string
	reader   *io.PipeReader
	line     string
	err      error
}

type setupOAuthCompletedMsg struct {
	provider string
	output   string
	err      error
}

type setupModelOptionsLoadedMsg struct {
	provider        string
	baseURL         string
	selectedModelID string
	models          []rpcclient.ModelOption
	err             error
}

type setupModelPullProgressMsg struct {
	provider string
	model    string
	lines    []string
}

type setupModelPullCompletedMsg struct {
	provider string
	model    string
	option   rpcclient.ModelOption
	err      error
}

type setupModelPullClosedMsg struct{}

type setupModelRuntimeSelectedMsg struct {
	Model rpcclient.ModelOption
	Err   error
}

func newNameInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = namePromptPlaceholder
	input.CharLimit = 80
	input.SetWidth(namePromptMaxWidth - 4)
	input.Focus()

	styles := input.Styles()
	styles.Focused.Text = styles.Focused.Text.
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		UnsetBackground()
	styles.Focused.Placeholder = styles.Focused.Placeholder.
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		UnsetBackground()
	styles.Focused.Prompt = styles.Focused.Prompt.
		UnsetBackground()
	styles.Cursor.Blink = false
	input.SetStyles(styles)

	return input
}

func loadProfileUserName() (string, bool, bool, error) {
	path := profileUserPath()
	if path == "" {
		return noticeBarName, true, false, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, true, nil
		}

		return "", false, false, fmt.Errorf("read user profile: %w", err)
	}

	var user profileUser
	if err := json.Unmarshal(data, &user); err != nil {
		return "", false, false, fmt.Errorf("parse user profile: %w", err)
	}
	nameValue := str.String(user.Name)
	name := nameValue.Trim()
	return name, name != "", false, nil
}

func saveProfileUserName(name string) error {
	nameValue2 := str.String(name)
	name = nameValue2.Trim()
	if name == "" {
		return nil
	}

	path := profileUserPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create profile metadata dir: %w", err)
	}

	data, err := json.MarshalIndent(profileUser{Name: name}, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func profileUserPath() string {
	active := profile.WithMetadataPaths(profile.Active())
	homeDirValue := str.String(active.HomeDir)
	home := homeDirValue.Trim()
	if home == "" {
		return ""
	}

	return filepath.Join(home, userNameFilename)
}

func (m model) shouldShowNamePrompt() bool {
	if !m.namePromptEnabled {
		return false
	}
	if m.setupNamePromptActive {
		return true
	}

	userNameValue := str.String(m.userName)
	return userNameValue.Trim() == "" &&
		len(m.messages) == 0 &&
		(m.live == nil || m.live.IsEmpty())
}

func (m model) shouldShowEmptyUserPrompt() bool {
	userDisplayNameValue := str.String(m.userDisplayName())
	return !m.shouldShowNamePrompt() &&
		!m.shouldShowProfileModelSetup() && userDisplayNameValue.
		Trim() != "" &&
		len(m.messages) == 0 &&
		(m.live == nil || m.live.IsEmpty())
}

func (m model) userDisplayName() string {
	userNameValue2 := str.String(m.userName)
	if name := userNameValue2.Trim(); name != "" {
		return name
	}

	return noticeBarName
}

func (m model) renderNamePrompt() string {
	width := max(m.transcript.Width(), m.getMainPaneWidth())
	height := max(m.transcript.Height(), 1)
	boxWidth := min(max(width/2, namePromptInputMinWidth), min(namePromptMaxWidth, width))
	inputWidth := max(boxWidth-4, 1)
	input := m.nameInput
	input.SetWidth(inputWidth)

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Bold(true).
		Render(namePromptTitle)
	mark := renderMorphBanner(morphHeaderMark)
	inputBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(defaultTUITheme.InputFrameBorder)).
		Padding(0, 1).
		Width(boxWidth).
		Render(input.View())
	hintText := namePromptSubmitHint
	hintColor := defaultTUITheme.MutedText
	namePromptErrorValue := str.String(m.namePromptError)
	if errorText := namePromptErrorValue.Trim(); errorText != "" {
		hintText = errorText
		hintColor = defaultTUITheme.ToolDeletion
	} else if m.setupNamePromptActive {
		hintText = "Enter to continue"
	}
	hint := m.renderNamePromptHint(hintText, hintColor, lipgloss.Width(inputBox))
	content := lipgloss.JoinVertical(
		lipgloss.Center,
		mark,
		"",
		title,
		"",
		inputBox,
		hint,
	)

	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		content,
	)
}

func (m model) renderEmptyUserPrompt() string {
	width := max(m.transcript.Width(), m.getMainPaneWidth())
	header := strings.Trim(m.renderHeaderWithWidth(width), "\n")
	headerHeight := lipgloss.Height(header)
	height := max(m.transcript.Height()-headerHeight, 1)
	name := m.userDisplayName()
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Bold(true).
		Render("Hi, " + name + " ☺")
	question := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Render(emptyUserPromptQuestion)
	content := lipgloss.JoinVertical(
		lipgloss.Center,
		title,
		question,
	)

	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		content,
	)
}

func (m model) renderNamePromptHint(hintText string, hintColor string, width int) string {
	width = max(width, 1)
	hintTextValue := str.String(hintText)
	hintText = hintTextValue.Trim()
	if m.setupDismissible {
		return renderProfileModelSetupSplitHint(hintText, setupCloseHint, width)
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(hintColor)).
		Width(width).
		Render(renderProfileModelSetupPaddedLabel(hintText, width))
}

func (m model) renderEmptyUserPromptContent() string {
	width := m.transcript.Width()
	if width <= 0 {
		width = m.getMainPaneWidth()
	}
	header := strings.Trim(m.renderHeaderWithWidth(width), "\n")

	return strings.TrimRight(lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		m.renderEmptyUserPrompt(),
	), "\n")
}

func (m model) handleNamePromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.setupDismissible && isSetupCloseKey(msg) {
		return m.closeProfileSetup()
	}
	if msg.Key().Code == tea.KeyEsc && m.setupDismissible {
		return m.closeProfileSetup()
	}
	if msg.Key().Code == tea.KeyEnter {
		return m.submitNamePrompt()
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)

	return m, inputHandledCmd(cmd)
}

func (m model) handleNamePromptPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	m.nameInput.SetValue(m.nameInput.Value() + normalizeComposerPaste(msg.Content))

	return m, inputHandledCmd(nil)
}

func (m model) submitNamePrompt() (tea.Model, tea.Cmd) {
	value := str.String(m.nameInput.Value())
	name := value.Trim()
	if name == "" {
		return m.setNamePromptError("name is required")
	}
	if !validUserName.MatchString(name) {
		return m.setNamePromptError(namePromptInvalidHint)
	}
	if err := saveProfileUserName(name); err != nil {
		return m, m.setStatus("name unavailable")
	}

	m.userName = name
	m.namePromptEnabled = false
	startSetup := m.setupNamePromptActive || m.profileModelSetupMissing()
	m.setupNamePromptActive = false
	m.nameInput.SetValue("")
	if startSetup {
		return m, m.startProfileModelSetup()
	}

	m.resize()
	m.setTranscriptContent()

	return m, m.setStatus("name saved")
}

func (m model) setNamePromptError(text string) (tea.Model, tea.Cmd) {
	textValue := str.String(text)
	m.namePromptError = textValue.Trim()
	m.namePromptErrorStartedAt = currentTime()
	startedAt := m.namePromptErrorStartedAt

	return m, tea.Tick(namePromptErrorWindow, func(time.Time) tea.Msg {
		return namePromptErrorExpiredMsg{startedAt: startedAt}
	})
}

func (m model) expireNamePromptError(msg namePromptErrorExpiredMsg) tea.Model {
	if m.namePromptErrorStartedAt.IsZero() || !m.namePromptErrorStartedAt.Equal(msg.startedAt) {
		return m
	}

	m.namePromptError = ""
	m.namePromptErrorStartedAt = time.Time{}
	return m
}

func (m *model) startProfileModelSetup() tea.Cmd {
	m.setupNamePromptActive = false
	m.setupModelStep = setupModelStepAuthMethod
	m.setupAuthMethod = ""
	m.setupProviders = nil
	m.setupModels = nil
	m.setupModelProvider = ""
	m.setupModelBaseURL = ""
	m.setupProviderAPIKey = ""
	m.setupPendingModelID = ""
	m.setupNoticeTitle = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.setupNoticeAction = ""
	m.setupItemSelected = 0
	m.setupOffset = 0
	m.resize()

	return m.setStatus("choose an auth method")
}

func (m *model) startProfileSetup(dismissible bool) tea.Cmd {
	userNameValue3 := str.String(m.userName)
	name := userNameValue3.Trim()
	m.clearProfileModelSetup()
	m.namePromptEnabled = true
	m.setupNamePromptActive = true
	m.setupDismissible = dismissible
	m.namePromptError = ""
	m.namePromptErrorStartedAt = time.Time{}
	m.nameInput = newNameInput()
	m.nameInput.SetValue(name)
	m.nameInput.CursorEnd()
	m.resize()

	return m.setStatus("enter your name")
}

func (m *model) selectCurrentSetupAuthMethodOption() (tea.Model, tea.Cmd) {
	if len(setupAuthMethodOptions) == 0 {
		return *m, nil
	}

	selected := min(max(m.setupItemSelected, 0), len(setupAuthMethodOptions)-1)
	m.setupAuthMethod = setupAuthMethodOptions[selected].ID
	providers := modelcatalog.ListProviders(modelcatalog.ProviderQuery{
		Current: m.loadRawProfileMainProvider(),
	})
	providers = filterSetupProvidersForAuthMethod(providers, m.setupAuthMethod)
	if len(providers) == 0 {
		m.setupAuthMethod = ""
		return *m, m.setStatus("model setup unavailable")
	}

	m.setupModelStep = setupModelStepProvider
	m.setupProviders = providers
	m.setupModels = nil
	m.setupModelProvider = ""
	m.setupProviderAPIKey = ""
	m.setupPendingModelID = ""
	m.setupItemSelected = 0
	m.setupOffset = 0
	m.resize()

	return *m, m.setStatus("choose a model provider")
}

func filterSetupProvidersForAuthMethod(
	providers []modelcatalog.ProviderOption,
	authMethod string,
) []modelcatalog.ProviderOption {
	filtered := make([]modelcatalog.ProviderOption, 0, len(providers))
	for _, provider := range providers {
		if isSetupProviderLocalOption(provider) {
			if authMethod == setupAuthMethodLocal {
				filtered = append(filtered, provider)
			}
			continue
		}

		switch authMethod {
		case setupAuthMethodAPIKey:
			iDValue := str.String(provider.ID)
			switch iDValue.Normalized() {
			case constants.ModelProviderOpenAICodex, constants.ModelProviderGitHubCopilot:
				continue
			}
			if provider.SupportsAPIKey {
				filtered = append(filtered, provider)
			}
		case setupAuthMethodSubscription:
			if provider.SupportsOAuth {
				filtered = append(filtered, provider)
			}
		case setupAuthMethodLocal:
			continue
		default:
			continue
		}
	}

	return filtered
}

func (m *model) selectCurrentSetupProviderOption() (tea.Model, tea.Cmd) {
	if len(m.setupProviders) == 0 {
		return *m, nil
	}

	provider := m.setupProviders[m.setupItemSelected]
	iDValue2 := str.String(provider.ID)
	providerID := iDValue2.Trim()
	if providerID == "" {
		return *m, m.setStatus("provider selection unavailable")
	}

	baseURL := m.getSetupProviderBaseURL(providerID)
	if isSetupProviderLocalOption(provider) {
		return m.showSetupBaseURLPrompt(providerID, baseURL)
	}

	err := m.loadSetupModels(providerID, baseURL)
	if err != nil {
		return *m, m.setStatus("models unavailable")
	}
	if len(m.setupModels) == 0 {
		return *m, m.setStatus("models unavailable")
	}

	if m.setupAuthMethod == setupAuthMethodAPIKey {
		return m.showSetupProviderAPIKeyPromptForProvider(providerID)
	}
	if m.setupAuthMethod == setupAuthMethodSubscription {
		if err := m.checkSetupModelAuth(m.setupModels[0]); err == nil {
			return m.showSetupModelSelection()
		} else if !isMissingModelCredentialError(err) {
			return m.showSetupNotice("Authentication unavailable", err.Error(), "enter to continue")
		}

		return m.startSetupOAuthLogin(providerID)
	}

	return m.showSetupModelSelection()
}

func (m model) getSetupProviderBaseURL(providerID string) string {
	rawConfig := m.loadRawProfileConfig()
	nameValue3 := str.String(rawConfig.Models.Main.Name)
	opts := clisetup.ModelOptions{
		Provider:  providerID,
		Current:   nameValue3.Trim(),
		OAuthOnly: m.setupAuthMethod == setupAuthMethodSubscription,
		Config:    rawConfig,
	}

	return clisetup.ResolveModelOptionsBaseURL(opts)
}

func (m *model) loadSetupModels(providerID string, baseURL string) error {
	rawConfig := m.loadRawProfileConfig()
	nameValue4 := str.String(rawConfig.Models.Main.Name)
	baseURLValue := str.String(baseURL)
	opts := clisetup.ModelOptions{
		Provider:  providerID,
		Current:   nameValue4.Trim(),
		BaseURL:   baseURLValue.Trim(),
		OAuthOnly: m.setupAuthMethod == setupAuthMethodSubscription,
		Config:    rawConfig,
	}
	models, _, err := clisetup.ListModelOptions(m.chatCtx, opts)
	if err != nil {
		return err
	}

	m.setupModels = models
	m.setupModelProvider = providerID
	baseURLValue2 := str.String(baseURL)
	m.setupModelBaseURL = baseURLValue2.Trim()
	m.modelFilterInput = newModelFilterInput()
	m.setupItemSelected = 0
	m.setupOffset = 0

	return nil
}

func (m *model) selectCurrentSetupModelOption() (tea.Model, tea.Cmd) {
	models := m.filteredSetupModels()
	if len(models) == 0 {
		return *m, nil
	}

	option := models[min(max(m.setupItemSelected, 0), len(models)-1)]
	provider := getSetupModelProvider(m.setupModelProvider, option)
	if isMissingLocalSetupModel(provider, option) {
		return m.showMissingSetupModelPullPrompt(option)
	}
	if shouldWarnSetupModelToolSupport(provider, option) {
		return m.showSetupModelToolWarning(option)
	}
	setupProviderAPIKeyValue := str.String(m.setupProviderAPIKey)
	apiKey := setupProviderAPIKeyValue.Trim()
	if apiKey == "" && !isLocalSetupProvider(provider) {
		if err := m.checkSetupModelAuth(option); err != nil {
			if isMissingModelCredentialError(err) {
				if option.SupportsOAuth {
					return m.startSetupOAuthLogin(provider)
				}

				return m.showSetupProviderAPIKeyPrompt(option)
			}

			return m.showSetupNotice("Model setup unavailable", err.Error(), "enter to continue")
		}
	}

	err := m.persistSetupModelSelection(option, apiKey)
	if err == nil {
		return m.completeSetupModelSelection(option)
	}
	if option.SupportsOAuth && isMissingModelCredentialError(err) {
		return m.startSetupOAuthLogin(provider)
	}
	if isEmbeddingSetupError(err) {
		return m.showSetupNotice("Embedding setup required", getEmbeddingSetupInstruction(), "enter to continue")
	}
	if isMissingModelCredentialError(err) {
		return m.showSetupProviderAPIKeyPrompt(option)
	}

	return m.showSetupNotice("Model setup unavailable", err.Error(), "enter to continue")
}

func (m *model) submitSetupProviderAPIKey() (tea.Model, tea.Cmd) {
	setupModelProviderValue := str.String(m.setupModelProvider)
	provider := setupModelProviderValue.Trim()
	setupPendingModelIDValue := str.String(m.setupPendingModelID)
	modelID := setupPendingModelIDValue.Trim()
	value2 := str.String(m.apiKeyInput.Value())
	apiKey := value2.Trim()
	if provider == "" {
		return *m, m.setStatus("provider API key unavailable")
	}
	if apiKey == "" {
		return *m, m.setStatus("provider API key required")
	}
	if modelID == "" {
		m.setupProviderAPIKey = apiKey

		return m.showSetupModelSelection()
	}

	option := rpcclient.ModelOption{ID: modelID, Provider: provider}
	if model, ok := modelprovider.DefaultRegistry().GetModel(provider, modelID); ok {
		option.Name = model.Name
		option.API = model.API
		option.ContextWindow = model.ContextWindow
		option.MaxTokens = model.MaxTokens
		option.Reasoning = model.Reasoning
		option.SupportsOAuth = model.SupportsOAuth
	}
	if err := m.persistSetupModelSelection(option, apiKey); err != nil {
		if isEmbeddingSetupError(err) {
			return m.showSetupNotice("Embedding setup required", getEmbeddingSetupInstruction(), "enter to continue")
		}

		return m.showSetupNotice("Provider API key unavailable", err.Error(), "enter to continue")
	}

	return m.completeSetupModelSelection(option)
}

func (m *model) showSetupBaseURLPrompt(providerID string, baseURL string) (tea.Model, tea.Cmd) {
	providerIDValue := str.String(providerID)
	providerID = providerIDValue.Trim()
	if providerID == "" {
		return *m, m.setStatus("provider selection unavailable")
	}

	m.cancelSetupModelPull()
	m.setupModelStep = setupModelStepBaseURL
	m.setupModelProvider = providerID
	baseURLValue3 := str.String(baseURL)
	m.setupModelBaseURL = baseURLValue3.Trim()
	m.setupModels = nil
	m.setupProviderAPIKey = ""
	m.setupPendingModelID = ""
	m.setupNoticeTitle = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.setupNoticeAction = ""
	m.baseURLInput = newSetupBaseURLInput()
	m.baseURLInput.SetValue(m.setupModelBaseURL)
	m.baseURLInput.CursorEnd()
	m.resize()

	return *m, m.setStatus("enter local provider base URL")
}

func (m *model) submitSetupBaseURL() (tea.Model, tea.Cmd) {
	setupModelProviderValue2 := str.String(m.setupModelProvider)
	providerID := setupModelProviderValue2.Trim()
	value3 := str.String(m.baseURLInput.Value())
	baseURL := strings.TrimRight(value3.Trim(), "/")
	if providerID == "" {
		return *m, m.setStatus("provider selection unavailable")
	}
	if baseURL == "" {
		return *m, m.setStatus("base URL required")
	}
	if err := validateSetupBaseURL(baseURL); err != nil {
		return *m, m.setStatus("base URL invalid")
	}
	err := m.loadSetupModels(providerID, baseURL)
	if err != nil {
		return m.showLocalProviderUnavailableNotice(baseURL)
	}
	if len(m.setupModels) == 0 {
		return *m, m.setStatus("models unavailable")
	}

	return m.showSetupModelSelection()
}

func validateSetupBaseURL(value string) error {
	value4 := str.String(value)
	parsed, err := url.Parse(value4.Trim())
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("base URL is invalid")
	}

	return nil
}

func (m *model) showMissingSetupModelPullPrompt(option rpcclient.ModelOption) (tea.Model, tea.Cmd) {
	iDValue3 := str.String(option.ID)
	modelID := iDValue3.Trim()
	if modelID == "" {
		return *m, m.setStatus("model selection unavailable")
	}

	m.cancelSetupModelPull()
	m.setupModelStep = setupModelStepNotice
	m.setupNoticeAction = setupNoticeActionMissingModelPull
	m.setupNoticeTitle = "Install " + modelID + "?"
	m.setupPendingModelID = modelID
	m.setupNoticeMessage = strings.Join([]string{
		"This Ollama model is not installed locally.",
		"Pull it now before saving.",
		"Or skip to save without installing.",
	}, "\n")
	m.setupNoticeHint = "enter to pull · s to skip · esc to go back"
	m.resize()

	return *m, m.setStatus("model not installed")
}

func (m *model) showLocalProviderUnavailableNotice(baseURL string) (tea.Model, tea.Cmd) {
	baseURLValue4 := str.String(baseURL)
	message := []string{
		"Could not connect to Ollama at " + baseURLValue4.Trim() + ".",
		"Start Ollama, or edit the base URL and retry.",
	}

	m.setupModelStep = setupModelStepNotice
	baseURLValue5 := str.String(baseURL)
	m.setupModelBaseURL = baseURLValue5.Trim()
	m.setupNoticeAction = setupNoticeActionLocalUnavailable
	m.setupNoticeTitle = "Ollama not reachable"
	m.setupPendingModelID = ""
	m.setupNoticeMessage = strings.Join(message, "\n")
	m.setupNoticeHint = "enter to retry · esc to edit base URL"
	m.resize()

	return *m, m.setStatus("ollama not reachable")
}

func (m *model) showSetupModelToolWarning(option rpcclient.ModelOption) (tea.Model, tea.Cmd) {
	iDValue4 := str.String(option.ID)
	modelID := iDValue4.Trim()
	if modelID == "" {
		return *m, m.setStatus("model selection unavailable")
	}

	m.setSetupModelOption(option)
	m.setupModelStep = setupModelStepNotice
	m.setupNoticeAction = setupNoticeActionToolWarning
	m.setupNoticeTitle = "Tool support warning"
	m.setupPendingModelID = modelID
	m.setupNoticeMessage = strings.Join([]string{
		modelID + " does not advertise tool support.",
		"Morph can save it for chat, but agent workflows that need tools may fail.",
	}, "\n")
	m.setupNoticeHint = "enter to save anyway · esc to choose another model"
	m.resize()

	return *m, m.setStatus("model may not support tools")
}

func (m *model) startSetupModelPull() (tea.Model, tea.Cmd) {
	option, ok := m.getPendingSetupModelOption()
	if !ok {
		return *m, m.setStatus("model selection unavailable")
	}

	provider := getSetupModelProvider(m.setupModelProvider, option)
	iDValue5 := str.String(option.ID)
	modelID := iDValue5.Trim()
	baseURL := getSetupModelOptionBaseURL(provider, m.setupModelBaseURL, option)
	baseURLValue6 := str.String(baseURL)
	if provider == "" || modelID == "" || baseURLValue6.Trim() == "" {
		return *m, m.setStatus("model selection unavailable")
	}

	m.cancelSetupModelPull()
	ctx, cancel := context.WithCancel(m.chatCtx)
	events := make(chan tea.Msg, 16)
	m.setupPullCancel = cancel
	m.setupPullEvents = events
	m.setupModelStep = setupModelStepNotice
	m.setupNoticeAction = setupNoticeActionPullingModel
	m.setupNoticeTitle = "Pulling " + modelID
	m.setupPendingModelID = modelID
	m.setupNoticeMessage = "Starting Ollama pull..."
	m.setupNoticeHint = "esc to cancel"
	m.resize()

	return *m, tea.Batch(
		runSetupModelPullCommand(ctx, provider, baseURL, option, events),
		waitForSetupModelPullEvent(events),
		m.setStatus("pulling model"),
	)
}

func runSetupModelPullCommand(
	ctx context.Context,
	provider string,
	baseURL string,
	option rpcclient.ModelOption,
	events chan<- tea.Msg,
) tea.Cmd {
	return func() tea.Msg {
		defer close(events)
		iDValue6 := str.String(option.ID)
		modelID := iDValue6.Trim()
		printer := clibase.NewPullProgressPrinter(io.Discard, true)
		var onProgress func(provider_ollama.PullProgress)
		if printer != nil {
			var lastLines []string
			onProgress = func(progress provider_ollama.PullProgress) {
				printer.Progress(progress)
				lines := printer.Lines()
				if sameSetupPullProgressLines(lastLines, lines) {
					return
				}
				lastLines = lines
				_ = sendSetupModelPullEvent(ctx, events, setupModelPullProgressMsg{
					provider: provider,
					model:    modelID,
					lines:    lines,
				})
			}
		}

		err := pullSetupOllamaModel(ctx, baseURL, modelID, nil, onProgress)
		if printer != nil {
			printer.Finish()
		}
		_ = sendSetupModelPullEvent(ctx, events, setupModelPullCompletedMsg{
			provider: provider,
			model:    modelID,
			option:   option,
			err:      err,
		})

		return nil
	}
}

func sameSetupPullProgressLines(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}

	return true
}

func sendSetupModelPullEvent(ctx context.Context, events chan<- tea.Msg, msg tea.Msg) bool {
	if events == nil {
		return false
	}

	select {
	case events <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForSetupModelPullEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return setupModelPullClosedMsg{}
		}

		return msg
	}
}

func (m model) updateSetupModelPullProgress(msg setupModelPullProgressMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentSetupModelPull(msg.provider, msg.model) {
		return m, nil
	}
	if len(msg.lines) == 0 {
		return m, waitForSetupModelPullEventFromState(&m)
	}

	m.setupNoticeMessage = strings.Join(msg.lines, "\n")
	m.resize()

	return m, waitForSetupModelPullEventFromState(&m)
}

func waitForSetupModelPullEventFromState(m *model) tea.Cmd {
	if m == nil || m.setupPullEvents == nil {
		return nil
	}

	return waitForSetupModelPullEvent(m.setupPullEvents)
}

func (m model) completeSetupModelPull(msg setupModelPullCompletedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentSetupModelPull(msg.provider, msg.model) {
		return m, nil
	}

	m.setupPullCancel = nil
	m.setupPullEvents = nil
	if msg.err != nil {
		next, cmd := m.showMissingSetupModelPullPrompt(msg.option)
		nextModel := next.(model)
		nextModel.setupNoticeMessage = getSetupModelPullFailureMessage(
			getSetupModelOptionBaseURL(msg.provider, m.setupModelBaseURL, msg.option),
			msg.err,
		)
		nextModel.setupNoticeHint = "enter to retry · s to skip · esc to go back"

		return nextModel, cmd
	}

	option := msg.option
	option.LocalMissing = false
	if shouldWarnSetupModelToolSupport(msg.provider, option) {
		return m.showSetupModelToolWarning(option)
	}
	if err := m.persistSetupModelSelection(option, ""); err != nil {
		return m.showSetupNotice("Model setup unavailable", err.Error(), "enter to continue")
	}

	return m.completeSetupModelSelection(option)
}

func (m *model) skipMissingSetupModelPull() (tea.Model, tea.Cmd) {
	option, ok := m.getPendingSetupModelOption()
	if !ok {
		return *m, m.setStatus("model selection unavailable")
	}
	provider := getSetupModelProvider(m.setupModelProvider, option)
	if shouldWarnSetupModelToolSupport(provider, option) {
		return m.showSetupModelToolWarning(option)
	}
	if err := m.persistSetupModelSelection(option, ""); err != nil {
		return m.showSetupNotice("Model setup unavailable", err.Error(), "enter to continue")
	}

	return m.completeSetupModelSelection(option)
}

func (m *model) confirmSetupModelToolWarning() (tea.Model, tea.Cmd) {
	option, ok := m.getPendingSetupModelOption()
	if !ok {
		return *m, m.setStatus("model selection unavailable")
	}
	if err := m.persistSetupModelSelection(option, ""); err != nil {
		return m.showSetupNotice("Model setup unavailable", err.Error(), "enter to continue")
	}

	return m.completeSetupModelSelection(option)
}

func (m model) getPendingSetupModelOption() (rpcclient.ModelOption, bool) {
	setupPendingModelIDValue2 := str.String(m.setupPendingModelID)
	modelID := setupPendingModelIDValue2.Trim()
	if modelID == "" {
		return rpcclient.ModelOption{}, false
	}

	for _, option := range m.setupModels {
		iDValue7 := str.String(option.ID)
		if iDValue7.Trim() == modelID {
			return option, true
		}
	}

	return rpcclient.ModelOption{}, false
}

func getSetupModelPullFailureMessage(baseURL string, err error) string {
	if err == nil {
		return ""
	}

	errorValue := str.String(err.Error())
	message := errorValue.Trim()
	baseURLValue7 := str.String(baseURL)
	if baseURL = baseURLValue7.Trim(); baseURL == "" {
		baseURL = constants.DefaultOllamaBaseURL
	}
	if strings.Contains(strings.ToLower(message), "ollama is not reachable") ||
		strings.Contains(strings.ToLower(message), "connection refused") {
		return strings.Join([]string{
			"Could not connect to Ollama at " + baseURL + ".",
			"Start Ollama, or edit the base URL and retry.",
		}, "\n")
	}

	return "Ollama pull failed: " + message
}

func (m *model) setSetupModelOption(option rpcclient.ModelOption) {
	iDValue8 := str.String(option.ID)
	modelID := iDValue8.Trim()
	if modelID == "" {
		return
	}

	for index := range m.setupModels {
		iDValue9 := str.String(m.setupModels[index].ID)
		if iDValue9.Trim() == modelID {
			m.setupModels[index] = option
			return
		}
	}

	m.setupModels = append(m.setupModels, option)
}

func shouldWarnSetupModelToolSupport(provider string, option rpcclient.ModelOption) bool {
	return isLocalSetupProvider(provider) && !option.SupportsTools
}

func (m model) isCurrentSetupModelPull(provider string, modelID string) bool {
	setupModelProviderValue3 := str.String(m.setupModelProvider)
	providerValue := str.String(provider)
	setupPendingModelIDValue3 := str.String(m.setupPendingModelID)
	modelIDValue := str.String(modelID)
	return m.setupModelStep == setupModelStepNotice &&
		m.setupNoticeAction == setupNoticeActionPullingModel && setupModelProviderValue3.
		Trim() == providerValue.Trim() && setupPendingModelIDValue3.
		Trim() == modelIDValue.Trim()
}

func (m *model) cancelSetupModelPull() {
	if m.setupPullCancel != nil {
		m.setupPullCancel()
		m.setupPullCancel = nil
	}
	m.setupPullEvents = nil
}

func (m *model) showSetupNotice(title string, message string, hint string) (tea.Model, tea.Cmd) {
	m.cancelSetupOAuthLogin()
	m.cancelSetupModelPull()
	m.setupModelStep = setupModelStepNotice
	m.setupNoticeAction = ""
	titleValue := str.String(title)
	m.setupNoticeTitle = titleValue.Trim()
	titleValue2 := str.String(title)
	m.setupPendingModelID = titleValue2.Trim()
	messageValue := str.String(message)
	m.setupNoticeMessage = messageValue.Trim()
	hintValue := str.String(hint)
	m.setupNoticeHint = hintValue.Trim()
	m.setupOAuthPending = false
	m.setupOAuthProvider = ""
	m.resize()
	titleValue3 := str.String(title)
	return *m, m.setStatus(titleValue3.Normalized())
}

func (m *model) startSetupOAuthLogin(provider string) (tea.Model, tea.Cmd) {
	providerValue2 := str.String(provider)
	provider = providerValue2.Trim()
	if provider == "" {
		return *m, m.setStatus("provider selection unavailable")
	}
	subscriptionProvider, ok := getSetupSubscriptionProvider(provider)
	if !ok {
		return m.showSetupNotice(
			"Authentication unavailable",
			"Subscription login is not available for "+getProviderDisplayName(provider)+".",
			"enter to continue",
		)
	}

	m.cancelSetupOAuthLogin()
	ctx, cancel := context.WithCancel(m.chatCtx)
	reader, writer := io.Pipe()
	m.setupModelStep = setupModelStepNotice
	m.setupModelProvider = provider
	m.setupPendingModelID = "Connect " + getProviderDisplayName(provider)
	m.setupNoticeMessage = strings.Join([]string{
		"Opening browser to connect " + getProviderDisplayName(provider) + ".",
		"Complete login in your browser, then return here.",
	}, "\n")
	m.setupNoticeHint = "esc to go back"
	m.setupOAuthPending = true
	m.setupOAuthProvider = provider
	m.setupOAuthCancel = cancel
	m.resize()

	return *m, tea.Batch(
		runSetupOAuthLoginCommand(ctx, provider, subscriptionProvider, writer),
		readSetupOAuthOutputCommand(provider, reader),
		m.setStatus("waiting for oauth login"),
	)
}

func runSetupOAuthLoginCommand(
	ctx context.Context,
	provider string,
	subscriptionProvider appcredential.SubscriptionProvider,
	writer *io.PipeWriter,
) tea.Cmd {
	return func() tea.Msg {
		var output bytes.Buffer
		multiWriter := io.MultiWriter(writer, &output)
		credential, err := subscriptionProvider.Login(ctx, appcredential.LoginOptions{
			Provider: provider,
			Output:   multiWriter,
		})
		if err == nil {
			err = newSetupCredentialStore().Set(provider, credential)
		}
		if closeErr := writer.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		textValue2 := str.String(output.String())
		return setupOAuthCompletedMsg{
			provider: provider,
			output:   textValue2.Trim(),
			err:      err,
		}
	}
}

func readSetupOAuthOutputCommand(provider string, reader *io.PipeReader) tea.Cmd {
	return func() tea.Msg {
		buffer := make([]byte, 1024)
		n, err := reader.Read(buffer)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				return setupOAuthOutputMsg{provider: provider, reader: reader}
			}

			return setupOAuthOutputMsg{provider: provider, reader: reader, err: err}
		}
		if n == 0 {
			return setupOAuthOutputMsg{provider: provider, reader: reader}
		}

		return setupOAuthOutputMsg{
			provider: provider,
			reader:   reader,
			line:     strings.TrimRight(string(buffer[:n]), "\r\n"),
		}
	}
}

func (m model) updateSetupOAuthOutput(msg setupOAuthOutputMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentSetupOAuthLogin(msg.provider) {
		return m, nil
	}
	if msg.err != nil {
		next, cmd := m.showSetupNotice("Authentication unavailable", msg.err.Error(), "enter to continue")
		return next, cmd
	}

	output := shortenSetupOAuthOutput(msg.line)
	if output == "" {
		return m, nil
	}
	trimmedValue := str.String(m.setupNoticeMessage + "\n" + output)
	m.setupNoticeMessage = trimmedValue.Trim()
	m.resize()

	return m, readSetupOAuthOutputCommand(msg.provider, msg.reader)
}

func shortenSetupOAuthOutput(output string) string {
	lines := strings.Split(output, "\n")
	shortened := make([]string, 0, len(lines))
	for _, line := range lines {
		line = shortenSetupOAuthOutputLine(line)
		if line != "" {
			shortened = append(shortened, line)
		}
	}

	return strings.Join(shortened, "\n")
}

func shortenSetupOAuthOutputLine(line string) string {
	lineValue := str.String(line)
	line = lineValue.Trim()
	if line == "" {
		return ""
	}
	parsed, err := url.Parse(line)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return line
	}

	shortURL := parsed.Scheme + "://" + parsed.Host
	if parsed.EscapedPath() != "" {
		shortURL += parsed.EscapedPath()
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		shortURL += "..."
	}

	return "URL: " + shortURL
}

func (m model) completeSetupOAuthLogin(msg setupOAuthCompletedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentSetupOAuthLogin(msg.provider) {
		return m, nil
	}
	if msg.err != nil {
		m.cancelSetupOAuthLogin()
		outputValue := str.String(msg.output)
		message := outputValue.Trim()
		if message != "" {
			message += "\n"
		}
		message += msg.err.Error()
		next, cmd := m.showSetupNotice("Authentication failed", message, "enter to retry")
		nextModel := next.(model)
		nextModel.setupOAuthProvider = msg.provider

		return nextModel, cmd
	}

	m.cancelSetupOAuthLogin()
	m.setupOAuthPending = false
	m.setupOAuthProvider = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.resize()

	return m.showSetupModelSelection()
}

func loadSetupModelOptionsCommand(
	ctx context.Context,
	provider string,
	selectedModelID string,
	opts clisetup.ModelOptions,
) tea.Cmd {
	return func() tea.Msg {
		baseURL := clisetup.ResolveModelOptionsBaseURL(opts)
		opts.BaseURL = baseURL
		models, _, err := clisetup.ListModelOptions(ctx, opts)
		providerValue3 := str.String(provider)
		selectedModelIDValue := str.String(selectedModelID)
		return setupModelOptionsLoadedMsg{
			provider:        providerValue3.Trim(),
			baseURL:         baseURL,
			selectedModelID: selectedModelIDValue.Trim(),
			models:          models,
			err:             err,
		}
	}
}

func (m model) completeSetupModelOptionsRefresh(msg setupModelOptionsLoadedMsg) (tea.Model, tea.Cmd) {
	providerValue4 := str.String(msg.provider)
	setupModelProviderValue4 := str.String(m.setupModelProvider)
	if providerValue4.Trim() != setupModelProviderValue4.Trim() {
		return m, nil
	}
	if msg.err != nil {
		if isLocalSetupProvider(msg.provider) {
			return m.showLocalProviderUnavailableNotice(msg.baseURL)
		}

		return m, m.setStatus("model discovery failed")
	}
	if len(msg.models) == 0 {
		return m, m.setStatus("models unavailable")
	}

	m.setupModels = msg.models
	baseURLValue8 := str.String(msg.baseURL)
	m.setupModelBaseURL = baseURLValue8.Trim()
	m.setProfileModelSetupModelSelection(msg.selectedModelID)
	m.resize()

	return m, m.setStatus("models refreshed")
}

func (m *model) refreshSetupModelOptions() (tea.Model, tea.Cmd) {
	setupModelProviderValue5 := str.String(m.setupModelProvider)
	provider := setupModelProviderValue5.Trim()
	if provider == "" {
		return *m, m.setStatus("provider selection unavailable")
	}

	rawConfig := m.loadRawProfileConfig()
	selectedModelID := m.currentSetupModelID()
	nameValue5 := str.String(rawConfig.Models.Main.Name)
	setupModelBaseURLValue := str.String(m.setupModelBaseURL)
	opts := clisetup.ModelOptions{
		Provider:  provider,
		Current:   nameValue5.Trim(),
		BaseURL:   setupModelBaseURLValue.Trim(),
		OAuthOnly: m.setupAuthMethod == setupAuthMethodSubscription,
		Config:    rawConfig,
		Refresh:   true,
	}

	return *m, tea.Batch(
		loadSetupModelOptionsCommand(m.chatCtx, provider, selectedModelID, opts),
		m.setStatus("refreshing models"),
	)
}

func (m *model) cancelSetupOAuthLogin() {
	if m.setupOAuthCancel != nil {
		m.setupOAuthCancel()
		m.setupOAuthCancel = nil
	}
}

func (m model) isCurrentSetupOAuthLogin(provider string) bool {
	setupOAuthProviderValue := str.String(m.setupOAuthProvider)
	providerValue5 := str.String(provider)
	return m.setupOAuthPending &&
		m.setupModelStep == setupModelStepNotice && setupOAuthProviderValue.
		Trim() == providerValue5.Trim()
}

func (m *model) showSetupProviderAPIKeyPrompt(option rpcclient.ModelOption) (tea.Model, tea.Cmd) {
	provider := getSetupModelProvider(m.setupModelProvider, option)
	if provider == "" {
		return *m, m.setStatus("model selection unavailable")
	}

	m.setupModelStep = setupModelStepAPIKey
	m.setupModelProvider = provider
	iDValue10 := str.String(option.ID)
	m.setupPendingModelID = iDValue10.Trim()
	m.apiKeyInput = newProviderAPIKeyInput("API key for " + getProviderDisplayName(provider))
	m.prefillSetupProviderAPIKeyInput(provider)
	m.resize()

	return *m, m.setStatus("provider API key required")
}

func (m *model) showSetupProviderAPIKeyPromptForProvider(provider string) (tea.Model, tea.Cmd) {
	providerValue6 := str.String(provider)
	provider = providerValue6.Trim()
	if provider == "" {
		return *m, m.setStatus("provider selection unavailable")
	}

	m.setupModelStep = setupModelStepAPIKey
	m.setupModelProvider = provider
	m.setupPendingModelID = ""
	m.apiKeyInput = newProviderAPIKeyInput("API key for " + getProviderDisplayName(provider))
	m.prefillSetupProviderAPIKeyInput(provider)
	m.resize()

	return *m, m.setStatus("provider API key required")
}

func (m *model) prefillSetupProviderAPIKeyInput(provider string) {
	apiKey := m.loadRawProviderAPIKey(provider)
	if apiKey == "" {
		return
	}

	m.apiKeyInput.SetValue(apiKey)
	m.apiKeyInput.CursorEnd()
}

func (m *model) completeSetupModelSelection(option rpcclient.ModelOption) (tea.Model, tea.Cmd) {
	m.clearProfileModelSetup()
	m.applySetupModelSelectionToRuntime(option)
	m.resize()
	m.setTranscriptContent()

	cmds := []tea.Cmd{m.setStatus("model setup saved")}
	if cmd := m.refreshSetupModelRuntimeCmd(option); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return *m, tea.Batch(cmds...)
}

func (m *model) persistSetupModelSelection(option rpcclient.ModelOption, apiKey string) error {
	provider := getSetupModelProvider(m.setupModelProvider, option)
	iDValue11 := str.String(option.ID)
	modelID := iDValue11.Trim()
	if provider == "" || modelID == "" {
		return errors.New("model selection unavailable")
	}
	configPathValue := str.String(m.configPath)
	if configPathValue.Trim() == "" {
		return errors.New("config path unavailable")
	}

	api := getSetupModelOptionAPI(provider, option)
	baseURL := getSetupModelOptionBaseURL(provider, m.setupModelBaseURL, option)
	updates := []config.ConfigUpdate{
		{Path: "models.main.provider", Value: provider},
		{Path: "models.main.name", Value: modelID},
		{Path: "models.main.api", Value: api},
		{Path: "models.main.baseUrl", Value: baseURL},
		{Path: "models.summary.provider", Value: provider},
		{Path: "models.summary.name", Value: modelID},
		{Path: "models.summary.api", Value: api},
		{Path: "models.summary.baseUrl", Value: baseURL},
	}
	updates = append(updates, config.ModelSetupEmbeddingUpdates(provider, baseURL)...)
	apiKeyValue := str.String(apiKey)
	if apiKey = apiKeyValue.Trim(); apiKey != "" {
		updates = append(updates, config.ConfigUpdate{
			Path:  fmt.Sprintf("models.providers.%s.apiKey", provider),
			Value: apiKey,
		})
	}
	if _, err := config.SetConfigValuesRelaxed(m.configEnvPath, m.configPath, updates); err != nil {
		return err
	}

	cfg, err := config.Load(m.configEnvPath, m.configPath)
	if err == nil {
		config.Set(cfg)
		m.setupSavedConfig = cfg
	}

	return nil
}

func getSetupModelOptionAPI(provider string, option rpcclient.ModelOption) string {
	aPIValue := str.String(option.API)
	if api := aPIValue.Trim(); api != "" {
		return api
	}
	providerValue7 := str.String(provider)
	providerDef, ok := modelprovider.DefaultRegistry().GetProvider(providerValue7.Normalized())
	if !ok {
		return ""
	}
	defaultAPIValue := str.String(providerDef.DefaultAPI)
	return defaultAPIValue.Trim()
}

func getSetupModelOptionBaseURL(provider string, setupBaseURL string, option rpcclient.ModelOption) string {
	baseURLValue9 := str.String(option.BaseURL)
	if baseURL := baseURLValue9.Trim(); baseURL != "" {
		return baseURL
	}
	if isLocalSetupProvider(provider) {
		setupBaseURLValue := str.String(setupBaseURL)
		return setupBaseURLValue.Trim()
	}

	return ""
}

func isLocalSetupProvider(provider string) bool {
	providerValue8 := str.String(provider)
	providerDef, ok := modelprovider.DefaultRegistry().GetProvider(providerValue8.Normalized())
	if !ok || providerDef.Local == nil {
		return false
	}
	authMarker := str.String(providerDef.Local.AuthMarker)
	return authMarker.Trim() != ""
}

func isMissingLocalSetupModel(provider string, option rpcclient.ModelOption) bool {
	return isLocalSetupProvider(provider) && option.LocalMissing
}

func isSetupProviderLocalOption(provider rpcclient.ProviderOption) bool {
	return provider.Local || isLocalSetupProvider(provider.ID)
}

func (m *model) applySetupModelSelectionToRuntime(option rpcclient.ModelOption) {
	m.applySelectedModelToRuntime(option)
	if m.setupSavedConfig == nil {
		return
	}

	info := runtimeInfoFromConfig(m.setupSavedConfig)
	m.runtimeInfo.Provider = info.Provider
	m.runtimeInfo.Model = info.Model
	m.runtimeInfo.SummaryProvider = info.SummaryProvider
	m.runtimeInfo.SummaryModel = info.SummaryModel
	m.runtimeInfo.EmbeddingProvider = info.EmbeddingProvider
	m.runtimeInfo.EmbeddingModel = info.EmbeddingModel
	m.runtimeInfo.Storage = info.Storage
	m.runtimeInfo.Streaming = info.Streaming
	m.modelName = getRuntimeModelDisplayName(info.Provider, info.Model)
}

func (m *model) refreshSetupModelRuntimeCmd(option rpcclient.ModelOption) tea.Cmd {
	client, ok := m.modelClient.(modelSelector)
	if m.modelClient == nil || !ok {
		return nil
	}

	provider := getSetupModelProvider(m.setupModelProvider, option)
	iDValue12 := str.String(option.ID)
	modelID := iDValue12.Trim()
	if provider == "" || modelID == "" {
		return nil
	}

	return func() tea.Msg {
		ctx := m.chatCtx
		if ctx == nil {
			ctx = context.Background()
		}

		model, err := client.SelectModel(ctx, modelID, rpcclient.ModelSelectOptions{Provider: provider})

		return setupModelRuntimeSelectedMsg{Model: model, Err: err}
	}
}

func (m *model) completeSetupModelRuntimeSelection(msg setupModelRuntimeSelectedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return *m, m.setStatus("model setup saved; daemon refresh unavailable")
	}

	m.applySetupModelSelectionToRuntime(msg.Model)

	return *m, m.setStatus("model setup saved; daemon restarting")
}

func (m model) checkSetupModelAuth(option rpcclient.ModelOption) error {
	provider := getSetupModelProvider(m.setupModelProvider, option)
	iDValue13 := str.String(option.ID)
	modelID := iDValue13.Trim()
	if provider == "" || modelID == "" {
		return errors.New("model selection unavailable")
	}

	cfg, err := config.Load(m.configEnvPath, m.configPath)
	if err != nil {
		return err
	}
	cfg.Models.Main.Provider = provider
	cfg.Models.Main.Name = modelID
	cfg.Models.Main.BaseURL = getSetupModelOptionBaseURL(provider, m.setupModelBaseURL, option)
	cfg.Models.Summary.Provider = provider
	cfg.Models.Summary.Name = modelID
	cfg.Models.Summary.BaseURL = getSetupModelOptionBaseURL(provider, m.setupModelBaseURL, option)
	cfg.Search.Vector.Enabled = false
	if option.API != "" {
		cfg.Models.Main.API = option.API
		cfg.Models.Summary.API = option.API
	}
	_, err = cfg.ResolveModelAuth()

	return err
}

func (m *model) handleProfileModelSetupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.setupDismissible && isSetupCloseKey(msg) {
		return m.closeProfileSetup()
	}

	switch m.setupModelStep {
	case setupModelStepAuthMethod:
		if msg.Key().Code == tea.KeyEsc {
			return *m, m.startProfileSetup(m.setupDismissible)
		}

		return m.handleProfileModelSetupListKey(msg, len(setupAuthMethodOptions), m.selectCurrentSetupAuthMethodOption)
	case setupModelStepProvider:
		switch msg.Key().Code {
		case tea.KeyEsc, tea.KeyLeft, tea.KeyBackspace:
			return m.showSetupAuthMethodSelection()
		}

		return m.handleProfileModelSetupListKey(msg, len(m.setupProviders), m.selectCurrentSetupProviderOption)
	case setupModelStepBaseURL:
		switch msg.Key().Code {
		case tea.KeyEsc, tea.KeyLeft:
			return m.showSetupProviderSelection()
		case tea.KeyEnter:
			return m.submitSetupBaseURL()
		}

		var cmd tea.Cmd
		m.baseURLInput, cmd = m.baseURLInput.Update(msg)
		m.resize()
		return *m, inputHandledCmd(cmd)
	case setupModelStepModel:
		if isSetupModelRefreshKey(msg) {
			return m.refreshSetupModelOptions()
		}
		if isModelFilterKey(msg) {
			var cmd tea.Cmd
			m.modelFilterInput, cmd = m.modelFilterInput.Update(msg)
			m.setupItemSelected = 0
			m.setupOffset = 0
			m.resize()
			return *m, inputHandledCmd(cmd)
		}

		switch msg.Key().Code {
		case tea.KeyEsc, tea.KeyLeft:
			if isLocalSetupProvider(m.setupModelProvider) {
				return m.showSetupBaseURLPrompt(m.setupModelProvider, m.setupModelBaseURL)
			}

			return m.showSetupProviderSelection()
		}

		return m.handleProfileModelSetupListKey(msg, len(m.filteredSetupModels()), m.selectCurrentSetupModelOption)
	case setupModelStepAPIKey:
		switch msg.Key().Code {
		case tea.KeyEsc:
			setupPendingModelIDValue4 := str.String(m.setupPendingModelID)
			if setupPendingModelIDValue4.Trim() == "" {
				if isLocalSetupProvider(m.setupModelProvider) {
					return m.showSetupBaseURLPrompt(m.setupModelProvider, m.setupModelBaseURL)
				}

				return m.showSetupProviderSelection()
			}

			return m.showSetupModelSelection()
		case tea.KeyEnter:
			return m.submitSetupProviderAPIKey()
		}

		var cmd tea.Cmd
		m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
		m.resize()
		return *m, inputHandledCmd(cmd)
	case setupModelStepNotice:
		if m.setupNoticeAction == setupNoticeActionMissingModelPull {
			switch msg.Key().Code {
			case tea.KeyEnter:
				return m.startSetupModelPull()
			case tea.KeyEsc, tea.KeyLeft, tea.KeyBackspace:
				return m.showSetupModelSelection()
			default:
				if strings.EqualFold(msg.Key().Text, "s") {
					return m.skipMissingSetupModelPull()
				}
			}

			return *m, nil
		}
		if m.setupNoticeAction == setupNoticeActionPullingModel {
			switch msg.Key().Code {
			case tea.KeyEsc, tea.KeyLeft, tea.KeyBackspace:
				m.cancelSetupModelPull()
				return m.showSetupModelSelection()
			}

			return *m, nil
		}
		if m.setupNoticeAction == setupNoticeActionLocalUnavailable {
			switch msg.Key().Code {
			case tea.KeyEnter:
				return m.submitSetupBaseURL()
			case tea.KeyEsc, tea.KeyLeft, tea.KeyBackspace:
				return m.showSetupBaseURLPrompt(m.setupModelProvider, m.setupModelBaseURL)
			}

			return *m, nil
		}
		if m.setupNoticeAction == setupNoticeActionToolWarning {
			switch msg.Key().Code {
			case tea.KeyEnter:
				return m.confirmSetupModelToolWarning()
			case tea.KeyEsc, tea.KeyLeft, tea.KeyBackspace:
				return m.showSetupModelSelection()
			}

			return *m, nil
		}

		switch msg.Key().Code {
		case tea.KeyEnter:
			if m.setupOAuthPending {
				return *m, nil
			}
			setupOAuthProviderValue2 := str.String(m.setupOAuthProvider)
			if setupOAuthProviderValue2.Trim() != "" {
				return m.startSetupOAuthLogin(m.setupOAuthProvider)
			}

			return m.showSetupModelSelection()
		case tea.KeyEsc, tea.KeyLeft, tea.KeyBackspace:
			return m.showSetupProviderSelection()
		}

		return *m, nil
	default:
		return *m, nil
	}
}

func (m *model) showSetupAuthMethodSelection() (tea.Model, tea.Cmd) {
	m.cancelSetupOAuthLogin()
	m.cancelSetupModelPull()
	setupAuthMethodValue := str.String(m.setupAuthMethod)
	authMethod := setupAuthMethodValue.Trim()
	m.setupModelStep = setupModelStepAuthMethod
	m.setupAuthMethod = ""
	m.setupProviders = nil
	m.setupModels = nil
	m.setupModelProvider = ""
	m.setupModelBaseURL = ""
	m.setupProviderAPIKey = ""
	m.setupPendingModelID = ""
	m.setupNoticeTitle = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.setupNoticeAction = ""
	m.setupOAuthPending = false
	m.setupOAuthProvider = ""
	for index, option := range setupAuthMethodOptions {
		if option.ID == authMethod {
			m.setProfileModelSetupSelection(index, len(setupAuthMethodOptions))
			break
		}
	}
	m.resize()

	return *m, m.setStatus("choose an auth method")
}

func (m *model) showSetupProviderSelection() (tea.Model, tea.Cmd) {
	m.cancelSetupOAuthLogin()
	m.cancelSetupModelPull()
	setupModelProviderValue6 := str.String(m.setupModelProvider)
	provider := setupModelProviderValue6.Trim()
	m.setupModelStep = setupModelStepProvider
	m.setupModels = nil
	m.setupModelProvider = ""
	m.setupModelBaseURL = ""
	m.setupProviderAPIKey = ""
	m.setupPendingModelID = ""
	m.setupNoticeTitle = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.setupNoticeAction = ""
	m.setupOAuthPending = false
	m.setupOAuthProvider = ""
	for index, option := range m.setupProviders {
		iDValue14 := str.String(option.ID)
		if iDValue14.Trim() == provider {
			m.setProfileModelSetupSelection(index, len(m.setupProviders))
			break
		}
	}
	m.resize()

	return *m, m.setStatus("choose a model provider")
}

func (m *model) showSetupModelSelection() (tea.Model, tea.Cmd) {
	m.setupModelStep = setupModelStepModel
	m.setupNoticeTitle = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.setupNoticeAction = ""
	m.setupPendingModelID = ""
	m.resize()

	return *m, m.setStatus("choose a model")
}

func (m *model) handleProfileModelSetupListKey(
	msg tea.KeyPressMsg,
	count int,
	submit func() (tea.Model, tea.Cmd),
) (tea.Model, tea.Cmd) {
	if count == 0 {
		return *m, nil
	}

	selection := m.setupItemSelected
	switch msg.Key().Code {
	case tea.KeyUp:
		selection--
	case tea.KeyDown:
		selection++
	case tea.KeyHome:
		selection = 0
	case tea.KeyEnd:
		selection = count - 1
	case tea.KeyPgUp:
		selection -= m.getProfileModelSetupListHeight()
	case tea.KeyPgDown:
		selection += m.getProfileModelSetupListHeight()
	case tea.KeyEnter:
		return submit()
	default:
		return *m, nil
	}

	m.setProfileModelSetupSelection(selection, count)
	return *m, nil
}

func isSetupModelRefreshKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.Keystroke() == "ctrl+r" || key.Code == 'r' && key.Mod == tea.ModCtrl
}

func (m model) currentSetupModelID() string {
	models := m.filteredSetupModels()
	if len(models) == 0 {
		return ""
	}

	index := min(max(m.setupItemSelected, 0), len(models)-1)
	iDValue15 := str.String(models[index].ID)
	return iDValue15.Trim()
}

func (m *model) setProfileModelSetupModelSelection(modelID string) {
	models := m.filteredSetupModels()
	if len(models) == 0 {
		m.setProfileModelSetupSelection(0, 0)
		return
	}
	modelIDValue2 := str.String(modelID)
	modelID = modelIDValue2.Trim()
	if modelID != "" {
		for index, option := range models {
			iDValue16 := str.String(option.ID)
			if iDValue16.Trim() == modelID {
				m.setProfileModelSetupSelection(index, len(models))
				return
			}
		}
	}

	m.setProfileModelSetupSelection(0, len(models))
}

func (m *model) handleProfileModelSetupPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.setupModelStep == setupModelStepModel {
		var cmd tea.Cmd
		m.modelFilterInput, cmd = m.modelFilterInput.Update(msg)
		m.setupItemSelected = 0
		m.setupOffset = 0
		m.resize()
		return *m, inputHandledCmd(cmd)
	}
	if m.setupModelStep == setupModelStepBaseURL {
		var cmd tea.Cmd
		m.baseURLInput, cmd = m.baseURLInput.Update(msg)
		m.resize()
		return *m, inputHandledCmd(cmd)
	}
	if m.setupModelStep != setupModelStepAPIKey {
		return *m, nil
	}

	msg.Content = normalizeProviderAPIKeyPaste(msg.Content)
	var cmd tea.Cmd
	m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
	m.resize()

	return *m, inputHandledCmd(cmd)
}

func (m *model) handleProfileModelSetupWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	count := 0
	switch m.setupModelStep {
	case setupModelStepAuthMethod:
		count = len(setupAuthMethodOptions)
	case setupModelStepProvider:
		count = len(m.setupProviders)
	case setupModelStepModel:
		count = len(m.filteredSetupModels())
	default:
		return *m, nil
	}

	switch msg.Mouse().Button {
	case tea.MouseWheelUp:
		m.setProfileModelSetupSelection(m.setupItemSelected-1, count)
	case tea.MouseWheelDown:
		m.setProfileModelSetupSelection(m.setupItemSelected+1, count)
	}

	return *m, nil
}

func (m *model) handleProfileModelSetupClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return *m, nil
	}

	count := 0
	var submit func() (tea.Model, tea.Cmd)
	switch m.setupModelStep {
	case setupModelStepAuthMethod:
		count = len(setupAuthMethodOptions)
		submit = m.selectCurrentSetupAuthMethodOption
	case setupModelStepProvider:
		count = len(m.setupProviders)
		submit = m.selectCurrentSetupProviderOption
	case setupModelStepModel:
		count = len(m.filteredSetupModels())
		submit = m.selectCurrentSetupModelOption
	default:
		return *m, nil
	}
	if count == 0 {
		return *m, nil
	}

	row := msg.Y - m.getProfileModelSetupListFirstRow()
	if row < 0 || row >= m.getProfileModelSetupRenderedListHeight() {
		return *m, nil
	}
	if m.getProfileModelSetupRowHeight() > 1 {
		row /= 2
	}

	selection := m.setupOffset + row
	if selection < 0 || selection >= count {
		return *m, nil
	}

	m.setProfileModelSetupSelection(selection, count)
	return submit()
}

func (m *model) setProfileModelSetupSelection(selection int, count int) {
	if count <= 0 {
		m.setupItemSelected = 0
		m.setupOffset = 0
		return
	}

	height := m.getProfileModelSetupListHeight()
	m.setupItemSelected = min(max(selection, 0), count-1)
	m.setupOffset = getChatsCommandViewOffsetForSelection(m.setupItemSelected, m.setupOffset, height, count)
}

func (m model) renderProfileModelSetup() string {
	switch m.setupModelStep {
	case setupModelStepAuthMethod:
		hint := "enter to select · esc to go back"

		return m.renderProfileModelSetupList(
			"Select login method",
			hint,
			m.renderProfileModelSetupAuthMethodRows(),
		)
	case setupModelStepProvider:
		return m.renderProfileModelSetupList(
			"Select model provider",
			"enter to select · esc to go back",
			m.renderProfileModelSetupProviderRows(),
		)
	case setupModelStepBaseURL:
		return m.renderProfileModelSetupBaseURL()
	case setupModelStepModel:
		return m.renderProfileModelSetupModelList()
	case setupModelStepAPIKey:
		return m.renderProfileModelSetupAPIKey()
	case setupModelStepNotice:
		return m.renderProfileModelSetupNotice()
	default:
		return ""
	}
}

func (m model) renderProfileModelSetupAuthMethodRows() []string {
	height := m.getProfileModelSetupListHeight()
	end := min(m.setupOffset+height, len(setupAuthMethodOptions))
	rows := make([]string, 0, max((end-m.setupOffset)*2, 1))
	for index := m.setupOffset; index < end; index++ {
		option := setupAuthMethodOptions[index]
		row := renderProfileModelSetupOptionRow(
			option.Label,
			option.Description,
			m.getProfileModelSetupListWidth(),
			index == m.setupItemSelected,
		)
		rows = append(rows, strings.Split(row, "\n")...)
	}
	for len(rows) < height*m.getProfileModelSetupRowHeight() {
		rows = append(rows, "")
	}

	return rows
}

func (m model) renderProfileModelSetupList(title string, hintText string, rows []string) string {
	boxWidth := m.getProfileModelSetupBoxWidth()

	list := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(defaultTUITheme.InputFrameBorder)).
		Width(boxWidth).
		Render(strings.Join(rows, "\n"))

	return m.renderProfileModelSetupFrame(title, hintText, list)
}

func (m model) renderProfileModelSetupModelList() string {
	boxWidth := m.getProfileModelSetupBoxWidth()

	list := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(defaultTUITheme.InputFrameBorder)).
		Width(boxWidth).
		Render(strings.Join(m.renderProfileModelSetupModelRows(), "\n"))
	if detail := m.renderProfileModelSetupModelDetail(boxWidth); detail != "" {
		list = lipgloss.JoinVertical(lipgloss.Left, detail, list)
	}

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Width(boxWidth).
		Render(m.renderProfileModelSetupHint("enter to select · ctrl+r to refresh · esc to go back", boxWidth))

	return m.renderProfileModelSetupFrameWithTitleContent(
		m.renderProfileModelSetupModelTitle(boxWidth),
		hint,
		list,
	)
}

func (m model) renderProfileModelSetupModelTitle(width int) string {
	width = max(width, 1)
	title := m.getProfileModelSetupModelTitleText()
	inputWidth := min(setupModelFilterWidth, max(width/2, 10))
	titleText := m.renderProfileModelSetupTitleLeft(title, max(width-inputWidth-1, 1))
	filter := m.renderInlineModelFilterInput(inputWidth)
	gap := max(width-lipgloss.Width(titleText)-lipgloss.Width(filter), 1)

	return titleText + strings.Repeat(" ", gap) + filter
}

func (m model) renderProfileModelSetupModelDetail(width int) string {
	if !isLocalSetupProvider(m.setupModelProvider) {
		return ""
	}

	setupModelBaseURLValue2 := str.String(m.setupModelBaseURL)
	baseURL := setupModelBaseURLValue2.Trim()
	if baseURL == "" {
		baseURL = constants.DefaultOllamaBaseURL
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Width(max(width, 1)).
		Render(renderProfileModelSetupPaddedLabel("base URL: "+baseURL, width))
}

func (m model) getProfileModelSetupModelTitleText() string {
	if isLocalSetupProvider(m.setupModelProvider) {
		return "Select Ollama model"
	}

	return "Select model from " + getProviderDisplayName(m.setupModelProvider)
}

func (m model) renderProfileModelSetupTitle(width int, title string) string {
	width = max(width, 1)
	right := m.getProfileModelSetupLoginMethodLabel()
	if right == "" {
		return m.renderProfileModelSetupTitleLeft(title, width)
	}

	rightText := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(renderProfileModelSetupPaddedLabel(right, min(lipgloss.Width(right)+2, width)))
	titleText := m.renderProfileModelSetupTitleLeft(title, max(width-lipgloss.Width(rightText)-1, 1))
	gap := max(width-lipgloss.Width(titleText)-lipgloss.Width(rightText), 1)

	return titleText + strings.Repeat(" ", gap) + rightText
}

func (m model) renderProfileModelSetupTitleLeft(title string, width int) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Bold(true).
		Render(renderProfileModelSetupPaddedLabel(title, width))
}

func (m model) getProfileModelSetupLoginMethodLabel() string {
	setupAuthMethodValue2 := str.String(m.setupAuthMethod)
	switch setupAuthMethodValue2.Trim() {
	case setupAuthMethodSubscription:
		return "login type: subscription"
	case setupAuthMethodAPIKey:
		return "login type: api key"
	case setupAuthMethodLocal:
		return "login type: local"
	default:
		return ""
	}
}

func (m model) renderInlineModelFilterInput(width int) string {
	width = max(width, 1)
	input := m.modelFilterInput
	input.SetWidth(width)
	content := truncateCommandMenuText(input.View(), width)
	content += strings.Repeat(" ", max(width-lipgloss.Width(content), 0))

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Width(width).
		Render(content)
}

func (m model) renderProfileModelSetupFrame(title string, hintText string, body string) string {
	boxWidth := m.getProfileModelSetupBoxWidth()
	hint := m.renderProfileModelSetupHint(hintText, boxWidth)

	return m.renderProfileModelSetupFrameWithHint(title, hint, body)
}

func (m model) renderProfileModelSetupFrameWithHint(title string, hint string, body string) string {
	width := max(m.transcript.Width(), m.getMainPaneWidth())
	height := max(m.transcript.Height(), 1)
	boxWidth := m.getProfileModelSetupBoxWidth()
	titleValue4 := str.String(title)
	titleText := m.renderProfileModelSetupTitle(boxWidth, titleValue4.Trim())
	mark := lipgloss.NewStyle().
		Width(boxWidth).
		Align(lipgloss.Center).
		Render(renderMorphBanner(morphHeaderMark))
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		mark,
		"",
		titleText,
		body,
		hint,
	)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) renderProfileModelSetupFrameWithTitleContent(titleContent string, hint string, body string) string {
	width := max(m.transcript.Width(), m.getMainPaneWidth())
	height := max(m.transcript.Height(), 1)
	boxWidth := m.getProfileModelSetupBoxWidth()
	mark := lipgloss.NewStyle().
		Width(boxWidth + 2).
		Align(lipgloss.Center).
		Render(renderMorphBanner(morphHeaderMark))
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		mark,
		"",
		titleContent,
		body,
		hint,
	)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) renderProfileModelSetupProviderRows() []string {
	height := m.getProfileModelSetupListHeight()
	end := min(m.setupOffset+height, len(m.setupProviders))
	rows := make([]string, 0, max((end-m.setupOffset)*m.getProfileModelSetupRowHeight(), 1))
	for index := m.setupOffset; index < end; index++ {
		row := renderProfileModelSetupProviderRow(
			m.setupProviders[index],
			m.setupAuthMethod,
			m.getProfileModelSetupListWidth(),
			index == m.setupItemSelected,
		)
		rows = append(rows, strings.Split(row, "\n")...)
	}
	for len(rows) < height*m.getProfileModelSetupRowHeight() {
		rows = append(rows, "")
	}

	return rows
}

func renderProfileModelSetupProviderRow(provider rpcclient.ProviderOption, authMethod string, width int, selected bool) string {
	return renderProfileModelSetupOptionRow(
		getProviderOptionDisplayName(provider),
		getProfileModelSetupProviderDescription(provider, str.String(authMethod)),
		width,
		selected,
	)
}

func renderProfileModelSetupOptionRow(label string, description string, width int, selected bool) string {
	width = max(width, 1)
	contentWidth := max(width-2, 1)
	name := truncateCommandMenuText(label, contentWidth)
	description = truncateCommandMenuText(description, contentWidth)
	if width <= 1 {
		return truncateChatsCommandRow(name, width) + "\n" + truncateChatsCommandRow(description, width)
	}

	nameForeground := ""
	if selected {
		nameForeground = defaultTUITheme.MarkdownLinkForeground
	}
	nameLine := renderProfileModelSetupProviderLine(name, width, nameForeground, selected)
	descriptionLine := renderProfileModelSetupProviderLine(description, width, defaultTUITheme.MutedText, selected)

	return nameLine + "\n" + descriptionLine
}

func renderProfileModelSetupProviderLine(text string, width int, foreground string, selected bool) string {
	width = max(width, 1)
	contentWidth := max(width-2, 1)
	line := " " + truncateChatsCommandRow(text, contentWidth) + " "
	line += strings.Repeat(" ", max(width-lipgloss.Width(line), 0))
	style := lipgloss.NewStyle().
		Width(width)
	foregroundValue := str.String(foreground)
	if foregroundValue.Trim() != "" {
		style = style.Foreground(lipgloss.Color(foreground))
	}
	if selected {
		style = style.Background(lipgloss.Color(defaultTUITheme.JumpToBottomBackground))
	}

	return style.Render(line)
}

func getProfileModelSetupProviderDescription(provider rpcclient.ProviderOption, authMethod str.String) string {
	if isSetupProviderLocalOption(provider) {
		trimmedValueValue := str.String(provider.Type)
		detail := trimmedValueValue.Trim()
		if detail == "" {
			detail = "local"
		}

		return "Local provider · " + detail
	}

	trimmedAuthMethod := authMethod.Trim()
	iDValue17 := str.String(provider.ID)
	switch iDValue17.Normalized() {
	case constants.ModelProviderAnthropic:
		if trimmedAuthMethod == setupAuthMethodAPIKey {
			return "Use your Anthropic API key"
		}

		return "Use your Anthropic subscription"
	case constants.ModelProviderOpenAICodex:
		return "Use your OpenAI account"
	case constants.ModelProviderGitHubCopilot:
		return "Use your GitHub Copilot subscription"
	case constants.ModelProviderOpenAI:
		return "Use your OpenAI API key"
	case constants.ModelProviderOpenRouter:
		return "Use your OpenRouter API key"
	default:
		name := getProviderOptionDisplayName(provider)
		if trimmedAuthMethod == setupAuthMethodSubscription || provider.SupportsOAuth && !provider.SupportsAPIKey {
			return "Use your " + name + " account"
		}
		if trimmedAuthMethod == setupAuthMethodAPIKey || provider.SupportsAPIKey {
			return "Use your " + name + " API key"
		}
		if detail := getProviderOptionDetail(provider); detail != "" {
			return detail
		}

		return "Use " + name
	}
}

func (m model) renderProfileModelSetupModelRows() []string {
	height := m.getProfileModelSetupListHeight()
	models := m.filteredSetupModels()
	end := min(m.setupOffset+height, len(models))
	rows := make([]string, 0, max(end-m.setupOffset, 1))
	if len(models) == 0 {
		rows = append(rows, renderNoMatchingModelsRow(m.getProfileModelSetupListWidth()))
	} else {
		for index := m.setupOffset; index < end; index++ {
			row := renderProfileModelSetupModelRow(
				models[index],
				m.getProfileModelSetupListWidth(),
				index == m.setupItemSelected,
			)
			rows = append(rows, row)
		}
	}
	for len(rows) < height {
		rows = append(rows, "")
	}

	return rows
}

func renderProfileModelSetupModelRow(model rpcclient.ModelOption, width int, selected bool) string {
	width = max(width, 1)
	contentWidth := max(width-2, 1)
	name := getModelOptionDisplayName(model)
	detail := getSetupModelOptionMutedDetail(model)

	return renderCommandListEntryRow(name, detail, width, contentWidth, selected)
}

func getSetupModelOptionMutedDetail(model rpcclient.ModelOption) string {
	detail := getModelOptionMutedDetail(model)
	if model.LocalMissing {
		if detail == "" {
			return "not installed"
		}

		return "not installed · " + detail
	}
	if model.Source == modelcatalog.OptionSourceDiscovery {
		if detail == "" {
			return "installed"
		}

		return "installed · " + detail
	}

	return detail
}

func (m model) filteredSetupModels() []rpcclient.ModelOption {
	return filterModelOptions(m.setupModels, m.modelFilterInput.Value())
}

func (m model) renderProfileModelSetupBaseURL() string {
	boxWidth := m.getProfileModelSetupBoxWidth()
	input := m.baseURLInput
	input.Placeholder = constants.DefaultOllamaBaseURL
	input.SetWidth(max(boxWidth-4, 1))

	body := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(defaultTUITheme.InputFrameBorder)).
		Padding(0, 1).
		Width(boxWidth).
		Render(input.View())
	hint := m.renderProfileModelSetupHint("enter to continue · esc to go back", lipgloss.Width(body))

	return m.renderProfileModelSetupFrameWithHint(
		"Set base URL for "+getProviderDisplayName(m.setupModelProvider),
		hint,
		body,
	)
}

func (m model) renderProfileModelSetupAPIKey() string {
	boxWidth := m.getProfileModelSetupBoxWidth()
	input := m.apiKeyInput
	input.Placeholder = "Enter key"
	input.SetWidth(max(boxWidth-4, 1))

	body := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(defaultTUITheme.InputFrameBorder)).
		Padding(0, 1).
		Width(boxWidth).
		Render(input.View())
	hint := renderProfileModelSetupAPIKeyHint(m.setupDismissible, lipgloss.Width(body))

	return m.renderProfileModelSetupFrameWithHint(
		"Enter API key for "+getProviderDisplayName(m.setupModelProvider),
		hint,
		body,
	)
}

func renderProfileModelSetupSplitHint(left string, right string, width int) string {
	width = max(width, 1)
	leftValue := str.String(left)
	left = leftValue.Trim()
	rightValue := str.String(right)
	right = rightValue.Trim()
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	spacer := max(width-2-leftWidth-rightWidth, 1)
	row := " " + left + strings.Repeat(" ", spacer) + right + " "

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Width(width).
		Render(row)
}

func (m model) renderProfileModelSetupHint(hintText string, width int) string {
	width = max(width, 1)
	hintTextValue2 := str.String(hintText)
	hintText = hintTextValue2.Trim()
	if m.setupDismissible {
		return renderProfileModelSetupSplitHint(hintText, setupCloseHint, width)
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Width(width).
		Render(renderProfileModelSetupPaddedLabel(hintText, width))
}

func renderProfileModelSetupAPIKeyHint(dismissible bool, width int) string {
	if dismissible {
		return renderProfileModelSetupSplitHint("enter to save · esc to go back", setupCloseHint, width)
	}

	return renderProfileModelSetupSplitHint("enter to save", "esc to go back", width)
}

func renderProfileModelSetupPaddedLabel(label string, width int) string {
	width = max(width, 1)
	labelValue := str.String(label)
	label = labelValue.Trim()
	if width <= 2 {
		return truncateCommandMenuText(label, width)
	}

	return " " + truncateCommandMenuText(label, width-2) + " "
}

func (m model) renderProfileModelSetupNotice() string {
	boxWidth := m.getProfileModelSetupBoxWidth()
	setupNoticeHintValue := str.String(m.setupNoticeHint)
	hint := setupNoticeHintValue.Trim()
	if !strings.Contains(strings.ToLower(hint), "esc") {
		trimmedValue2 := str.String(hint + " · esc to go back")
		hint = trimmedValue2.Trim()
	}

	message := lipgloss.NewStyle().
		Width(max(boxWidth-2, 1)).
		Render(renderProfileModelSetupNoticeMessage(m.setupNoticeMessage, max(boxWidth-2, 1)))
	body := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(defaultTUITheme.InputFrameBorder)).
		Padding(0, 1).
		Width(boxWidth).
		Render(message)

	return m.renderProfileModelSetupFrame(
		m.getSetupNoticeTitle(),
		hint,
		body,
	)
}

func (m model) getSetupNoticeTitle() string {
	setupNoticeTitleValue := str.String(m.setupNoticeTitle)
	if title := setupNoticeTitleValue.Trim(); title != "" {
		return title
	}
	setupPendingModelIDValue5 := str.String(m.setupPendingModelID)
	return setupPendingModelIDValue5.Trim()
}

func renderProfileModelSetupNoticeMessage(message string, width int) string {
	width = max(width, 1)
	messageValue2 := str.String(message)
	message = messageValue2.Trim()
	if message == "" {
		return ""
	}

	lines := make([]string, 0)
	for _, paragraph := range strings.Split(message, "\n") {
		paragraphValue := str.String(paragraph)
		paragraph = paragraphValue.Trim()
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		for _, line := range strings.Split(wordwrap.String(paragraph, width), "\n") {
			lines = append(lines, renderProfileModelSetupNoticeMessageLine(line))
		}
	}

	return strings.Join(lines, "\n")
}

func renderProfileModelSetupNoticeMessageLine(line string) string {
	command := getProfileModelSetupNoticeProviderCommand(line)
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.MutedText))
	if command == "" {
		return mutedStyle.Render(line)
	}

	commandIndex := strings.Index(line, command)
	commandStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.MarkdownLinkForeground))

	return strings.Join([]string{
		mutedStyle.Render(line[:commandIndex]),
		commandStyle.Render(command),
		mutedStyle.Render(line[commandIndex+len(command):]),
	}, "")
}

func getProfileModelSetupNoticeProviderCommand(line string) string {
	fields := strings.Fields(line)
	for index := 0; index+3 < len(fields); index++ {
		if fields[index] == "morph" && fields[index+1] == "provider" && fields[index+2] == "login" {
			return strings.Join(fields[index:index+4], " ")
		}
	}

	return ""
}

func (m model) getProfileModelSetupListHeight() int {
	count := 0
	switch m.setupModelStep {
	case setupModelStepAuthMethod:
		count = len(setupAuthMethodOptions)
	case setupModelStepProvider:
		count = len(m.setupProviders)
	case setupModelStepModel:
		count = len(m.setupModels)
	}

	return min(max(count, 1), setupModelMaxListHeight)
}

func (m model) getProfileModelSetupRenderedListHeight() int {
	return m.getProfileModelSetupListHeight() * m.getProfileModelSetupRowHeight()
}

func (m model) getProfileModelSetupPreListDetailHeight() int {
	if m.setupModelStep == setupModelStepModel && isLocalSetupProvider(m.setupModelProvider) {
		return 1
	}

	return 0
}

func (m model) getProfileModelSetupRowHeight() int {
	switch m.setupModelStep {
	case setupModelStepAuthMethod, setupModelStepProvider:
		return 2
	default:
		return 1
	}
}

func (m model) getProfileModelSetupListWidth() int {
	return max(m.getProfileModelSetupBoxWidth()-2, 1)
}

func (m model) getProfileModelSetupBoxWidth() int {
	width := max(m.transcript.Width(), m.getMainPaneWidth())
	minWidth := setupModelMinWidth
	if m.setupDismissible || m.getProfileModelSetupLoginMethodLabel() != "" {
		minWidth = max(minWidth, setupModelLoginMinWidth)
	}

	return min(max(width/2, minWidth), min(setupModelMaxWidth, width))
}

func (m model) getProfileModelSetupListFirstRow() int {
	height := max(m.transcript.Height(), 1)
	listHeight := m.getProfileModelSetupRenderedListHeight()
	detailHeight := m.getProfileModelSetupPreListDetailHeight()
	markHeight := lipgloss.Height(renderMorphBanner(morphHeaderMark))
	contentHeight := markHeight + listHeight + detailHeight + 6
	top := max((height-contentHeight)/2, 0)

	return m.getTranscriptTop() + top + markHeight + detailHeight + 4
}

func (m *model) clearProfileModelSetup() {
	m.cancelSetupOAuthLogin()
	m.cancelSetupModelPull()
	m.setupModelStep = ""
	m.setupAuthMethod = ""
	m.setupProviders = nil
	m.setupModels = nil
	m.setupModelProvider = ""
	m.setupModelBaseURL = ""
	m.setupProviderAPIKey = ""
	m.setupPendingModelID = ""
	m.setupNoticeTitle = ""
	m.setupNoticeMessage = ""
	m.setupNoticeHint = ""
	m.setupNoticeAction = ""
	m.setupItemSelected = 0
	m.setupOffset = 0
	m.modelFilterInput = newModelFilterInput()
	m.setupDismissible = false
	m.setupOAuthPending = false
	m.setupOAuthProvider = ""
	m.setupOAuthCancel = nil
}

func (m model) closeProfileSetup() (tea.Model, tea.Cmd) {
	m.namePromptEnabled = false
	m.setupNamePromptActive = false
	m.setupDismissible = false
	m.namePromptError = ""
	m.namePromptErrorStartedAt = time.Time{}
	m.nameInput.SetValue("")
	m.clearProfileModelSetup()
	m.resize()
	m.setTranscriptContent()

	return m, m.setStatus("setup closed")
}

func isSetupCloseKey(msg tea.KeyPressMsg) bool {
	return msg.Keystroke() == "ctrl+x"
}

func getSetupModelProvider(provider string, option rpcclient.ModelOption) string {
	providerValue9 := str.String(provider)
	if provider = providerValue9.Trim(); provider != "" {
		return provider
	}
	providerValue10 := str.String(option.Provider)
	return providerValue10.Trim()
}

func isMissingModelCredentialError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "API key is required")
}

func isEmbeddingSetupError(err error) bool {
	if err == nil {
		return false
	}

	message := err.Error()
	return strings.Contains(message, "embedding model") ||
		strings.Contains(message, "embedding API key")
}

func getEmbeddingSetupInstruction() string {
	return "embedding setup required; configure models.embedding or run morph config set search.vector.enabled false"
}

func (m model) profileModelSetupMissing() bool {
	raw := m.loadRawProfileConfig()
	providerValue11 := str.String(raw.Models.Main.Provider)
	nameValue6 := str.String(raw.Models.Main.Name)
	return providerValue11.Trim() == "" || nameValue6.
		Trim() == ""
}

func (m model) shouldShowProfileModelSetup() bool {
	setupModelStepValue := str.String(m.setupModelStep)
	return setupModelStepValue.Trim() != ""
}

func (m model) loadRawProfileMainProvider() string {
	providerValue12 := str.String(m.loadRawProfileConfig().Models.Main.Provider)
	return providerValue12.Trim()
}

func (m model) loadRawProfileMainModel() string {
	nameValue7 := str.String(m.loadRawProfileConfig().Models.Main.Name)
	return nameValue7.Trim()
}

func (m model) loadRawProviderAPIKey(provider string) string {
	providerValue13 := str.String(provider)
	provider = providerValue13.Trim()
	if provider == "" {
		return ""
	}

	raw := m.loadRawProfileConfig()
	if raw.Models.Providers == nil {
		return ""
	}
	aPIKeyValue := str.String(raw.Models.Providers[provider].APIKey)
	return aPIKeyValue.Trim()
}

func (m model) loadRawProfileConfig() *config.Config {
	cfg := config.NewProfileConfig()
	configPathValue2 := str.String(m.configPath)
	if configPathValue2.Trim() == "" {
		return cfg
	}

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return cfg
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return config.NewProfileConfig()
	}

	return cfg
}
