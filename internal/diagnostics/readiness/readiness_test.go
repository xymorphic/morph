package readiness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/storememory"
)

func TestReport_HasFailuresAndSummary(t *testing.T) {
	report := Report{Groups: []Group{
		{
			Name: "models",
			Checks: []Check{
				check("main", StatusPass, "ready"),
				check("summary", StatusFail, "missing auth"),
			},
		},
	}}

	require.True(t, report.HasFailures())
	require.Equal(t, "models summary: missing auth", report.Summary())

	report.Groups[0].Checks[1].Status = StatusWarn
	require.False(t, report.HasFailures())
	require.Equal(t, "readiness checks passed", report.Summary())
}

func TestBuildPermissionGroup_ReportsUnsafeAndImpossiblePolicies(t *testing.T) {
	fullAccess := readyConfig()
	fullAccess.Permissions.Preset = permissions.PresetFullAccess
	group := buildPermissionGroup(context.Background(), fullAccess, profile.Profile{})
	require.Equal(t, StatusWarn, getReadinessGroupCheck(t, group, "policy").Status)

	unattendedAsk := readyConfig()
	unattendedAsk.Normalize()
	unattendedAsk.Permissions.SurfaceKindDefaults[permissions.SurfaceKindAutomation] = permissions.DecisionAsk
	group = buildPermissionGroup(context.Background(), unattendedAsk, profile.Profile{})
	check := getReadinessGroupCheck(t, group, "unattended approvals")
	require.Equal(t, StatusFail, check.Status)
	require.Contains(t, check.Message, "automation surfaces")

	exactAsk := readyConfig()
	exactAsk.Normalize()
	exactAsk.Permissions.SurfaceDefaults[permissions.SurfaceSlack] = permissions.DecisionAsk
	group = buildPermissionGroup(context.Background(), exactAsk, profile.Profile{})
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "unattended approvals").Status)

	invalid := readyConfig()
	invalid.Permissions.Preset = "invalid"
	group = buildPermissionGroup(context.Background(), invalid, profile.Profile{})
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "policy").Status)

	group = buildPermissionGroup(context.Background(), nil, profile.Profile{})
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "config").Status)
}

func TestBuildPermissionGroup_ReportsStaleActiveGrants(t *testing.T) {
	originalOpen := openPermissionReadinessStore
	t.Cleanup(func() { openPermissionReadinessStore = originalOpen })
	store := storememory.NewStore()
	now := time.Now()
	request, _, err := store.CreateApprovalRequest(context.Background(), permissions.ApprovalRequest{
		ID: "approval_stale", Fingerprint: "fingerprint", Actor: permissions.Actor{Kind: permissions.ActorLocalOwner},
		SurfaceKind: permissions.SurfaceKindLocal, Surface: permissions.SurfaceCLI,
		Resource: permissions.ResourceFile, Action: permissions.ActionRead,
		Status: permissions.ApprovalPending, CreatedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-2 * time.Hour),
	})
	require.NoError(t, err)
	_, err = store.ResolveApprovalRequest(
		context.Background(), request.ID, permissions.ApprovalApproved, permissions.GrantSession, now.Add(-2*time.Hour),
	)
	require.NoError(t, err)
	_, err = store.CreateApprovalGrant(context.Background(), permissions.ApprovalGrant{
		ID: "grant_stale", RequestID: request.ID, Fingerprint: "fingerprint", Actor: permissions.Actor{Kind: permissions.ActorLocalOwner},
		Scope: permissions.GrantSession, Status: permissions.GrantActive,
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	})
	require.NoError(t, err)
	openPermissionReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return store, nil
	}

	group := buildPermissionGroup(context.Background(), readyConfig(), profile.Profile{})
	check := getReadinessGroupCheck(t, group, "grants")
	require.Equal(t, StatusWarn, check.Status)
	require.Equal(t, "1 active grants are stale", check.Message)
}

func TestBuildPermissionGroup_ReportsGrantStoreFailure(t *testing.T) {
	originalOpen := openPermissionReadinessStore
	t.Cleanup(func() { openPermissionReadinessStore = originalOpen })
	store := storememory.NewStore()
	openPermissionReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return automationReadinessStoreStub{
			permissionStore: permissionListErrorStore{ApprovalStore: store, err: errors.New("list failed")},
		}, nil
	}

	group := buildPermissionGroup(context.Background(), readyConfig(), profile.Profile{})
	check := getReadinessGroupCheck(t, group, "grants")
	require.Equal(t, StatusFail, check.Status)
	require.Equal(t, "list failed", check.Message)
}

func getReadinessGroupCheck(t *testing.T, group Group, name string) Check {
	t.Helper()
	for _, candidate := range group.Checks {
		if candidate.Name == name {
			return candidate
		}
	}
	require.FailNow(t, "check not found", name)

	return Check{}
}

