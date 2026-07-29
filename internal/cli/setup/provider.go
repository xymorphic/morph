package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	clibase "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	appcredential "github.com/wandxy/morph/internal/credential"
	modelcatalog "github.com/wandxy/morph/internal/model"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	_ "github.com/wandxy/morph/internal/model/provider_anthropic"
	_ "github.com/wandxy/morph/internal/model/provider_copilot"
	provider_ollama "github.com/wandxy/morph/internal/model/provider_ollama"
	_ "github.com/wandxy/morph/internal/model/provider_openai"
	tuirender "github.com/wandxy/morph/internal/tui/render"
	"github.com/wandxy/morph/pkg/str"
)

var (
	discoverOllamaModels = func(ctx context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
		discoverer, err := provider_ollama.NewDiscoverer(baseURL)
		if err != nil {
			return nil, err
		}

		return discoverer.DiscoverModels(ctx)
	}
	pullOllamaModel         = provider_ollama.EnsureModel
	setConfigValuesRelaxed  = config.SetConfigValuesRelaxed
	getSubscriptionProvider = appcredential.GetSubscriptionProvider
	newCredentialStore      = func() setupCredentialStore {
		return appcredential.NewFileStore("")
	}
	runSetupSelectorProgram = func(
		ctx context.Context,
		input io.Reader,
		output io.Writer,
		model selectorModel,
	) (tea.Model, error) {
		return tea.NewProgram(
			model,
			tea.WithContext(ctx),
			tea.WithInput(input),
			tea.WithOutput(output),
			tea.WithoutSignalHandler(),
		).Run()
	}
	runSetupWizardProgram = func(
		ctx context.Context,
		input io.Reader,
		output io.Writer,
		model setupWizardModel,
	) (tea.Model, error) {
		return tea.NewProgram(
			model,
			tea.WithContext(ctx),
			tea.WithInput(input),
			tea.WithOutput(output),
			tea.WithoutSignalHandler(),
		).Run()
	}
)

type setupCredentialStore interface {
	Set(string, appcredential.StoredCredential) error
}

type ProviderOptions struct {
	Input      io.Reader
	Output     io.Writer
	EnvPath    string
	ConfigPath string
	Provider   string
	Model      string
	BaseURL    string
	API        string
	APIKey     string
	Pull       bool
	PullQuiet  bool
	Refresh    bool
	Registry   *modelprovider.Registry
}

type ProviderResult struct {
	Provider   string
	Model      string
	ConfigPath string
}

type providerRunner struct {
	input    io.Reader
	output   io.Writer
	registry *modelprovider.Registry
	selector func(context.Context, string, []selectChoice) (string, error)
}

type setupSelection struct {
	provider          string
	api               string
	baseURL           string
	model             string
	apiKey            string
	authMethod        string
	localModelMissing bool
	pullAnswered      bool
	pullSelected      bool
}

type modelSelection struct {
	id           string
	localMissing bool
}

type selectChoice struct {
	ID          string
	Label       string
	Description string
}

const setupOptionIndent = " "

type selectorModel struct {
	title    string
	choices  []selectChoice
	selected int
	choice   string
	err      error
}

type setupWizardStep string

const (
	setupWizardStepProvider setupWizardStep = "provider"
	setupWizardStepModel    setupWizardStep = "model"
	setupWizardStepAuth     setupWizardStep = "auth"
	setupWizardStepAPIKey   setupWizardStep = "api-key"
	setupWizardStepPull     setupWizardStep = "pull"
)

type setupWizardModel struct {
	ctx         context.Context
	runner      providerRunner
	opts        ProviderOptions
	cfg         *config.Config
	step        setupWizardStep
	choices     []selectChoice
	selected    int
	apiKeyInput textinput.Model
	selection   setupSelection
	err         error
	done        bool
}

func RunProvider(ctx context.Context, opts ProviderOptions) (ProviderResult, error) {
	opts = normalizeProviderOptions(opts)
	cfg, err := config.Load(opts.EnvPath, opts.ConfigPath)
	if err != nil {
		return ProviderResult{}, err
	}

	runner := providerRunner{input: opts.Input, output: opts.Output, registry: opts.Registry}
	selection, err := runner.getSetupSelection(ctx, opts, cfg)
	if err != nil {
		return ProviderResult{}, err
	}

	selection, err = runner.ensureSetupAuth(ctx, cfg, selection)
	if err != nil {
		return ProviderResult{}, err
	}

	if err := runner.pullMissingLocalModel(ctx, opts, selection); err != nil {
		return ProviderResult{}, err
	}

	if err := persistProviderSelection(opts, selection); err != nil {
		return ProviderResult{}, err
	}

	return ProviderResult{
		Provider:   selection.provider,
		Model:      selection.model,
		ConfigPath: opts.ConfigPath,
	}, nil
}

