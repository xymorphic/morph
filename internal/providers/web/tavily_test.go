package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/logutils"
)

func TestRealTavilyProvider_Search(t *testing.T) {
	provider, err := NewTavily(Options{APIKey: "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"})
	require.NoError(t, err)
	results, err := provider.Search(context.Background(), "nairaland", 10)
	require.NoError(t, err)
	logutils.PrettyPrint(results)
}

func TestNewTavily_BuildsFromAPIKeyOnly(t *testing.T) {
	provider, err := NewTavily(Options{APIKey: "tavily-key"})
	require.NoError(t, err)

	tavilyProvider, ok := provider.(*TavilyProvider)
	require.True(t, ok)
	require.Equal(t, tavilyDefaultBaseURL, tavilyProvider.client.baseURL)
	require.Zero(t, tavilyProvider.maxCharsPerResult)
	require.Zero(t, tavilyProvider.maxExtractCharsPerResult)
	require.Zero(t, tavilyProvider.maxExtractResponseBytes)
}

func TestNewTavily_PreservesConfiguredBaseURL(t *testing.T) {
	provider, err := NewTavily(Options{APIKey: "tavily-key", BaseURL: "https://tavily.example"})
	require.NoError(t, err)

	tavilyProvider, ok := provider.(*TavilyProvider)
	require.True(t, ok)
	require.Equal(t, "https://tavily.example", tavilyProvider.client.baseURL)
}

func TestNewTavily_UsesConfiguredMaxCharPerResult(t *testing.T) {
	provider, err := NewTavily(Options{
		APIKey:                  "tavily-key",
		MaxCharPerResult:        222,
		MaxExtractCharPerResult: 12000,
		MaxExtractResponseBytes: 64000,
	})
	require.NoError(t, err)

	tavilyProvider, ok := provider.(*TavilyProvider)
	require.True(t, ok)
	require.Equal(t, 222, tavilyProvider.maxCharsPerResult)
	require.Equal(t, 12000, tavilyProvider.maxExtractCharsPerResult)
	require.Equal(t, 64000, tavilyProvider.maxExtractResponseBytes)
}

func TestNewTavily_ReturnsCredentialError(t *testing.T) {
	_, err := NewTavily(Options{})
	require.EqualError(t, err, "tavily requires web API key")
	require.ErrorIs(t, err, ErrProviderCredential)
}

func TestTavilyProvider_SearchNormalizesResults(t *testing.T) {
	var captured struct {
		Path              string
		Authorization     string
		Query             string `json:"query"`
		SearchDepth       string `json:"search_depth"`
		MaxResults        int    `json:"max_results"`
		IncludeRawContent bool   `json:"include_raw_content"`
		IncludeImages     bool   `json:"include_images"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		captured.Path = r.URL.Path
		captured.Authorization = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Tavily result", "url": "https://example.com/tavily", "content": "A summary"},
			},
		}))
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	results, err := provider.Search(context.Background(), "tavily search", 2)
	require.NoError(t, err)
	require.Equal(t, "/search", captured.Path)
	require.Equal(t, "Bearer tavily-key", captured.Authorization)
	require.Equal(t, "tavily search", captured.Query)
	require.Equal(t, "basic", captured.SearchDepth)
	require.Equal(t, 2, captured.MaxResults)
	require.False(t, captured.IncludeRawContent)
	require.False(t, captured.IncludeImages)
	require.Equal(t, []SearchResult{{
		Title:    "Tavily result",
		URL:      "https://example.com/tavily",
		Snippet:  "A summary",
		Position: 1,
	}}, results)
}

func TestTavilyProvider_SearchTruncatesSnippet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Tavily result", "url": "https://example.com/tavily", "content": "123456789"},
			},
		}))
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxCharsPerResult: 6,
	}

	results, err := provider.Search(context.Background(), "tavily search", 2)
	require.NoError(t, err)
	require.Equal(t, "123456", results[0].Snippet)
}

func TestTavilyProvider_SearchReturnsClientErrors(t *testing.T) {
	provider := &TavilyProvider{client: &httpClient{}}

	_, err := provider.Search(context.Background(), "tavily search", 2)
	require.EqualError(t, err, "web provider base URL is required")
}

func TestTavilyProvider_ExtractNormalizesResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		var body struct {
			URLs          []string `json:"urls"`
			ExtractDepth  string   `json:"extract_depth"`
			Format        string   `json:"format"`
			IncludeImages bool     `json:"include_images"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, []string{"https://example.com"}, body.URLs)
		require.Equal(t, "basic", body.ExtractDepth)
		require.Equal(t, "markdown", body.Format)
		require.False(t, body.IncludeImages)

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"url": "https://example.com", "title": "Example", "raw_content": "Extracted content"},
			},
			"failed_results": []map[string]any{
				{"url": "https://bad.example", "error": "timeout"},
			},
			"failed_urls": []string{"https://gone.example"},
		}))
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractCharsPerResult: 100,
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com"})
	require.NoError(t, err)
	require.Equal(t, []ExtractResult{
		{
			URL:           "https://example.com",
			Title:         "Example",
			Content:       "Extracted content",
			ContentFormat: "markdown",
		},
		{
			URL:           "https://bad.example",
			ContentFormat: "markdown",
			Error:         "timeout",
		},
		{
			URL:           "https://gone.example",
			ContentFormat: "markdown",
			Error:         "extraction failed",
		},
	}, results)
}

func TestTavilyProvider_ExtractUsesContextOptions(t *testing.T) {
	var captured struct {
		Format string `json:"format"`
		Query  string `json:"query"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"url": "https://example.com", "title": "Example", "raw_content": "abcdef"},
			},
		}))
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractCharsPerResult: 100,
	}
	ctx := WithExtractOptions(context.Background(), ExtractOptions{Format: "text", MaxChars: 3, Query: "pricing"})

	results, err := provider.Extract(ctx, []string{"https://example.com"})
	require.NoError(t, err)
	require.Equal(t, "text", captured.Format)
	require.Equal(t, "pricing", captured.Query)
	require.Equal(t, []ExtractResult{{
		URL:           "https://example.com",
		Title:         "Example",
		Content:       "abc",
		ContentFormat: "text",
		Truncated:     true,
	}}, results)
}

func TestTavilyProvider_ExtractFallsBackToContentAndDefaultErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"url": "https://example.com", "title": "Example", "content": "summary content"},
			},
			"failed_results": []map[string]any{
				{"url": "https://bad.example"},
			},
		}))
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractCharsPerResult: 100,
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com"})
	require.NoError(t, err)
	require.Equal(t, []ExtractResult{
		{
			URL:           "https://example.com",
			Title:         "Example",
			Content:       "summary content",
			ContentFormat: "markdown",
		},
		{
			URL:           "https://bad.example",
			ContentFormat: "markdown",
			Error:         "extraction failed",
		},
	}, results)
}

func TestTavilyProvider_ExtractReturnsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	_, err := provider.Extract(context.Background(), []string{"https://example.com"})
	require.EqualError(t, err, "web provider request failed: bad credentials")
}

func TestTavilyProvider_ExtractRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com","raw_content":"abcdef"}]}`))
	}))
	defer server.Close()

	provider := &TavilyProvider{
		client: &httpClient{
			apiKey:  "tavily-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractResponseBytes: 5,
	}

	_, err := provider.Extract(context.Background(), []string{"https://example.com"})
	require.EqualError(t, err, "web provider response exceeds 5 bytes")
	require.True(t, isResponseTooLarge(err))
}
