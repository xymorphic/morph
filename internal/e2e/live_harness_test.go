package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	models "github.com/xymorphic/morph/internal/model"
	modelclient "github.com/xymorphic/morph/internal/model/client"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	provider_openai "github.com/xymorphic/morph/internal/model/provider_openai"
	"github.com/xymorphic/morph/internal/profile"
)

type liveModelClientFactoryStub struct {
	newClient func(modelclient.ClientRequest) (models.Client, error)
}

func (s liveModelClientFactoryStub) NewClient(req modelclient.ClientRequest) (models.Client, error) {
	return s.newClient(req)
}

func TestNewLiveClients(t *testing.T) {
	originalFactory := liveModelClientFactoryInstance
	t.Cleanup(func() {
		liveModelClientFactoryInstance = originalFactory
	})

	t.Run("requires config", func(t *testing.T) {
		modelClient, summaryClient, err := NewLiveClients(nil)
		require.Error(t, err)
		assert.Nil(t, modelClient)
		assert.Nil(t, summaryClient)
		assert.EqualError(t, err, "live harness config is required")
	})

	t.Run("reuses main client when auth matches", func(t *testing.T) {
		retries := 3
		var calls []modelclient.ClientRequest
		liveModelClientFactoryInstance = liveModelClientFactoryStub{
			newClient: func(req modelclient.ClientRequest) (models.Client, error) {
				calls = append(calls, req)
				return &provider_openai.OpenAIClient{}, nil
			},
		}

		cfg := &config.Config{
			Models: config.ModelsConfig{
				Providers:  map[string]config.ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
				MaxRetries: &retries,
				Main:       config.MainModelConfig{Provider: "openrouter", BaseURL: "https://router.example/v1"},
			},
		}

		modelClient, summaryClient, err := NewLiveClients(cfg)
		require.NoError(t, err)
		require.NotNil(t, modelClient)
		require.NotNil(t, summaryClient)
		assert.Same(t, modelClient, summaryClient)
		require.Len(t, calls, 1)
		assert.Equal(t, modelclient.ModelRoleMain, calls[0].Role)
		assert.Equal(t, "router-key", calls[0].APIKey)
		assert.Equal(t, "https://router.example/v1", calls[0].BaseURL)
		assert.Equal(t, 3, calls[0].MaxRetries)
	})

	t.Run("builds distinct summary client when auth differs", func(t *testing.T) {
		retries := 1
		var calls []modelclient.ClientRequest
		liveModelClientFactoryInstance = liveModelClientFactoryStub{
			newClient: func(req modelclient.ClientRequest) (models.Client, error) {
				calls = append(calls, req)
				return &provider_openai.OpenAIClient{}, nil
			},
		}

		cfg := &config.Config{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderModelConfig{
					"openrouter": {APIKey: "router-key"},
					"openai":     {APIKey: "openai-key"},
				},
				MaxRetries: &retries,
				Main:       config.MainModelConfig{Provider: "openrouter", Name: "openai/gpt-4o-mini"},
				Summary:    config.SummaryModelConfig{Provider: "openai", Name: "gpt-4o-mini", BaseURL: "https://openai.example/v1"},
			},
		}

		modelClient, summaryClient, err := NewLiveClients(cfg)
		require.NoError(t, err)
		require.NotNil(t, modelClient)
		require.NotNil(t, summaryClient)
		assert.NotSame(t, modelClient, summaryClient)
		assert.Equal(t, []modelclient.ClientRequest{
			{
				Role:       modelclient.ModelRoleMain,
				Model:      "openai/gpt-4o-mini",
				Provider:   "openrouter",
				API:        modelprovider.APIOpenAIResponses,
				APIKey:     "router-key",
				BaseURL:    constants.DefaultOpenRouterResponsesBaseURL,
				MaxRetries: 1,
			},
			{
				Role:       modelclient.ModelRoleSummary,
				Model:      "gpt-4o-mini",
				Provider:   "openai",
				API:        modelprovider.APIOpenAIResponses,
				APIKey:     "openai-key",
				BaseURL:    "https://openai.example/v1",
				MaxRetries: 1,
			},
		}, calls)
	})

	t.Run("returns factory errors", func(t *testing.T) {
		liveModelClientFactoryInstance = liveModelClientFactoryStub{
			newClient: func(modelclient.ClientRequest) (models.Client, error) {
				return nil, errors.New("client failed")
			},
		}

		cfg := &config.Config{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
				Main:      config.MainModelConfig{Provider: "openrouter", Name: "openai/gpt-4o-mini"},
			},
		}

		modelClient, summaryClient, err := NewLiveClients(cfg)
		require.Error(t, err)
		assert.Nil(t, modelClient)
		assert.Nil(t, summaryClient)
		assert.EqualError(t, err, "client failed")
	})

	t.Run("returns main auth error", func(t *testing.T) {
		setLiveHarnessTestProfile(t)
		t.Setenv("OPENROUTER_API_KEY", "")
		liveModelClientFactoryInstance = originalFactory

		cfg := &config.Config{
			Models: config.ModelsConfig{
				Main: config.MainModelConfig{Provider: "openrouter", Name: "openai/gpt-4o-mini"},
			},
		}

		modelClient, summaryClient, err := NewLiveClients(cfg)
		require.Error(t, err)
		assert.Nil(t, modelClient)
		assert.Nil(t, summaryClient)
		assert.ErrorContains(t, err, `model API key is required for provider "openrouter"`)
	})

	t.Run("returns summary auth error", func(t *testing.T) {
		setLiveHarnessTestProfile(t)
		t.Setenv("OPENAI_API_KEY", "")
		liveModelClientFactoryInstance = originalFactory

		cfg := &config.Config{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
				Main:      config.MainModelConfig{Provider: "openrouter", Name: "openai/gpt-4o-mini"},
				Summary:   config.SummaryModelConfig{Provider: "openai", Name: "gpt-4o-mini"},
			},
		}

		modelClient, summaryClient, err := NewLiveClients(cfg)
		require.Error(t, err)
		assert.Nil(t, modelClient)
		assert.Nil(t, summaryClient)
		assert.ErrorContains(t, err, `model API key is required for provider "openai"`)
	})

	t.Run("returns summary client factory error", func(t *testing.T) {
		liveModelClientFactoryInstance = liveModelClientFactoryStub{
			newClient: func(req modelclient.ClientRequest) (models.Client, error) {
				if req.APIKey == "openai-key" {
					return nil, errors.New("summary client failed")
				}

				return &provider_openai.OpenAIClient{}, nil
			},
		}

		retries := 1
		cfg := &config.Config{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderModelConfig{
					"openrouter": {APIKey: "router-key"},
					"openai":     {APIKey: "openai-key"},
				},
				MaxRetries: &retries,
				Main:       config.MainModelConfig{Provider: "openrouter"},
				Summary:    config.SummaryModelConfig{Provider: "openai", BaseURL: "https://openai.example/v1"},
			},
		}

		modelClient, summaryClient, err := NewLiveClients(cfg)
		require.Error(t, err)
		assert.Nil(t, modelClient)
		assert.Nil(t, summaryClient)
		assert.EqualError(t, err, "summary client failed")
	})
}

