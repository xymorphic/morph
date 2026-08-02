package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	clibase "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	appcredential "github.com/wandxy/morph/internal/credential"
	modelcatalog "github.com/wandxy/morph/internal/model"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	provider_ollama "github.com/wandxy/morph/internal/model/provider_ollama"
	"github.com/wandxy/morph/internal/profile"
	tuirender "github.com/wandxy/morph/internal/tui/render"
	"github.com/wandxy/morph/pkg/str"
)

var errSetupTestWrite = errors.New("write failed")
var errSetupTestSelector = errors.New("selector failed")
var errSetupTestPull = errors.New("pull failed")
var errSetupTestRead = errors.New("read failed")

type failingSetupWriter struct{}

func (failingSetupWriter) Write([]byte) (int, error) {
	return 0, errSetupTestWrite
}

type setupTestCredentialStore struct {
	err error
}

func (s setupTestCredentialStore) Set(string, appcredential.StoredCredential) error {
	return s.err
}

type unavailableSetupModel struct{}

func (unavailableSetupModel) Init() tea.Cmd {
	return nil
}

func (m unavailableSetupModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (unavailableSetupModel) View() tea.View {
	return tea.NewView("")
}

type setupTestSubscriptionProvider struct {
	credential appcredential.StoredCredential
	err        error
}

func (p setupTestSubscriptionProvider) Login(
	context.Context,
	appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	return p.credential, p.err
}

func (p setupTestSubscriptionProvider) Refresh(
	context.Context,
	appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	return p.credential, p.err
}

func (setupTestSubscriptionProvider) AuthHeaders(
	context.Context,
	appcredential.StoredCredential,
) (map[string]string, error) {
	return nil, nil
}

func TestRunProviderConfiguresCloudProviderFromOptions(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
		APIKey:     "openai-key",
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Main.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.Models.Main.API)
	require.Equal(t, constants.DefaultOpenAIBaseURL, cfg.Models.Main.BaseURL)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Summary.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Summary.Name)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Embedding.Provider)
	require.Equal(t, constants.DefaultProfileEmbeddingModel, cfg.Models.Embedding.Name)
	require.Equal(t, "openai-key", cfg.Models.Providers[constants.ModelProviderOpenAI].APIKey)
	require.Empty(t, output.String())
}

func TestRunProviderUsesWizardForMissingProviderAPIKey(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	originalWizard := runSetupWizardProgram
	t.Cleanup(func() {
		runSetupWizardProgram = originalWizard
	})

	runSetupWizardProgram = func(
		_ context.Context,
		_ io.Reader,
		_ io.Writer,
		model setupWizardModel,
	) (tea.Model, error) {
		require.Equal(t, setupWizardStepAuth, model.step)
		model.selection.apiKey = "prompt-key"
		model.done = true
		return model, nil
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
	})

	require.NoError(t, err)

	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "prompt-key", cfg.Models.Providers[constants.ModelProviderOpenAI].APIKey)
}

func TestRunProviderUsesPagedSetupWhenProviderOrModelMissing(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	originalWizard := runSetupWizardProgram
	t.Cleanup(func() {
		runSetupWizardProgram = originalWizard
	})

	runSetupWizardProgram = func(
		context.Context,
		io.Reader,
		io.Writer,
		setupWizardModel,
	) (tea.Model, error) {
		return setupWizardModel{
			done: true,
			selection: setupSelection{
				provider:   constants.ModelProviderOpenAI,
				api:        modelprovider.APIOpenAIResponses,
				baseURL:    constants.DefaultOpenAIBaseURL,
				model:      "gpt-5.5",
				authMethod: "api-key",
				apiKey:     "wizard-key",
			},
		}, nil
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Input:      strings.NewReader("wizard-key\n"),
		Output:     &output,
		ConfigPath: configPath,
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Equal(t, "wizard-key", cfg.Models.Providers[constants.ModelProviderOpenAI].APIKey)
}

func TestRunProviderRequiresMissingProviderAPIKey(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	originalWizard := runSetupWizardProgram
	t.Cleanup(func() {
		runSetupWizardProgram = originalWizard
	})

	runSetupWizardProgram = func(context.Context, io.Reader, io.Writer, setupWizardModel) (tea.Model, error) {
		return setupWizardModel{}, nil
	}

	_, err := RunProvider(context.Background(), ProviderOptions{
		Input:      strings.NewReader(""),
		Output:     io.Discard,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
	})

	require.EqualError(t, err, "setup selection cancelled")
}

func TestRunProviderReturnsWizardAuthError(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	originalWizard := runSetupWizardProgram
	t.Cleanup(func() {
		runSetupWizardProgram = originalWizard
	})

	runSetupWizardProgram = func(context.Context, io.Reader, io.Writer, setupWizardModel) (tea.Model, error) {
		return setupWizardModel{
			done: true,
			selection: setupSelection{
				provider:   constants.ModelProviderOpenAI,
				api:        modelprovider.APIOpenAIResponses,
				baseURL:    constants.DefaultOpenAIBaseURL,
				model:      "gpt-5.5",
				authMethod: "api-key",
			},
		}, nil
	}

	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     io.Discard,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
	})

	require.EqualError(t, err, "setup API key is unavailable")
}

func TestRunProviderConfiguresOllamaFromDiscoveredModels(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	var discoveredBaseURL string
	discoverOllamaModels = func(_ context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
		discoveredBaseURL = baseURL
		return []modelprovider.ModelDefinition{{
			ID:       "qwen3:8b",
			Name:     "Qwen 3 8B",
			Provider: constants.ModelProviderOllama,
			API:      modelprovider.APIOllamaNative,
			Input:    []modelprovider.InputKind{modelprovider.InputText},
		}}, nil
	}
	pullOllamaModel = func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		t.Fatal("pull should not run for a discovered model")
		return nil
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    "http://127.0.0.1:11434",
		Model:      "qwen3:8b",
	})

	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:11434", discoveredBaseURL)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, "qwen3:8b", cfg.Models.Main.Name)
	require.Equal(t, modelprovider.APIOllamaNative, cfg.Models.Main.API)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Embedding.Provider)
	require.Equal(t, constants.DefaultOllamaEmbeddingModel, cfg.Models.Embedding.Name)
	require.Equal(t, modelprovider.APIOllamaEmbeddings, cfg.Models.Embedding.API)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Embedding.BaseURL)
	require.NotContains(t, output.String(), "Pull qwen3:8b if missing?")
}

func TestRunProviderNormalizesInheritedOllamaOpenAICompatibleBaseURL(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")
	_, err := config.SetConfigValuesRelaxed("", configPath, []config.ConfigUpdate{
		{Path: "models.main.provider", Value: constants.ModelProviderOllama},
		{Path: "models.main.name", Value: "openai-compatible:latest"},
		{Path: "models.main.api", Value: modelprovider.APIOpenAICompletions},
		{Path: "models.main.baseURL", Value: "http://127.0.0.1:11434/v1"},
	})
	require.NoError(t, err)

	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	var discoveredBaseURL string
	discoverOllamaModels = func(_ context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
		discoveredBaseURL = baseURL
		return []modelprovider.ModelDefinition{{
			ID:       "native:latest",
			Provider: constants.ModelProviderOllama,
			API:      modelprovider.APIOllamaNative,
			Input:    []modelprovider.InputKind{modelprovider.InputText},
		}}, nil
	}
	pullOllamaModel = func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		t.Fatal("pull should not run for a discovered model")
		return nil
	}

	_, err = RunProvider(context.Background(), ProviderOptions{
		Output:     io.Discard,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		Model:      "native:latest",
	})

	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:11434", discoveredBaseURL)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, modelprovider.APIOllamaNative, cfg.Models.Main.API)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Embedding.BaseURL)
	rawConfig, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NotContains(t, string(rawConfig), "baseURL:")
	require.Contains(t, string(rawConfig), "baseUrl: http://127.0.0.1:11434")
}

func TestRunProviderUsesDefaultOllamaDiscoverer(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    server.URL,
		Model:      "llama3.2:3b",
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "llama3.2:3b", cfg.Models.Main.Name)
}

func TestRunProviderPullsSelectedOllamaModel(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		t.Fatal("discovery should not run when --model and --pull are provided")
		return nil, nil
	}

	var pulledBaseURL string
	var pulledModel string
	pullOllamaModel = func(
		_ context.Context,
		baseURL string,
		model string,
		headers map[string]string,
		onProgress func(provider_ollama.PullProgress),
	) error {
		require.Nil(t, headers)
		pulledBaseURL = baseURL
		pulledModel = model
		onProgress(provider_ollama.PullProgress{
			Status:    "downloading",
			Completed: 50,
			Total:     100,
		})
		return nil
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Input:      strings.NewReader(""),
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    "http://127.0.0.1:11434",
		Model:      "llama3.2:3b",
		Pull:       true,
	})

	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:11434", pulledBaseURL)
	require.Equal(t, "llama3.2:3b", pulledModel)
	require.Contains(t, output.String(), "Ollama pull: downloading 50%")

	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "llama3.2:3b", cfg.Models.Main.Name)
}