func TestBuild_ReportsProfileAndMissingDaemonWithoutFailure(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("name: test\n"), 0o600))
	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: home})
	cfg := readyConfig()

	report := Build(context.Background(), Options{
		Config:     cfg,
		Profile:    active,
		ConfigPath: configPath,
		EnvPath:    filepath.Join(home, ".env"),
	})

	require.False(t, report.HasFailures())
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "profile", "home").Status)
	safety := findReadinessCheck(t, report, "safety", "policy")
	require.Equal(t, StatusPass, safety.Status)
	require.Equal(t, "input=enabled, output=enabled, pii=enabled", safety.Message)
	memory := findReadinessCheck(t, report, "memory", "status")
	require.Equal(t, StatusPass, memory.Status)
	require.Contains(t, memory.Message, `enabled, provider="default-memory", backend="sqlite"`)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "pinned").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "retrieval").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "flush").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "episodic").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "reflection").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "promotion").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "memory", "write").Status)
	compaction := findReadinessCheck(t, report, "session", "compaction")
	require.Equal(t, StatusPass, compaction.Status)
	require.Equal(t, "enabled, triggerPercent=0.85, warnPercent=0.95, recentSessionTail=8", compaction.Message)
	web := findReadinessCheck(t, report, "tools", "web tools")
	require.Equal(t, StatusWarn, web.Status)
	require.Equal(t, "native web extraction is configured; web search requires a configured web provider", web.Message)
	daemon := findReadinessCheck(t, report, "daemon", "runtime")
	require.Equal(t, StatusWarn, daemon.Status)
	require.Contains(t, daemon.Message, "runtime metadata is not present")
	require.Equal(t, "morph daemon", daemon.Actions[0].Command)
}

func TestBuild_ReportsModelAuthWithoutLeakingCredentials(t *testing.T) {
	cfg := readyConfig()
	cfg.Models.Providers[constants.ModelProviderOpenRouter] = config.ProviderModelConfig{APIKey: "secret-openrouter-key"}

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	main := findReadinessCheck(t, report, "models", "main")
	require.Equal(t, StatusPass, main.Status)
	require.Contains(t, main.Message, "provider-config auth")
	embedding := findReadinessCheck(t, report, "models", "embedding")
	require.Equal(t, StatusWarn, embedding.Status)
	require.Contains(t, embedding.Message, "embedding model")
	require.Contains(t, embedding.Message, "vector search is disabled")
	require.NotContains(t, report.Summary()+main.Message, "secret-openrouter-key")
}

func TestBuild_ReportsOllamaReadiness(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	var discoveredBaseURL string
	discoverOllamaModels = func(_ context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
		discoveredBaseURL = baseURL
		return []modelprovider.ModelDefinition{
			{
				ID:            "llama3.2:3b",
				Provider:      constants.ModelProviderOllama,
				ContextWindow: 8192,
				SupportsTools: true,
			},
			{
				ID:       constants.DefaultOllamaEmbeddingModel,
				Provider: constants.ModelProviderOllama,
			},
		}, nil
	}

	cfg := readyConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.Name = "llama3.2:3b"
	cfg.Models.Main.BaseURL = "http://127.0.0.1:11434"
	cfg.Models.Main.ContextLength = 4096
	cfg.Models.Embedding.Provider = constants.ModelProviderOllama
	cfg.Models.Embedding.Name = constants.DefaultOllamaEmbeddingModel
	cfg.Models.Embedding.API = modelprovider.APIOllamaEmbeddings
	cfg.Models.Embedding.BaseURL = "http://127.0.0.1:11434"
	cfg.Search.Vector.Enabled = true

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	require.Equal(t, "http://127.0.0.1:11434", discoveredBaseURL)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "models", "ollama").Status)
	model := findReadinessCheck(t, report, "models", "ollama model")
	require.Equal(t, StatusPass, model.Status)
	require.Contains(t, model.Message, "installed")
	contextCheck := findReadinessCheck(t, report, "models", "ollama context")
	require.Equal(t, StatusPass, contextCheck.Status)
	require.Contains(t, contextCheck.Message, "context window=8192")
	tools := findReadinessCheck(t, report, "models", "ollama tools")
	require.Equal(t, StatusPass, tools.Status)
	require.Contains(t, tools.Message, "reports tool support")
	embeddings := findReadinessCheck(t, report, "models", "ollama embeddings")
	require.Equal(t, StatusPass, embeddings.Status)
	require.Contains(t, embeddings.Message, "embedding model")
}

func TestBuildOllamaReadinessChecksSkipsNilConfig(t *testing.T) {
	require.Nil(t, buildOllamaReadinessChecks(context.Background(), nil))
}

func TestBuild_ReportsOllamaEmbeddingReadinessWithHostedMainModel(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	var discoveredBaseURL string
	discoverOllamaModels = func(_ context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
		discoveredBaseURL = baseURL
		return []modelprovider.ModelDefinition{{
			ID:       constants.DefaultOllamaEmbeddingModel,
			Provider: constants.ModelProviderOllama,
		}}, nil
	}

	cfg := readyConfig()
	cfg.Models.Embedding.Provider = constants.ModelProviderOllama
	cfg.Models.Embedding.Name = constants.DefaultOllamaEmbeddingModel
	cfg.Models.Embedding.API = modelprovider.APIOllamaEmbeddings
	cfg.Models.Embedding.BaseURL = "http://127.0.0.1:11435"
	cfg.Search.Vector.Enabled = true

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	require.Equal(t, "http://127.0.0.1:11435", discoveredBaseURL)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "models", "ollama").Status)
	embeddings := findReadinessCheck(t, report, "models", "ollama embeddings")
	require.Equal(t, StatusPass, embeddings.Status)
	require.Contains(t, embeddings.Message, constants.DefaultOllamaEmbeddingModel)
}

func TestBuild_ReportsOllamaEmbeddingReadinessWithImplicitLatestTag(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{
			ID:       constants.DefaultOllamaEmbeddingModel + ":latest",
			Provider: constants.ModelProviderOllama,
		}}, nil
	}

	cfg := readyConfig()
	cfg.Models.Embedding.Provider = constants.ModelProviderOllama
	cfg.Models.Embedding.Name = constants.DefaultOllamaEmbeddingModel
	cfg.Models.Embedding.API = modelprovider.APIOllamaEmbeddings
	cfg.Models.Embedding.BaseURL = "http://127.0.0.1:11435"
	cfg.Search.Vector.Enabled = true

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	embeddings := findReadinessCheck(t, report, "models", "ollama embeddings")
	require.Equal(t, StatusPass, embeddings.Status)
	require.Contains(t, embeddings.Message, constants.DefaultOllamaEmbeddingModel)
}

