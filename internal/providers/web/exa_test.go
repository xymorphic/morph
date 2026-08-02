package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/constants"
)

func TestNewExa_BuildsFromAPIKeyOnly(t *testing.T) {
	provider, err := NewExa(Options{APIKey: "exa-key"})
	require.NoError(t, err)

	exaProvider, ok := provider.(*ExaProvider)
	require.True(t, ok)
	require.Equal(t, exaDefaultBaseURL, exaProvider.client.baseURL)
	require.Zero(t, exaProvider.maxCharsPerResult)
	require.Zero(t, exaProvider.maxExtractCharsPerResult)
	require.Zero(t, exaProvider.maxExtractResponseBytes)
}

func TestNewExa_PreservesConfiguredBaseURL(t *testing.T) {
	provider, err := NewExa(Options{APIKey: "exa-key", BaseURL: "https://exa.example"})
	require.NoError(t, err)

	exaProvider, ok := provider.(*ExaProvider)
	require.True(t, ok)
	require.Equal(t, "https://exa.example", exaProvider.client.baseURL)
}

func TestNewExa_UsesConfiguredMaxCharPerResult(t *testing.T) {
	provider, err := NewExa(Options{
		APIKey:                  "exa-key",
		MaxCharPerResult:        400,
		MaxExtractCharPerResult: 12000,
		MaxExtractResponseBytes: 64000,
	})
	require.NoError(t, err)

	exaProvider, ok := provider.(*ExaProvider)
	require.True(t, ok)
	require.Equal(t, 400, exaProvider.maxCharsPerResult)
	require.Equal(t, 12000, exaProvider.maxExtractCharsPerResult)
	require.Equal(t, 64000, exaProvider.maxExtractResponseBytes)
}

func TestNewExa_ReturnsCredentialError(t *testing.T) {
	_, err := NewExa(Options{})
	require.EqualError(t, err, "exa requires web API key")
	require.ErrorIs(t, err, ErrProviderCredential)
}

func TestExaProvider_SearchNormalizesResults(t *testing.T) {
	var captured struct {
		Path       string
		APIKey     string
		Query      string `json:"query"`
		NumResults int    `json:"numResults"`
		Contents   struct {
			Highlights struct {
				MaxCharacters int `json:"maxCharacters"`
			} `json:"highlights"`
		} `json:"contents"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		captured.Path = r.URL.Path
		captured.APIKey = r.Header.Get("x-api-key")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Exa result", "url": "https://example.com/exa", "highlights": []string{"Extracted snippet"}},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxCharsPerResult: constants.DefaultWebMaxCharPerResult,
	}

	results, err := provider.Search(context.Background(), "exa search", 6)
	require.NoError(t, err)
	require.Equal(t, "/search", captured.Path)
	require.Equal(t, "exa-key", captured.APIKey)
	require.Equal(t, "exa search", captured.Query)
	require.Equal(t, 6, captured.NumResults)
	require.Equal(t, constants.DefaultWebMaxCharPerResult, captured.Contents.Highlights.MaxCharacters)
	require.Equal(t, []SearchResult{{
		Title:    "Exa result",
		URL:      "https://example.com/exa",
		Snippet:  "Extracted snippet",
		Position: 1,
	}}, results)
}

func TestExaProvider_SearchUsesConfiguredHighlightLimit(t *testing.T) {
	var captured struct {
		Contents struct {
			Highlights struct {
				MaxCharacters int `json:"maxCharacters"`
			} `json:"highlights"`
		} `json:"contents"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxCharsPerResult: 300,
	}

	_, err := provider.Search(context.Background(), "exa search", 2)
	require.NoError(t, err)
	require.Equal(t, 300, captured.Contents.Highlights.MaxCharacters)
}

func TestExaProvider_SearchFallsBackToText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Exa result", "url": "https://example.com/exa", "text": "text fallback"},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	results, err := provider.Search(context.Background(), "exa search", 6)
	require.NoError(t, err)
	require.Equal(t, "text fallback", results[0].Snippet)
}

func TestExaProvider_SearchFallsBackToSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Exa result", "url": "https://example.com/exa", "summary": "summary fallback"},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	results, err := provider.Search(context.Background(), "exa search", 6)
	require.NoError(t, err)
	require.Equal(t, "summary fallback", results[0].Snippet)
}

func TestExaProvider_SearchTruncatesSnippet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Exa result", "url": "https://example.com/exa", "summary": "123456789"},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxCharsPerResult: 7,
	}

	results, err := provider.Search(context.Background(), "exa search", 6)
	require.NoError(t, err)
	require.Equal(t, "1234567", results[0].Snippet)
}