func TestRunProviderValidatesSelectedOllamaModelReachability(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{
			ID:  "qwen3:8b",
			API: modelprovider.APIOllamaNative,
		}}, nil
	}
	pullOllamaModel = func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		t.Fatal("pull should not run for an installed model")
		return nil
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    "http://127.0.0.1:11434",
		Model:      "qwen3:8b",
	})

	require.NoError(t, err)
	require.NotContains(t, output.String(), "Pull qwen3:8b if missing?")
}

func TestProviderRunnerAuthenticatesWithSubscriptionLogin(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)

	models, err := modelcatalog.ListOptions(modelcatalog.OptionQuery{
		Provider:  constants.ModelProviderAnthropic,
		OAuthOnly: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, models)

	originalSubscriptionProvider := getSubscriptionProvider
	t.Cleanup(func() {
		getSubscriptionProvider = originalSubscriptionProvider
	})
	getSubscriptionProvider = func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, constants.ModelProviderAnthropic, provider)
		return setupTestSubscriptionProvider{
			credential: appcredential.StoredCredential{
				Type:  appcredential.TypeOAuth,
				Token: "subscription-token",
			},
		}, true
	}

	runner := providerRunner{
		input:    strings.NewReader(""),
		output:   io.Discard,
		registry: modelprovider.DefaultRegistry(),
		selector: func(_ context.Context, title string, choices []selectChoice) (string, error) {
			require.Equal(t, "Authenticate Anthropic", title)
			require.Len(t, choices, 2)
			return "oauth", nil
		},
	}
	selection, err := runner.ensureSetupAuth(context.Background(), cfg, setupSelection{
		provider: constants.ModelProviderAnthropic,
		api:      modelprovider.APIAnthropicMessages,
		baseURL:  constants.DefaultAnthropicBaseURL,
		model:    models[0].ID,
	})

	require.NoError(t, err)
	require.Empty(t, selection.apiKey)

	credential, ok, err := appcredential.NewFileStore("").Get(constants.ModelProviderAnthropic)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "subscription-token", credential.Token)
}

func TestProviderRunnerReturnsAuthResolutionError(t *testing.T) {
	_, err := providerRunner{registry: modelprovider.DefaultRegistry()}.ensureSetupAuth(
		context.Background(),
		nil,
		setupSelection{provider: constants.ModelProviderOpenAI, model: "gpt-5.5"},
	)

	require.EqualError(t, err, "config is required")
}

func TestProviderRunnerReturnsUnknownAuthProvider(t *testing.T) {
	_, err := providerRunner{registry: modelprovider.DefaultRegistry()}.ensureSetupAuth(
		context.Background(),
		config.NewProfileConfig(),
		setupSelection{provider: "missing", model: "model"},
	)

	require.ErrorContains(t, err, "model provider must be one of:")
}

func TestProviderRunnerReturnsUnavailableAuthMethod(t *testing.T) {
	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{{
			ID:             "custom",
			DefaultAPI:     modelprovider.APIOpenAIResponses,
			SupportsModels: true,
		}},
		nil,
	)

	_, err := providerRunner{registry: registry}.ensureSetupAuth(
		context.Background(),
		config.NewProfileConfig(),
		setupSelection{
			provider: "custom",
			api:      modelprovider.APIOpenAIResponses,
			model:    "model",
		},
	)

	require.ErrorContains(t, err, `model API key is required for provider "custom"`)
}

func TestProviderRunnerReturnsInvalidAuthMethod(t *testing.T) {
	setupProviderTestProfile(t, "invalid-auth-method")
	cfg := config.NewProfileConfig()
	models, err := modelcatalog.ListOptions(modelcatalog.OptionQuery{
		Provider:  constants.ModelProviderAnthropic,
		OAuthOnly: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, models)

	originalSubscriptionProvider := getSubscriptionProvider
	t.Cleanup(func() {
		getSubscriptionProvider = originalSubscriptionProvider
	})
	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return setupTestSubscriptionProvider{}, true
	}

	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(context.Context, string, []selectChoice) (string, error) {
			return "bogus", nil
		},
	}
	_, err = runner.ensureSetupAuth(context.Background(), cfg, setupSelection{
		provider:   constants.ModelProviderAnthropic,
		api:        modelprovider.APIAnthropicMessages,
		baseURL:    constants.DefaultAnthropicBaseURL,
		model:      models[0].ID,
		authMethod: "bogus",
	})

	require.EqualError(t, err, "authentication method unavailable")
}

func TestProviderRunnerUsesExplicitAndSingleAuthMethods(t *testing.T) {
	runner := providerRunner{registry: modelprovider.DefaultRegistry()}
	provider := mustSetupProvider(t, constants.ModelProviderOpenAI)

	method, err := runner.getSetupAuthMethod(
		context.Background(),
		provider,
		setupSelection{provider: constants.ModelProviderOpenAI, authMethod: "api-key"},
	)
	require.NoError(t, err)
	require.Equal(t, "api-key", method)

	method, err = runner.getSetupAuthMethod(
		context.Background(),
		provider,
		setupSelection{provider: constants.ModelProviderOpenAI},
	)
	require.NoError(t, err)
	require.Equal(t, "api-key", method)
}

func TestProviderRunnerReturnsUnavailableAPIKeyFromAuthStep(t *testing.T) {
	cfg := config.NewProfileConfig()

	_, err := providerRunner{registry: modelprovider.DefaultRegistry()}.ensureSetupAuth(
		context.Background(),
		cfg,
		setupSelection{
			provider:   constants.ModelProviderOpenAI,
			api:        modelprovider.APIOpenAIResponses,
			baseURL:    constants.DefaultOpenAIBaseURL,
			model:      "gpt-5.5",
			authMethod: "api-key",
		},
	)

	require.EqualError(t, err, "setup API key is unavailable")
}

func TestProviderRunnerReturnsPostLoginAuthError(t *testing.T) {
	setupProviderTestProfile(t, "post-login-auth-error")
	cfg := config.NewProfileConfig()
	models, err := modelcatalog.ListOptions(modelcatalog.OptionQuery{
		Provider:  constants.ModelProviderAnthropic,
		OAuthOnly: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, models)

	originalSubscriptionProvider := getSubscriptionProvider
	originalCredentialStore := newCredentialStore
	t.Cleanup(func() {
		getSubscriptionProvider = originalSubscriptionProvider
		newCredentialStore = originalCredentialStore
	})
	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return setupTestSubscriptionProvider{
			credential: appcredential.StoredCredential{Type: appcredential.TypeOAuth, Token: "token"},
		}, true
	}
	newCredentialStore = func() setupCredentialStore {
		return setupTestCredentialStore{}
	}

	runner := providerRunner{
		output:   io.Discard,
		registry: modelprovider.DefaultRegistry(),
		selector: func(context.Context, string, []selectChoice) (string, error) {
			return "oauth", nil
		},
	}
	_, err = runner.ensureSetupAuth(context.Background(), cfg, setupSelection{
		provider:   constants.ModelProviderAnthropic,
		api:        modelprovider.APIAnthropicMessages,
		baseURL:    constants.DefaultAnthropicBaseURL,
		model:      models[0].ID,
		authMethod: "oauth",
	})

	require.EqualError(t, err, `model API key is required for provider "anthropic"; set a provider API key, provider env var, role apiKey, or provider login`)
}

func TestProviderRunnerReturnsOAuthLoginErrorFromAuthStep(t *testing.T) {
	setupProviderTestProfile(t, "oauth-login-error")
	cfg := config.NewProfileConfig()
	models, err := modelcatalog.ListOptions(modelcatalog.OptionQuery{
		Provider:  constants.ModelProviderAnthropic,
		OAuthOnly: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, models)

	originalSubscriptionProvider := getSubscriptionProvider
	t.Cleanup(func() {
		getSubscriptionProvider = originalSubscriptionProvider
	})
	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return setupTestSubscriptionProvider{err: errSetupTestRead}, true
	}

	runner := providerRunner{
		output:   io.Discard,
		registry: modelprovider.DefaultRegistry(),
		selector: func(context.Context, string, []selectChoice) (string, error) {
			return "oauth", nil
		},
	}
	_, err = runner.ensureSetupAuth(context.Background(), cfg, setupSelection{
		provider:   constants.ModelProviderAnthropic,
		api:        modelprovider.APIAnthropicMessages,
		baseURL:    constants.DefaultAnthropicBaseURL,
		model:      models[0].ID,
		authMethod: "oauth",
	})

	require.ErrorIs(t, err, errSetupTestRead)
}