func TestBuild_WarnsWhenOllamaEmbeddingModelIsMissing(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{ID: "chat:latest"}}, nil
	}

	cfg := readyConfig()
	cfg.Models.Embedding.Provider = constants.ModelProviderOllama
	cfg.Models.Embedding.Name = constants.DefaultOllamaEmbeddingModel
	cfg.Models.Embedding.API = modelprovider.APIOllamaEmbeddings
	cfg.Models.Embedding.BaseURL = "http://127.0.0.1:11435"
	cfg.Search.Vector.Enabled = true

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	embeddings := findReadinessCheck(t, report, "models", "ollama embeddings")
	require.Equal(t, StatusWarn, embeddings.Status)
	require.Contains(t, embeddings.Message, "is not installed")
	require.Equal(
		t,
		"morph provider configure --provider ollama --base-url http://127.0.0.1:11435 --model nomic-embed-text --pull",
		embeddings.Actions[0].Command,
	)
}

func TestBuild_FailsWhenOllamaEmbeddingModelIsEmpty(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, nil
	}

	cfg := readyConfig()
	cfg.Models.Embedding.Provider = constants.ModelProviderOllama
	cfg.Models.Embedding.Name = ""
	cfg.Models.Embedding.API = modelprovider.APIOllamaEmbeddings
	cfg.Models.Embedding.BaseURL = "http://127.0.0.1:11435"
	cfg.Search.Vector.Enabled = true

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	embeddings := findReadinessCheck(t, report, "models", "ollama embeddings")
	require.Equal(t, StatusFail, embeddings.Status)
	require.Equal(t, "embedding model is required", embeddings.Message)
}

func TestGetOllamaReadinessBaseURL(t *testing.T) {
	require.Empty(t, getOllamaReadinessBaseURL(nil))

	cfg := readyConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.BaseURL = "http://main.local:11434"
	require.Equal(t, "http://main.local:11434", getOllamaReadinessBaseURL(cfg))

	cfg = readyConfig()
	cfg.Models.Embedding.Provider = constants.ModelProviderOllama
	cfg.Models.Embedding.API = modelprovider.APIOllamaEmbeddings
	cfg.Models.Embedding.BaseURL = "http://embedding.local:11434"
	require.Equal(t, "http://embedding.local:11434", getOllamaReadinessBaseURL(cfg))

	cfg = readyConfig()
	cfg.Models.Embedding.Provider = "missing"
	cfg.Models.Embedding.BaseURL = "http://role.local:11434"
	require.Equal(t, "http://role.local:11434", getOllamaReadinessBaseURL(cfg))

	cfg = readyConfig()
	cfg.Models.Embedding.Provider = "missing"
	cfg.Models.Providers[constants.ModelProviderOllama] = config.ProviderModelConfig{
		BaseURL: "http://provider.local:11434",
	}
	require.Equal(t, "http://provider.local:11434", getOllamaReadinessBaseURL(cfg))

	cfg = readyConfig()
	cfg.Models.Embedding.Provider = "missing"
	require.Equal(t, constants.DefaultOllamaBaseURL, getOllamaReadinessBaseURL(cfg))
}

func TestBuild_ReportsMissingOllamaModel(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{ID: "installed:latest"}}, nil
	}

	cfg := readyConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.Name = "missing:latest"
	cfg.Models.Main.BaseURL = "http://127.0.0.1:11434"

	report := Build(context.Background(), Options{Config: cfg})

	model := findReadinessCheck(t, report, "models", "ollama model")
	require.Equal(t, StatusFail, model.Status)
	require.Contains(t, model.Message, `model "missing:latest" is not installed`)
	require.Equal(
		t,
		"morph provider configure --provider ollama --base-url http://127.0.0.1:11434 --model missing:latest --pull",
		model.Actions[0].Command,
	)
}

func TestBuild_ReportsOllamaDiscoveryAndContextWarnings(t *testing.T) {
	originalDiscover := discoverOllamaModels
	t.Cleanup(func() {
		discoverOllamaModels = originalDiscover
	})

	cfg := readyConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.Name = "small:latest"
	cfg.Models.Main.BaseURL = "http://127.0.0.1:11434"
	cfg.Models.Main.ContextLength = 9000

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return nil, errors.New("ollama is not reachable")
	}
	report := Build(context.Background(), Options{Config: cfg})
	ollama := findReadinessCheck(t, report, "models", "ollama")
	require.Equal(t, StatusFail, ollama.Status)
	require.Contains(t, ollama.Message, "not reachable")
	require.Equal(t, "ollama serve", ollama.Actions[0].Command)

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{ID: "small:latest", ContextWindow: 4096}}, nil
	}
	report = Build(context.Background(), Options{Config: cfg})
	contextCheck := findReadinessCheck(t, report, "models", "ollama context")
	require.Equal(t, StatusWarn, contextCheck.Status)
	require.Contains(t, contextCheck.Message, "configured contextLength=9000 exceeds")
	require.Equal(t, "morph config set models.main.contextLength 4096", contextCheck.Actions[0].Command)

	discoverOllamaModels = func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		return []modelprovider.ModelDefinition{{ID: "small:latest"}}, nil
	}
	report = Build(context.Background(), Options{Config: cfg})
	contextCheck = findReadinessCheck(t, report, "models", "ollama context")
	require.Equal(t, StatusWarn, contextCheck.Status)
	require.Contains(t, contextCheck.Message, "context metadata is unavailable")
	tools := findReadinessCheck(t, report, "models", "ollama tools")
	require.Equal(t, StatusWarn, tools.Status)
	require.Contains(t, tools.Message, "does not report tool support")
}

