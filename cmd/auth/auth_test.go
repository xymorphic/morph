package authcmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/config"
	appcredential "github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestCommand_GeneratesStandaloneIdentityKeypair(t *testing.T) {
	output := &bytes.Buffer{}
	restore := SetOutput(output)
	t.Cleanup(func() { SetOutput(restore) })

	err := NewCommand().Run(context.Background(), []string{"auth", "--json", "genkey"})
	require.NoError(t, err)

	var generated generatedKeyOutput
	require.NoError(t, json.Unmarshal(output.Bytes(), &generated))
	require.Len(t, generated.IdentityID, 40)
	require.Len(t, generated.PrivateKey32, ed25519.SeedSize*2)
	require.Len(t, generated.PrivateKey64, ed25519.PrivateKeySize*2)
	require.Len(t, generated.PublicKey, ed25519.PublicKeySize*2)

	fromSeed, err := morphauth.ParseIdentity([]byte(generated.PrivateKey32), 1)
	require.NoError(t, err)
	fromPrivateKey, err := morphauth.ParseIdentity([]byte(generated.PrivateKey64), 1)
	require.NoError(t, err)
	require.Equal(t, generated.IdentityID, fromSeed.ID)
	require.Equal(t, generated.IdentityID, fromPrivateKey.ID)
	require.Equal(t, generated.PublicKey, hex.EncodeToString(fromSeed.PublicKey))

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{"auth", "genkey"})
	require.NoError(t, err)
	require.Contains(t, output.String(), "identity: ")
	require.Contains(t, output.String(), "private key (32-byte seed): ")
	require.Contains(t, output.String(), "private key (64 bytes): ")
	require.Contains(t, output.String(), "public key (32 bytes): ")
}

func TestCommand_GenkeyReturnsGenerationFailure(t *testing.T) {
	originalGenerate := generateMorphIdentity
	t.Cleanup(func() { generateMorphIdentity = originalGenerate })
	generateMorphIdentity = func(uint64) (morphauth.Identity, error) {
		return morphauth.Identity{}, errors.New("generate failed")
	}

	err := NewCommand().Run(context.Background(), []string{"auth", "genkey"})
	require.ErrorContains(t, err, "generate failed")
}

func TestRotateIdentity_RejectsAuthDatabaseStatFailureBeforePreparingKey(t *testing.T) {
	home := setAuthTestProfile(t)
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	current, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	databasePath := datadir.AuthDBPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(databasePath), 0o700))
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
		"genkey",
		"identity",
		"session",
		"token",
		"authorization",
		"audit",
		"prune",
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

func TestCommand_AuditListHonorsTextAndJSONOutput(t *testing.T) {
	setAuthTestProfile(t)
	createdAt := timestamppb.New(time.Date(2026, 7, 25, 18, 30, 0, 0, time.UTC))
	events := []*morphpb.AuthAuditEvent{{
		Id:         "audit-1",
		Type:       "scope_denied",
		IdentityId: "identity-1",
		SessionId:  "session-1",
		TokenId:    "token-1",
		Method:     "/morph.v1.AuthService/ListTokens",
		Reason:     "method is not authorized",
		CreatedAt:  createdAt,
	}}
	var requestedOptions []rpcclient.AuthAuditListOptions
	originalClient := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = originalClient })
	newMorphAuthClient = func(
		_ context.Context,
		_ *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, []string{
			morphpb.AuthService_ListAudit_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			auditEvents: events,
			auditOptions: func(options rpcclient.AuthAuditListOptions) {
				requestedOptions = append(requestedOptions, options)
			},
		}}, nil
	}
	output := &bytes.Buffer{}
	restoreOutput := SetOutput(output)
	t.Cleanup(func() { SetOutput(restoreOutput) })

	err := NewCommand().Run(context.Background(), []string{
		"auth", "audit", "list",
	})
	require.NoError(t, err)
	require.Equal(t, int32(25), requestedOptions[0].Limit)
	require.True(t, strings.HasPrefix(output.String(), "[scope_denied] "))
	require.Contains(
		t,
		output.String(),
		"[scope_denied] "+formatProtoTime(createdAt),
	)
	require.Contains(t, output.String(), "Event ID:    audit-1")
	require.Contains(t, output.String(), "Identity:    identity-1")
	require.Contains(t, output.String(), "Session:     session-1")
	require.Contains(t, output.String(), "Token:       token-1")
	require.Contains(t, output.String(), "Method:      /morph.v1.AuthService/ListTokens")
	require.Contains(t, output.String(), "Reason:      method is not authorized")

	output.Reset()
	requestedAt := time.Now()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "--json", "audit", "list", "--limit", "1",
		"--type", "scope_denied",
		"--identity", "identity-1",
		"--session", "session-1",
		"--token", "token-1",
		"--method", "/morph.v1.AuthService/ListTokens",
		"--since", "1h",
	})
	require.NoError(t, err)
	require.Len(t, requestedOptions, 2)
	require.Equal(t, int32(1), requestedOptions[1].Limit)
	require.Equal(t, "scope_denied", requestedOptions[1].Type)
	require.Equal(t, "identity-1", requestedOptions[1].IdentityID)
	require.Equal(t, "session-1", requestedOptions[1].SessionID)
	require.Equal(t, "token-1", requestedOptions[1].TokenID)
	require.Equal(t, "/morph.v1.AuthService/ListTokens", requestedOptions[1].Method)
	require.WithinDuration(t, requestedAt.Add(-time.Hour), requestedOptions[1].Since, time.Second)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "audit-1", decoded[0]["id"])
}