func normalizeProviderOptions(opts ProviderOptions) ProviderOptions {
	if opts.Input == nil {
		opts.Input = strings.NewReader("")
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Registry == nil {
		opts.Registry = modelprovider.DefaultRegistry()
	}
	providerValue := str.String(opts.Provider)
	opts.Provider = providerValue.Normalized()
	modelValue := str.String(opts.Model)
	opts.Model = modelValue.Trim()
	baseURLValue := str.String(opts.BaseURL)
	opts.BaseURL = baseURLValue.Trim()
	aPIValue := str.String(opts.API)
	opts.API = aPIValue.Trim()
	aPIKeyValue := str.String(opts.APIKey)
	opts.APIKey = aPIKeyValue.Trim()

	return opts
}

func (r providerRunner) getSetupSelection(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
) (setupSelection, error) {
	if r.selector == nil {
		needsPagedSetup, err := r.shouldRunPagedSetup(ctx, opts, cfg)
		if err != nil {
			return setupSelection{}, err
		}
		if needsPagedSetup {
			return r.runPagedSetup(ctx, opts, cfg)
		}
	}

	provider, err := r.getSetupProvider(ctx, opts, cfg)
	if err != nil {
		return setupSelection{}, err
	}

	providerDef, ok := r.registry.GetProvider(provider)
	if !ok || !providerDef.SupportsModels {
		return setupSelection{}, fmt.Errorf("model provider must be one of: %s", r.getProviderList())
	}

	api := opts.API
	if api == "" {
		defaultAPIValue := str.String(providerDef.DefaultAPI)
		api = defaultAPIValue.Trim()
	}
	if err := config.ValidateModelGenerationAPIForProvider("model API", provider, api); err != nil {
		return setupSelection{}, err
	}

	baseURL := r.getSetupBaseURL(opts, cfg, providerDef, api)
	model, err := r.getSetupModel(ctx, opts, cfg, providerDef, baseURL)
	if err != nil {
		return setupSelection{}, err
	}

	return setupSelection{
		provider:          provider,
		api:               api,
		baseURL:           baseURL,
		model:             model.id,
		apiKey:            opts.APIKey,
		localModelMissing: model.localMissing,
	}, nil
}

func (r providerRunner) shouldRunPagedSetup(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
) (bool, error) {
	if opts.Provider == "" || opts.Model == "" {
		return true, nil
	}

	providerDef, ok := r.registry.GetProvider(opts.Provider)
	if !ok || !providerDef.SupportsModels {
		return false, fmt.Errorf("model provider must be one of: %s", r.getProviderList())
	}

	api := opts.API
	if api == "" {
		defaultAPIValue2 := str.String(providerDef.DefaultAPI)
		api = defaultAPIValue2.Trim()
	}
	if err := config.ValidateModelGenerationAPIForProvider("model API", opts.Provider, api); err != nil {
		return false, err
	}

	baseURL := r.getSetupBaseURL(opts, cfg, providerDef, api)
	missing, err := r.checkLocalModelMissing(ctx, opts, cfg, providerDef, baseURL, opts.Model)
	if err != nil {
		return false, err
	}

	selection := setupSelection{
		provider:          opts.Provider,
		api:               api,
		baseURL:           baseURL,
		model:             opts.Model,
		apiKey:            opts.APIKey,
		localModelMissing: missing,
	}
	if err := checkSetupAuth(cfg, selection); err != nil {
		if isMissingModelCredentialError(err) {
			return true, nil
		}

		return false, err
	}
	if selection.provider == constants.ModelProviderOllama && selection.localModelMissing && !opts.Pull {
		return true, nil
	}

	return false, nil
}

func (r providerRunner) getSetupProvider(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
) (string, error) {
	if opts.Provider != "" {
		return opts.Provider, nil
	}

	options := modelcatalog.ListProviders(modelcatalog.ProviderQuery{
		Current:  cfg.Models.Main.Provider,
		Registry: r.registry,
	})
	if len(options) == 0 {
		return "", errors.New("no model providers are available")
	}

	choices := make([]selectChoice, 0, len(options))
	for _, option := range options {
		iDValue := str.String(option.ID)
		nameValue := str.String(option.Name)
		iDValue2 := str.String(option.ID)
		choices = append(choices, selectChoice{
			ID:          iDValue.Trim(),
			Label:       nameValue.Trim(),
			Description: iDValue2.Trim(),
		})
	}

	return r.selectChoice(ctx, "Select a provider", choices)
}

func (r providerRunner) getSetupBaseURL(
	opts ProviderOptions,
	cfg *config.Config,
	provider modelprovider.ProviderDefinition,
	api string,
) string {
	if opts.BaseURL != "" {
		return opts.BaseURL
	}

	if cfg != nil && strings.EqualFold(cfg.Models.Main.Provider, provider.ID) {
		baseURLValue2 := str.String(cfg.Models.Main.BaseURL)
		if value := baseURLValue2.Trim(); value != "" {
			return normalizeInheritedSetupBaseURL(provider.ID, api, value)
		}
	}
	apiValue := str.String(api)
	baseURLsValue := str.String(provider.BaseURLs[apiValue.Normalized()])
	return baseURLsValue.Trim()
}

func normalizeInheritedSetupBaseURL(provider string, api string, value string) string {
	valueText := strings.TrimRight(str.String(value).Trim(), "/")
	providerValue2 := str.String(provider)
	apiValue2 := str.String(api)
	if providerValue2.Normalized() != constants.ModelProviderOllama || apiValue2.
		Normalized() != modelprovider.APIOllamaNative {
		return valueText
	}

	if strings.HasSuffix(strings.ToLower(valueText), "/v1") {
		return strings.TrimRight(valueText[:len(valueText)-len("/v1")], "/")
	}

	return valueText
}

func (r providerRunner) getSetupModel(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
	provider modelprovider.ProviderDefinition,
	baseURL string,
) (modelSelection, error) {
	if opts.Model != "" {
		missing, err := r.checkLocalModelMissing(ctx, opts, cfg, provider, baseURL, opts.Model)
		if err != nil {
			return modelSelection{}, err
		}

		return modelSelection{id: opts.Model, localMissing: missing}, nil
	}

	options, _, err := r.getModelOptions(ctx, opts, cfg, provider, cfg.Models.Main.Name, baseURL)
	if err != nil {
		return modelSelection{}, err
	}
	if len(options) == 0 {
		return modelSelection{}, errors.New("models unavailable")
	}

	choices := make([]selectChoice, 0, len(options))
	for _, option := range options {
		nameValue2 := str.String(option.Name)
		name := nameValue2.Trim()
		if name == "" || strings.EqualFold(name, option.ID) {
			name = option.ID
		}
		iDValue3 := str.String(option.ID)
		choices = append(choices, selectChoice{
			ID:          iDValue3.Trim(),
			Label:       name,
			Description: getSetupModelDescription(option),
		})
	}

	modelID, err := r.selectChoice(ctx, "Select a model", choices)
	if err != nil {
		return modelSelection{}, err
	}

	missing, err := r.checkLocalModelMissing(ctx, opts, cfg, provider, baseURL, modelID)
	if err != nil {
		return modelSelection{}, err
	}

	return modelSelection{id: modelID, localMissing: missing}, nil
}

func (r providerRunner) runPagedSetup(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
) (setupSelection, error) {
	model, err := newSetupWizardModel(ctx, r, opts, cfg)
	if err != nil {
		return setupSelection{}, err
	}

	finalModel, err := runSetupWizardProgram(ctx, r.input, r.output, model)
	if err != nil {
		return setupSelection{}, err
	}

	selected, ok := finalModel.(setupWizardModel)
	if !ok {
		return setupSelection{}, errors.New("setup selection unavailable")
	}
	if selected.err != nil {
		return setupSelection{}, selected.err
	}
	if !selected.done {
		return setupSelection{}, errors.New("setup selection cancelled")
	}

	return selected.selection, nil
}

func newSetupWizardModel(
	ctx context.Context,
	runner providerRunner,
	opts ProviderOptions,
	cfg *config.Config,
) (setupWizardModel, error) {
	model := setupWizardModel{
		ctx:    ctx,
		runner: runner,
		opts:   opts,
		cfg:    cfg,
	}
	if opts.Provider != "" {
		if err := model.setProvider(opts.Provider); err != nil {
			return setupWizardModel{}, err
		}

		return model, nil
	}

	model.showProviderPage()
	return model, nil
}

func (m *setupWizardModel) showProviderPage() {
	options := modelcatalog.ListProviders(modelcatalog.ProviderQuery{
		Current:  m.cfg.Models.Main.Provider,
		Registry: m.runner.registry,
	})
	m.choices = make([]selectChoice, 0, len(options))
	for _, option := range options {
		iDValue4 := str.String(option.ID)
		nameValue3 := str.String(option.Name)
		iDValue5 := str.String(option.ID)
		m.choices = append(m.choices, selectChoice{
			ID:          iDValue4.Trim(),
			Label:       nameValue3.Trim(),
			Description: iDValue5.Trim(),
		})
	}
	m.step = setupWizardStepProvider
	m.selected = 0
}

func (m *setupWizardModel) setProvider(provider string) error {
	providerDef, ok := m.runner.registry.GetProvider(provider)
	if !ok || !providerDef.SupportsModels {
		return fmt.Errorf("model provider must be one of: %s", m.runner.getProviderList())
	}

	api := m.opts.API
	if api == "" {
		defaultAPIValue3 := str.String(providerDef.DefaultAPI)
		api = defaultAPIValue3.Trim()
	}
	if err := config.ValidateModelGenerationAPIForProvider("model API", provider, api); err != nil {
		return err
	}
	iDValue6 := str.String(providerDef.ID)
	m.selection = setupSelection{
		provider: iDValue6.Trim(),
		api:      api,
		baseURL:  m.runner.getSetupBaseURL(m.opts, m.cfg, providerDef, api),
		apiKey:   m.opts.APIKey,
	}
	if m.opts.Model != "" {
		return m.setModel(m.opts.Model)
	}

	return m.showModelPage(providerDef)
}

func (m *setupWizardModel) showModelPage(provider modelprovider.ProviderDefinition) error {
	options, _, err := m.runner.getModelOptions(
		m.ctx,
		m.opts,
		m.cfg,
		provider,
		m.cfg.Models.Main.Name,
		m.selection.baseURL,
	)
	if err != nil {
		return err
	}
	if len(options) == 0 {
		return errors.New("models unavailable")
	}

	m.choices = make([]selectChoice, 0, len(options))
	for _, option := range options {
		nameValue4 := str.String(option.Name)
		name := nameValue4.Trim()
		if name == "" || strings.EqualFold(name, option.ID) {
			name = option.ID
		}
		iDValue7 := str.String(option.ID)
		m.choices = append(m.choices, selectChoice{
			ID:          iDValue7.Trim(),
			Label:       name,
			Description: getSetupModelDescription(option),
		})
	}
	m.step = setupWizardStepModel
	m.selected = 0
	return nil
}

func (m *setupWizardModel) setModel(modelID string) error {
	provider, ok := m.runner.registry.GetProvider(m.selection.provider)
	if !ok {
		return fmt.Errorf("model provider must be one of: %s", m.runner.getProviderList())
	}

	missing, err := m.runner.checkLocalModelMissing(
		m.ctx,
		m.opts,
		m.cfg,
		provider,
		m.selection.baseURL,
		modelID,
	)
	if err != nil {
		return err
	}
	modelIDValue := str.String(modelID)
	m.selection.model = modelIDValue.Trim()
	m.selection.localModelMissing = missing
	return m.advanceAfterModel()
}

func (m *setupWizardModel) advanceAfterModel() error {
	if err := checkSetupAuth(m.cfg, m.selection); err == nil {
		return m.showPullPageOrFinish()
	} else if !isMissingModelCredentialError(err) {
		return err
	}

	provider, ok := m.runner.registry.GetProvider(m.selection.provider)
	if !ok {
		return fmt.Errorf("model provider must be one of: %s", m.runner.getProviderList())
	}
	choices := m.runner.getSetupAuthChoices(provider, m.selection)
	if len(choices) == 0 {
		return fmt.Errorf(
			"model API key is required for provider %q; run morph provider login or set a provider API key",
			m.selection.provider,
		)
	}

	m.step = setupWizardStepAuth
	m.choices = choices
	m.selected = 0
	return nil
}

func (m *setupWizardModel) showPullPageOrFinish() error {
	if m.selection.provider == constants.ModelProviderOllama &&
		m.selection.localModelMissing &&
		!m.opts.Pull {
		m.step = setupWizardStepPull
		m.choices = []selectChoice{
			{ID: "no", Label: "No"},
			{ID: "yes", Label: "Yes"},
		}
		m.selected = 0
		return nil
	}

	m.done = true
	return nil
}

func (m setupWizardModel) Init() tea.Cmd {
	return nil
}

func (m setupWizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.step == setupWizardStepAPIKey {
			return m.updateAPIKey(msg)
		}
		return m.updateKey(msg)
	default:
		return m, nil
	}
}

