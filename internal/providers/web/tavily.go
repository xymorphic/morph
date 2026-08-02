package web

import (
	"context"
	"net/http"

	"github.com/xymorphic/morph/pkg/str"
)

const tavilyDefaultBaseURL = "https://api.tavily.com"

// TavilyProvider sends web requests to Tavily.
type TavilyProvider struct {
	client                   *httpClient
	maxCharsPerResult        int
	maxExtractCharsPerResult int
	maxExtractResponseBytes  int
}

// NewTavily returns a web provider backed by Tavily.
func NewTavily(opts Options) (Provider, error) {
	opts = opts.Normalize()
	if opts.APIKey == "" {
		return nil, providerCredentialError("tavily requires web API key")
	}
	if opts.BaseURL == "" {
		opts.BaseURL = tavilyDefaultBaseURL
	}

	return &TavilyProvider{
		client: &httpClient{
			apiKey:  opts.APIKey,
			baseURL: opts.BaseURL,
			client:  http.DefaultClient,
		},
		maxCharsPerResult:        opts.MaxCharPerResult,
		maxExtractCharsPerResult: opts.MaxExtractCharPerResult,
		maxExtractResponseBytes:  opts.MaxExtractResponseBytes,
	}, nil
}

func (p *TavilyProvider) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	var response struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := p.client.postJSON(ctx, "/search", map[string]any{
		"query":               query,
		"search_depth":        "basic",
		"max_results":         count,
		"include_raw_content": false,
		"include_images":      false,
	}, p.client.authorizationHeaders(), &response); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(response.Results))
	for idx, result := range response.Results {
		titleValue := str.String(result.Title)
		uRLValue := str.String(result.URL)
		results = append(results, SearchResult{
			Title:    titleValue.Trim(),
			URL:      uRLValue.Trim(),
			Snippet:  truncateToMaxChars(result.Content, p.maxCharsPerResult),
			Position: idx + 1,
		})
	}

	return results, nil
}

func (p *TavilyProvider) Extract(ctx context.Context, urls []string) ([]ExtractResult, error) {
	format := getExtractFormat(ctx, "markdown")
	maxChars := getExtractCharLimit(ctx, p.maxExtractCharsPerResult)
	query := getExtractQuery(ctx)

	var response struct {
		Results []struct {
			URL        string `json:"url"`
			Title      string `json:"title"`
			Content    string `json:"content"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
		FailedResults []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"failed_results"`
		FailedURLs []string `json:"failed_urls"`
	}

	payload := map[string]any{
		"urls":             urls,
		"extract_depth":    "basic",
		"format":           format,
		"include_images":   false,
		"include_raw_html": false,
	}
	if query != "" {
		payload["query"] = query
	}

	if err := p.client.postJSONLimited(ctx, "/extract", payload, p.client.authorizationHeaders(), &response, p.maxExtractResponseBytes); err != nil {
		return nil, err
	}

	results := make([]ExtractResult, 0, len(response.Results)+len(response.FailedResults)+len(response.FailedURLs))
	for _, result := range response.Results {
		content, truncated, downloadTruncated := limitExtractContent(
			getFirstNonEmpty(result.RawContent, result.Content),
			p.maxExtractResponseBytes,
			maxChars)
		uRLValue2 := str.String(result.URL)
		titleValue2 := str.String(result.Title)
		results = append(results, ExtractResult{
			URL:               uRLValue2.Trim(),
			Title:             titleValue2.Trim(),
			Content:           content,
			ContentFormat:     format,
			Truncated:         truncated,
			DownloadTruncated: downloadTruncated,
		})
	}
	for _, result := range response.FailedResults {
		uRLValue3 := str.String(result.URL)
		results = append(results, ExtractResult{
			URL:           uRLValue3.Trim(),
			ContentFormat: format,
			Error:         getFirstNonEmpty(result.Error, "extraction failed"),
		})
	}
	for _, url := range response.FailedURLs {
		urlValue := str.String(url)
		results = append(results, ExtractResult{
			URL:           urlValue.Trim(),
			ContentFormat: format,
			Error:         "extraction failed",
		})
	}

	return results, nil
}