func TestBuild_ReportsDisabledMemoryAsWarningOnly(t *testing.T) {
	disabled := false
	cfg := readyConfig()
	cfg.Memory.Enabled = &disabled

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	memory := findReadinessCheck(t, report, "memory", "status")
	require.Equal(t, StatusWarn, memory.Status)
	require.Contains(t, memory.Message, `disabled, provider="default-memory", backend="sqlite"`)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "pinned").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "retrieval").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "flush").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "episodic").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "reflection").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "promotion").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "write").Status)
	require.False(t, report.HasFailures())
}

func TestBuild_ReportsExplicitMemoryBackend(t *testing.T) {
	cfg := readyConfig()
	cfg.Storage.Backend = "sqlite"
	cfg.Memory.Backend = "memory"

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	memory := findReadinessCheck(t, report, "memory", "status")
	require.Equal(t, StatusPass, memory.Status)
	require.Contains(t, memory.Message, `backend="memory"`)
}

func TestBuild_ReportsDisabledCompactionAsWarningOnly(t *testing.T) {
	disabled := false
	cfg := readyConfig()
	cfg.Compaction.Enabled = &disabled
	cfg.Compaction.TriggerPercent = 0.7
	cfg.Compaction.WarnPercent = 0.9
	recentTail := 3
	cfg.Compaction.RecentSessionTail = &recentTail

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	compaction := findReadinessCheck(t, report, "session", "compaction")
	require.Equal(t, StatusWarn, compaction.Status)
	require.Equal(t, "disabled, triggerPercent=0.70, warnPercent=0.90, recentSessionTail=3", compaction.Message)
	require.False(t, report.HasFailures())
}

func TestBuild_ReportsMissingWebCredentialAsWarning(t *testing.T) {
	clearWebCredentialEnv(t)
	original := resolveWebAPIKeySource
	t.Cleanup(func() {
		resolveWebAPIKeySource = original
	})
	resolveWebAPIKeySource = func(*config.Config) (config.WebCredentialSource, error) {
		return config.WebCredentialSource{}, nil
	}
	cfg := readyConfig()
	cfg.Web.Provider = constants.WebProviderExa

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	web := findReadinessCheck(t, report, "tools", "web tools")
	require.Equal(t, StatusWarn, web.Status)
	require.Contains(t, web.Message, "exa web credentials are not configured")
	require.Equal(t, "morph config set web.provider exa && morph config set web.apiKey <api-key>", web.Actions[0].Command)
}

func TestBuild_DoesNotEmitAnsi(t *testing.T) {
	report := Build(context.Background(), Options{
		Config:  readyConfig(),
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	require.NotRegexp(t, regexp.MustCompile(`\x1b\[[0-9;]*m`), report.Summary())
	for _, group := range report.Groups {
		for _, check := range group.Checks {
			require.NotRegexp(t, regexp.MustCompile(`\x1b\[[0-9;]*m`), check.Message)
		}
	}
}

func TestBuild_CoversModelAndCapabilityBranches(t *testing.T) {
	cfg := readyConfig()
	cfg.Search.Vector.Enabled = true
	cfg.Search.Vector.Required = true
	cfg.Models.Embedding.Provider = constants.ModelProviderOpenAI
	cfg.Reranker.Enabled = new(bool)
	cfg.Cap.Network = new(bool)
	cfg.Memory.Enabled = new(bool)
	cfg.Web.Provider = "native"

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	require.True(t, report.HasFailures())
	embedding := findReadinessCheck(t, report, "models", "embedding")
	require.Equal(t, StatusFail, embedding.Status)
	require.Equal(t, "morph provider login openai --api-key <api-key>", embedding.Actions[0].Command)
	require.Equal(t, "morph config set models.providers.openai.apiKey <api-key>", embedding.Actions[1].Command)
	vector := findReadinessCheck(t, report, "search", "vector")
	require.Equal(t, StatusFail, vector.Status)
	require.Contains(t, vector.Message, `auth=missing for provider "openai"`)
	require.Equal(t, "morph provider login openai --api-key <api-key>", vector.Actions[0].Command)
	require.Equal(t, "morph config set models.providers.openai.apiKey <api-key>", vector.Actions[1].Command)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "status").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "memory", "retrieval").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "search", "rerank").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "tools", "web tools").Status)
}

func TestBuild_ReportsUnconfiguredDisabledVectorEmbedding(t *testing.T) {
	cfg := readyConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOpenAICodex
	cfg.Models.Embedding.Name = ""
	cfg.Search.Vector.Enabled = false
	cfg.Search.Vector.Required = true

	report := Build(context.Background(), Options{
		Config:  cfg,
		Profile: profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()}),
	})

	vector := findReadinessCheck(t, report, "search", "vector")
	require.Equal(t, StatusWarn, vector.Status)
	require.Contains(t, vector.Message, "embedding=not configured")
	require.NotContains(t, vector.Message, `"openai-codex"/""`)
}