func (m setupWizardModel) updateAPIKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isSetupCancelKey(msg) {
		m.err = errors.New("setup selection cancelled")
		return m, tea.Quit
	}

	switch msg.Key().Code {
	case tea.KeyEnter:
		value2 := str.String(m.apiKeyInput.Value())
		apiKey := value2.Trim()
		if apiKey == "" {
			m.err = errors.New("api key is required")
			return m, tea.Quit
		}
		m.selection.apiKey = apiKey
		m.done = true
		return m, tea.Quit
	case tea.KeyEsc:
		return m.goBack()
	default:
		input, cmd := m.apiKeyInput.Update(msg)
		m.apiKeyInput = input
		return m, cmd
	}
}

func (m setupWizardModel) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isSetupCancelKey(msg) {
		m.err = errors.New("setup selection cancelled")
		return m, tea.Quit
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		if len(m.choices) > 0 {
			m.selected = (m.selected - 1 + len(m.choices)) % len(m.choices)
		}
	case tea.KeyDown:
		if len(m.choices) > 0 {
			m.selected = (m.selected + 1) % len(m.choices)
		}
	case tea.KeyHome:
		m.selected = 0
	case tea.KeyEnd:
		m.selected = max(0, len(m.choices)-1)
	case tea.KeyLeft, tea.KeyBackspace:
		return m.goBack()
	case tea.KeyEsc:
		m.err = errors.New("setup selection cancelled")
		return m, tea.Quit
	case tea.KeyEnter:
		return m.chooseSelected()
	default:
		if index, ok := numericSelectionIndex(msg, len(m.choices)); ok {
			m.selected = index
			return m.chooseSelected()
		}
	}

	return m, nil
}