func TestCommand_SessionListHonorsTextAndJSONOutput(t *testing.T) {
	setAuthTestProfile(t)
	createdAt := timestamppb.New(time.Date(2026, 7, 25, 18, 30, 0, 0, time.UTC))
	lastSeenAt := timestamppb.New(time.Date(2026, 7, 25, 18, 45, 0, 0, time.UTC))
	idleExpiresAt := timestamppb.New(time.Date(2026, 7, 25, 19, 0, 0, 0, time.UTC))
	absoluteExpiresAt := timestamppb.New(time.Date(2026, 7, 26, 18, 30, 0, 0, time.UTC))
	sessions := []*morphpb.AuthSession{{
		Id:                "session-1",
		IdentityId:        "identity-1",
		UserId:            "user-1",
		Source:            "cli",
		Status:            morphauth.StatusActive,
		CreatedAt:         createdAt,
		LastSeenAt:        lastSeenAt,
		IdleExpiresAt:     idleExpiresAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
	}}
	var requestedOptions []rpcclient.AuthSessionListOptions
	originalClient := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = originalClient })
	newMorphAuthClient = func(
		_ context.Context,
		_ *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, []string{
			morphpb.AuthService_ListSessions_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			sessions: sessions,
			sessionOptions: func(options rpcclient.AuthSessionListOptions) {
				requestedOptions = append(requestedOptions, options)
			},
		}}, nil
	}
	output := &bytes.Buffer{}
	restoreOutput := SetOutput(output)
	t.Cleanup(func() { SetOutput(restoreOutput) })

	err := NewCommand().Run(context.Background(), []string{
		"auth", "session", "list",
	})
	require.NoError(t, err)
	require.Equal(t, []rpcclient.AuthSessionListOptions{{Limit: 25}}, requestedOptions)
	require.True(t, strings.HasPrefix(output.String(), "[active] session-1\n"))
	require.Contains(t, output.String(), "Identity:    identity-1")
	require.Contains(t, output.String(), "User:        user-1")
	require.Contains(t, output.String(), "Source:      cli")
	require.Contains(t, output.String(), "Created:     "+formatProtoTime(createdAt))
	require.Contains(t, output.String(), "Last seen:   "+formatProtoTime(lastSeenAt))
	require.Contains(t, output.String(), "Idle expires: "+formatProtoTime(idleExpiresAt))
	require.Contains(t, output.String(), "Expires:     "+formatProtoTime(absoluteExpiresAt))

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "--json", "session", "list", "--limit", "3", "--status", "expired",
	})
	require.NoError(t, err)
	require.Equal(t, []rpcclient.AuthSessionListOptions{
		{Limit: 25},
		{Limit: 3, Status: "expired"},
	}, requestedOptions)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "session-1", decoded[0]["id"])
}