func TestBuild_CoversWebCredentialBranches(t *testing.T) {
	original := resolveWebAPIKeySource
	t.Cleanup(func() {
		resolveWebAPIKeySource = original
	})

	cfg := readyConfig()
	cfg.Web.Provider = constants.WebProviderExa
	resolveWebAPIKeySource = func(*config.Config) (config.WebCredentialSource, error) {
		return config.WebCredentialSource{Configured: true, Source: "environment"}, nil
	}
	report := Build(context.Background(), Options{Config: cfg})
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "tools", "web tools").Status)

	resolveWebAPIKeySource = func(*config.Config) (config.WebCredentialSource, error) {
		return config.WebCredentialSource{}, errors.New("stored failed")
	}
	report = Build(context.Background(), Options{Config: cfg})
	web := findReadinessCheck(t, report, "tools", "web tools")
	require.Equal(t, StatusWarn, web.Status)
	require.Contains(t, web.Message, "stored failed")

	cfg.Web.Provider = "custom"
	report = Build(context.Background(), Options{Config: cfg})
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "tools", "web tools").Status)

	cfg.Web.Provider = "native"
	report = Build(context.Background(), Options{Config: cfg})
	nativeWeb := findReadinessCheck(t, report, "tools", "web tools")
	require.Equal(t, StatusWarn, nativeWeb.Status)
	require.Equal(t, "native web extraction is configured; web search requires a configured web provider", nativeWeb.Message)

	require.Equal(t, "morph config set web.provider exa && morph config set web.apiKey <api-key>", webAuthAction("").Command)
}

func TestBuild_ReportsDisabledGatewayAsInformational(t *testing.T) {
	cfg := readyConfig()

	report := Build(context.Background(), Options{Config: cfg})

	listener := findReadinessCheck(t, report, "gateway", "listener")
	require.Equal(t, StatusPass, listener.Status)
	require.Equal(t, "disabled", listener.Message)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "gateway", "telegram").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "gateway", "slack").Status)
}

func TestBuild_WarnsWhenGatewayExternalBindMissingAuth(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Enabled = true
	cfg.Gateway.Address = "0.0.0.0"
	cfg.Gateway.AuthToken = ""

	report := Build(context.Background(), Options{Config: cfg})

	listener := findReadinessCheck(t, report, "gateway", "listener")
	require.Equal(t, StatusWarn, listener.Status)
	require.Contains(t, listener.Message, "without gateway auth token")
	require.Equal(t, "morph config set gateway.authToken <token>", listener.Actions[0].Command)
}

func TestBuild_PassesGatewayExternalBindWithAuth(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Enabled = true
	cfg.Gateway.Address = "0.0.0.0"
	cfg.Gateway.AuthToken = "secret-token"

	report := Build(context.Background(), Options{Config: cfg})

	listener := findReadinessCheck(t, report, "gateway", "listener")
	require.Equal(t, StatusPass, listener.Status)
	require.Contains(t, listener.Message, "auth=configured")
	require.NotContains(t, listener.Message, "secret-token")
}

func TestBuild_WarnsWhenTelegramPollingMissingBotToken(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Telegram.Enabled = true
	cfg.Gateway.Telegram.Mode = config.GatewayTelegramModePolling
	cfg.Gateway.Telegram.BotToken = ""

	report := Build(context.Background(), Options{Config: cfg})

	telegram := findReadinessCheck(t, report, "gateway", "telegram")
	require.Equal(t, StatusWarn, telegram.Status)
	require.Equal(t, "enabled in polling mode without bot token", telegram.Message)
	require.Equal(t, "morph config set gateway.telegram.botToken <bot-token>", telegram.Actions[0].Command)
}

func TestBuild_WarnsWhenTelegramWebhookMissingSecrets(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Telegram.Enabled = true
	cfg.Gateway.Telegram.Mode = config.GatewayTelegramModeWebhook
	cfg.Gateway.Telegram.BotToken = ""

	report := Build(context.Background(), Options{Config: cfg})

	telegram := findReadinessCheck(t, report, "gateway", "telegram")
	require.Equal(t, StatusWarn, telegram.Status)
	require.Contains(t, telegram.Message, "without bot token")

	cfg.Gateway.Telegram.BotToken = "telegram-token"
	cfg.Gateway.Telegram.WebhookSecret = ""
	report = Build(context.Background(), Options{Config: cfg})

	telegram = findReadinessCheck(t, report, "gateway", "telegram")
	require.Equal(t, StatusWarn, telegram.Status)
	require.Equal(t, "enabled in webhook mode without webhook secret", telegram.Message)
	require.Equal(t, "morph config set gateway.telegram.webhookSecret <secret-token>", telegram.Actions[0].Command)
	require.NotContains(t, telegram.Message, "telegram-token")
}

func TestBuild_WarnsWhenSlackSocketMissingTokens(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Slack.Enabled = true
	cfg.Gateway.Slack.Mode = config.GatewaySlackModeSocket
	cfg.Gateway.Slack.BotToken = ""

	report := Build(context.Background(), Options{Config: cfg})

	slack := findReadinessCheck(t, report, "gateway", "slack")
	require.Equal(t, StatusWarn, slack.Status)
	require.Equal(t, "enabled in socket mode without bot token", slack.Message)
	require.Equal(t, "morph config set gateway.slack.botToken <bot-token>", slack.Actions[0].Command)

	cfg.Gateway.Slack.BotToken = "slack-bot-token"
	cfg.Gateway.Slack.AppToken = ""
	report = Build(context.Background(), Options{Config: cfg})

	slack = findReadinessCheck(t, report, "gateway", "slack")
	require.Equal(t, StatusWarn, slack.Status)
	require.Equal(t, "enabled in socket mode without app token", slack.Message)
	require.Equal(t, "morph config set gateway.slack.appToken <app-token>", slack.Actions[0].Command)
	require.NotContains(t, slack.Message, "slack-bot-token")
}