func (m setupWizardModel) goBack() (tea.Model, tea.Cmd) {
	switch m.step {
	case setupWizardStepProvider:
		return m, nil
	case setupWizardStepModel:
		if m.hasLockedProvider() {
			return m, nil
		}
		m.selection = setupSelection{apiKey: m.opts.APIKey}
		m.showProviderPage()
	case setupWizardStepAuth, setupWizardStepPull:
		if m.hasLockedModel() {
			return m.goBackFromLockedModel()
		}

		provider, ok := m.runner.registry.GetProvider(m.selection.provider)
		if !ok {
			m.err = fmt.Errorf("model provider must be one of: %s", m.runner.getProviderList())
			return m, tea.Quit
		}
		m.selection.model = ""
		m.selection.authMethod = ""
		m.selection.pullAnswered = false
		m.selection.pullSelected = false
		if err := m.showModelPage(provider); err != nil {
			m.err = err
			return m, tea.Quit
		}
	case setupWizardStepAPIKey:
		provider, ok := m.runner.registry.GetProvider(m.selection.provider)
		if !ok {
			m.err = fmt.Errorf("model provider must be one of: %s", m.runner.getProviderList())
			return m, tea.Quit
		}
		m.selection.apiKey = ""
		m.step = setupWizardStepAuth
		m.choices = m.runner.getSetupAuthChoices(provider, m.selection)
		m.selected = 0
	default:
		m.err = errors.New("setup selection cancelled")
		return m, tea.Quit
	}

	return m, nil
}

