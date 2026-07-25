package authcmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/config"
	appcredential "github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newProviderTestCommand() *cli.Command {
	return &cli.Command{
		Name:  "provider",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "json"}},
		Commands: []*cli.Command{
			NewProviderLoginCommand(),
			NewProviderStatusCommand(),
			NewProviderLogoutCommand(),
		},
	}
}

func setAuthTestSubscriptionProviderLookup(t *testing.T) {
	t.Helper()

	previousProvider := getSubscriptionProvider
	getSubscriptionProvider = func(string) (appcredential.SubscriptionProvider, bool) {
		return nil, false
	}
	t.Cleanup(func() { getSubscriptionProvider = previousProvider })
}

func TestCommand_LoginStoresAPIKeyWithoutPrintingSecret(t *testing.T) {
	setAuthTestSubscriptionProviderLookup(t)

	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "login", "openai", "--api-key", "sk-secret-value"})
	require.NoError(t, err)
	require.NotContains(t, output.String(), "sk-secret-value")
	require.Contains(t, output.String(), "openai credential stored")

	credential, ok, err := appcredential.NewFileStore(filepath.Join(home, "auth.json")).Get("openai")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, appcredential.TypeAPIKey, credential.Type)
	require.Equal(t, "sk-secret-value", credential.Key)
}

func TestCommand_ProviderCommandsHonorJSONOutput(t *testing.T) {
	setAuthTestSubscriptionProviderLookup(t)
	setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })

	err := newProviderTestCommand().Run(context.Background(), []string{
		"provider", "--json", "login", "openai", "--api-key", "secret",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"provider":"openai","status":"stored"}`, output.String())

	output.Reset()
	err = newProviderTestCommand().Run(context.Background(), []string{
		"provider", "--json", "status", "openai",
	})
	require.NoError(t, err)
	var statuses []map[string]string
	require.NoError(t, json.Unmarshal(output.Bytes(), &statuses))
	require.Equal(t, []map[string]string{{
		"provider": "openai",
		"status":   "stored api_key",
	}}, statuses)

	output.Reset()
	err = newProviderTestCommand().Run(context.Background(), []string{
		"provider", "--json", "logout", "openai",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"provider":"openai","status":"removed"}`, output.String())
}

func TestCommand_LoginStoresOAuthTokenWithExpiry(t *testing.T) {
	setAuthTestSubscriptionProviderLookup(t)
	home := setAuthTestProfile(t)
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	err := newProviderTestCommand().Run(context.Background(), []string{
		"provider", "login", "github-copilot",
		"--token", "token-secret",
		"--refresh-token", "refresh-secret",
		"--expires-at", expiresAt,
		"--scope", "read",
		"--scope", "write",
	})
	require.NoError(t, err)

	credential, ok, err := appcredential.NewFileStore(filepath.Join(home, "auth.json")).Get("github-copilot")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "token-secret", credential.Token)
	require.Equal(t, "refresh-secret", credential.Refresh)
	require.Equal(t, []string{"read", "write"}, credential.Scopes)
	require.NotNil(t, credential.ExpiresAt)
}

func TestCommand_LoginValidatesCredentialFlags(t *testing.T) {
	setAuthTestSubscriptionProviderLookup(t)
	setAuthTestProfile(t)

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "login", "openai"})
	require.EqualError(t, err, "credential is required; pass --api-key or --token, or use a provider with subscription login")

	err = newProviderTestCommand().Run(context.Background(), []string{
		"provider", "login", "openai", "--api-key", "key", "--token", "token",
	})
	require.EqualError(t, err, "use either --api-key or --token, not both")

	err = newProviderTestCommand().Run(context.Background(), []string{
		"provider", "login", "openai", "--token", "token", "--expires-at", "not-time",
	})
	require.ErrorContains(t, err, "parse --expires-at")

	err = newProviderTestCommand().Run(context.Background(), []string{"provider", "login"})
	require.EqualError(t, err, "provider is required")
}

func TestCommand_LoginUsesSubscriptionProviderWhenNoCredentialFlags(t *testing.T) {
	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restoreOutput := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restoreOutput) })

	previousProvider := getSubscriptionProvider
	getSubscriptionProvider = func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, "openai", provider)
		return fakeSubscriptionProvider{
			login: func(options appcredential.LoginOptions) {
				require.Equal(t, "openai", options.Provider)
				require.NotNil(t, options.Input)
				require.NotNil(t, options.Output)
			},
		}, true
	}
	t.Cleanup(func() { getSubscriptionProvider = previousProvider })

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "login", "openai"})
	require.NoError(t, err)
	require.NotContains(t, output.String(), "subscription-secret")

	credential, ok, err := appcredential.NewFileStore(filepath.Join(home, "auth.json")).Get("openai")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "subscription-secret", credential.Token)
}