func TestProviderRunnerLoginSetupProviderErrors(t *testing.T) {
	originalSubscriptionProvider := getSubscriptionProvider
	originalCredentialStore := newCredentialStore
	t.Cleanup(func() {
		getSubscriptionProvider = originalSubscriptionProvider
		newCredentialStore = originalCredentialStore
	})

	provider := modelprovider.ProviderDefinition{ID: "custom", DisplayName: "Custom"}
	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return nil, false
	}
	err := providerRunner{output: io.Discard}.loginSetupProvider(context.Background(), provider)
	require.EqualError(t, err, "subscription login is not available for Custom")

	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return setupTestSubscriptionProvider{}, true
	}
	err = providerRunner{output: failingSetupWriter{}}.loginSetupProvider(context.Background(), provider)
	require.ErrorIs(t, err, errSetupTestWrite)

	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return setupTestSubscriptionProvider{err: errSetupTestRead}, true
	}
	err = providerRunner{output: io.Discard}.loginSetupProvider(context.Background(), provider)
	require.ErrorIs(t, err, errSetupTestRead)

	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return setupTestSubscriptionProvider{
			credential: appcredential.StoredCredential{Type: appcredential.TypeOAuth, Token: "token"},
		}, true
	}
	newCredentialStore = func() setupCredentialStore {
		return setupTestCredentialStore{err: errSetupTestWrite}
	}
	err = providerRunner{output: io.Discard}.loginSetupProvider(context.Background(), provider)
	require.ErrorIs(t, err, errSetupTestWrite)
}

func TestCheckSetupAuthAndProviderDisplayNameFallbacks(t *testing.T) {
	require.EqualError(t, checkSetupAuth(nil, setupSelection{}), "config is required")
	require.Equal(t, "custom", getProviderDisplayName(modelprovider.ProviderDefinition{ID: "custom"}))
	require.Equal(t, "provider", getProviderDisplayName(modelprovider.ProviderDefinition{}))
}

func TestSetupWizardMovesForwardAndBackBetweenProviderAndModel(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{Registry: modelprovider.DefaultRegistry()},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepProvider, model.step)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepProvider, model.step)
	require.NoError(t, model.err)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepProvider, model.step)
	require.NoError(t, model.err)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepModel, model.step)
	require.NotEmpty(t, model.selection.provider)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepProvider, model.step)
	require.Empty(t, model.selection.provider)
}

func TestSetupWizardFinishesWithAuthMethodSelection(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{Provider: constants.ModelProviderOpenAI, Registry: modelprovider.DefaultRegistry()},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepModel, model.step)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAuth, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.False(t, model.done)
	require.Equal(t, "api-key", model.selection.authMethod)
	require.Equal(t, setupWizardStepAPIKey, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.NotNil(t, cmd)
	model = updated.(setupWizardModel)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	model = updated.(setupWizardModel)
	require.True(t, model.done)
	require.Equal(t, "k", model.selection.apiKey)
}

func TestSetupWizardSupportsBackFromAuthToModel(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{Provider: constants.ModelProviderOpenAI, Registry: modelprovider.DefaultRegistry()},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAuth, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepModel, model.step)
	require.Empty(t, model.selection.model)
	require.Equal(t, constants.ModelProviderOpenAI, model.selection.provider)
}

func TestSetupWizardDoesNotBackFromModelToProviderWhenProviderFlagSet(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{Provider: constants.ModelProviderOpenAI, Registry: modelprovider.DefaultRegistry()},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepModel, model.step)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})

	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepModel, model.step)
	require.Equal(t, constants.ModelProviderOpenAI, model.selection.provider)
}

func TestSetupWizardDoesNotBackFromAuthToModelWhenModelFlagSet(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{
			Provider: constants.ModelProviderOpenAI,
			Model:    "gpt-5.5",
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepAuth, model.step)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})

	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAuth, model.step)
	require.Equal(t, "gpt-5.5", model.selection.model)
}

func TestSetupWizardBackFromAuthReturnsToProviderWhenOnlyModelFlagSet(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{
			Model:    "gpt-5.5",
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepProvider, model.step)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAuth, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})

	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepProvider, model.step)
	require.Empty(t, model.selection.model)
}

func TestSetupWizardSupportsBackFromAPIKeyToAuth(t *testing.T) {
	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{Provider: constants.ModelProviderOpenAI, Registry: modelprovider.DefaultRegistry()},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAuth, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAPIKey, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepAuth, model.step)
	require.Empty(t, model.selection.apiKey)
}

func TestSetupWizardAPIKeyBackReturnsProviderError(t *testing.T) {
	model := setupWizardModel{
		step:   setupWizardStepAPIKey,
		runner: providerRunner{registry: modelprovider.DefaultRegistry()},
		selection: setupSelection{
			provider: "missing",
		},
	}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	require.ErrorContains(t, updated.(setupWizardModel).err, "model provider must be one of:")
}

func TestSetupWizardRequiresAPIKeyOnAPIKeyPage(t *testing.T) {
	model := setupWizardModel{
		step: setupWizardStepAPIKey,
	}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "api key is required")
}

func TestSetupWizardFinishesWithOAuthAuthMethod(t *testing.T) {
	model := setupWizardModel{
		step: setupWizardStepAuth,
		choices: []selectChoice{{
			ID:    "oauth",
			Label: "Use account",
		}},
	}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	model = updated.(setupWizardModel)
	require.True(t, model.done)
	require.Equal(t, "oauth", model.selection.authMethod)
}

func TestSetupWizardRecordsPullChoice(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, nil
	}

	model, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{
			Provider: constants.ModelProviderOllama,
			BaseURL:  "http://127.0.0.1:11434",
			Refresh:  true,
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepModel, model.step)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, setupWizardStepPull, model.step)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	model = updated.(setupWizardModel)
	require.True(t, model.done)
	require.True(t, model.selection.pullAnswered)
	require.True(t, model.selection.pullSelected)
}

func TestRunPagedSetupHandlesProgramResults(t *testing.T) {
	originalWizard := runSetupWizardProgram
	t.Cleanup(func() {
		runSetupWizardProgram = originalWizard
	})

	runner := providerRunner{
		input:    strings.NewReader(""),
		output:   io.Discard,
		registry: modelprovider.DefaultRegistry(),
	}
	opts := ProviderOptions{Provider: constants.ModelProviderOpenAI, Registry: modelprovider.DefaultRegistry()}
	cfg := config.NewProfileConfig()

	_, err := runner.runPagedSetup(
		context.Background(),
		ProviderOptions{Provider: "missing", Registry: modelprovider.DefaultRegistry()},
		cfg,
	)
	require.ErrorContains(t, err, "model provider must be one of:")

	runSetupWizardProgram = func(context.Context, io.Reader, io.Writer, setupWizardModel) (tea.Model, error) {
		return setupWizardModel{}, errSetupTestSelector
	}
	_, err = runner.runPagedSetup(context.Background(), opts, cfg)
	require.ErrorIs(t, err, errSetupTestSelector)

	runSetupWizardProgram = func(context.Context, io.Reader, io.Writer, setupWizardModel) (tea.Model, error) {
		return unavailableSetupModel{}, nil
	}
	_, err = runner.runPagedSetup(context.Background(), opts, cfg)
	require.EqualError(t, err, "setup selection unavailable")

	runSetupWizardProgram = func(context.Context, io.Reader, io.Writer, setupWizardModel) (tea.Model, error) {
		return setupWizardModel{err: errSetupTestSelector}, nil
	}
	_, err = runner.runPagedSetup(context.Background(), opts, cfg)
	require.ErrorIs(t, err, errSetupTestSelector)

	runSetupWizardProgram = func(context.Context, io.Reader, io.Writer, setupWizardModel) (tea.Model, error) {
		return setupWizardModel{}, nil
	}
	_, err = runner.runPagedSetup(context.Background(), opts, cfg)
	require.EqualError(t, err, "setup selection cancelled")
}

func TestSetupWizardProgramReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runSetupWizardProgram(
		ctx,
		strings.NewReader(""),
		io.Discard,
		setupWizardModel{},
	)

	require.Error(t, err)
}