func (m setupWizardModel) goBackFromLockedModel() (tea.Model, tea.Cmd) {
	if m.hasLockedProvider() {
		return m, nil
	}

	m.selection = setupSelection{apiKey: m.opts.APIKey}
	m.showProviderPage()
	return m, nil
}

func (m setupWizardModel) hasLockedProvider() bool {
	providerValue3 := str.String(m.opts.Provider)
	return providerValue3.Trim() != ""
}

func (m setupWizardModel) hasLockedModel() bool {
	modelValue2 := str.String(m.opts.Model)
	return modelValue2.Trim() != ""
}

func (m setupWizardModel) chooseSelected() (tea.Model, tea.Cmd) {
	if len(m.choices) == 0 {
		m.err = errors.New("no setup options are available")
		return m, tea.Quit
	}

	if m.selected < 0 || m.selected >= len(m.choices) {
		m.selected = 0
	}
	iDValue8 := str.String(m.choices[m.selected].ID)
	choice := iDValue8.Trim()
	var err error
	switch m.step {
	case setupWizardStepProvider:
		err = m.setProvider(choice)
	case setupWizardStepModel:
		err = m.setModel(choice)
	case setupWizardStepAuth:
		m.selection.authMethod = choice
		if choice == "api-key" {
			m.showAPIKeyPage()
		} else {
			m.done = true
		}
	case setupWizardStepPull:
		m.selection.pullAnswered = true
		m.selection.pullSelected = choice == "yes"
		m.done = true
	default:
		err = errors.New("setup selection unavailable")
	}
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	if m.done {
		return m, tea.Quit
	}

	return m, nil
}

func (m *setupWizardModel) showAPIKeyPage() {
	provider, _ := m.runner.registry.GetProvider(m.selection.provider)
	input := textinput.New()
	input.Placeholder = "API key for " + getProviderDisplayName(provider)
	input.CharLimit = 4096
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '*'
	styles := input.Styles()
	styles.Focused.Prompt = renderSetupAccentStyle()
	styles.Blurred.Prompt = renderSetupAccentStyle()
	input.SetStyles(styles)
	input.Focus()
	m.apiKeyInput = input
	m.step = setupWizardStepAPIKey
	m.choices = nil
	m.selected = 0
}

func (m setupWizardModel) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

func (m setupWizardModel) render() string {
	title := "Setup"
	description := "Choose setup options for this profile."
	switch m.step {
	case setupWizardStepProvider:
		title = "Select a provider"
		description = "Choose the model provider Morph should use by default."
	case setupWizardStepModel:
		provider, _ := m.runner.registry.GetProvider(m.selection.provider)
		title = getSetupModelPageTitle(provider)
		description = getSetupModelPageDescription(provider, m.selection.baseURL)
	case setupWizardStepAuth:
		provider, _ := m.runner.registry.GetProvider(m.selection.provider)
		title = "Authenticate " + getProviderDisplayName(provider)
		description = "Choose how Morph should authenticate with this provider."
	case setupWizardStepAPIKey:
		provider, _ := m.runner.registry.GetProvider(m.selection.provider)
		title = "API key for " + getProviderDisplayName(provider)
		description = "Enter the API key for this provider."
	case setupWizardStepPull:
		title = "Pull " + m.selection.model + " if missing?"
		description = "Install the selected local model before saving setup."
	}
	if m.step == setupWizardStepAPIKey {
		return renderSetupInputPage(title, description, m.apiKeyInput.View())
	}

	return renderSetupChoices(title, description, m.choices, m.selected, true)
}

func (r providerRunner) getModelOptions(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
	provider modelprovider.ProviderDefinition,
	current string,
	baseURL string,
) ([]modelcatalog.Option, bool, error) {
	return ListModelOptions(ctx, ModelOptions{
		Provider: provider.ID,
		Current:  current,
		Config:   cfg,
		BaseURL:  baseURL,
		Refresh:  opts.Refresh,
		Registry: r.registry,
	})
}

func getSetupModelDescription(option modelcatalog.Option) string {
	iDValue9 := str.String(option.ID)
	description := iDValue9.Trim()
	if option.LocalMissing {
		if description == "" {
			return "not installed"
		}

		return description + " - not installed"
	}

	return description
}

func hasInstalledLocalOptions(options []modelcatalog.Option) bool {
	for _, option := range options {
		if option.Source == modelcatalog.OptionSourceDiscovery && !option.LocalMissing {
			return true
		}
	}

	return false
}

func getSetupModelPageTitle(provider modelprovider.ProviderDefinition) string {
	if provider.ID == constants.ModelProviderOllama {
		return "Select an Ollama model"
	}

	return "Select a model"
}

func getSetupModelPageDescription(provider modelprovider.ProviderDefinition, baseURL string) string {
	if provider.ID == constants.ModelProviderOllama {
		baseURLValue3 := str.String(baseURL)
		baseURL = baseURLValue3.Trim()
		if baseURL == "" {
			baseURL = "the configured Ollama base URL"
		}

		return "Choose an installed or suggested Ollama model from " + baseURL + "."
	}

	return "Choose the model Morph should use for chat, one-shot commands, and summaries."
}

