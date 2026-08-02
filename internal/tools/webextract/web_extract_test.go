package webextract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	webprovider "github.com/xymorphic/morph/internal/providers/web"
	"github.com/xymorphic/morph/internal/tools"
)

type stubProvider struct {
	extract func(context.Context, []string) ([]webprovider.ExtractResult, error)
}

type stubSummarizer struct {
	inputs []SummaryInput
	output string
	err    error
}

func (stubProvider) Search(context.Context, string, int) ([]webprovider.SearchResult, error) {
	return nil, errors.New("unexpected search call")
}

func (s stubProvider) Extract(ctx context.Context, urls []string) ([]webprovider.ExtractResult, error) {
	return s.extract(ctx, urls)
}

func (s *stubSummarizer) SummarizeExtract(_ context.Context, input SummaryInput) (string, error) {
	s.inputs = append(s.inputs, input)
	return s.output, s.err
}

func registerTool(t *testing.T, provider webprovider.Provider, options ...Options) tools.Registry {
	t.Helper()

	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(provider, options...)))

	return registry
}

func TestWebExtract_RejectsMalformedInput(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
}

func TestWebExtract_RejectsEmptyURLs(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":[]}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "urls is required", toolErr.Message)
}

func TestWebExtract_RejectsBlankURL(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":["https://example.com","  "]}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "url at index 1 is required", toolErr.Message)
}

func TestWebExtract_RejectsTooManyURLs(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":["1","2","3","4","5","6"]}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "too many urls", toolErr.Message)
}

func TestWebExtract_ReturnsProviderResults(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://example.com"}, urls)
			return []webprovider.ExtractResult{{
				URL:               "https://example.com",
				Title:             "Example",
				Content:           "Hello",
				ContentFormat:     "markdown",
				Truncated:         true,
				DownloadTruncated: true,
			}}, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":["https://example.com"]}`})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "Example", payload.Results[0].Title)
	require.True(t, payload.Results[0].Truncated)
	require.True(t, payload.Results[0].DownloadTruncated)
	require.Contains(t, result.Output, `"download_truncated":true`)
	require.Equal(t, "content: Hello\ntitle: Example\nurl: https://example.com", result.SemanticContent)
}

func TestWebExtract_AskPresetRequiresApprovalBeforeProviderCall(t *testing.T) {
	called := false
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetAskForApproval},
	})
	require.NoError(t, registry.Register(Definition(stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			called = true
			return nil, nil
		},
	})))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "web_extract", Input: `{"urls":["https://example.com"]}`,
	})

	require.NoError(t, err)
	require.False(t, called)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeApprovalRequired, toolErr.Code)
}

func TestWebExtract_ApprovePresetCallsProvider(t *testing.T) {
	called := false
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetApproveForMe},
	})
	require.NoError(t, registry.Register(Definition(stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			called = true
			return nil, nil
		},
	})))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "web_extract", Input: `{"urls":["https://example.com"]}`,
	})

	require.NoError(t, err)
	require.True(t, called)
	require.Empty(t, result.Error)
}

func TestWebExtract_BlocksConfiguredURLsBeforeProviderCall(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://blocked.example/page"],"format":"markdown"}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "https://blocked.example/page", payload.Results[0].URL)
	require.Equal(t, "markdown", payload.Results[0].ContentFormat)
	require.Contains(t, payload.Results[0].Error, "blocked by configured website blocklist policy")
}