func TestSetupWizardInitializationAndTransitionErrors(t *testing.T) {
	runner := providerRunner{registry: modelprovider.DefaultRegistry()}
	_, err := newSetupWizardModel(
		context.Background(),
		runner,
		ProviderOptions{Provider: "missing", Registry: modelprovider.DefaultRegistry()},
		config.NewProfileConfig(),
	)
	require.ErrorContains(t, err, "model provider must be one of:")

	_, err = newSetupWizardModel(
		context.Background(),
		runner,
		ProviderOptions{
			Provider: constants.ModelProviderOpenAI,
			API:      "wrong",
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)
	require.ErrorContains(t, err, "model API must be one of:")

	model, err := newSetupWizardModel(
		context.Background(),
		runner,
		ProviderOptions{
			Provider: constants.ModelProviderOpenAI,
			Model:    "gpt-5.5",
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)
	require.NoError(t, err)
	require.Equal(t, setupWizardStepAuth, model.step)
}

func TestSetupWizardModelOptionErrors(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, os.ErrPermission
	}
	_, err := newSetupWizardModel(
		context.Background(),
		providerRunner{registry: modelprovider.DefaultRegistry()},
		ProviderOptions{
			Provider: constants.ModelProviderOllama,
			BaseURL:  "http://127.0.0.1:11434",
			Refresh:  true,
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)
	require.ErrorIs(t, err, os.ErrPermission)

	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{{
			ID:             constants.ModelProviderOpenAI,
			DefaultAPI:     modelprovider.APIOpenAIResponses,
			SupportsModels: true,
		}},
		nil,
	)
	_, err = newSetupWizardModel(
		context.Background(),
		providerRunner{registry: registry},
		ProviderOptions{Provider: constants.ModelProviderOpenAI, Registry: registry},
		config.NewProfileConfig(),
	)
	require.EqualError(t, err, "models unavailable")
}

func TestSetupWizardSetModelAndAdvanceErrors(t *testing.T) {
	model := setupWizardModel{runner: providerRunner{registry: modelprovider.DefaultRegistry()}}
	err := model.setModel("model")
	require.ErrorContains(t, err, "model provider must be one of:")

	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})
	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, os.ErrPermission
	}
	model = setupWizardModel{
		ctx:    context.Background(),
		runner: providerRunner{registry: modelprovider.DefaultRegistry()},
		opts: ProviderOptions{
			Provider: constants.ModelProviderOllama,
			Registry: modelprovider.DefaultRegistry(),
		},
		cfg: config.NewProfileConfig(),
		selection: setupSelection{
			provider: constants.ModelProviderOllama,
			api:      modelprovider.APIOllamaNative,
			baseURL:  "http://127.0.0.1:11434",
		},
	}
	err = model.setModel("missing:latest")
	require.ErrorIs(t, err, os.ErrPermission)

	model = setupWizardModel{runner: providerRunner{registry: modelprovider.DefaultRegistry()}}
	err = model.advanceAfterModel()
	require.EqualError(t, err, "config is required")

	model = setupWizardModel{
		runner: providerRunner{registry: modelprovider.DefaultRegistry()},
		cfg:    config.NewProfileConfig(),
		selection: setupSelection{
			provider: "missing",
			model:    "model",
		},
	}
	err = model.advanceAfterModel()
	require.ErrorContains(t, err, "model provider must be one of:")

	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{{
			ID:             "custom",
			DefaultAPI:     modelprovider.APIOpenAIResponses,
			SupportsModels: true,
		}},
		[]modelprovider.ModelDefinition{{
			ID:       "model",
			Provider: "custom",
			API:      modelprovider.APIOpenAIResponses,
			Input:    []modelprovider.InputKind{modelprovider.InputText},
		}},
	)
	model = setupWizardModel{
		runner: providerRunner{registry: registry},
		cfg:    config.NewProfileConfig(),
		selection: setupSelection{
			provider: "custom",
			api:      modelprovider.APIOpenAIResponses,
			model:    "model",
		},
	}
	err = model.advanceAfterModel()
	require.ErrorContains(t, err, `model API key is required for provider "custom"`)
}

func TestSetupWizardFinishesWhenAuthAndPullAreAlreadySatisfied(t *testing.T) {
	model := setupWizardModel{
		runner: providerRunner{registry: modelprovider.DefaultRegistry()},
		opts:   ProviderOptions{Registry: modelprovider.DefaultRegistry()},
		cfg:    config.NewProfileConfig(),
		selection: setupSelection{
			provider: constants.ModelProviderOllama,
			api:      modelprovider.APIOllamaNative,
			baseURL:  "http://127.0.0.1:11434",
			model:    "installed:latest",
		},
	}

	err := model.advanceAfterModel()

	require.NoError(t, err)
	require.True(t, model.done)
}

func TestSetupWizardKeyAndRenderBranches(t *testing.T) {
	model := setupWizardModel{
		step: setupWizardStepProvider,
		choices: []selectChoice{
			{ID: "first", Label: "First"},
			{ID: "second", Label: "Second"},
		},
	}
	require.Nil(t, model.Init())
	require.NotNil(t, model.View())
	rendered := model.render()
	require.Contains(t, rendered, "Select a provider")
	require.Contains(t, rendered, "Choose the model provider Morph should use by default.")
	requireSetupAccentCursor(t, rendered, "1. First")
	require.Contains(t, rendered, "arrow")
	require.Contains(t, rendered, "number")
	require.Contains(t, rendered, "backspace")
	require.Contains(t, rendered, "back arrow")

	updated, cmd := model.Update(struct{}{})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, 1, model.selected)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, 0, model.selected)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, 1, model.selected)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	require.Nil(t, cmd)
	model = updated.(setupWizardModel)
	require.Equal(t, 0, model.selected)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	require.NotNil(t, cmd)
	model = updated.(setupWizardModel)
	require.Error(t, model.err)

	updated, cmd = setupWizardModel{}.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "setup selection cancelled")

	updated, cmd = setupWizardModel{}.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "setup selection cancelled")

	updated, cmd = setupWizardModel{step: setupWizardStepAPIKey}.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "setup selection cancelled")
}

func TestSetupWizardBackAndChooseErrorBranches(t *testing.T) {
	model := setupWizardModel{
		step: setupWizardStepModel,
		opts: ProviderOptions{Provider: constants.ModelProviderOpenAI},
	}
	updated, cmd := model.goBack()
	require.Nil(t, cmd)
	require.Equal(t, setupWizardStepModel, updated.(setupWizardModel).step)

	model = setupWizardModel{
		step: setupWizardStepPull,
		opts: ProviderOptions{
			Provider: constants.ModelProviderOllama,
			Model:    "missing:latest",
		},
		selection: setupSelection{
			provider: constants.ModelProviderOllama,
			model:    "missing:latest",
		},
	}
	updated, cmd = model.goBack()
	require.Nil(t, cmd)
	require.Equal(t, setupWizardStepPull, updated.(setupWizardModel).step)
	require.Equal(t, "missing:latest", updated.(setupWizardModel).selection.model)

	model = setupWizardModel{
		step:   setupWizardStepAuth,
		runner: providerRunner{registry: modelprovider.DefaultRegistry()},
		selection: setupSelection{
			provider: "missing",
		},
	}
	updated, cmd = model.goBack()
	require.NotNil(t, cmd)
	require.Error(t, updated.(setupWizardModel).err)

	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})
	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, os.ErrPermission
	}
	model = setupWizardModel{
		step: setupWizardStepPull,
		ctx:  context.Background(),
		cfg:  config.NewProfileConfig(),
		opts: ProviderOptions{Refresh: true},
		runner: providerRunner{
			registry: modelprovider.DefaultRegistry(),
		},
		selection: setupSelection{
			provider: constants.ModelProviderOllama,
			baseURL:  "http://127.0.0.1:11434",
		},
	}
	updated, cmd = model.goBack()
	require.NotNil(t, cmd)
	require.ErrorIs(t, updated.(setupWizardModel).err, os.ErrPermission)

	updated, cmd = setupWizardModel{}.goBack()
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "setup selection cancelled")

	updated, cmd = setupWizardModel{}.chooseSelected()
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "no setup options are available")

	model = setupWizardModel{
		step:     "missing",
		choices:  []selectChoice{{ID: "first", Label: "First"}},
		selected: 99,
	}
	updated, cmd = model.chooseSelected()
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(setupWizardModel).err, "setup selection unavailable")
}