func (r providerRunner) checkLocalModelMissing(
	ctx context.Context,
	opts ProviderOptions,
	cfg *config.Config,
	provider modelprovider.ProviderDefinition,
	baseURL string,
	modelID string,
) (bool, error) {
	if provider.ID != constants.ModelProviderOllama {
		return false, nil
	}
	if hasExplicitProviderModels(cfg, provider.ID) {
		return false, nil
	}
	if opts.Pull {
		return true, nil
	}

	models, err := discoverOllamaModels(ctx, baseURL)
	if err != nil {
		return false, err
	}
	for _, model := range models {
		iDValue10 := str.String(model.ID)
		modelIDValue2 := str.String(modelID)
		if strings.EqualFold(iDValue10.Trim(), modelIDValue2.Trim()) {
			return false, nil
		}
	}

	return true, nil
}

func hasExplicitProviderModels(cfg *config.Config, provider string) bool {
	if cfg == nil || len(cfg.Models.Providers) == 0 {
		return false
	}

	providerValue4 := str.String(provider)
	provider = providerValue4.Normalized()
	if providerConfig, ok := cfg.Models.Providers[provider]; ok {
		return len(providerConfig.Models) > 0
	}

	for key, providerConfig := range cfg.Models.Providers {
		keyValue := str.String(key)
		if strings.EqualFold(keyValue.Trim(), provider) {
			return len(providerConfig.Models) > 0
		}
	}

	return false
}

func (r providerRunner) pullMissingLocalModel(
	ctx context.Context,
	opts ProviderOptions,
	selection setupSelection,
) error {
	if selection.provider != constants.ModelProviderOllama {
		return nil
	}

	pull := opts.Pull
	if selection.pullAnswered {
		pull = selection.pullSelected
	}
	if !pull && !selection.localModelMissing {
		return nil
	}
	if !pull {
		return nil
	}

	progressPrinter := clibase.NewPullProgressPrinter(r.output, !opts.PullQuiet)
	var onProgress func(provider_ollama.PullProgress)
	if progressPrinter != nil {
		onProgress = progressPrinter.Progress
	}

	err := pullOllamaModel(ctx, selection.baseURL, selection.model, nil, onProgress)
	if progressPrinter != nil {
		progressPrinter.Finish()
	}

	return err
}

func (r providerRunner) ensureSetupAuth(
	ctx context.Context,
	cfg *config.Config,
	selection setupSelection,
) (setupSelection, error) {
	if err := checkSetupAuth(cfg, selection); err == nil {
		return selection, nil
	} else if !isMissingModelCredentialError(err) {
		return setupSelection{}, err
	}

	provider, ok := r.registry.GetProvider(selection.provider)
	if !ok {
		return setupSelection{}, fmt.Errorf("model provider must be one of: %s", r.getProviderList())
	}
	method, err := r.getSetupAuthMethod(ctx, provider, selection)
	if err != nil {
		return setupSelection{}, err
	}

	switch method {
	case "api-key":
		if selection.apiKey == "" {
			return setupSelection{}, errors.New("setup API key is unavailable")
		}
	case "oauth":
		if err := r.loginSetupProvider(ctx, provider); err != nil {
			return setupSelection{}, err
		}
	default:
		return setupSelection{}, errors.New("authentication method unavailable")
	}
	if err := checkSetupAuth(cfg, selection); err != nil {
		return setupSelection{}, err
	}

	return selection, nil
}

func (r providerRunner) getSetupAuthMethod(
	ctx context.Context,
	provider modelprovider.ProviderDefinition,
	selection setupSelection,
) (string, error) {
	if selection.authMethod != "" {
		return selection.authMethod, nil
	}

	choices := r.getSetupAuthChoices(provider, selection)
	if len(choices) == 0 {
		return "", fmt.Errorf(
			"model API key is required for provider %q; run morph provider login or set a provider API key",
			selection.provider,
		)
	}
	if len(choices) == 1 {
		return choices[0].ID, nil
	}

	return r.selectChoice(ctx, "Authenticate "+getProviderDisplayName(provider), choices)
}

func (r providerRunner) getSetupAuthChoices(
	provider modelprovider.ProviderDefinition,
	selection setupSelection,
) []selectChoice {
	choices := make([]selectChoice, 0, 2)
	if provider.SupportsOAuth && r.modelSupportsOAuth(selection) {
		if _, ok := getSubscriptionProvider(provider.ID); ok {
			choices = append(choices, selectChoice{
				ID:          "oauth",
				Label:       "Use " + getProviderDisplayName(provider) + " account",
				Description: "subscription login",
			})
		}
	}
	if provider.SupportsAPIKey {
		choices = append(choices, selectChoice{
			ID:          "api-key",
			Label:       "Enter API key",
			Description: "stored in profile config",
		})
	}
	return choices
}

func (r providerRunner) modelSupportsOAuth(selection setupSelection) bool {
	model, ok := r.registry.GetModelForAPI(selection.provider, selection.api, selection.model)
	if strings.TrimSpace(selection.api) == "" {
		model, ok = r.registry.GetModel(selection.provider, selection.model)
	}
	return ok && model.SupportsOAuth
}