func TestCommand_LoginReturnsOutputError(t *testing.T) {
	setAuthTestSubscriptionProviderLookup(t)
	setAuthTestProfile(t)
	restore := SetOutput(errorWriter{})
	t.Cleanup(func() { SetOutput(restore) })

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "login", "openai", "--api-key", "key"})
	require.EqualError(t, err, "write failed")
}

func TestCommand_StatusReportsStoredEnvironmentAndConfigSources(t *testing.T) {
	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })
	t.Setenv("ANTHROPIC_API_KEY", "env-secret")
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`
models:
  providers:
    openrouter:
      apiKey: config-secret
`), 0o600))
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set("openai", appcredential.StoredCredential{Type: appcredential.TypeAPIKey, Key: "stored-secret"}))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status", "openai", "anthropic", "openrouter"})
	require.NoError(t, err)
	requireAuthStatusRow(t, output.String(), "openai", "stored api_key")
	requireAuthStatusRow(t, output.String(), "anthropic", "environment")
	requireAuthStatusRow(t, output.String(), "openrouter", "provider-config")
	require.NotContains(t, output.String(), "stored-secret")
	require.NotContains(t, output.String(), "env-secret")
	require.NotContains(t, output.String(), "config-secret")
}

func TestCommand_StatusReportsWebProviderSources(t *testing.T) {
	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })
	t.Setenv("EXA_API_KEY", "exa-env-secret")
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`
web:
  provider: tavily
  apiKey: tavily-config-secret
`), 0o600))
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set("firecrawl", appcredential.StoredCredential{
		Type: appcredential.TypeAPIKey,
		Key:  "firecrawl-stored-secret",
	}))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status", "firecrawl", "exa", "tavily"})
	require.NoError(t, err)
	requireAuthStatusRow(t, output.String(), "firecrawl", "stored api_key")
	requireAuthStatusRow(t, output.String(), "exa", "environment")
	requireAuthStatusRow(t, output.String(), "tavily", "provider-config")
	require.NotContains(t, output.String(), "firecrawl-stored-secret")
	require.NotContains(t, output.String(), "exa-env-secret")
	require.NotContains(t, output.String(), "tavily-config-secret")
}

func TestCommand_StatusReportsStoredOAuthExpiryStates(t *testing.T) {
	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })

	expired := time.Now().Add(-time.Hour)
	fresh := time.Now().Add(time.Hour)
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set("openai", appcredential.StoredCredential{
		Type:      appcredential.TypeOAuth,
		Token:     "old-token",
		ExpiresAt: &expired,
	}))
	require.NoError(t, store.Set("anthropic", appcredential.StoredCredential{
		Type:      appcredential.TypeOAuth,
		Token:     "fresh-token",
		ExpiresAt: &fresh,
	}))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status", "openai", "anthropic"})
	require.NoError(t, err)
	requireAuthStatusRow(t, output.String(), "openai", "stored oauth expired")
	requireAuthStatusRow(t, output.String(), "anthropic", "stored oauth refreshable")
	require.NotContains(t, output.String(), "old-token")
	require.NotContains(t, output.String(), "fresh-token")
}

func TestCommand_StatusReportsCustomProviderEnvSource(t *testing.T) {
	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })
	t.Setenv("CUSTOM_PROVIDER_KEY", "custom-secret")
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`
models:
  providers:
    custom:
      apiKeyEnv:
        - CUSTOM_PROVIDER_KEY
`), 0o600))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status", "custom"})
	require.NoError(t, err)
	requireAuthStatusRow(t, output.String(), "custom", "environment")
	require.NotContains(t, output.String(), "custom-secret")
}