func TestCommand_TokenListHonorsTextAndJSONOutput(t *testing.T) {
	setAuthTestProfile(t)
	expiresAt := timestamppb.New(time.Date(2026, 7, 25, 19, 0, 0, 0, time.UTC))
	lastUsedAt := timestamppb.New(time.Date(2026, 7, 25, 18, 45, 0, 0, time.UTC))
	tokens := []*morphpb.AuthToken{{
		Id:         "token-1",
		SessionId:  "session-1",
		IdentityId: "identity-1",
		UserId:     "user-1",
		Status:     morphauth.StatusActive,
		ExpiresAt:  expiresAt,
		LastUsedAt: lastUsedAt,
		UseCount:   3,
	}}
	var requestedOptions []rpcclient.AuthTokenListOptions
	originalClient := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = originalClient })
	newMorphAuthClient = func(
		_ context.Context,
		_ *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, []string{
			morphpb.AuthService_ListTokens_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			tokens: tokens,
			tokenOptions: func(options rpcclient.AuthTokenListOptions) {
				requestedOptions = append(requestedOptions, options)
			},
		}}, nil
	}
	output := &bytes.Buffer{}
	restoreOutput := SetOutput(output)
	t.Cleanup(func() { SetOutput(restoreOutput) })

	err := NewCommand().Run(context.Background(), []string{
		"auth", "token", "list",
	})
	require.NoError(t, err)
	require.Equal(t, []rpcclient.AuthTokenListOptions{{Limit: 25}}, requestedOptions)
	require.True(t, strings.HasPrefix(output.String(), "[active] token-1\n"))
	require.Contains(t, output.String(), "[active] token-1")
	require.Contains(t, output.String(), "Session:     session-1")
	require.Contains(t, output.String(), "Identity:    identity-1")
	require.Contains(t, output.String(), "User:        user-1")
	require.Contains(t, output.String(), "Expires:     "+formatProtoTime(expiresAt))
	require.Contains(t, output.String(), "Last used:   "+formatProtoTime(lastUsedAt))
	require.Contains(t, output.String(), "Uses:        3")

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "--json", "token", "list", "--limit", "3", "--status", "revoked",
	})
	require.NoError(t, err)
	require.Equal(t, []rpcclient.AuthTokenListOptions{
		{Limit: 25},
		{Limit: 3, Status: "revoked"},
	}, requestedOptions)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "token-1", decoded[0]["id"])
}