func TestBuild_WarnsWhenSlackHTTPMissingSigningSecret(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Slack.Enabled = true
	cfg.Gateway.Slack.Mode = config.GatewaySlackModeHTTP
	cfg.Gateway.Slack.BotToken = "slack-bot-token"
	cfg.Gateway.Slack.SigningSecret = ""

	report := Build(context.Background(), Options{Config: cfg})

	slack := findReadinessCheck(t, report, "gateway", "slack")
	require.Equal(t, StatusWarn, slack.Status)
	require.Equal(t, "enabled in http mode without signing secret", slack.Message)
	require.Equal(t, "morph config set gateway.slack.signingSecret <signing-secret>", slack.Actions[0].Command)
	require.NotContains(t, slack.Message, "slack-bot-token")
}

func TestBuild_PassesConfiguredSlackAndTelegramModes(t *testing.T) {
	cfg := readyConfig()
	cfg.Gateway.Telegram.Enabled = true
	cfg.Gateway.Telegram.Mode = config.GatewayTelegramModeWebhook
	cfg.Gateway.Telegram.BotToken = "telegram-token"
	cfg.Gateway.Telegram.WebhookSecret = "webhook-secret"
	cfg.Gateway.Telegram.AllowedUsers = []string{"123"}
	cfg.Gateway.Slack.Enabled = true
	cfg.Gateway.Slack.Mode = config.GatewaySlackModeHTTP
	cfg.Gateway.Slack.BotToken = "slack-bot-token"
	cfg.Gateway.Slack.SigningSecret = "slack-signing-secret"

	report := Build(context.Background(), Options{Config: cfg})

	telegram := findReadinessCheck(t, report, "gateway", "telegram")
	require.Equal(t, StatusPass, telegram.Status)
	require.Equal(t, "enabled in webhook mode, bot token configured", telegram.Message)
	slack := findReadinessCheck(t, report, "gateway", "slack")
	require.Equal(t, StatusPass, slack.Status)
	require.Equal(t, "enabled in http mode, bot token configured", slack.Message)
	require.NotContains(t, telegram.Message+slack.Message, "webhook-secret")
	require.NotContains(t, telegram.Message+slack.Message, "slack-signing-secret")
}

func TestBuild_CoversProfilePathBranches(t *testing.T) {
	home := t.TempDir()
	filePath := filepath.Join(home, "file")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))
	dirPath := filepath.Join(home, "dir")
	require.NoError(t, os.Mkdir(dirPath, 0o700))

	report := Build(context.Background(), Options{
		Config: readyConfig(),
		Profile: profile.Profile{
			Name:        "",
			HomeDir:     filePath,
			ConfigPath:  dirPath,
			EnvPath:     "",
			RuntimePath: filepath.Join(home, "missing-runtime.json"),
		},
	})

	require.Equal(t, StatusFail, findReadinessCheck(t, report, "profile", "home").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "profile", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "profile", "env").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "profile", "runtime").Status)
}

func TestBuild_CoversReadyDaemon(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		close(accepted)
	}()

	home := t.TempDir()
	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: home})
	require.NoError(t, os.WriteFile(active.RuntimePath, []byte(`{
  "profile": "work",
  "pid": `+fmt.Sprint(os.Getpid())+`,
  "rpc": {
    "address": "127.0.0.1",
    "port": `+fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)+`
  },
  "started_at": "2026-06-03T00:00:00Z"
}`), 0o600))

	report := Build(context.Background(), Options{
		Config:  readyConfig(),
		Profile: active,
	})

	require.Equal(t, StatusPass, findReadinessCheck(t, report, "daemon", "runtime").Status)
	select {
	case <-accepted:
	case <-time.After(time.Second):
		require.Fail(t, "runtime probe did not dial test listener")
	}
}

func TestBuild_CoversNilConfig(t *testing.T) {
	report := Build(context.Background(), Options{})

	require.True(t, report.HasFailures())
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "models", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "session", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "memory", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "search", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "tools", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "gateway", "config").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "automation", "config").Status)
}

func TestBuild_ReportsAutomationReadiness(t *testing.T) {
	store := storememory.NewStore()
	now := time.Now().UTC()
	_, err := store.CreateJob(context.Background(), storage.AutomationJob{
		ID:       "auto_projectaprojectaproje",
		Enabled:  true,
		Schedule: storage.AutomationSchedule{Kind: storage.AutomationScheduleEvery, Every: time.Hour},
		Delivery: storage.AutomationDelivery{Mode: storage.AutomationDeliveryWebhook},
		State:    storage.AutomationJobState{RunningAt: now.Add(-20 * time.Minute)},
	})
	require.NoError(t, err)

	originalOpenStore := openAutomationReadinessStore
	t.Cleanup(func() { openAutomationReadinessStore = originalOpenStore })
	openAutomationReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return store, nil
	}

	report := Build(context.Background(), Options{Config: readyConfig()})

	require.Equal(t, StatusPass, findReadinessCheck(t, report, "automation", "scheduler").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "automation", "store").Status)
	require.Equal(t, StatusPass, findReadinessCheck(t, report, "automation", "invalid schedules").Status)
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "automation", "stuck running").Status)
	delivery := findReadinessCheck(t, report, "automation", "delivery targets")
	require.Equal(t, StatusFail, delivery.Status)
	require.Contains(t, delivery.Message, "webhook delivery requires")
	require.NotEmpty(t, delivery.Actions)
}