func TestSetupWizardRenderStepTitles(t *testing.T) {
	model := setupWizardModel{
		runner:  providerRunner{registry: modelprovider.DefaultRegistry()},
		choices: []selectChoice{{ID: "x", Label: "X"}},
	}

	model.step = setupWizardStepModel
	rendered := model.render()
	require.Contains(t, rendered, "Select a model")
	requireSetupAccentCursor(t, rendered, "1. X")

	model.step = setupWizardStepAuth
	model.selection.provider = constants.ModelProviderOpenAI
	rendered = model.render()
	require.Contains(t, rendered, "Authenticate OpenAI")
	requireSetupAccentCursor(t, rendered, "1. X")

	model.step = setupWizardStepPull
	model.selection.model = "missing:latest"
	rendered = model.render()
	require.Contains(t, rendered, "Pull missing:latest if missing?")
	requireSetupAccentCursor(t, rendered, "1. X")

	model.step = setupWizardStepAPIKey
	model.selection.provider = constants.ModelProviderOpenAI
	model.showAPIKeyPage()
	rendered = model.render()
	require.Contains(t, rendered, "API key for OpenAI")
	require.Contains(t, rendered, "Enter the API key for this provider.")
	requireSetupAPIKeyPromptAccent(t, model.apiKeyInput)
	requireSetupIndentedLayer(t, rendered, model.apiKeyInput.View())
	requireSetupHelpGuideNotIndented(t, rendered, renderSetupInputHint())
	require.Contains(t, rendered, "enter")
	require.Contains(t, rendered, "esc")

	model.step = "unknown"
	require.Contains(t, model.render(), "Setup")
}

func TestPullMissingLocalModelUsesWizardPullDecline(t *testing.T) {
	err := providerRunner{output: io.Discard}.pullMissingLocalModel(
		context.Background(),
		ProviderOptions{},
		setupSelection{
			provider:          constants.ModelProviderOllama,
			model:             "missing:latest",
			localModelMissing: true,
			pullAnswered:      true,
			pullSelected:      false,
		},
	)

	require.NoError(t, err)
}

func TestProviderRunnerPullsWhenWizardSelectedPull(t *testing.T) {
	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, nil
	}

	var pulled bool
	pullOllamaModel = func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		pulled = true
		return nil
	}

	runner := providerRunner{
		output: io.Discard,
	}

	err := runner.pullMissingLocalModel(context.Background(), ProviderOptions{
		Provider: constants.ModelProviderOllama,
	}, setupSelection{
		provider:          constants.ModelProviderOllama,
		baseURL:           "http://127.0.0.1:11434",
		model:             "missing:latest",
		localModelMissing: true,
		pullAnswered:      true,
		pullSelected:      true,
	})

	require.NoError(t, err)
	require.True(t, pulled)
}

func TestProviderRunnerUsesSharedPullProgressPrinter(t *testing.T) {
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		pullOllamaModel = originalPull
	})

	pullOllamaModel = func(
		_ context.Context,
		_ string,
		_ string,
		_ map[string]string,
		onProgress func(provider_ollama.PullProgress),
	) error {
		for _, progress := range []provider_ollama.PullProgress{
			{Status: "pulling manifest"},
			{
				Status:    "pulling a",
				Completed: 10,
				Total:     100,
			},
			{
				Status:    "pulling b",
				Completed: 20,
				Total:     100,
			},
			{
				Status:    "pulling c",
				Completed: 30,
				Total:     100,
			},
			{
				Status:    "pulling c",
				Completed: 30,
				Total:     100,
			},
			{
				Status:    "pulling d",
				Completed: 40,
				Total:     100,
			},
			{Status: "verifying sha256 digest"},
			{Status: "success"},
			{Status: "success"},
		} {
			onProgress(progress)
		}

		return nil
	}

	var output bytes.Buffer
	err := providerRunner{output: &output}.pullMissingLocalModel(
		context.Background(),
		ProviderOptions{Provider: constants.ModelProviderOllama},
		setupSelection{
			provider:          constants.ModelProviderOllama,
			baseURL:           "http://127.0.0.1:11434",
			model:             "missing:latest",
			localModelMissing: true,
			pullAnswered:      true,
			pullSelected:      true,
		},
	)

	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		"Ollama pull: pulling b 20%",
		"Ollama pull: pulling c 30%",
		"Ollama pull: pulling d 40%",
		"Ollama pull: verifying sha256 digest",
		"Ollama pull: success",
		"",
	}, "\n"), output.String())
}

func TestProviderRunnerSkipsPullWhenWizardDeclines(t *testing.T) {
	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, nil
	}
	pullOllamaModel = func(
		context.Context,
		string,
		string,
		map[string]string,
		func(provider_ollama.PullProgress),
	) error {
		t.Fatal("pull should not run when the user declines")
		return nil
	}

	runner := providerRunner{
		output: io.Discard,
	}

	err := runner.pullMissingLocalModel(context.Background(), ProviderOptions{
		Provider: constants.ModelProviderOllama,
	}, setupSelection{
		provider:          constants.ModelProviderOllama,
		baseURL:           "http://127.0.0.1:11434",
		model:             "missing:latest",
		localModelMissing: true,
		pullAnswered:      true,
		pullSelected:      false,
	})

	require.NoError(t, err)
}

func TestRunProviderSuppressesPullProgress(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	originalDiscover := discoverOllamaModels
	originalPull := pullOllamaModel
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
		pullOllamaModel = originalPull
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		t.Fatal("discovery should not run when --pull and --model are provided")
		return nil, nil
	}
	pullOllamaModel = func(
		_ context.Context,
		_ string,
		_ string,
		_ map[string]string,
		onProgress func(provider_ollama.PullProgress),
	) error {
		require.Nil(t, onProgress)
		return nil
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    "http://127.0.0.1:11434",
		Model:      "llama3.2:3b",
		Pull:       true,
		PullQuiet:  true,
	})

	require.NoError(t, err)
	require.NotContains(t, output.String(), "Ollama pull:")
}

func TestRunProviderRejectsUnsupportedAPI(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	_, err := RunProvider(context.Background(), ProviderOptions{
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		Model:      "qwen3:8b",
		API:        "wrong",
	})

	require.ErrorContains(t, err, "model API must be one of:")
}

func TestRunProviderRejectsUnknownProvider(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	_, err := RunProvider(context.Background(), ProviderOptions{
		ConfigPath: configPath,
		Provider:   "missing",
		Model:      "model",
	})

	require.ErrorContains(t, err, "model provider must be one of:")
	require.ErrorContains(t, err, constants.ModelProviderOpenAI)
	require.ErrorContains(t, err, constants.ModelProviderOllama)
}

func TestRunProviderReturnsDiscoveryError(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, os.ErrPermission
	}

	_, err := RunProvider(context.Background(), ProviderOptions{
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    "http://127.0.0.1:11434",
		Model:      "qwen3:8b",
	})

	require.ErrorIs(t, err, os.ErrPermission)
}

func TestRunProviderReturnsPullError(t *testing.T) {
	configPath := setupProviderTestProfile(t, "local")

	originalPull := pullOllamaModel
	t.Cleanup(func() {
		pullOllamaModel = originalPull
	})

	pullOllamaModel = func(
		_ context.Context,
		_ string,
		_ string,
		_ map[string]string,
		onProgress func(provider_ollama.PullProgress),
	) error {
		onProgress(provider_ollama.PullProgress{Status: "pulling manifest"})
		return errSetupTestPull
	}

	var output bytes.Buffer
	_, err := RunProvider(context.Background(), ProviderOptions{
		Output:     &output,
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOllama,
		BaseURL:    "http://127.0.0.1:11434",
		Model:      "qwen3:8b",
		Pull:       true,
	})

	require.ErrorIs(t, err, errSetupTestPull)
	require.Equal(t, "Ollama pull: pulling manifest\n", output.String())
}

func TestRunProviderReturnsPersistError(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")

	originalSetConfig := setConfigValuesRelaxed
	t.Cleanup(func() {
		setConfigValuesRelaxed = originalSetConfig
	})

	setConfigValuesRelaxed = func(string, string, []config.ConfigUpdate) ([]string, error) {
		return nil, errSetupTestWrite
	}

	_, err := RunProvider(context.Background(), ProviderOptions{
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
		APIKey:     "openai-key",
	})

	require.ErrorIs(t, err, errSetupTestWrite)
}

func TestRunProviderReturnsConfigLoadError(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")
	require.NoError(t, os.WriteFile(configPath, []byte("models: ["), 0o600))

	_, err := RunProvider(context.Background(), ProviderOptions{
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
	})

	require.ErrorContains(t, err, "failed to parse config file")
}

func TestRunProviderReturnsSelectionErrors(t *testing.T) {
	runner := providerRunner{
		input:    strings.NewReader(""),
		output:   io.Discard,
		registry: modelprovider.NewRegistry(nil, nil, nil),
	}

	_, err := runner.getSetupProvider(context.Background(), ProviderOptions{}, config.NewProfileConfig())
	require.EqualError(t, err, "no model providers are available")

	runner = providerRunner{
		input:  strings.NewReader(""),
		output: io.Discard,
		registry: modelprovider.NewRegistry(
			nil,
			[]modelprovider.ProviderDefinition{{
				ID:             "custom",
				DisplayName:    "Custom",
				SupportsModels: true,
			}},
			nil,
		),
	}
	_, err = runner.getSetupModel(
		context.Background(),
		ProviderOptions{Refresh: true},
		config.NewProfileConfig(),
		modelprovider.ProviderDefinition{ID: "custom"},
		"https://custom.example/v1",
	)
	require.EqualError(t, err, "models unavailable")
}

