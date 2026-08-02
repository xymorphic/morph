package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	appcredential "github.com/xymorphic/morph/internal/credential"
	"github.com/xymorphic/morph/internal/profile"
)

func TestResolveOptions_UsesExplicitConfig(t *testing.T) {
	setWebAuthTestProfile(t)

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider:                "exa",
			APIKey:                  "exa-config-key",
			BaseURL:                 "https://exa.example",
			MaxCharPerResult:        3200,
			MaxExtractCharPerResult: 12000,
			MaxExtractResponseBytes: 64000,
			NativeAllowedHosts:      []string{"allowed.example"},
			NativeBlockedHosts:      []string{"blocked.example"},
			NativeAllowedHostFiles:  []string{"allow.txt"},
			NativeBlockedHostFiles:  []string{"deny.txt"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ProviderExa, opts.Provider)
	require.Equal(t, "exa-config-key", opts.APIKey)
	require.Equal(t, "https://exa.example", opts.BaseURL)
	require.Equal(t, 3200, opts.MaxCharPerResult)
	require.Equal(t, 12000, opts.MaxExtractCharPerResult)
	require.Equal(t, 64000, opts.MaxExtractResponseBytes)
	require.Equal(t, []string{"allowed.example"}, opts.NativeAllowedHosts)
	require.Equal(t, []string{"blocked.example"}, opts.NativeBlockedHosts)
	require.Equal(t, []string{"allow.txt"}, opts.NativeAllowedHostFiles)
	require.Equal(t, []string{"deny.txt"}, opts.NativeBlockedHostFiles)
}

func TestResolveOptions_UsesConfigAPIKeyBeforeStoredAndEnv(t *testing.T) {
	home := setWebAuthTestProfile(t)
	t.Setenv("EXA_API_KEY", "env-exa-key")
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set(ProviderExa, appcredential.StoredCredential{
		Type: appcredential.TypeAPIKey,
		Key:  "stored-exa-key",
	}))

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderExa,
			APIKey:   "config-exa-key",
		},
	})
	require.NoError(t, err)
	require.Equal(t, ProviderExa, opts.Provider)
	require.Equal(t, "config-exa-key", opts.APIKey)
}

func TestResolveOptions_UsesStoredProviderAPIKeyBeforeEnv(t *testing.T) {
	home := setWebAuthTestProfile(t)
	t.Setenv("EXA_API_KEY", "env-exa-key")
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set(ProviderExa, appcredential.StoredCredential{
		Type: appcredential.TypeAPIKey,
		Key:  "stored-exa-key",
	}))

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderExa,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "stored-exa-key", opts.APIKey)
}

func TestResolveOptions_IgnoresStoredOAuthCredentialForWebProvider(t *testing.T) {
	home := setWebAuthTestProfile(t)
	store := appcredential.NewFileStore(filepath.Join(home, "auth.json"))
	require.NoError(t, store.Set(ProviderExa, appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: "oauth-token",
	}))

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderExa,
		},
	})
	require.NoError(t, err)
	require.Empty(t, opts.APIKey)
}

func TestResolveOptions_UsesProviderEnvWhenConfigAndStoredKeyAreMissing(t *testing.T) {
	setWebAuthTestProfile(t)
	t.Setenv("EXA_API_KEY", "env-exa-key")

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderExa,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "env-exa-key", opts.APIKey)
}

func TestResolveOptions_UsesGenericWebEnvWhenConfigStoredAndProviderEnvAreMissing(t *testing.T) {
	setWebAuthTestProfile(t)
	t.Setenv("MORPH_WEB_API_KEY", "generic-web-key")

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderTavily,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "generic-web-key", opts.APIKey)
}

func TestResolveOptions_ReturnsStoredCredentialParseError(t *testing.T) {
	home := setWebAuthTestProfile(t)
	require.NoError(t, os.Chmod(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte("{"), 0o600))

	_, err := ResolveOptions(&config.Config{Web: config.WebConfig{Provider: ProviderExa}})
	require.ErrorContains(t, err, "parse credential store")
}