func TestBuild_ReportsAutomationStoreErrors(t *testing.T) {
	originalOpenStore := openAutomationReadinessStore
	t.Cleanup(func() { openAutomationReadinessStore = originalOpenStore })

	openAutomationReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return storememory.NewStore(), nil
	}
	report := Build(context.Background(), Options{Config: readyConfig()})
	require.Equal(t, "0 automation jobs reachable", findReadinessCheck(t, report, "automation", "store").Message)
	require.Equal(t, "no automation jobs to check", findReadinessCheck(t, report, "automation", "invalid schedules").Message)
	require.Equal(t, "no automation jobs to check", findReadinessCheck(t, report, "automation", "stuck running").Message)
	require.Equal(t, "no automation jobs to check", findReadinessCheck(t, report, "automation", "delivery targets").Message)

	expected := errors.New("store failed")
	openAutomationReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return nil, expected
	}
	report = Build(context.Background(), Options{Config: readyConfig()})
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "automation", "store").Status)

	openAutomationReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return automationReadinessStoreStub{}, nil
	}
	report = Build(context.Background(), Options{Config: readyConfig()})
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "automation", "store").Status)

	openAutomationReadinessStore = func(*config.Config, profile.Profile) (storage.Store, error) {
		return automationReadinessStoreStub{automationStore: automationReadinessListErrStore{err: expected}}, nil
	}
	report = Build(context.Background(), Options{Config: readyConfig()})
	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "automation", "scheduler").Status)
	require.Equal(t, StatusFail, findReadinessCheck(t, report, "automation", "store").Status)
}

func TestOpenProfileReadinessStore_UsesRequestedProfileAndBackend(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() { profile.SetActive(originalProfile) })
	activeHome := t.TempDir()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "active", HomeDir: activeHome}))
	requestedHome := t.TempDir()
	requestedProfile := profile.WithMetadataPaths(profile.Profile{Name: "requested", HomeDir: requestedHome})

	cfg := readyConfig()
	store, err := openProfileReadinessStore(cfg, requestedProfile)
	require.NoError(t, err)
	closer, ok := store.(interface{ Close() error })
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })
	require.FileExists(t, filepath.Join(requestedHome, "data", "state.db"))
	require.NoFileExists(t, filepath.Join(activeHome, "data", "state.db"))

	cfg.Storage.Backend = "memory"
	store, err = openProfileReadinessStore(cfg, requestedProfile)
	require.NoError(t, err)
	_, ok = store.Automation()
	require.True(t, ok)

	_, err = openProfileReadinessStore(nil, requestedProfile)
	require.EqualError(t, err, "config is required")
	cfg.Storage.Backend = "unsupported"
	_, err = openProfileReadinessStore(cfg, requestedProfile)
	require.EqualError(t, err, "storage backend must be one of: memory, sqlite")
}

func TestAutomationFindingsToCheck_CoversWarningsWithoutActions(t *testing.T) {
	check := automationFindingsToCheck("custom", []automation.DiagnosticFinding{
		{
			Severity: automation.DiagnosticSeverityWarn,
			Code:     "custom",
			Message:  "first",
		},
		{
			Severity: automation.DiagnosticSeverityWarn,
			Code:     "custom",
			Message:  "second",
		},
	})

	require.Equal(t, StatusWarn, check.Status)
	require.Equal(t, "first and 1 more", check.Message)
	require.Empty(t, check.Actions)
}

func TestReadinessHelperBranches(t *testing.T) {
	require.True(t, isReadinessLoopbackGatewayAddress(""))
	require.True(t, isReadinessLoopbackGatewayAddress("localhost"))
	require.True(t, isReadinessLoopbackGatewayAddress("[::1]"))
	require.False(t, isReadinessLoopbackGatewayAddress("not-an-ip"))
	require.False(t, isReadinessLoopbackGatewayAddress("0.0.0.0"))
	require.Nil(t, providerAPIKeyActions(" "))
	require.False(t, searchRerankEnabled(nil))

	cfg := readyConfig()
	enabled := true
	cfg.Reranker.Enabled = &enabled
	cfg.Search.EnableRerank = &enabled
	require.True(t, searchRerankEnabled(cfg))
}

func TestBuild_CoversRerankDisabledBySearch(t *testing.T) {
	cfg := readyConfig()
	enabled := true
	disabled := false
	cfg.Reranker.Enabled = &enabled
	cfg.Search.EnableRerank = &disabled

	report := Build(context.Background(), Options{Config: cfg})

	require.Equal(t, StatusWarn, findReadinessCheck(t, report, "search", "rerank").Status)
}