func TestProviderRunnerReturnsInvalidProviderFromNonPagedSelection(t *testing.T) {
	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(context.Context, string, []selectChoice) (string, error) {
			return "missing", nil
		},
	}

	_, err := runner.getSetupSelection(
		context.Background(),
		ProviderOptions{Refresh: true},
		config.NewProfileConfig(),
	)

	require.ErrorContains(t, err, "model provider must be one of:")
}

func TestProviderRunnerReturnsInvalidAPIFromNonPagedSelection(t *testing.T) {
	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(context.Context, string, []selectChoice) (string, error) {
			return constants.ModelProviderOpenAI, nil
		},
	}

	_, err := runner.getSetupSelection(
		context.Background(),
		ProviderOptions{API: "wrong"},
		config.NewProfileConfig(),
	)

	require.ErrorContains(t, err, "model API must be one of:")
}

func TestProviderRunnerReturnsNonCredentialPagedSetupError(t *testing.T) {
	runner := providerRunner{registry: modelprovider.DefaultRegistry()}

	paged, err := runner.shouldRunPagedSetup(
		context.Background(),
		ProviderOptions{
			Provider: constants.ModelProviderOpenAI,
			Model:    "gpt-5.5",
			APIKey:   "openai-key",
			Registry: modelprovider.DefaultRegistry(),
		},
		nil,
	)

	require.False(t, paged)
	require.EqualError(t, err, "config is required")
}

func TestProviderRunnerRequiresPagedSetupForMissingOllamaModel(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, nil
	}

	paged, err := providerRunner{registry: modelprovider.DefaultRegistry()}.shouldRunPagedSetup(
		context.Background(),
		ProviderOptions{
			Provider: constants.ModelProviderOllama,
			BaseURL:  "http://127.0.0.1:11434",
			Model:    "missing:latest",
			Registry: modelprovider.DefaultRegistry(),
		},
		config.NewProfileConfig(),
	)

	require.NoError(t, err)
	require.True(t, paged)
}

func TestProviderRunnerReturnsProviderSelectionError(t *testing.T) {
	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(context.Context, string, []selectChoice) (string, error) {
			return "", errSetupTestSelector
		},
	}

	_, err := runner.getSetupSelection(
		context.Background(),
		ProviderOptions{},
		config.NewProfileConfig(),
	)

	require.ErrorIs(t, err, errSetupTestSelector)
}

func TestProviderRunnerSelectsProviderFromCatalog(t *testing.T) {
	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(_ context.Context, title string, choices []selectChoice) (string, error) {
			require.Equal(t, "Select a provider", title)
			require.NotEmpty(t, choices)
			return constants.ModelProviderOpenAI, nil
		},
	}

	provider, err := runner.getSetupProvider(
		context.Background(),
		ProviderOptions{},
		config.NewProfileConfig(),
	)

	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAI, provider)
}

func TestProviderRunnerReturnsModelSelectionError(t *testing.T) {
	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(_ context.Context, title string, _ []selectChoice) (string, error) {
			require.Equal(t, "Select a model", title)
			return "", errSetupTestSelector
		},
	}

	_, err := runner.getSetupSelection(
		context.Background(),
		ProviderOptions{Provider: constants.ModelProviderOpenAI},
		config.NewProfileConfig(),
	)

	require.ErrorIs(t, err, errSetupTestSelector)
}

func TestProviderRunnerSelectsModelFromCatalog(t *testing.T) {
	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(_ context.Context, title string, choices []selectChoice) (string, error) {
			require.Equal(t, "Select a model", title)
			require.NotEmpty(t, choices)
			return choices[0].ID, nil
		},
	}

	model, err := runner.getSetupModel(
		context.Background(),
		ProviderOptions{},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOpenAI),
		constants.DefaultOpenAIBaseURL,
	)

	require.NoError(t, err)
	require.NotEmpty(t, model.id)
	require.False(t, model.localMissing)
}

func TestProviderRunnerReturnsSelectedOllamaModelMissingCheckError(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	calls := 0
	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return nil, os.ErrPermission
	}

	runner := providerRunner{
		registry: modelprovider.DefaultRegistry(),
		selector: func(_ context.Context, title string, choices []selectChoice) (string, error) {
			require.Equal(t, "Select a model", title)
			require.NotEmpty(t, choices)
			require.Contains(t, choices[0].Description, "not installed")
			return choices[0].ID, nil
		},
	}
	_, err := runner.getSetupModel(
		context.Background(),
		ProviderOptions{Refresh: true},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOllama),
		constants.DefaultOllamaBaseURL,
	)

	require.ErrorIs(t, err, os.ErrPermission)
}

func TestProviderRunnerReturnsModelOptionError(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, os.ErrPermission
	}

	runner := providerRunner{registry: modelprovider.DefaultRegistry()}
	_, err := runner.getSetupModel(
		context.Background(),
		ProviderOptions{Refresh: true},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOllama),
		"http://127.0.0.1:11434",
	)

	require.ErrorIs(t, err, os.ErrPermission)
}

func TestProviderRunnerReturnsSelectedModelMissingCheckError(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, os.ErrPermission
	}

	runner := providerRunner{registry: modelprovider.DefaultRegistry()}
	_, err := runner.getSetupModel(
		context.Background(),
		ProviderOptions{Model: "missing:latest"},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOllama),
		"http://127.0.0.1:11434",
	)

	require.ErrorIs(t, err, os.ErrPermission)
}

func TestProviderRunnerFallsBackToCatalogWhenOllamaHasNoLiveModels(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, nil
	}

	runner := providerRunner{registry: modelprovider.DefaultRegistry()}
	options, fromLiveDiscovery, err := runner.getModelOptions(
		context.Background(),
		ProviderOptions{Refresh: true},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOllama),
		"",
		"http://127.0.0.1:11434",
	)

	require.NoError(t, err)
	require.False(t, fromLiveDiscovery)
	require.NotEmpty(t, options)
	require.True(t, options[0].LocalMissing)
}

func TestProviderRunnerMergesLiveAndCataloguedOllamaModels(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{
			ID:  "local:latest",
			API: modelprovider.APIOllamaNative,
		}}, nil
	}

	runner := providerRunner{registry: modelprovider.DefaultRegistry()}
	options, fromLiveDiscovery, err := runner.getModelOptions(
		context.Background(),
		ProviderOptions{Refresh: true},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOllama),
		"",
		"http://127.0.0.1:11434",
	)

	require.NoError(t, err)
	require.True(t, fromLiveDiscovery)
	require.Equal(t, "local:latest", options[0].ID)
	require.False(t, options[0].LocalMissing)
	require.Contains(t, getSetupModelIDs(options), constants.DefaultOllamaModel)
	require.True(t, getSetupModelOption(t, options, constants.DefaultOllamaModel).LocalMissing)
}

func TestListModelOptionsUsesLocalAwareCatalogPath(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(_ context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
		require.Equal(t, "http://configured.local:11434", baseURL)
		return []modelprovider.ModelDefinition{{
			ID:  "local:latest",
			API: modelprovider.APIOllamaNative,
		}}, nil
	}

	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOllama: {
			BaseURL: "http://configured.local:11434",
		},
	}
	options, hasInstalled, err := ListModelOptions(context.Background(), ModelOptions{
		Provider: constants.ModelProviderOllama,
		Current:  constants.DefaultOllamaModel,
		Config:   cfg,
		Refresh:  true,
	})

	require.NoError(t, err)
	require.True(t, hasInstalled)
	require.Equal(t, "local:latest", options[0].ID)
	require.False(t, options[0].LocalMissing)
	require.True(t, getSetupModelOption(t, options, constants.DefaultOllamaModel).LocalMissing)
}

func TestListModelOptionsExcludesDiscoveredOllamaEmbeddingModels(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{
			{
				ID:  "chat:latest",
				API: modelprovider.APIOllamaNative,
			},
			{
				ID:  "nomic-embed-text:latest",
				API: modelprovider.APIOllamaEmbeddings,
			},
		}, nil
	}

	options, hasInstalled, err := ListModelOptions(context.Background(), ModelOptions{
		Provider: constants.ModelProviderOllama,
		Refresh:  true,
	})

	require.NoError(t, err)
	require.True(t, hasInstalled)
	require.Contains(t, getSetupModelIDs(options), "chat:latest")
	require.NotContains(t, getSetupModelIDs(options), "nomic-embed-text:latest")
}