func TestWebExtract_BlocksMixedURLsAndPreservesOrder(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://allowed.example"}, urls)
			return []webprovider.ExtractResult{{
				URL:           "https://allowed.example",
				Content:       "allowed",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://blocked.example","https://allowed.example"]}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 2)
	require.Contains(t, payload.Results[0].Error, "blocked by configured website blocklist policy")
	require.Equal(t, "text", payload.Results[0].ContentFormat)
	require.Equal(t, "allowed", payload.Results[1].Content)
}

func TestWebExtract_BlocksProviderFinalURL(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://allowed.example"}, urls)
			return []webprovider.ExtractResult{{
				URL:           "https://blocked.example/final",
				Content:       "blocked final content",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://allowed.example"]}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "https://blocked.example/final", payload.Results[0].URL)
	require.Empty(t, payload.Results[0].Content)
	require.Contains(t, payload.Results[0].Error, "blocked by configured website blocklist policy")
}

func TestWebExtract_PassesThroughWhenWebsitePolicyDisabled(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://blocked.example"}, urls)
			return []webprovider.ExtractResult{{
				URL:           "https://blocked.example",
				Content:       "allowed because policy is disabled",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(false, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://blocked.example"]}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "allowed because policy is disabled", payload.Results[0].Content)
}

func TestWebExtract_ReturnsBlockedResultsWhenAllURLsBlocked(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://blocked.example/one","https://blocked.example/two"]}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 2)
	require.Contains(t, payload.Results[0].Error, "blocked by configured website blocklist policy")
	require.Contains(t, payload.Results[1].Error, "blocked by configured website blocklist policy")
}

func TestWebExtract_FillsMissingProviderResults(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://one.example", "https://two.example"}, urls)
			return []webprovider.ExtractResult{{
				URL:           "https://one.example",
				Content:       "one",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://one.example","https://two.example"]}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 2)
	require.Equal(t, "one", payload.Results[0].Content)
	require.Equal(t, "https://two.example", payload.Results[1].URL)
	require.Equal(t, "web extraction provider returned no result", payload.Results[1].Error)
}

func TestWebExtract_IgnoresExtraProviderResults(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://one.example"}, urls)
			return []webprovider.ExtractResult{
				{URL: "https://one.example", Content: "one", ContentFormat: "text"},
				{URL: "https://extra.example", Content: "extra", ContentFormat: "text"},
			}, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://one.example"]}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "one", payload.Results[0].Content)
}

func TestWebExtract_AppliesPerCallMaxChars(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(ctx context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://example.com"}, urls)
			require.Equal(t, webprovider.ExtractOptions{MaxChars: 3}, webprovider.ExtractOptionsFromContext(ctx))
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "abc",
				ContentFormat: "text",
				Truncated:     true,
			}}, nil
		},
	}, Options{MaxExtractCharPerResult: 10})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"max_chars":3}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "abc", payload.Results[0].Content)
	require.True(t, payload.Results[0].Truncated)
	require.False(t, payload.Results[0].DownloadTruncated)
}

func TestWebExtract_ClampsPerCallMaxCharsToConfiguredMax(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(ctx context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://example.com"}, urls)
			require.Equal(t, webprovider.ExtractOptions{MaxChars: 4}, webprovider.ExtractOptionsFromContext(ctx))
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "abcd",
				ContentFormat: "text",
				Truncated:     true,
			}}, nil
		},
	}, Options{MaxExtractCharPerResult: 4})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"max_chars":10}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "abcd", payload.Results[0].Content)
	require.True(t, payload.Results[0].Truncated)
}

func TestWebExtract_RejectsInvalidMaxChars(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"max_chars":0}`,
	})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "max_chars must be greater than zero", toolErr.Message)
}

func TestWebExtract_AppliesTextFormat(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(ctx context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://example.com"}, urls)
			require.Equal(t, webprovider.ExtractOptions{
				Format: "text",
				Query:  "release notes",
			}, webprovider.ExtractOptionsFromContext(ctx))
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "Title\n\nRead docs and ship it.",
				ContentFormat: "text",
			}}, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"format":"text","query":" release notes "}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "text", payload.Results[0].ContentFormat)
	require.Equal(t, "Title\n\nRead docs and ship it.", payload.Results[0].Content)
}

func TestWebExtract_AppliesExtractModeAlias(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(ctx context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Equal(t, []string{"https://example.com"}, urls)
			require.Equal(t, webprovider.ExtractOptions{Format: "markdown"}, webprovider.ExtractOptionsFromContext(ctx))
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "plain content",
				ContentFormat: "markdown",
			}}, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"extract_mode":"markdown"}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "markdown", payload.Results[0].ContentFormat)
	require.Equal(t, "plain content", payload.Results[0].Content)
}

func TestWebExtract_RejectsInvalidFormat(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"format":"html"}`,
	})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "format must be text or markdown", toolErr.Message)
}

func TestWebExtract_RejectsConflictingFormatAliases(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			t.Fatal("extract should not be called")
			return nil, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"format":"text","extract_mode":"markdown"}`,
	})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "format and extract_mode must match when both are provided", toolErr.Message)
}

func TestWebExtract_PreservesPartialFailures(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			require.Len(t, urls, 2)
			return []webprovider.ExtractResult{
				{URL: "https://ok.example", Content: "ok", ContentFormat: "markdown"},
				{URL: "https://bad.example", ContentFormat: "markdown", Error: "fetch failed"},
			}, nil
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":["https://ok.example","https://bad.example"]}`})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 2)
	require.Equal(t, "fetch failed", payload.Results[1].Error)
}