func TestCommand_AuthorizationListHonorsTextAndJSONOutput(t *testing.T) {
	setAuthTestProfile(t)
	createdAt := timestamppb.New(time.Date(2026, 7, 25, 18, 30, 0, 0, time.UTC))
	updatedAt := timestamppb.New(time.Date(2026, 7, 25, 18, 45, 0, 0, time.UTC))
	revokedAt := timestamppb.New(time.Date(2026, 7, 25, 19, 0, 0, 0, time.UTC))
	authorizations := []*morphpb.AuthAuthorization{{
		IdentityId: "identity-1",
		PublicKey:  []byte{0x01, 0x02},
		OwnerId:    "owner-1",
		UserId:     "user-1",
		Roles:      []string{"operator"},
		Services: []string{
			"morph.v1.SessionService",
			"morph.v1.AuthService",
		},
		Methods: []string{
			"/morph.v1.SessionService/List",
			"/morph.v1.AuthService/OpenSession",
		},
		MaximumTtlSeconds: 1800,
		Generation:        2,
		Revision:          3,
		Status:            morphauth.StatusActive,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, {
		IdentityId:     "identity-2",
		Status:         morphauth.StatusRevoked,
		RevokedAt:      revokedAt,
		RevocationNote: "retired",
	}}
	var requestedOptions []rpcclient.AuthAuthorizationListOptions
	originalClient := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = originalClient })
	newMorphAuthClient = func(
		_ context.Context,
		_ *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, []string{
			morphpb.AuthService_ListAuthorizations_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			authorizations: authorizations,
			authorizationOptions: func(options rpcclient.AuthAuthorizationListOptions) {
				requestedOptions = append(requestedOptions, options)
			},
		}}, nil
	}
	output := &bytes.Buffer{}
	restoreOutput := SetOutput(output)
	t.Cleanup(func() { SetOutput(restoreOutput) })

	err := NewCommand().Run(context.Background(), []string{
		"auth", "authorization", "list", "--status", "active",
	})
	require.NoError(t, err)
	require.Equal(t, []rpcclient.AuthAuthorizationListOptions{{Status: "active"}}, requestedOptions)
	require.True(t, strings.HasPrefix(output.String(), "[active] identity-1\n"))
	require.Contains(t, output.String(), "Public key:  0102")
	require.Contains(t, output.String(), "Owner:       owner-1")
	require.Contains(t, output.String(), "User:        user-1")
	require.Contains(t, output.String(), "Roles:       operator")
	require.Contains(t, output.String(), "Services:    morph.v1.SessionService\n")
	require.Contains(t, output.String(), "             morph.v1.AuthService\n")
	require.Contains(t, output.String(), "Methods:     /morph.v1.SessionService/List\n")
	require.Contains(t, output.String(), "             /morph.v1.AuthService/OpenSession\n")
	require.Contains(t, output.String(), "Maximum TTL: 1800s")
	require.Contains(t, output.String(), "Generation:  2")
	require.Contains(t, output.String(), "Revision:    3")
	require.Contains(t, output.String(), "Created:     "+formatProtoTime(createdAt))
	require.Contains(t, output.String(), "Updated:     "+formatProtoTime(updatedAt))
	require.Contains(t, output.String(), "\n[revoked] identity-2\n")
	require.Contains(t, output.String(), "Revoked at:  "+formatProtoTime(revokedAt))
	require.Contains(t, output.String(), "Reason:      retired")

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "--json", "authorization", "list", "--status", "revoked",
	})
	require.NoError(t, err)
	require.Equal(t, []rpcclient.AuthAuthorizationListOptions{
		{Status: "active"},
		{Status: "revoked"},
	}, requestedOptions)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Len(t, decoded, 2)
	require.Equal(t, "identity-1", decoded[0]["identity_id"])
	require.Equal(t, "0102", decoded[0]["public_key"])
}

func TestCommand_AuthorizationGrantUsesConciseTextAndDetailedJSON(t *testing.T) {
	setAuthTestProfile(t)
	publicKey := strings.Repeat("01", ed25519.PublicKeySize)
	result := &morphpb.AuthAuthorization{
		IdentityId:        "delegate-id",
		PublicKey:         bytes.Repeat([]byte{0x01}, ed25519.PublicKeySize),
		OwnerId:           "delegate-owner",
		UserId:            "delegate-id",
		Roles:             []string{"operator"},
		Methods:           []string{"/morph.v1.SessionService/List"},
		MaximumTtlSeconds: 1800,
		Generation:        1,
		Revision:          1,
		Status:            morphauth.StatusActive,
	}
	originalClient := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = originalClient })
	newMorphAuthClient = func(
		_ context.Context,
		_ *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, []string{
			morphpb.AuthService_GrantAuthorization_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			grantedAuthorization: result,
			grantAuthorization: func(request *morphpb.AuthAuthorization) {
				require.Equal(t, "delegate-id", request.GetIdentityId())
				require.Equal(t, "delegate-owner", request.GetOwnerId())
				require.Equal(t, []string{"operator"}, request.GetRoles())
			},
		}}, nil
	}
	output := &bytes.Buffer{}
	restoreOutput := SetOutput(output)
	t.Cleanup(func() { SetOutput(restoreOutput) })
	args := []string{
		"auth", "authorization", "grant",
		"--identity", "delegate-id",
		"--public-key", publicKey,
		"--owner", "delegate-owner",
		"--user", "delegate-id",
		"--role", "operator",
		"--method", "/morph.v1.SessionService/List",
		"--maximum-ttl", "30m",
	}

	err := NewCommand().Run(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "authorization granted for delegate-id\n", output.String())

	output.Reset()
	jsonArgs := append([]string{"auth", "--json"}, args[1:]...)
	err = NewCommand().Run(context.Background(), jsonArgs)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Equal(t, "delegate-id", decoded["identity_id"])
	require.Equal(t, strings.Repeat("01", ed25519.PublicKeySize), decoded["public_key"])
}