func TestResolveModelOptionsBaseURLUsesExplicitAndProfileConfig(t *testing.T) {
	cfg := config.NewProfileConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.BaseURL = "http://main.local:11434"
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		"Ollama": {BaseURL: "http://provider.local:11434"},
	}

	require.Equal(t, "http://explicit.local:11434", ResolveModelOptionsBaseURL(ModelOptions{
		Provider: constants.ModelProviderOllama,
		BaseURL:  "http://explicit.local:11434",
		Config:   cfg,
	}))
	require.Equal(t, "http://main.local:11434", ResolveModelOptionsBaseURL(ModelOptions{
		Provider: constants.ModelProviderOllama,
		Config:   cfg,
	}))

	cfg.Models.Main.Provider = ""
	require.Equal(t, "http://provider.local:11434", ResolveModelOptionsBaseURL(ModelOptions{
		Provider: constants.ModelProviderOllama,
		Config:   cfg,
	}))
}

func TestResolveModelOptionsBaseURLNormalizesInheritedOllamaOpenAICompatibleBaseURL(t *testing.T) {
	cfg := config.NewProfileConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.API = modelprovider.APIOpenAICompletions
	cfg.Models.Main.BaseURL = "http://main.local:11434/v1"
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOllama: {BaseURL: "http://provider.local:11434/v1"},
	}

	require.Equal(t, "http://main.local:11434", ResolveModelOptionsBaseURL(ModelOptions{
		Provider: constants.ModelProviderOllama,
		Config:   cfg,
	}))

	cfg.Models.Main.Provider = ""
	require.Equal(t, "http://provider.local:11434", ResolveModelOptionsBaseURL(ModelOptions{
		Provider: constants.ModelProviderOllama,
		Config:   cfg,
	}))
}

func TestSetupWizardLabelsMissingOllamaCatalogModels(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{
			ID:  "local:latest",
			API: modelprovider.APIOllamaNative,
		}}, nil
	}

	model := setupWizardModel{
		ctx:    context.Background(),
		cfg:    config.NewProfileConfig(),
		opts:   ProviderOptions{Refresh: true},
		runner: providerRunner{registry: modelprovider.DefaultRegistry()},
		selection: setupSelection{
			provider: constants.ModelProviderOllama,
			baseURL:  constants.DefaultOllamaBaseURL,
		},
	}
	err := model.showModelPage(mustSetupProvider(t, constants.ModelProviderOllama))

	require.NoError(t, err)
	require.Equal(t, setupWizardStepModel, model.step)
	require.Contains(t, getSetupChoiceIDs(model.choices), "local:latest")
	defaultChoice := getSetupChoice(t, model.choices, constants.DefaultOllamaModel)
	require.Contains(t, defaultChoice.Description, "not installed")
	require.Contains(t, model.render(), "not installed")
}

func TestProviderRunnerDetectsMissingSelectedLocalModel(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{
			ID:  "installed:latest",
			API: modelprovider.APIOllamaNative,
		}}, nil
	}

	missing, err := providerRunner{}.checkLocalModelMissing(
		context.Background(),
		ProviderOptions{},
		config.NewProfileConfig(),
		mustSetupProvider(t, constants.ModelProviderOllama),
		"http://127.0.0.1:11434",
		"missing:latest",
	)

	require.NoError(t, err)
	require.True(t, missing)
}

func TestProviderRunnerTrustsExplicitLocalModelConfig(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		t.Fatal("discovery should not run when provider models are pinned in config")
		return nil, nil
	}

	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOllama: {
			Models: map[string]config.ProviderModelMetadata{
				"pinned:latest": {},
			},
		},
	}
	missing, err := providerRunner{}.checkLocalModelMissing(
		context.Background(),
		ProviderOptions{},
		cfg,
		mustSetupProvider(t, constants.ModelProviderOllama),
		"http://127.0.0.1:11434",
		"pinned:latest",
	)

	require.NoError(t, err)
	require.False(t, missing)
}

func TestHasExplicitProviderModels(t *testing.T) {
	require.False(t, hasExplicitProviderModels(nil, constants.ModelProviderOllama))
	require.False(t, hasExplicitProviderModels(config.NewProfileConfig(), constants.ModelProviderOllama))

	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOllama: {},
	}
	require.False(t, hasExplicitProviderModels(cfg, constants.ModelProviderOllama))
	require.False(t, hasExplicitProviderModels(cfg, constants.ModelProviderOpenAI))

	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		"Ollama": {
			Models: map[string]config.ProviderModelMetadata{
				"pinned:latest": {},
			},
		},
	}
	require.True(t, hasExplicitProviderModels(cfg, constants.ModelProviderOllama))
}

func TestSetupModelPageCopy(t *testing.T) {
	require.Equal(t, "Select an Ollama model", getSetupModelPageTitle(mustSetupProvider(t, constants.ModelProviderOllama)))
	require.Equal(t, "Select a model", getSetupModelPageTitle(mustSetupProvider(t, constants.ModelProviderOpenAI)))
	require.Equal(
		t,
		"Choose an installed or suggested Ollama model from http://127.0.0.1:11434.",
		getSetupModelPageDescription(mustSetupProvider(t, constants.ModelProviderOllama), "http://127.0.0.1:11434"),
	)
	require.Equal(
		t,
		"Choose an installed or suggested Ollama model from the configured Ollama base URL.",
		getSetupModelPageDescription(mustSetupProvider(t, constants.ModelProviderOllama), " "),
	)
	require.Equal(
		t,
		"Choose the model Morph should use for chat, one-shot commands, and summaries.",
		getSetupModelPageDescription(mustSetupProvider(t, constants.ModelProviderOpenAI), ""),
	)
}

func TestDiscoverOllamaModelsReturnsBaseURLError(t *testing.T) {
	_, err := discoverOllamaModels(context.Background(), "://")

	require.Error(t, err)
}

func TestPersistProviderSelectionReturnsWriteError(t *testing.T) {
	err := persistProviderSelection(ProviderOptions{ConfigPath: t.TempDir()}, setupSelection{
		provider: constants.ModelProviderOpenAI,
		api:      modelprovider.APIOpenAIResponses,
		baseURL:  constants.DefaultOpenAIBaseURL,
		model:    "gpt-5.5",
	})

	require.ErrorContains(t, err, "read config file")
}

func TestRunProviderUsesExistingBaseURLForMatchingProvider(t *testing.T) {
	configPath := setupProviderTestProfile(t, "work")
	_, err := config.SetConfigValuesRelaxed("", configPath, []config.ConfigUpdate{
		{Path: "models.main.provider", Value: constants.ModelProviderOpenAI},
		{Path: "models.main.name", Value: "gpt-5.5"},
		{Path: "models.main.baseUrl", Value: "https://custom.example/v1"},
	})
	require.NoError(t, err)

	_, err = RunProvider(context.Background(), ProviderOptions{
		ConfigPath: configPath,
		Provider:   constants.ModelProviderOpenAI,
		Model:      "gpt-5.5",
		APIKey:     "openai-key",
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "https://custom.example/v1", cfg.Models.Main.BaseURL)
}

func TestRunProviderReturnsErrorWhenProviderHasNoCatalog(t *testing.T) {
	runner := providerRunner{
		output: io.Discard,
		registry: modelprovider.NewRegistry(
			[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
			[]modelprovider.ProviderDefinition{{
				ID:             "custom",
				DefaultAPI:     modelprovider.APIOpenAIResponses,
				SupportsModels: true,
				BaseURLs: map[string]string{
					modelprovider.APIOpenAIResponses: "https://custom.example/v1",
				},
			}},
			nil,
		),
	}

	model, err := runner.getSetupModel(
		context.Background(),
		ProviderOptions{},
		config.NewProfileConfig(),
		modelprovider.ProviderDefinition{ID: "custom"},
		"https://custom.example/v1",
	)

	require.EqualError(t, err, "models unavailable")
	require.Equal(t, modelSelection{}, model)
}

func TestSelectorModelSupportsArrowAndNumericSelection(t *testing.T) {
	model := newSelectorModel("Pick one", []selectChoice{
		{ID: "first", Label: "First"},
		{ID: "second", Label: "Second"},
	})

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, cmd)
	require.Equal(t, 1, updated.(selectorModel).selected)

	updated, cmd = updated.(selectorModel).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, "second", updated.(selectorModel).choice)

	updated, cmd = model.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	require.NotNil(t, cmd)
	require.Equal(t, "second", updated.(selectorModel).choice)
}