func TestWebExtract_SummarizesLongResults(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Title:         "Example",
				Content:       "abcdef",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{MinSummarizeChars: 3, MaxSummaryChars: 4, MaxSummaryChunkChars: 10, SummarizeRefusalThresholdChars: 20})
	summarizer := &stubSummarizer{output: "summary text"}
	ctx := WithSummarizer(context.Background(), summarizer)

	result, err := registry.Invoke(ctx, tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"query":"pricing","summarize":true}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "summ", payload.Results[0].Content)
	require.Equal(t, "summary", payload.Results[0].ContentFormat)
	require.True(t, payload.Results[0].Summarized)
	require.Equal(t, 6, payload.Results[0].SourceContentChars)
	require.Equal(t, 4, payload.Results[0].SummaryChars)
	require.True(t, payload.Results[0].Truncated)
	require.Len(t, summarizer.inputs, 1)
	require.Equal(t, "pricing", summarizer.inputs[0].Query)
	require.Equal(t, "abcdef", summarizer.inputs[0].Content)
	require.Equal(t, 4, summarizer.inputs[0].MaxSummaryChars)
	require.Equal(t, 10, summarizer.inputs[0].MaxSummaryChunkChars)
}

func TestWebExtract_SkipsSummarizationBelowMinimum(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "short",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{MinSummarizeChars: 10, MaxSummaryChars: 4, SummarizeRefusalThresholdChars: 20})
	summarizer := &stubSummarizer{output: "summary"}

	result, err := registry.Invoke(WithSummarizer(context.Background(), summarizer), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"summarize":true}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "short", payload.Results[0].Content)
	require.False(t, payload.Results[0].Summarized)
	require.Empty(t, summarizer.inputs)
}

func TestWebExtract_RefusesSummarizationAboveThreshold(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "abcdef",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{MinSummarizeChars: 3, MaxSummaryChars: 4, SummarizeRefusalThresholdChars: 5})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"summarize":true}`,
	})
	require.NoError(t, err)

	var payload struct {
		Results []webprovider.ExtractResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "abcdef", payload.Results[0].Content)
	require.True(t, payload.Results[0].SummaryRefused)
	require.Equal(t, 6, payload.Results[0].SourceContentChars)
	require.Equal(t, "content exceeds summarization threshold", payload.Results[0].Error)
}

func TestWebExtract_ReturnsErrorWhenSummarizerIsMissing(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return []webprovider.ExtractResult{{
				URL:           "https://example.com",
				Content:       "abcdef",
				ContentFormat: "text",
			}}, nil
		},
	}, Options{MinSummarizeChars: 3, MaxSummaryChars: 4, SummarizeRefusalThresholdChars: 20})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://example.com"],"summarize":true}`,
	})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "tool_error", toolErr.Code)
	require.Equal(t, "web extract summarizer is not configured", toolErr.Message)
}

func TestWebExtract_ReturnsProviderErrorsAsToolErrors(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return nil, errors.New("provider failed")
		},
	})

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":["https://example.com"]}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "tool_error", toolErr.Code)
	require.Equal(t, "provider failed", toolErr.Message)
}

func TestWebExtract_ReturnsPolicyEnabledProviderErrorsAsToolErrors(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return nil, errors.New("provider failed")
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "web_extract",
		Input: `{"urls":["https://allowed.example"]}`,
	})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "tool_error", toolErr.Code)
	require.Equal(t, "provider failed", toolErr.Message)
}

func TestExtractWithPolicy_HandlesEmptyURLList(t *testing.T) {
	called := false
	results, stats, err := extractWithPolicy(context.Background(), stubProvider{
		extract: func(_ context.Context, urls []string) ([]webprovider.ExtractResult, error) {
			called = true
			require.Empty(t, urls)
			return nil, nil
		},
	}, nil, "text", guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil))

	require.NoError(t, err)
	require.True(t, called)
	require.Nil(t, results)
	require.Equal(t, extractPolicyStats{}, stats)
}

func TestWebExtract_ReturnsErrorWhenProviderIsNil(t *testing.T) {
	registry := registerTool(t, nil)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_extract", Input: `{"urls":["https://example.com"]}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "tool_error", toolErr.Code)
	require.Equal(t, "web extract provider is not configured", toolErr.Message)
}

func TestWebExtract_RequiresNetworkCapability(t *testing.T) {
	registry := registerTool(t, stubProvider{
		extract: func(context.Context, []string) ([]webprovider.ExtractResult, error) {
			return nil, nil
		},
	})

	withNetwork, err := registry.Resolve(tools.Policy{Capabilities: tools.Capabilities{Network: true}})
	require.NoError(t, err)
	require.Len(t, withNetwork, 1)
	require.Equal(t, "web_extract", withNetwork[0].Name)

	withoutNetwork, err := registry.Resolve(tools.Policy{})
	require.NoError(t, err)
	require.Empty(t, withoutNetwork)
}