func TestAuditListToText_FormatsEmptyState(t *testing.T) {
	require.Equal(
		t,
		"No RPC authentication audit events found.\n",
		auditListToText(nil),
	)
}

func TestAuthorizationListToText_FormatsEmptyState(t *testing.T) {
	require.Equal(
		t,
		"No RPC identity authorizations found.\n",
		authorizationListToText(nil),
	)
}

func TestFormatAuthSeconds_OmitsNonPositiveValues(t *testing.T) {
	require.Empty(t, formatAuthSeconds(0))
	require.Empty(t, formatAuthSeconds(-1))
}

func TestCommand_PruneHonorsTextJSONAndDryRunOptions(t *testing.T) {
	setAuthTestProfile(t)
	var requestedOptions []rpcclient.AuthPruneOptions
	var pruneErr error
	originalClient := newMorphAuthClient
	t.Cleanup(func() { newMorphAuthClient = originalClient })
	newMorphAuthClient = func(
		_ context.Context,
		_ *config.Config,
		methods []string,
	) (morphAuthClient, error) {
		require.Equal(t, []string{
			morphpb.AuthService_Prune_FullMethodName,
		}, methods)
		return morphAuthClientStub{api: authAPIStub{
			pruneResult: &morphpb.PruneAuthResponse{
				Tokens: 2, Sessions: 3, Authorizations: 4, AuditEvents: 5,
			},
			pruneOptions: func(options rpcclient.AuthPruneOptions) {
				requestedOptions = append(requestedOptions, options)
			},
			pruneErr: pruneErr,
		}}, nil
	}
	output := &bytes.Buffer{}
	restoreOutput := SetOutput(output)
	t.Cleanup(func() { SetOutput(restoreOutput) })

	requestedAt := time.Now()
	err := NewCommand().Run(context.Background(), []string{
		"auth", "prune", "--older-than", "2h", "--limit", "50",
	})
	require.NoError(t, err)
	require.Len(t, requestedOptions, 1)
	require.WithinDuration(t, requestedAt.Add(-2*time.Hour), requestedOptions[0].Before, time.Second)
	require.Equal(t, int32(50), requestedOptions[0].Limit)
	require.False(t, requestedOptions[0].DryRun)
	require.Equal(t, "Tokens:         2\n"+
		"Sessions:       3\n"+
		"Authorizations: 4\n"+
		"Audit events:   5\n"+
		"Total:          14\n", output.String())

	output.Reset()
	requestedAt = time.Now()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "--json", "prune", "--older-than", "1h", "--limit", "25", "--dry-run",
	})
	require.NoError(t, err)
	require.Len(t, requestedOptions, 2)
	require.WithinDuration(t, requestedAt.Add(-time.Hour), requestedOptions[1].Before, time.Second)
	require.Equal(t, int32(25), requestedOptions[1].Limit)
	require.True(t, requestedOptions[1].DryRun)
	var decoded authPruneOutput
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Equal(t, int32(14), decoded.Total)
	require.True(t, decoded.DryRun)

	output.Reset()
	err = NewCommand().Run(context.Background(), []string{
		"auth", "prune", "--dry-run",
	})
	require.NoError(t, err)
	require.Contains(t, output.String(), "Dry run:        true\n")

	pruneErr = errors.New("prune failed")
	err = NewCommand().Run(context.Background(), []string{
		"auth", "prune",
	})
	require.ErrorContains(t, err, "prune failed")
}

func TestCommand_PruneRejectsInvalidBounds(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{
			args: []string{"auth", "prune", "--older-than=-1s"},
			want: "older-than duration must not be negative",
		},
		{
			args: []string{"auth", "prune", "--limit", "0"},
			want: "limit must be between 1 and " + strconv.Itoa(morphauth.MaximumPruneLimit),
		},
		{
			args: []string{
				"auth", "prune", "--limit",
				strconv.Itoa(morphauth.MaximumPruneLimit + 1),
			},
			want: "limit must be between 1 and " + strconv.Itoa(morphauth.MaximumPruneLimit),
		},
	} {
		err := NewCommand().Run(context.Background(), test.args)
		require.ErrorContains(t, err, test.want)
	}
}