func TestCommand_StatusReportsAllKnownProviders(t *testing.T) {
	setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status"})
	require.NoError(t, err)
	requireAuthStatusRow(t, output.String(), "anthropic", "missing")
	requireAuthStatusRow(t, output.String(), "github-copilot", "missing")
	requireAuthStatusRow(t, output.String(), "openai", "missing")
	requireAuthStatusRow(t, output.String(), "openrouter", "missing")
	requireAuthStatusRow(t, output.String(), "exa", "missing")
	requireAuthStatusRow(t, output.String(), "firecrawl", "missing")
	requireAuthStatusRow(t, output.String(), "parallel", "missing")
	requireAuthStatusRow(t, output.String(), "tavily", "missing")
}

func TestCommand_StatusReportsConfigAndStoredProvidersWithoutArgs(t *testing.T) {
	home := setAuthTestProfile(t)
	var output bytes.Buffer
	restore := SetOutput(&output)
	t.Cleanup(func() { SetOutput(restore) })
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`
models:
  providers:
    custom-config:
      apiKey: config-secret
`), 0o600))
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set("custom-stored", appcredential.StoredCredential{
		Type: appcredential.TypeAPIKey,
		Key:  "stored-secret",
	}))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status"})
	require.NoError(t, err)
	requireAuthStatusRow(t, output.String(), "custom-config", "provider-config")
	requireAuthStatusRow(t, output.String(), "custom-stored", "stored api_key")
	require.NotContains(t, output.String(), "config-secret")
	require.NotContains(t, output.String(), "stored-secret")
}

func TestCommand_StatusReturnsCredentialStoreParseError(t *testing.T) {
	home := setAuthTestProfile(t)
	require.NoError(t, os.Chmod(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte("{"), 0o600))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status", "openai"})
	require.ErrorContains(t, err, "parse credential store")
}

func TestCommand_StatusReturnsOutputError(t *testing.T) {
	setAuthTestProfile(t)
	restore := SetOutput(errorWriter{})
	t.Cleanup(func() { SetOutput(restore) })

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "status", "openai"})
	require.EqualError(t, err, "write failed")
}

func TestCommand_ManagesLocalMorphIdentityAndGeneratesTokenFile(t *testing.T) {
	home := setAuthTestProfile(t)
	output := &bytes.Buffer{}
	restore := SetOutput(output)
	t.Cleanup(func() { SetOutput(restore) })

	err := NewCommand().Run(context.Background(), []string{"auth", "identity", "init"})
	require.NoError(t, err)
	record, found, err := appcredential.NewFileStore(
		filepath.Join(home, "auth.json"),
	).LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, output.String(), record.IdentityID)

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{"auth", "--json", "identity", "show"})
	require.NoError(t, err)
	require.Contains(t, output.String(), record.IdentityID)
	require.NotContains(t, output.String(), "PRIVATE KEY")

	tokenPath := filepath.Join(home, "token.jwt")
	err = NewCommand().Run(context.Background(), []string{
		"auth", "token", "generate", "--output", tokenPath,
	})
	require.NoError(t, err)
	info, err := os.Stat(tokenPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	body, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(strings.TrimSpace(string(body)), "."))

	narrowedPath := filepath.Join(home, "narrowed.jwt")
	err = NewCommand().Run(context.Background(), []string{
		"auth", "token", "generate",
		"--method", "/morph.v1.SessionService/List",
		"--output", narrowedPath,
	})
	require.NoError(t, err)
	identity, err := morphauth.ParseIdentity([]byte(record.PrivateKey), record.Generation)
	require.NoError(t, err)
	narrowed, err := os.ReadFile(narrowedPath)
	require.NoError(t, err)
	claims, err := morphauth.VerifyAccessToken(
		strings.TrimSpace(string(narrowed)),
		identity.PublicKey,
		morphauth.VerifyOptions{
			Audience: "morph-rpc:test",
			Issuer:   identity.ID,
		},
	)
	require.NoError(t, err)
	require.Contains(t, claims.Methods, "/morph.v1.SessionService/List")
	require.Contains(t, claims.Methods, "/morph.v1.AuthService/OpenSession")

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "--json", "token", "generate",
	})
	require.NoError(t, err)
	var generated map[string]string
	require.NoError(t, json.Unmarshal(output.Bytes(), &generated))
	require.Equal(t, 2, strings.Count(generated["token"], "."))

	err = NewCommand().Run(context.Background(), []string{
		"auth", "token", "generate", "--ttl", "25h",
	})
	require.ErrorContains(t, err, "token TTL must be greater than zero and at most")

	err = NewCommand().Run(context.Background(), []string{"auth", "identity", "rotate"})
	require.NoError(t, err)
	rotated, found, err := appcredential.NewFileStore(
		filepath.Join(home, "auth.json"),
	).LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, record.IdentityID, rotated.IdentityID)
	require.Equal(t, record.Generation+1, rotated.Generation)
}