func setLiveHarnessTestProfile(t *testing.T) {
	t.Helper()

	original := profile.Active()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    "live-harness-test",
		HomeDir: t.TempDir(),
	}))
	t.Cleanup(func() {
		profile.SetActive(original)
	})
}

func TestNewLiveHarnessAndRPCHarness(t *testing.T) {
	originalFactory := liveModelClientFactoryInstance
	originalLoad := loadLiveConfig
	originalNewHarness := newLiveHarness
	originalNewRPCHarness := newLiveRPCHarness
	t.Cleanup(func() {
		liveModelClientFactoryInstance = originalFactory
		loadLiveConfig = originalLoad
		newLiveHarness = originalNewHarness
		newLiveRPCHarness = originalNewRPCHarness
	})

	liveModelClientFactoryInstance = liveModelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}

	writeConfig := func(t *testing.T) (string, string) {
		t.Helper()

		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env")
		configPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(envPath, []byte("OPENROUTER_API_KEY=test-key\n"), 0o600))
		require.NoError(t, os.WriteFile(configPath, []byte(`
name: live-test
models:
  main:
    name: openai/gpt-4o-mini
    provider: openrouter
rpc:
  address: 127.0.0.1
  port: 50051
storage:
  backend: sqlite
`), 0o600))
		return envPath, configPath
	}

	t.Run("new live harness loads config", func(t *testing.T) {
		envPath, configPath := writeConfig(t)

		h, err := NewLiveHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), envPath, configPath)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, h.Close())
		})

		require.NotNil(t, h.Config())
		assert.Equal(t, "live-test", h.Config().Name)
	})

	t.Run("new live rpc harness loads config", func(t *testing.T) {
		envPath, configPath := writeConfig(t)

		h, err := NewLiveRPCHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), envPath, configPath)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, h.Close())
		})

		require.NotNil(t, h.Config())
		assert.Equal(t, "live-test", h.Config().Name)
	})

	t.Run("returns config load error", func(t *testing.T) {
		loadLiveConfig = func(string, string) (*config.Config, error) {
			return nil, errors.New("load failed")
		}

		_, err := NewLiveHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), "", "config.yaml")
		require.Error(t, err)
		assert.EqualError(t, err, "load failed")
	})

	t.Run("returns live client error", func(t *testing.T) {
		previousFactory := liveModelClientFactoryInstance
		defer func() {
			liveModelClientFactoryInstance = previousFactory
		}()

		loadLiveConfig = func(string, string) (*config.Config, error) {
			return &config.Config{
				Models: config.ModelsConfig{
					Providers: map[string]config.ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
					Main:      config.MainModelConfig{Provider: "openrouter"},
				},
			}, nil
		}
		liveModelClientFactoryInstance = liveModelClientFactoryStub{
			newClient: func(modelclient.ClientRequest) (models.Client, error) {
				return nil, errors.New("client failed")
			},
		}

		_, err := NewLiveHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), "", "config.yaml")
		require.Error(t, err)
		assert.EqualError(t, err, "client failed")
	})

	t.Run("returns harness construction error", func(t *testing.T) {
		loadLiveConfig = func(string, string) (*config.Config, error) {
			return &config.Config{
				Models: config.ModelsConfig{Providers: map[string]config.ProviderModelConfig{"openrouter": {APIKey: "router-key"}}, Main: config.MainModelConfig{Provider: "openrouter"}},
			}, nil
		}
		newLiveHarness = func(context.Context, HarnessOptions) (*Harness, error) {
			return nil, errors.New("harness failed")
		}

		_, err := NewLiveHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), "", "config.yaml")
		require.Error(t, err)
		assert.EqualError(t, err, "harness failed")
	})

	t.Run("returns rpc harness construction error", func(t *testing.T) {
		loadLiveConfig = func(string, string) (*config.Config, error) {
			return &config.Config{
				Models: config.ModelsConfig{Providers: map[string]config.ProviderModelConfig{"openrouter": {APIKey: "router-key"}}, Main: config.MainModelConfig{Provider: "openrouter"}},
			}, nil
		}
		newLiveRPCHarness = func(context.Context, HarnessOptions) (*RPCHarness, error) {
			return nil, errors.New("rpc harness failed")
		}

		_, err := NewLiveRPCHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), "", "config.yaml")
		require.Error(t, err)
		assert.EqualError(t, err, "rpc harness failed")
	})

	t.Run("returns rpc config load error", func(t *testing.T) {
		loadLiveConfig = func(string, string) (*config.Config, error) {
			return nil, errors.New("load failed")
		}

		_, err := NewLiveRPCHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), "", "config.yaml")
		require.Error(t, err)
		assert.EqualError(t, err, "load failed")
	})

	t.Run("returns rpc live client error", func(t *testing.T) {
		loadLiveConfig = func(string, string) (*config.Config, error) {
			return &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{Provider: "openrouter"}}}, nil
		}

		_, err := NewLiveRPCHarness(context.Background(), filepath.Join(t.TempDir(), "morph-home"), "", "config.yaml")
		require.Error(t, err)
	})
}

