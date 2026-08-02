package web

import (
	"context"
	"net/http"

	"github.com/xymorphic/morph/pkg/str"
)

const firecrawlDefaultBaseURL = "https://api.firecrawl.dev"

// FirecrawlProvider sends web requests to Firecrawl.
type FirecrawlProvider struct {
	client                   *httpClient
	maxCharsPerResult        int
	maxExtractCharsPerResult int
	maxExtractResponseBytes  int
}

// NewFirecrawl returns a web provider backed by Firecrawl.
func NewFirecrawl(opts Options) (Provider, error) {
	opts = opts.Normalize()
	if opts.APIKey == "" && opts.BaseURL == "" {
		return nil, providerCredentialError("firecrawl requires web API key or base URL")
	}
	if opts.BaseURL == "" {
		opts.BaseURL = firecrawlDefaultBaseURL
	}

	return &FirecrawlProvider{
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

func (p *FirecrawlProvider) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	type firecrawlResult struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Snippet     string `json:"snippet"`
	}

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Web []firecrawlResult `json:"web"`
		} `json:"data"`
		Web []firecrawlResult `json:"web"`
	}

	if err := p.client.postJSON(ctx, "/v2/search", map[string]any{
		"query": query,
		"limit": count,
	}, p.client.authorizationHeaders(), &response); err != nil {
		return nil, err
	}

	rawResults := response.Data.Web
	if len(rawResults) == 0 {
		rawResults = response.Web
	}

	results := make([]SearchResult, 0, len(rawResults))
	for idx, result := range rawResults {
		titleValue := str.String(result.Title)
		uRLValue := str.String(result.URL)
		results = append(results, SearchResult{
			Title:    titleValue.Trim(),
			URL:      uRLValue.Trim(),
			Snippet:  truncateToMaxChars(getFirstNonEmpty(result.Description, result.Snippet), p.maxCharsPerResult),
			Position: idx + 1,
		})
	}

	return results, nil
}

func (p *FirecrawlProvider) Extract(ctx context.Context, urls []string) ([]ExtractResult, error) {
	type scrapeMetadata struct {
		SourceURL string `json:"sourceURL"`
		Title     string `json:"title"`
	}

	type scrapeData struct {
		Markdown string         `json:"markdown"`
		Text     string         `json:"text"`
		HTML     string         `json:"html"`
		Metadata scrapeMetadata `json:"metadata"`
	}

	type scrapeResponse struct {
		Success bool       `json:"success"`
		Data    scrapeData `json:"data"`
	}

	format := getExtractFormat(ctx, "markdown")
	maxChars := getExtractCharLimit(ctx, p.maxExtractCharsPerResult)
	results := make([]ExtractResult, 0, len(urls))
	for _, rawURL := range urls {
		rawURLValue := str.String(rawURL)
		url := rawURLValue.Trim()

		var response scrapeResponse
		err := p.client.postJSONLimited(ctx, "/v2/scrape", map[string]any{
			"url": url,
			"formats": []string{
				format,
			},
			"onlyMainContent": true,
			"parsers": []string{
				"pdf",
			},
		}, p.client.authorizationHeaders(), &response, p.maxExtractResponseBytes)
		if err != nil {
			results = append(results, ExtractResult{
				URL:               url,
				ContentFormat:     format,
				DownloadTruncated: isResponseTooLarge(err),
				Error:             err.Error(),
			})
			continue
		}

		content, truncated, downloadTruncated := limitExtractContent(
			getFirstNonEmpty(response.Data.Text, response.Data.Markdown, response.Data.HTML),
			p.maxExtractResponseBytes,
			maxChars)
		titleValue2 := str.String(response.Data.Metadata.Title)
		results = append(results, ExtractResult{
			URL:               getFirstNonEmpty(response.Data.Metadata.SourceURL, url),
			Title:             titleValue2.Trim(),
			Content:           content,
			ContentFormat:     format,
			Truncated:         truncated,
			DownloadTruncated: downloadTruncated,
		})
	}

	return results, nil
}