func TestRotateIdentity_RejectsAuthDatabaseStatFailureBeforePreparingKey(t *testing.T) {
	home := setAuthTestProfile(t)
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	current, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	databasePath := filepath.Join(home, "auth.db")
	if err := os.Symlink(databasePath, databasePath); err != nil {
		t.Skipf("create auth database symlink: %v", err)
	}

	err = NewCommand().Run(context.Background(), []string{
		"auth", "identity", "rotate",
	})
	require.ErrorContains(t, err, "inspect RPC auth database")
	record, found, err := store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, current.ID, record.IdentityID)
	require.Nil(t, record.Pending)
}

func TestLoadOrPrepareIdentityRotation_ResumesPendingKey(t *testing.T) {
	store := appcredential.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	_, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	current, pending, err := store.PrepareIdentityRotation()
	require.NoError(t, err)

	resumedRecord, resumed, err := loadOrPrepareIdentityRotation(store)
	require.NoError(t, err)
	require.Equal(t, current.IdentityID, resumedRecord.IdentityID)
	require.Equal(t, pending.ID, resumed.ID)
	require.Equal(t, pending.Generation, resumed.Generation)
}

func TestShouldAbortIdentityRotation_OnlyAbortsDefinitePrecommitFailures(t *testing.T) {
	require.True(t, shouldAbortIdentityRotation(
		status.Error(codes.InvalidArgument, "invalid"),
	))
	require.True(t, shouldAbortIdentityRotation(
		status.Error(codes.PermissionDenied, "denied"),
	))
	require.False(t, shouldAbortIdentityRotation(
		status.Error(codes.Unavailable, "response lost"),
	))
	require.False(t, shouldAbortIdentityRotation(context.DeadlineExceeded))
}

func TestCheckPendingIdentityActive_UsesPendingKey(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(2)
	require.NoError(t, err)
	original := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = original })
	newMorphAuthClient = func(
		_ context.Context,
		cfg *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, hex.EncodeToString(identity.PrivateKey.Seed()), cfg.Auth.Key)
		require.Equal(t, identity.Generation, cfg.Auth.Generation)
		require.Equal(t, []string{
			morphpb.AuthService_IdentityStatus_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			identityStatus: &morphpb.GetAuthIdentityStatusResponse{
				IdentityId: identity.ID,
				Generation: identity.Generation,
				Status:     morphauth.StatusActive,
			},
		}}, nil
	}

	active, err := checkPendingIdentityActive(
		context.Background(), config.NewDefaultConfig(), identity,
	)
	require.NoError(t, err)
	require.True(t, active)
}

func TestCommand_LogoutRemovesStoredCredential(t *testing.T) {
	home := setAuthTestProfile(t)
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set("openai", appcredential.StoredCredential{Type: appcredential.TypeAPIKey, Key: "stored-secret"}))

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "logout", "openai"})
	require.NoError(t, err)

	_, ok, err := store.Get("openai")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestCommand_LogoutValidatesProviderArg(t *testing.T) {
	setAuthTestProfile(t)

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "logout"})
	require.EqualError(t, err, "provider is required")
}

func TestCommand_LogoutReturnsOutputError(t *testing.T) {
	home := setAuthTestProfile(t)
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set("openai", appcredential.StoredCredential{Type: appcredential.TypeAPIKey, Key: "stored-secret"}))
	restore := SetOutput(errorWriter{})
	t.Cleanup(func() { SetOutput(restore) })

	err := newProviderTestCommand().Run(context.Background(), []string{"provider", "logout", "openai"})
	require.EqualError(t, err, "write failed")
}

func TestCommand_ShowsHelpWithoutSubcommand(t *testing.T) {
	setAuthTestProfile(t)

	err := NewCommand().Run(context.Background(), []string{"auth"})
	require.NoError(t, err)
}

func TestCommand_ExposesOnlyRPCAuthenticationOperations(t *testing.T) {
	command := NewCommand()
	names := make([]string, 0, len(command.Commands))
	for _, child := range command.Commands {
		names = append(names, child.Name)
	}

	require.Equal(t, []string{
		"identity",
		"session",
		"token",
		"authorization",
		"audit",
		"mtls",
	}, names)
}