func TestSelectorModelSupportsBoundaryKeysAndDefaults(t *testing.T) {
	model := newSelectorModel("Pick one", []selectChoice{
		{ID: "first", Label: "First"},
		{ID: "second", Label: "Second"},
	})

	require.Nil(t, model.Init())

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	require.Nil(t, cmd)
	require.Equal(t, 1, updated.(selectorModel).selected)

	updated, cmd = updated.(selectorModel).Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Nil(t, cmd)
	require.Equal(t, 0, updated.(selectorModel).selected)

	updated, cmd = updated.(selectorModel).Update(tea.KeyPressMsg{Code: tea.KeyHome})
	require.Nil(t, cmd)
	require.Equal(t, 0, updated.(selectorModel).selected)

	updated, cmd = updated.(selectorModel).Update(struct{}{})
	require.Nil(t, cmd)
	require.Equal(t, 0, updated.(selectorModel).selected)

	model.selected = 99
	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, "first", updated.(selectorModel).choice)
}

func TestSelectorModelCancelsAndRenders(t *testing.T) {
	model := newSelectorModel("Pick", []selectChoice{
		{
			ID:          "first",
			Label:       "First",
			Description: "id",
		},
	})

	require.Contains(t, model.render(), "1. First (id)")
	requireSetupAccentCursor(t, model.render(), "1. First")
	require.NotContains(t, model.render(), "backspace")
	require.NotNil(t, model.View())

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(selectorModel).err, "setup selection cancelled")

	updated, cmd = model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.EqualError(t, updated.(selectorModel).err, "setup selection cancelled")
}

func requireSetupAccentCursor(t *testing.T, rendered string, label string) {
	t.Helper()

	accentCursor := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tuirender.DefaultTheme.MarkdownLinkForeground)).
		Render(">")
	plainForegroundCursor := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tuirender.DefaultTheme.NoticeForeground)).
		Render(">")
	require.Contains(t, rendered, setupOptionIndent+accentCursor+" "+label)
	require.NotContains(t, rendered, "\n"+accentCursor+" "+label)
	require.NotContains(t, rendered, plainForegroundCursor+" "+label)
}

func requireSetupAPIKeyPromptAccent(t *testing.T, input textinput.Model) {
	t.Helper()

	styles := input.Styles()
	require.Equal(t, lipgloss.Color(tuirender.DefaultTheme.MarkdownLinkForeground), styles.Focused.Prompt.GetForeground())
	require.Equal(t, lipgloss.Color(tuirender.DefaultTheme.MarkdownLinkForeground), styles.Blurred.Prompt.GetForeground())
	require.Contains(t, input.View(), renderSetupAccent("> "))
}

func requireSetupIndentedLayer(t *testing.T, rendered string, layer string) {
	t.Helper()

	for _, line := range strings.Split(layer, "\n") {
		lineValue := str.String(line)
		if lineValue.Trim() == "" {
			continue
		}
		require.Contains(t, rendered, "\n"+setupOptionIndent+line)
		require.NotContains(t, rendered, "\n"+line)
	}
}

func requireSetupHelpGuideNotIndented(t *testing.T, rendered string, guide string) {
	t.Helper()

	for _, line := range strings.Split(guide, "\n") {
		lineValue2 := str.String(line)
		if lineValue2.Trim() == "" {
			continue
		}
		require.Contains(t, rendered, "\n"+line)
		require.NotContains(t, rendered, "\n"+setupOptionIndent+line)
	}
}

func TestSelectorModelReturnsErrorWhenEmpty(t *testing.T) {
	model := newSelectorModel("Pick", nil)

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	require.EqualError(t, updated.(selectorModel).err, "no setup options are available")
}

func TestProviderRunnerSelectChoiceHandlesProgramResults(t *testing.T) {
	originalRunner := runSetupSelectorProgram
	t.Cleanup(func() {
		runSetupSelectorProgram = originalRunner
	})

	runner := providerRunner{input: strings.NewReader(""), output: io.Discard}

	runSetupSelectorProgram = func(
		context.Context,
		io.Reader,
		io.Writer,
		selectorModel,
	) (tea.Model, error) {
		return selectorModel{choice: "second"}, nil
	}
	choice, err := runner.selectChoice(context.Background(), "Pick", []selectChoice{{ID: "second"}})
	require.NoError(t, err)
	require.Equal(t, "second", choice)

	runSetupSelectorProgram = func(
		context.Context,
		io.Reader,
		io.Writer,
		selectorModel,
	) (tea.Model, error) {
		return selectorModel{}, errSetupTestSelector
	}
	_, err = runner.selectChoice(context.Background(), "Pick", []selectChoice{{ID: "second"}})
	require.ErrorIs(t, err, errSetupTestSelector)

	runSetupSelectorProgram = func(
		context.Context,
		io.Reader,
		io.Writer,
		selectorModel,
	) (tea.Model, error) {
		return unavailableSetupModel{}, nil
	}
	_, err = runner.selectChoice(context.Background(), "Pick", []selectChoice{{ID: "second"}})
	require.EqualError(t, err, "setup selection unavailable")

	runSetupSelectorProgram = func(
		context.Context,
		io.Reader,
		io.Writer,
		selectorModel,
	) (tea.Model, error) {
		return selectorModel{err: errSetupTestSelector}, nil
	}
	_, err = runner.selectChoice(context.Background(), "Pick", []selectChoice{{ID: "second"}})
	require.ErrorIs(t, err, errSetupTestSelector)

	runSetupSelectorProgram = func(
		context.Context,
		io.Reader,
		io.Writer,
		selectorModel,
	) (tea.Model, error) {
		return selectorModel{}, nil
	}
	_, err = runner.selectChoice(context.Background(), "Pick", []selectChoice{{ID: "second"}})
	require.EqualError(t, err, "setup selection cancelled")
}

func TestRunSetupSelectorProgramReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runSetupSelectorProgram(
		ctx,
		strings.NewReader(""),
		io.Discard,
		newSelectorModel("Pick", []selectChoice{{ID: "first", Label: "First"}}),
	)

	require.Error(t, err)
}

func TestSelectionHelpers(t *testing.T) {
	require.Equal(t, 1, mustSelectionIndex(t, "1", 2))
	index, ok := numericSelectionIndex(tea.KeyPressMsg{Code: '2'}, 2)
	require.True(t, ok)
	require.Equal(t, 1, index)

	_, ok = numericSelectionIndex(tea.KeyPressMsg{Code: 'x'}, 2)
	require.False(t, ok)

	_, ok = parseSelectionIndex("3", 2)
	require.False(t, ok)
}

func TestFormatPullProgress(t *testing.T) {
	require.Empty(t, clibase.FormatPullProgress(provider_ollama.PullProgress{}))
	require.Equal(
		t,
		"Ollama pull: pulling manifest",
		clibase.FormatPullProgress(provider_ollama.PullProgress{Status: " pulling manifest "}),
	)
	require.Equal(
		t,
		"Ollama pull: downloading 25%",
		clibase.FormatPullProgress(provider_ollama.PullProgress{
			Status:    "downloading",
			Completed: 25,
			Total:     100,
		}),
	)
}

func setupProviderTestProfile(t *testing.T, name string) string {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{})

	home := t.TempDir()
	t.Setenv("HOME", home)
	profileHome := filepath.Join(home, ".morph", "profiles", name)
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	configPath := filepath.Join(profileHome, "config.yaml")
	cfg := config.NewProfileConfig()
	cfg.Name = name
	require.NoError(t, config.SaveYAML(configPath, cfg))

	return configPath
}

func mustSelectionIndex(t *testing.T, value string, length int) int {
	t.Helper()

	index, ok := parseSelectionIndex(value, length)
	require.True(t, ok)

	return index + 1
}

func mustSetupProvider(t *testing.T, id string) modelprovider.ProviderDefinition {
	t.Helper()

	provider, ok := modelprovider.DefaultRegistry().GetProvider(id)
	require.True(t, ok)

	return provider
}

func getSetupModelIDs(options []modelcatalog.Option) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}

	return ids
}

func getSetupModelOption(t *testing.T, options []modelcatalog.Option, id string) modelcatalog.Option {
	t.Helper()

	for _, option := range options {
		if option.ID == id {
			return option
		}
	}
	require.Failf(t, "missing model option", "model %q was not found", id)

	return modelcatalog.Option{}
}

func getSetupChoiceIDs(choices []selectChoice) []string {
	ids := make([]string, 0, len(choices))
	for _, choice := range choices {
		ids = append(ids, choice.ID)
	}

	return ids
}

func getSetupChoice(t *testing.T, choices []selectChoice, id string) selectChoice {
	t.Helper()

	for _, choice := range choices {
		if choice.ID == id {
			return choice
		}
	}
	require.Failf(t, "missing setup choice", "choice %q was not found", id)

	return selectChoice{}
}