func TestExaProvider_HeadersUseAPIKeyHeader(t *testing.T) {
	provider := &ExaProvider{client: &httpClient{apiKey: " exa-key "}}
	require.Equal(t, map[string]string{"x-api-key": "exa-key"}, provider.exaHeaders())
	require.Nil(t, (&ExaProvider{}).exaHeaders())
}

func TestExaProvider_SearchReturnsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	_, err := provider.Search(context.Background(), "exa search", 2)
	require.EqualError(t, err, "web provider request failed: bad credentials")
}

func TestExaProvider_ExtractNormalizesResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		var body struct {
			URLs []string `json:"urls"`
			Text struct {
				MaxCharacters int `json:"maxCharacters"`
			} `json:"text"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, []string{"https://example.com", "https://bad.example"}, body.URLs)
		require.Equal(t, 900, body.Text.MaxCharacters)

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"url": "https://example.com", "title": "Example", "text": "Extracted content"},
			},
			"statuses": []map[string]any{
				{"id": "https://example.com", "status": "success"},
				{"id": "https://bad.example", "status": "error", "error": map[string]any{"tag": "CRAWL_NOT_FOUND", "httpStatusCode": 404}},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxCharsPerResult:        100,
		maxExtractCharsPerResult: 900,
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com", "https://bad.example"})
	require.NoError(t, err)
	require.Equal(t, []ExtractResult{
		{
			URL:           "https://example.com",
			Title:         "Example",
			Content:       "Extracted content",
			ContentFormat: "text",
		},
		{
			URL:           "https://bad.example",
			ContentFormat: "text",
			Error:         "CRAWL_NOT_FOUND (404)",
		},
	}, results)
}

func TestExaProvider_ExtractUsesContextOptions(t *testing.T) {
	var captured struct {
		Text struct {
			MaxCharacters int `json:"maxCharacters"`
		} `json:"text"`
		Highlights struct {
			Query         string `json:"query"`
			MaxCharacters int    `json:"maxCharacters"`
		} `json:"highlights"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"url": "https://example.com", "title": "Example", "text": "abcdef", "highlights": []string{"focus text"}},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractCharsPerResult: 100,
	}
	ctx := WithExtractOptions(context.Background(), ExtractOptions{Format: "markdown", MaxChars: 3, Query: "pricing"})

	results, err := provider.Extract(ctx, []string{"https://example.com"})
	require.NoError(t, err)
	require.Equal(t, 3, captured.Text.MaxCharacters)
	require.Equal(t, "pricing", captured.Highlights.Query)
	require.Equal(t, 3, captured.Highlights.MaxCharacters)
	require.Equal(t, []ExtractResult{{
		URL:           "https://example.com",
		Title:         "Example",
		Content:       "foc",
		ContentFormat: "markdown",
		Truncated:     true,
	}}, results)
}

func TestExaProvider_ExtractUsesResultErrorAndSkipsSeenStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"url": "https://bad.example", "title": "Bad", "text": "partial", "error": "result failed"},
			},
			"statuses": []map[string]any{
				{"id": "https://bad.example", "status": "error", "error": map[string]any{"tag": "STATUS_FAILED", "httpStatusCode": 500}},
			},
		}))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractCharsPerResult: 100,
	}

	results, err := provider.Extract(context.Background(), []string{"https://bad.example"})
	require.NoError(t, err)
	require.Equal(t, []ExtractResult{{
		URL:           "https://bad.example",
		Title:         "Bad",
		Content:       "partial",
		ContentFormat: "text",
		Error:         "result failed",
	}}, results)
}

func TestExaProvider_ExtractReturnsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	_, err := provider.Extract(context.Background(), []string{"https://example.com"})
	require.EqualError(t, err, "web provider request failed: bad credentials")
}

func TestExaProvider_ExtractRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com","text":"abcdef"}]}`))
	}))
	defer server.Close()

	provider := &ExaProvider{
		client: &httpClient{
			apiKey:  "exa-key",
			baseURL: server.URL,
			client:  server.Client(),
		},
		maxExtractResponseBytes: 5,
	}

	_, err := provider.Extract(context.Background(), []string{"https://example.com"})
	require.EqualError(t, err, "web provider response exceeds 5 bytes")
	require.True(t, isResponseTooLarge(err))
}

func TestExaStatusError_FormatsFallbacks(t *testing.T) {
	require.Equal(t, "extraction failed", getExaStatusError("", 0))
	require.Equal(t, "TIMEOUT", getExaStatusError("TIMEOUT", 0))
	require.Equal(t, "TIMEOUT (408)", getExaStatusError("TIMEOUT", 408))
}