func TestOptionsNormalize_CleansFieldsAndNegativeLimit(t *testing.T) {
	opts := Options{
		Provider:                " EXA ",
		APIKey:                  " key ",
		BaseURL:                 " https://exa.example ",
		MaxCharPerResult:        -10,
		MaxExtractCharPerResult: -20,
		MaxExtractResponseBytes: -30,
		NativeAllowedHosts:      []string{" allowed.example ", "allowed.example", ""},
		NativeBlockedHosts:      []string{" blocked.example ", "blocked.example", ""},
		NativeAllowedHostFiles:  []string{" allow.txt ", "allow.txt", ""},
		NativeBlockedHostFiles:  []string{" deny.txt ", "deny.txt", ""},
	}.Normalize()

	require.Equal(t, ProviderExa, opts.Provider)
	require.Equal(t, "key", opts.APIKey)
	require.Equal(t, "https://exa.example", opts.BaseURL)
	require.Zero(t, opts.MaxCharPerResult)
	require.Zero(t, opts.MaxExtractCharPerResult)
	require.Zero(t, opts.MaxExtractResponseBytes)
	require.Equal(t, []string{"allowed.example"}, opts.NativeAllowedHosts)
	require.Equal(t, []string{"blocked.example"}, opts.NativeBlockedHosts)
	require.Equal(t, []string{"allow.txt"}, opts.NativeAllowedHostFiles)
	require.Equal(t, []string{"deny.txt"}, opts.NativeBlockedHostFiles)
}

func setWebAuthTestProfile(t *testing.T) string {
	t.Helper()

	for _, key := range []string{
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

func TestExtractOptionsNormalize_CleansFieldsAndNegativeLimit(t *testing.T) {
	opts := ExtractOptions{Format: " TEXT ", MaxChars: -10, Query: " docs "}.Normalize()
	require.Equal(t, "text", opts.Format)
	require.Equal(t, "docs", opts.Query)
	require.Zero(t, opts.MaxChars)

	opts = ExtractOptions{Format: "html", MaxChars: 10}.Normalize()
	require.Empty(t, opts.Format)
	require.Equal(t, 10, opts.MaxChars)
}

func TestWithExtractOptions_RoundTripsNormalizedOptions(t *testing.T) {
	ctx := WithExtractOptions(context.Background(), ExtractOptions{Format: " MARKDOWN ", MaxChars: 12, Query: " specs "})

	require.Equal(t, ExtractOptions{Format: "markdown", MaxChars: 12, Query: "specs"}, ExtractOptionsFromContext(ctx))
	require.Equal(t, ExtractOptions{}, ExtractOptionsFromContext(context.Background()))
	var nilContext context.Context
	require.Equal(t, ExtractOptions{}, ExtractOptionsFromContext(nilContext))
}

func TestExtractCharLimit_UsesRequestLimitWhenPresent(t *testing.T) {
	ctx := WithExtractOptions(context.Background(), ExtractOptions{MaxChars: 12})

	require.Equal(t, 12, getExtractCharLimit(ctx, 50))
	require.Equal(t, 50, getExtractCharLimit(context.Background(), 50))
}

func TestExtractFormat_UsesRequestFormatWhenPresent(t *testing.T) {
	ctx := WithExtractOptions(context.Background(), ExtractOptions{Format: "text"})

	require.Equal(t, "text", getExtractFormat(ctx, "markdown"))
	require.Equal(t, "markdown", getExtractFormat(context.Background(), "markdown"))
}

func TestExtractQuery_UsesRequestQueryWhenPresent(t *testing.T) {
	ctx := WithExtractOptions(context.Background(), ExtractOptions{Query: "release notes"})

	require.Equal(t, "release notes", getExtractQuery(ctx))
	require.Empty(t, getExtractQuery(context.Background()))
}

func TestResolveOptions_UsesDetectedProviderFallback(t *testing.T) {
	setWebAuthTestProfile(t)

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{Provider: ProviderParallel, APIKey: "parallel-key"},
	})
	require.NoError(t, err)
	require.Equal(t, ProviderParallel, opts.Provider)
	require.Equal(t, "parallel-key", opts.APIKey)
	require.Equal(t, parallelDefaultBaseURL, opts.BaseURL)
	require.Equal(t, constants.DefaultWebMaxCharPerResult, opts.MaxCharPerResult)
	require.Equal(t, constants.DefaultWebMaxExtractCharPerResult, opts.MaxExtractCharPerResult)
	require.Equal(t, constants.DefaultWebMaxExtractResponseBytes, opts.MaxExtractResponseBytes)
}