func TestMissingAuthActionAndCredentialSourceFormatting(t *testing.T) {
	modelMissingAuthActions := modelErrorActions(constants.ModelProviderOpenRouter, errors.New("model API key is required for provider"))
	require.Equal(t, "morph provider login openrouter --api-key <api-key>", modelMissingAuthActions[0].Command)
	require.Equal(t, "morph config set models.providers.openrouter.apiKey <api-key>", modelMissingAuthActions[1].Command)

	embeddingMissingAuthActions := embeddingModelErrorActions(constants.ModelProviderOpenAI, errors.New("embedding API key is required for provider"))
	require.Equal(t, "morph provider login openai --api-key <api-key>", embeddingMissingAuthActions[0].Command)
	require.Equal(t, "morph config set models.providers.openai.apiKey <api-key>", embeddingMissingAuthActions[1].Command)

	modelSelectionActions := modelErrorActions(constants.ModelProviderOpenRouter, errors.New("model provider must be one of: openrouter"))
	require.Len(t, modelSelectionActions, 2)
	require.Equal(t, "/providers", modelSelectionActions[0].Command)
	require.Equal(t, "/models", modelSelectionActions[1].Command)

	require.False(t, isMissingAuthError(nil))
	require.Equal(t, "morph provider login openai", missingAuthActions(constants.ModelProviderOpenAI)[0].Command)
	require.Equal(
		t,
		"morph provider login openrouter --api-key <api-key>",
		missingAuthActions(constants.ModelProviderOpenRouter)[0].Command,
	)
	require.Equal(
		t,
		"morph config set models.providers.openrouter.apiKey <api-key>",
		missingAuthActions(constants.ModelProviderOpenRouter)[1].Command,
	)
	require.Empty(t, missingAuthActions(""))

	require.Equal(t, "role-config", formatCredentialSource(config.ModelAuth{
		CredentialSource: config.ModelCredentialSource{Kind: config.ModelCredentialSourceRoleConfig},
	}))
	require.Equal(t, "oauth env", formatCredentialSource(config.ModelAuth{
		CredentialSource: config.ModelCredentialSource{
			Kind: config.ModelCredentialSourceProviderEnv,
			Type: "oauth",
		},
	}))
	require.Equal(t, "environment", formatCredentialSource(config.ModelAuth{
		CredentialSource: config.ModelCredentialSource{Kind: config.ModelCredentialSourceProviderEnv},
	}))
	require.Equal(t, "token-store oauth refreshable", formatCredentialSource(config.ModelAuth{
		CredentialSource: config.ModelCredentialSource{
			Kind:      config.ModelCredentialSourceTokenStore,
			Type:      "oauth",
			HasExpiry: true,
		},
	}))
	require.Equal(t, "api-key", formatCredentialSource(config.ModelAuth{APIKey: "key"}))
}

func findReadinessCheck(t *testing.T, report Report, groupName string, checkName string) Check {
	t.Helper()

	for _, group := range report.Groups {
		if group.Name != groupName {
			continue
		}

		for _, check := range group.Checks {
			if check.Name == checkName {
				return check
			}
		}
	}

	require.Failf(t, "missing readiness check", "%s/%s", groupName, checkName)
	return Check{}
}

func readyConfig() *config.Config {
	cfg := config.NewDefaultConfig()
	cfg.Name = "test"
	cfg.Models.Main.Provider = constants.ModelProviderOpenRouter
	cfg.Models.Main.Name = "gpt-4o-mini"
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOpenRouter: {APIKey: "model-key"},
	}
	cfg.Search.Vector.Enabled = false
	cfg.Web.Provider = ""

	return cfg
}

type automationReadinessStoreStub struct {
	automationStore storage.AutomationStore
	permissionStore permissions.ApprovalStore
}

func (automationReadinessStoreStub) Session() storage.SessionStore {
	return nil
}

func (s automationReadinessStoreStub) Automation() (storage.AutomationStore, bool) {
	if s.automationStore == nil {
		return nil, false
	}

	return s.automationStore, true
}

func (s automationReadinessStoreStub) Permission() (permissions.ApprovalStore, bool) {
	return s.permissionStore, s.permissionStore != nil
}

type permissionListErrorStore struct {
	permissions.ApprovalStore
	err error
}

func (s permissionListErrorStore) ListApprovalGrants(
	context.Context,
	permissions.GrantQuery,
) ([]permissions.ApprovalGrant, error) {
	return nil, s.err
}

func (automationReadinessStoreStub) Memory() (storage.MemoryStore, bool) {
	return nil, false
}

func (automationReadinessStoreStub) Trace() (storage.TraceStore, bool) {
	return nil, false
}

func (automationReadinessStoreStub) SupportsVectorSearch() bool {
	return false
}

type automationReadinessListErrStore struct {
	err error
}

func (automationReadinessListErrStore) CreateJob(
	context.Context,
	storage.AutomationJob,
) (storage.AutomationJob, error) {
	return storage.AutomationJob{}, nil
}

func (automationReadinessListErrStore) GetJob(
	context.Context,
	string,
) (storage.AutomationJob, bool, error) {
	return storage.AutomationJob{}, false, nil
}

func (s automationReadinessListErrStore) ListJobs(
	context.Context,
	storage.AutomationJobQuery,
) (storage.AutomationJobResult, error) {
	return storage.AutomationJobResult{}, s.err
}

func (automationReadinessListErrStore) PatchJob(
	context.Context,
	storage.AutomationJobPatch,
) (storage.AutomationJob, error) {
	return storage.AutomationJob{}, nil
}

func (automationReadinessListErrStore) DeleteJob(context.Context, string) error {
	return nil
}

func (automationReadinessListErrStore) CreateRun(
	context.Context,
	storage.AutomationRun,
) (storage.AutomationRun, error) {
	return storage.AutomationRun{}, nil
}

func (automationReadinessListErrStore) FinishRun(
	context.Context,
	storage.AutomationRunPatch,
) (storage.AutomationRun, error) {
	return storage.AutomationRun{}, nil
}

func (automationReadinessListErrStore) ListRuns(
	context.Context,
	storage.AutomationRunQuery,
) (storage.AutomationRunResult, error) {
	return storage.AutomationRunResult{}, nil
}

func (automationReadinessListErrStore) DeleteRuns(
	context.Context,
	storage.AutomationRunDeleteQuery,
) (int, error) {
	return 0, nil
}

func clearWebCredentialEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"MORPH_EXA_API_KEY",
		"EXA_API_KEY",
		"MORPH_FIRECRAWL_API_KEY",
		"FIRECRAWL_API_KEY",
		"MORPH_PARALLEL_API_KEY",
		"PARALLEL_API_KEY",
		"MORPH_TAVILY_API_KEY",
		"TAVILY_API_KEY",
		"MORPH_WEB_API_KEY",
	} {
		t.Setenv(key, "")
	}
}