func TestLoadAuthConfig_ReturnsConfigLoadError(t *testing.T) {
	home := setAuthTestProfile(t)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("models: ["), 0o600))

	_, err := loadAuthConfig(nil)
	require.ErrorContains(t, err, "failed to parse config file")
}

func TestSetOutput_NilDiscardsOutput(t *testing.T) {
	previous := SetOutput(nil)
	t.Cleanup(func() { SetOutput(previous) })

	require.Equal(t, io.Discard, authOutput)
}

func TestAuthorizationOutput_EncodesPublicKeyAsHex(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	output := &bytes.Buffer{}
	restore := SetOutput(output)
	t.Cleanup(func() { SetOutput(restore) })

	err = writeJSONValue(getAuthorizationOutput(&morphpb.AuthAuthorization{
		IdentityId: identity.ID,
		PublicKey:  identity.PublicKey,
	}))
	require.NoError(t, err)

	var value map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &value))
	require.Equal(t, hex.EncodeToString(identity.PublicKey), value["public_key"])
}

func TestRandomAuthID_UsesHexEncoding(t *testing.T) {
	id, err := randomAuthID()
	require.NoError(t, err)
	require.Len(t, id, 48)
	_, err = hex.DecodeString(id)
	require.NoError(t, err)
}

func TestFormatAuthStatus_ReturnsUnknownSourceValue(t *testing.T) {
	status := appcredential.Status{
		Configured: true,
		Source:     appcredential.CredentialSource("runtime"),
	}

	require.Equal(t, "runtime", formatAuthStatus(status))
}

func requireAuthStatusRow(t *testing.T, output string, provider string, status string) {
	t.Helper()
	require.Contains(t, output, "PROVIDER")
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == provider {
			require.Equal(t, status, strings.Join(fields[1:], " "))
			return
		}
	}

	t.Fatalf("provider status row for %q not found in:\n%s", provider, output)
}

func TestGetFirstEnvValue_SkipsBlankAndMissingKeys(t *testing.T) {
	value, key := getFirstEnvValue([]string{" ", "MISSING_AUTH_TEST_KEY"})

	require.Empty(t, value)
	require.Empty(t, key)
}

func TestGetWebProviderEnvKeys_ReturnsGenericFallbackForUnknownProvider(t *testing.T) {
	require.Equal(t, []string{"MORPH_WEB_API_KEY"}, config.WebProviderAPIKeyEnv("custom"))
}

func setAuthTestProfile(t *testing.T) string {
	t.Helper()

	for _, key := range []string{
		"OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"ANTHROPIC_API_KEY",
		"COPILOT_GITHUB_TOKEN",
		"MORPH_FIRECRAWL_API_KEY",
		"FIRECRAWL_API_KEY",
		"MORPH_PARALLEL_API_KEY",
		"PARALLEL_API_KEY",
		"MORPH_TAVILY_API_KEY",
		"TAVILY_API_KEY",
		"MORPH_EXA_API_KEY",
		"EXA_API_KEY",
		"MORPH_WEB_API_KEY",
	} {
		t.Setenv(key, "")
	}

	original := profile.Active()
	home := t.TempDir()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: home}))
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	return home
}

type errorWriter struct{}

type morphAuthClientStub struct {
	api rpcclient.AuthAPI
}

func (s morphAuthClientStub) Close() error {
	return nil
}

func (s morphAuthClientStub) AuthAPI() rpcclient.AuthAPI {
	return s.api
}

type authAPIStub struct {
	rpcclient.AuthAPI
	identityStatus *morphpb.GetAuthIdentityStatusResponse
	identityErr    error
}

func (s authAPIStub) IdentityStatus(
	context.Context,
) (*morphpb.GetAuthIdentityStatusResponse, error) {
	return s.identityStatus, s.identityErr
}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type fakeSubscriptionProvider struct {
	login func(appcredential.LoginOptions)
}

func (p fakeSubscriptionProvider) Login(
	_ context.Context,
	options appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	if p.login != nil {
		p.login(options)
	}
	return appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: "subscription-secret",
	}, nil
}

func (fakeSubscriptionProvider) Refresh(
	context.Context,
	appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	return appcredential.StoredCredential{}, nil
}

func (fakeSubscriptionProvider) AuthHeaders(
	context.Context,
	appcredential.StoredCredential,
) (map[string]string, error) {
	return nil, nil
}