func TestResolveOptions_UsesConfiguredBaseURL(t *testing.T) {
	setWebAuthTestProfile(t)

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderTavily,
			APIKey:   "generic-key",
			BaseURL:  "https://web.example",
		},
	})
	require.NoError(t, err)
	require.Equal(t, ProviderTavily, opts.Provider)
	require.Equal(t, "generic-key", opts.APIKey)
	require.Equal(t, "https://web.example", opts.BaseURL)
}

func TestResolveOptions_UsesConfiguredFirecrawlBaseURL(t *testing.T) {
	setWebAuthTestProfile(t)

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{
			Provider: ProviderFirecrawl,
			BaseURL:  "http://localhost:3002",
		},
	})
	require.NoError(t, err)
	require.Equal(t, ProviderFirecrawl, opts.Provider)
	require.Equal(t, "http://localhost:3002", opts.BaseURL)
}

func TestResolveOptions_UsesConfiguredProviderWithoutAmbientEnvironment(t *testing.T) {
	setWebAuthTestProfile(t)

	opts, err := ResolveOptions(&config.Config{
		Web: config.WebConfig{Provider: ProviderExa, APIKey: "generic-key"},
	})
	require.NoError(t, err)
	require.Equal(t, ProviderExa, opts.Provider)
	require.Equal(t, "generic-key", opts.APIKey)
}

func TestResolveOptions_RejectsUnsupportedProvider(t *testing.T) {
	setWebAuthTestProfile(t)

	_, err := ResolveOptions(&config.Config{Web: config.WebConfig{Provider: "unknown"}})
	require.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestResolveOptions_DefaultsMissingProviderToNative(t *testing.T) {
	opts, err := ResolveOptions(&config.Config{})
	require.NoError(t, err)
	require.Equal(t, ProviderNative, opts.Provider)
}

func TestFillProviderDefaults_LeavesUnsupportedProviderUnchanged(t *testing.T) {
	opts := applyProviderDefaults(Options{Provider: "custom", BaseURL: "https://custom.example"})
	require.Equal(t, "custom", opts.Provider)
	require.Equal(t, "https://custom.example", opts.BaseURL)
}

func TestFillProviderDefaults_AppliesKnownProviderDefaults(t *testing.T) {
	testCases := []struct {
		name     string
		opts     Options
		expected string
	}{
		{name: "firecrawl", opts: Options{Provider: ProviderFirecrawl}, expected: firecrawlDefaultBaseURL},
		{name: "parallel", opts: Options{Provider: ProviderParallel}, expected: parallelDefaultBaseURL},
		{name: "tavily", opts: Options{Provider: ProviderTavily}, expected: tavilyDefaultBaseURL},
		{name: "exa", opts: Options{Provider: ProviderExa}, expected: exaDefaultBaseURL},
		{name: "native", opts: Options{Provider: ProviderNative}, expected: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := applyProviderDefaults(tc.opts)
			require.Equal(t, tc.expected, opts.BaseURL)
		})
	}
}

func TestTruncateToMaxChars_TrimsAndClamps(t *testing.T) {
	require.Equal(t, "", truncateToMaxChars("   ", 10))
	require.Equal(t, "hello", truncateToMaxChars(" hello ", 10))
	require.Equal(t, "hello world", truncateToMaxChars(" hello world ", 0))
	require.Equal(t, "hello", truncateToMaxChars("hello world", 5))
}