func TestDefaultLiveArtifactDir(t *testing.T) {
	dir := DefaultLiveArtifactDir("")
	require.NotEmpty(t, dir)
	assert.Contains(t, dir, "morph-live-artifacts")

	assert.Equal(t, "/tmp/custom-artifacts", DefaultLiveArtifactDir(" /tmp/custom-artifacts "))
}

func TestRunLiveScenario(t *testing.T) {
	originalNow := liveNow
	t.Cleanup(func() {
		liveNow = originalNow
	})

	t.Run("writes passed artifact", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
		calls := 0
		liveNow = func() time.Time {
			calls++
			return now.Add(time.Duration(calls-1) * time.Second)
		}

		artifact, err := RunLiveScenario(
			"Simple Answer",
			"say alpha",
			dir,
			func(string) (string, error) { return "ALPHA", nil },
			func(output string) error {
				if output != "ALPHA" {
					return errors.New("unexpected output")
				}

				return nil
			},
		)
		require.NoError(t, err)
		assert.Equal(t, LiveClassificationPassed, artifact.Classification)
		assert.Equal(t, "ALPHA", artifact.Output)

		raw, readErr := os.ReadFile(filepath.Join(dir, "simple-answer.json"))
		require.NoError(t, readErr)

		var written LiveArtifact
		require.NoError(t, json.Unmarshal(raw, &written))
		assert.Equal(t, LiveClassificationPassed, written.Classification)
		assert.Equal(t, "say alpha", written.Prompt)
	})

	t.Run("classifies command error", func(t *testing.T) {
		dir := t.TempDir()
		liveNow = func() time.Time { return time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC) }

		artifact, err := RunLiveScenario(
			"Command Error",
			"run",
			dir,
			func(string) (string, error) { return "", errors.New("rpc failed") },
			nil,
		)
		require.Error(t, err)
		assert.Equal(t, LiveClassificationCommandError, artifact.Classification)
		assert.Equal(t, "rpc failed", artifact.Error)

		raw, readErr := os.ReadFile(filepath.Join(dir, "command-error.json"))
		require.NoError(t, readErr)

		var written LiveArtifact
		require.NoError(t, json.Unmarshal(raw, &written))
		assert.Equal(t, LiveClassificationCommandError, written.Classification)
	})

	t.Run("classifies expectation failure", func(t *testing.T) {
		dir := t.TempDir()
		liveNow = func() time.Time { return time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC) }

		artifact, err := RunLiveScenario(
			"Expectation Failed",
			"check",
			dir,
			func(string) (string, error) { return "wrong", nil },
			func(string) error { return errors.New("missing token") },
		)
		require.Error(t, err)
		assert.Equal(t, LiveClassificationExpectationFailed, artifact.Classification)
		assert.Equal(t, "missing token", artifact.Error)

		raw, readErr := os.ReadFile(filepath.Join(dir, "expectation-failed.json"))
		require.NoError(t, readErr)

		var written LiveArtifact
		require.NoError(t, json.Unmarshal(raw, &written))
		assert.Equal(t, LiveClassificationExpectationFailed, written.Classification)
	})

	t.Run("returns artifact write errors", func(t *testing.T) {
		originalWrite := writeLiveArtifactFile
		originalMkdir := mkdirAllLiveArtifacts
		t.Cleanup(func() {
			writeLiveArtifactFile = originalWrite
			mkdirAllLiveArtifacts = originalMkdir
		})

		mkdirAllLiveArtifacts = func(string, os.FileMode) error { return nil }
		writeLiveArtifactFile = func(string, []byte, os.FileMode) error {
			return errors.New("write failed")
		}

		_, err := RunLiveScenario(
			"Write Failed",
			"prompt",
			t.TempDir(),
			func(string) (string, error) { return "ok", nil },
			nil,
		)
		require.Error(t, err)
		assert.EqualError(t, err, "write failed")
	})
}