func (r providerRunner) loginSetupProvider(
	ctx context.Context,
	provider modelprovider.ProviderDefinition,
) error {
	subscriptionProvider, ok := getSubscriptionProvider(provider.ID)
	if !ok {
		return fmt.Errorf("subscription login is not available for %s", getProviderDisplayName(provider))
	}
	if _, err := fmt.Fprintf(r.output, "Authenticating %s...\n", getProviderDisplayName(provider)); err != nil {
		return err
	}

	credential, err := subscriptionProvider.Login(ctx, appcredential.LoginOptions{
		Provider: provider.ID,
		Input:    r.input,
		Output:   r.output,
	})
	if err != nil {
		return err
	}

	return newCredentialStore().Set(provider.ID, credential)
}

func checkSetupAuth(cfg *config.Config, selection setupSelection) error {
	if cfg == nil {
		return errors.New("config is required")
	}

	candidate := *cfg
	candidate.Models = cfg.Models
	candidate.Models.Providers = maps.Clone(cfg.Models.Providers)
	candidate.Models.Main.Provider = selection.provider
	candidate.Models.Main.Name = selection.model
	candidate.Models.Main.API = selection.api
	candidate.Models.Main.BaseURL = selection.baseURL
	candidate.Models.Summary.Provider = selection.provider
	candidate.Models.Summary.Name = selection.model
	candidate.Models.Summary.API = selection.api
	candidate.Models.Summary.BaseURL = selection.baseURL
	candidate.Search.Vector.Enabled = false
	if selection.apiKey != "" {
		if candidate.Models.Providers == nil {
			candidate.Models.Providers = make(map[string]config.ProviderModelConfig)
		}
		providerConfig := candidate.Models.Providers[selection.provider]
		providerConfig.APIKey = selection.apiKey
		candidate.Models.Providers[selection.provider] = providerConfig
	}

	_, err := candidate.ResolveModelAuth()
	return err
}

func isMissingModelCredentialError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "API key is required")
}

func getProviderDisplayName(provider modelprovider.ProviderDefinition) string {
	displayNameValue := str.String(provider.DisplayName)
	if value := displayNameValue.Trim(); value != "" {
		return value
	}
	iDValue11 := str.String(provider.ID)
	if value := iDValue11.Trim(); value != "" {
		return value
	}

	return "provider"
}

func (r providerRunner) selectChoice(
	ctx context.Context,
	title string,
	choices []selectChoice,
) (string, error) {
	if r.selector != nil {
		return r.selector(ctx, title, choices)
	}

	finalModel, err := runSetupSelectorProgram(ctx, r.input, r.output, newSelectorModel(title, choices))
	if err != nil {
		return "", err
	}

	selected, ok := finalModel.(selectorModel)
	if !ok {
		return "", errors.New("setup selection unavailable")
	}
	if selected.err != nil {
		return "", selected.err
	}
	choiceValue := str.String(selected.choice)
	if choiceValue.Trim() == "" {
		return "", errors.New("setup selection cancelled")
	}

	return selected.choice, nil
}

func (r providerRunner) getProviderList() string {
	providers := r.registry.GetProviderIDs()
	sort.Strings(providers)

	return strings.Join(providers, ", ")
}

func persistProviderSelection(opts ProviderOptions, selection setupSelection) error {
	updates := []config.ConfigUpdate{
		{Path: "models.main.provider", Value: selection.provider},
		{Path: "models.main.name", Value: selection.model},
		{Path: "models.main.api", Value: selection.api},
		{Path: "models.main.baseUrl", Value: selection.baseURL},
		{Path: "models.summary.provider", Value: selection.provider},
		{Path: "models.summary.name", Value: selection.model},
		{Path: "models.summary.api", Value: selection.api},
		{Path: "models.summary.baseUrl", Value: selection.baseURL},
	}
	updates = append(updates, config.ModelSetupEmbeddingUpdates(selection.provider, selection.baseURL)...)
	if selection.apiKey != "" {
		updates = append(updates, config.ConfigUpdate{
			Path:  fmt.Sprintf("models.providers.%s.apiKey", selection.provider),
			Value: selection.apiKey,
		})
	}
	if _, err := setConfigValuesRelaxed(opts.EnvPath, opts.ConfigPath, updates); err != nil {
		return err
	}

	cfg, err := config.Load(opts.EnvPath, opts.ConfigPath)
	if err == nil {
		config.Set(cfg)
	}

	return nil
}

func newSelectorModel(title string, choices []selectChoice) selectorModel {
	titleValue := str.String(title)
	return selectorModel{
		title:   titleValue.Trim(),
		choices: choices,
	}
}

func (m selectorModel) Init() tea.Cmd {
	return nil
}

func (m selectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	default:
		return m, nil
	}
}