func TestLimitExtractContent_AppliesByteLimitBeforeCharLimit(t *testing.T) {
	content, truncated, downloadTruncated := limitExtractContent(" abcdef ", 4, 10)
	require.Equal(t, "abcd", content)
	require.True(t, truncated)
	require.True(t, downloadTruncated)
}

func TestLimitExtractContent_AppliesCharLimitWithoutDownloadTruncation(t *testing.T) {
	content, truncated, downloadTruncated := limitExtractContent("abcdef", 10, 4)
	require.Equal(t, "abcd", content)
	require.True(t, truncated)
	require.False(t, downloadTruncated)
}

func TestLimitExtractContent_TrimsPartialUTF8Rune(t *testing.T) {
	content, truncated, downloadTruncated := limitExtractContent("éclair", 1, 10)
	require.Equal(t, "", content)
	require.True(t, truncated)
	require.True(t, downloadTruncated)
}

func TestTruncateContent_ReportsTruncation(t *testing.T) {
	content, truncated := truncateContent("   ", 5)
	require.Empty(t, content)
	require.False(t, truncated)

	content, truncated = truncateContent(" hello ", 10)
	require.Equal(t, "hello", content)
	require.False(t, truncated)

	content, truncated = truncateContent(" hello world ", 0)
	require.Equal(t, "hello world", content)
	require.False(t, truncated)

	content, truncated = truncateContent("hello world", 5)
	require.Equal(t, "hello", content)
	require.True(t, truncated)
}

func TestNewProvider_ReturnsUnsupportedProviderError(t *testing.T) {
	setWebAuthTestProfile(t)

	_, err := NewProvider(&config.Config{Web: config.WebConfig{Provider: "custom"}})
	require.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestNewProvider_DefaultsMissingConfigToNative(t *testing.T) {
	provider, err := NewProvider(nil)
	require.NoError(t, err)
	require.IsType(t, &NativeProvider{}, provider)
}

func TestNewProviderFromOptions_ReturnsUnsupportedProviderError(t *testing.T) {
	_, err := newProvider(Options{Provider: "custom"})
	require.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestNewProvider_BuildsConcreteProviders(t *testing.T) {
	setWebAuthTestProfile(t)

	testCases := []struct {
		name string
		cfg  *config.Config
	}{
		{
			name: "firecrawl",
			cfg: &config.Config{Web: config.WebConfig{
				Provider: ProviderFirecrawl,
				BaseURL:  "http://localhost:3002",
			}},
		},
		{
			name: "parallel",
			cfg:  &config.Config{Web: config.WebConfig{Provider: ProviderParallel, APIKey: "parallel-key"}},
		},
		{
			name: "tavily",
			cfg:  &config.Config{Web: config.WebConfig{Provider: ProviderTavily, APIKey: "tavily-key"}},
		},
		{
			name: "exa",
			cfg:  &config.Config{Web: config.WebConfig{Provider: ProviderExa, APIKey: "exa-key"}},
		},
		{
			name: "native",
			cfg:  &config.Config{Web: config.WebConfig{Provider: ProviderNative}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewProvider(tc.cfg)
			require.NoError(t, err)
			require.NotNil(t, provider)
		})
	}
}

func TestNewProviderFromOptions_BuildsConcreteProviders(t *testing.T) {
	testCases := []struct {
		name string
		opts Options
	}{
		{
			name: "firecrawl",
			opts: Options{Provider: ProviderFirecrawl, BaseURL: "http://localhost:3002"},
		},
		{
			name: "parallel",
			opts: Options{Provider: ProviderParallel, APIKey: "parallel-key"},
		},
		{
			name: "tavily",
			opts: Options{Provider: ProviderTavily, APIKey: "tavily-key"},
		},
		{
			name: "exa",
			opts: Options{Provider: ProviderExa, APIKey: "exa-key"},
		},
		{
			name: "native",
			opts: Options{Provider: ProviderNative},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := newProvider(tc.opts)
			require.NoError(t, err)
			require.NotNil(t, provider)
		})
	}
}