func TestWriteAuthPruneResult_ReturnsWriteFailure(t *testing.T) {
	restoreOutput := SetOutput(errorWriter{})
	t.Cleanup(func() { SetOutput(restoreOutput) })

	err := writeAuthPruneResult(&cli.Command{}, &morphpb.PruneAuthResponse{})
	require.ErrorContains(t, err, "write failed")
}

func TestSessionListToText_FormatsEmptyState(t *testing.T) {
	require.Equal(t, "No RPC authentication sessions found.\n", sessionListToText(nil))
}

func TestTokenListToText_FormatsEmptyState(t *testing.T) {
	require.Equal(t, "No RPC access tokens found.\n", tokenListToText(nil))
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
	identityStatus       *morphpb.GetAuthIdentityStatusResponse
	identityErr          error
	auditEvents          []*morphpb.AuthAuditEvent
	auditErr             error
	auditOptions         func(rpcclient.AuthAuditListOptions)
	sessions             []*morphpb.AuthSession
	sessionErr           error
	sessionOptions       func(rpcclient.AuthSessionListOptions)
	tokens               []*morphpb.AuthToken
	tokenErr             error
	tokenOptions         func(rpcclient.AuthTokenListOptions)
	authorizations       []*morphpb.AuthAuthorization
	authorizationErr     error
	authorizationOptions func(rpcclient.AuthAuthorizationListOptions)
	grantedAuthorization *morphpb.AuthAuthorization
	grantAuthorization   func(*morphpb.AuthAuthorization)
	pruneResult          *morphpb.PruneAuthResponse
	pruneErr             error
	pruneOptions         func(rpcclient.AuthPruneOptions)
}

func (s authAPIStub) IdentityStatus(
	context.Context,
) (*morphpb.GetAuthIdentityStatusResponse, error) {
	return s.identityStatus, s.identityErr
}

func (s authAPIStub) ListAudit(
	_ context.Context,
	options rpcclient.AuthAuditListOptions,
) ([]*morphpb.AuthAuditEvent, error) {
	if s.auditOptions != nil {
		s.auditOptions(options)
	}
	return s.auditEvents, s.auditErr
}

func (s authAPIStub) ListSessions(
	_ context.Context,
	options rpcclient.AuthSessionListOptions,
) ([]*morphpb.AuthSession, error) {
	if s.sessionOptions != nil {
		s.sessionOptions(options)
	}
	return s.sessions, s.sessionErr
}

func (s authAPIStub) ListTokens(
	_ context.Context,
	options rpcclient.AuthTokenListOptions,
) ([]*morphpb.AuthToken, error) {
	if s.tokenOptions != nil {
		s.tokenOptions(options)
	}
	return s.tokens, s.tokenErr
}

func (s authAPIStub) ListAuthorizations(
	_ context.Context,
	options rpcclient.AuthAuthorizationListOptions,
) ([]*morphpb.AuthAuthorization, error) {
	if s.authorizationOptions != nil {
		s.authorizationOptions(options)
	}
	return s.authorizations, s.authorizationErr
}

func (s authAPIStub) GrantAuthorization(
	_ context.Context,
	authorization *morphpb.AuthAuthorization,
) (*morphpb.AuthAuthorization, error) {
	if s.grantAuthorization != nil {
		s.grantAuthorization(authorization)
	}
	return s.grantedAuthorization, s.authorizationErr
}

func (s authAPIStub) Prune(
	_ context.Context,
	options rpcclient.AuthPruneOptions,
) (*morphpb.PruneAuthResponse, error) {
	if s.pruneOptions != nil {
		s.pruneOptions(options)
	}
	if s.pruneResult != nil {
		s.pruneResult.DryRun = options.DryRun
	}
	return s.pruneResult, s.pruneErr
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