func (m selectorModel) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isSetupCancelKey(msg) {
		m.err = errors.New("setup selection cancelled")
		return m, tea.Quit
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		if len(m.choices) > 0 {
			m.selected = (m.selected - 1 + len(m.choices)) % len(m.choices)
		}
	case tea.KeyDown:
		if len(m.choices) > 0 {
			m.selected = (m.selected + 1) % len(m.choices)
		}
	case tea.KeyHome:
		m.selected = 0
	case tea.KeyEnd:
		m.selected = max(0, len(m.choices)-1)
	case tea.KeyEnter:
		return m.chooseSelected()
	case tea.KeyEsc:
		m.err = errors.New("setup selection cancelled")
		return m, tea.Quit
	default:
		if index, ok := numericSelectionIndex(msg, len(m.choices)); ok {
			m.selected = index
			return m.chooseSelected()
		}
	}

	return m, nil
}

func (m selectorModel) chooseSelected() (tea.Model, tea.Cmd) {
	if len(m.choices) == 0 {
		m.err = errors.New("no setup options are available")
		return m, tea.Quit
	}

	if m.selected < 0 || m.selected >= len(m.choices) {
		m.selected = 0
	}
	iDValue12 := str.String(m.choices[m.selected].ID)
	m.choice = iDValue12.Trim()

	return m, tea.Quit
}

func (m selectorModel) View() tea.View {
	return tea.NewView(m.render())
}

func (m selectorModel) render() string {
	return renderSetupChoices(m.title, "", m.choices, m.selected, false)
}

func renderSetupInputPage(title string, description string, input string) string {
	return strings.Join([]string{
		renderSetupTitle(title),
		renderSetupDescription(description),
		"",
		renderSetupIndentedLayer(input),
		"",
		renderSetupInputHint(),
	}, "\n")
}

func renderSetupChoices(title string, description string, choices []selectChoice, selected int, includeBackHint bool) string {
	var builder strings.Builder
	if title != "" {
		builder.WriteString(renderSetupTitle(title))
		builder.WriteByte('\n')
	}
	descriptionValue := str.String(description)
	if descriptionValue.Trim() != "" {
		builder.WriteString(renderSetupDescription(description))
		builder.WriteString("\n\n")
	}

	for index, choice := range choices {
		prefix := setupOptionIndent + "  "
		if index == selected {
			prefix = setupOptionIndent + renderSetupAccent(">") + " "
		}
		builder.WriteString(prefix)
		builder.WriteString(strconv.Itoa(index + 1))
		builder.WriteString(". ")
		labelValue := str.String(choice.Label)
		builder.WriteString(labelValue.Trim())
		descriptionValue2 := str.String(choice.Description)
		if description := descriptionValue2.Trim(); description != "" &&
			!strings.EqualFold(description, choice.Label) {
			builder.WriteString(" (")
			builder.WriteString(description)
			builder.WriteByte(')')
		}
		builder.WriteByte('\n')
	}

	builder.WriteByte('\n')
	builder.WriteString(renderSetupChoiceHint(includeBackHint))
	return builder.String()
}

func renderSetupTitle(value string) string {
	value3 := str.String(value)
	return lipgloss.NewStyle().Bold(true).Render(value3.Trim())
}

func renderSetupDescription(value string) string {
	value4 := str.String(value)
	return renderSetupMuted(value4.Trim())
}

func renderSetupChoiceHint(includeBack bool) string {
	lines := []string{
		renderSetupMuted("Use"),
		"   " + renderSetupKey("arrow") + renderSetupMuted(" to choose"),
		"   " + renderSetupKey("number") + renderSetupMuted(" to select"),
		"   " + renderSetupKey("enter") + renderSetupMuted(" to select"),
	}
	if includeBack {
		lines = append(
			lines,
			"   "+renderSetupKey("backspace")+renderSetupMuted(" or ")+renderSetupKey("back arrow")+renderSetupMuted(" to go back."),
		)
	}

	return strings.Join(lines, "\n")
}

func renderSetupInputHint() string {
	return strings.Join([]string{
		renderSetupMuted("Use"),
		"   " + renderSetupKey("enter") + renderSetupMuted(" to save"),
		"   " + renderSetupKey("esc") + renderSetupMuted(" to go back."),
	}, "\n")
}

func renderSetupAccent(value string) string {
	return renderSetupAccentStyle().Render(value)
}

func renderSetupMuted(value string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(tuirender.DefaultTheme.MutedText)).
		Render(value)
}

func renderSetupKey(value string) string {
	return lipgloss.NewStyle().Bold(true).Render(value)
}

func renderSetupAccentStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(tuirender.DefaultTheme.MarkdownLinkForeground))
}

func renderSetupIndentedLayer(value string) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lineValue := str.String(line)
		if lineValue.Trim() != "" {
			lines[index] = setupOptionIndent + line
		}
	}

	return strings.Join(lines, "\n")
}

func numericSelectionIndex(msg tea.KeyPressMsg, length int) (int, bool) {
	textValue := str.String(msg.Key().Text)
	value := textValue.Trim()
	if value == "" && msg.Key().Code >= '0' && msg.Key().Code <= '9' {
		value = string(msg.Key().Code)
	}
	index, ok := parseSelectionIndex(value, length)

	return index, ok
}

func isSetupCancelKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "ctrl+c" || key.Code == 'c' && key.Mod == tea.ModCtrl
}

func parseSelectionIndex(value string, length int) (int, bool) {
	value5 := str.String(value)
	number, err := strconv.Atoi(value5.Trim())
	if err != nil || number < 1 || number > length {
		return 0, false
	}

	return number - 1, true
}
