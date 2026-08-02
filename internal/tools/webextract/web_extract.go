package webextract

import (
	"context"
	"fmt"

	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	webprovider "github.com/xymorphic/morph/internal/providers/web"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

const maxURLs = constants.WebExtractToolMaxURLs

var log = logutils.Module("tool.webextract")

// Options configures this package operation.
type Options struct {
	MaxExtractCharPerResult        int
	MinSummarizeChars              int
	MaxSummaryChars                int
	MaxSummaryChunkChars           int
	SummarizeRefusalThresholdChars int
	WebsitePolicy                  guardrails.WebsitePolicy
}

type extractPolicyStats struct {
	InputBlocked      int
	ResultBlocked     int
	MissingResults    int
	ExtraResults      int
	ProviderRequested int
}

// Definition returns the model-visible tool definition.
func Definition(provider webprovider.Provider, options ...Options) tools.Definition {
	type input struct {
		URLs        []string `json:"urls"`
		MaxChars    *int     `json:"max_chars"`
		Query       string   `json:"query"`
		Summarize   bool     `json:"summarize"`
		Format      string   `json:"format"`
		ExtractMode string   `json:"extract_mode"`
	}

	opts := getWebExtractOptions(options)

	return tools.Definition{
		Name: "web_extract",
		Description: "Extract readable content from one or more URLs. " +
			"Use this to retrieve and read page contents after discovery.",
		ParallelSafe: true,
		Groups:       []string{"core"},
		Requires:     tools.Capabilities{Network: true},
		SemanticIndex: tools.ProjectSemanticIndex(tools.ProjectJSONFieldsForSemanticIndex(
			"title", "url", "content", "text", "summary", "description",
		)),
		Permission: permissions.Operation{
			Resource: permissions.ResourceNetwork,
			Action:   permissions.ActionRead,
			Effects: []permissions.Effect{
				permissions.EffectRead,
				permissions.EffectNetwork,
				permissions.EffectExternalSystem,
			},
		},
		InputSchema: common.ObjectSchema(map[string]any{
			"urls": map[string]any{
				"type":        "array",
				"description": "URLs to extract readable content from.",
				"items":       common.StringSchema("URL to fetch and extract."),
			},

			"max_chars": common.IntegerSchema("Optional maximum characters to return per extracted page. " +
				"Values above configured maxExtractCharPerResult are clamped."),

			"query": common.StringSchema("Optional focused extraction query. Providers that support it " +
				"use this to return content most relevant to the query."),

			"summarize": common.BooleanSchema("When true, summarize extracted content that exceeds the " +
				"configured minimum summarization size."),

			"format": map[string]any{
				"type":        "string",
				"description": "Optional output content format. Valid values are text or markdown.",
				"enum":        []string{"text", "markdown"},
			},

			"extract_mode": map[string]any{
				"type":        "string",
				"description": "Alias for format. Valid values are text or markdown.",
				"enum":        []string{"text", "markdown"},
			},
		}, "urls"),

		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var req input

			if result := common.DecodeInput(call, &req); result.Error != "" {
				return result, nil
			}

			if provider == nil {
				return common.ToolError("tool_error", "web extract provider is not configured"), nil
			}

			if len(req.URLs) == 0 {
				return common.ToolError("invalid_input", "urls is required"), nil
			}

			if len(req.URLs) > maxURLs {
				return common.ToolError("invalid_input", "too many urls"), nil
			}

			urls := make([]string, 0, len(req.URLs))
			for idx, rawURL := range req.URLs {
				rawURLValue := str.String(rawURL)
				url := rawURLValue.Trim()
				if url == "" {
					return common.ToolError("invalid_input", fmt.Sprintf("url at index %d is required", idx)), nil
				}
				urls = append(urls, url)
			}

			maxChars, validationErr := getRequestMaxChars(req.MaxChars, opts.MaxExtractCharPerResult)
			if validationErr != nil {
				return common.ToolError("invalid_input", validationErr.Error()), nil
			}

			format, validationErr := getFormat(req.Format, req.ExtractMode)
			if validationErr != nil {
				return common.ToolError("invalid_input", validationErr.Error()), nil
			}
			queryValue := str.String(req.Query)
			query := queryValue.Trim()

			log.Info().
				Str("tool", "web_extract").
				Str("phase", "start").
				Int("url_count", len(urls)).
				Int("max_chars", maxChars).
				Int("query_chars", len([]rune(query))).
				Str("format", format).
				Bool("summarize", req.Summarize).
				Bool("website_policy_enabled", opts.WebsitePolicy.Enabled).
				Msg("web extract tool started")

			ctx = webprovider.WithExtractOptions(ctx, webprovider.ExtractOptions{
				Format:        format,
				MaxChars:      maxChars,
				Query:         query,
				WebsitePolicy: opts.WebsitePolicy,
			})

			log.Debug().
				Str("tool", "web_extract").
				Str("phase", "execute").
				Int("url_count", len(urls)).
				Msg("web extract provider request started")

			results, stats, err := extractWithPolicy(ctx, provider, urls, format, opts.WebsitePolicy)
			if err != nil {
				log.Warn().
					Err(err).
					Str("tool", "web_extract").
					Str("phase", "error").
					Msg("web extract provider request failed")
				return common.ToolError("tool_error", err.Error()), nil
			}

			if req.Summarize {
				log.Debug().
					Str("tool", "web_extract").
					Str("phase", "summarize").
					Int("result_count", len(results)).
					Msg("web extract summarization started")

				results, err = summarizeExtractResults(ctx, results, summarizeOptions{
					Query:                          query,
					MinSummarizeChars:              opts.MinSummarizeChars,
					MaxSummaryChars:                opts.MaxSummaryChars,
					MaxSummaryChunkChars:           opts.MaxSummaryChunkChars,
					SummarizeRefusalThresholdChars: opts.SummarizeRefusalThresholdChars,
				})
				if err != nil {
					log.Warn().
						Err(err).
						Str("tool", "web_extract").
						Str("phase", "error").
						Msg("web extract summarization failed")
					return common.ToolError("tool_error", err.Error()), nil
				}
			}

			log.Info().
				Str("tool", "web_extract").
				Str("phase", "complete").
				Int("result_count", len(results)).
				Int("provider_requested", stats.ProviderRequested).
				Int("input_blocked", stats.InputBlocked).
				Int("result_blocked", stats.ResultBlocked).
				Int("missing_results", stats.MissingResults).
				Int("extra_results", stats.ExtraResults).
				Msg("web extract tool completed")

			return common.EncodeOutput(map[string]any{"results": results})
		}),
	}
}

func extractWithPolicy(
	ctx context.Context,
	provider webprovider.Provider,
	urls []string,
	format string,
	policy guardrails.WebsitePolicy,
) ([]webprovider.ExtractResult, extractPolicyStats, error) {

	if len(urls) == 0 || !policy.Enabled {
		results, err := provider.Extract(ctx, urls)
		return results, extractPolicyStats{ProviderRequested: len(urls)}, err
	}

	stats := extractPolicyStats{}

	results := make([]webprovider.ExtractResult, len(urls))
	allowedURLs := make([]string, 0, len(urls))
	allowedIndexes := make([]int, 0, len(urls))

	for idx, rawURL := range urls {
		if block, blocked := policy.Check(rawURL); blocked {
			results[idx] = websiteBlockToExtractResult(rawURL, format, block)
			stats.InputBlocked++
			continue
		}

		allowedURLs = append(allowedURLs, rawURL)
		allowedIndexes = append(allowedIndexes, idx)
	}

	stats.ProviderRequested = len(allowedURLs)

	if len(allowedURLs) == 0 {
		return results, stats, nil
	}

	fetched, err := provider.Extract(ctx, allowedURLs)
	if err != nil {
		return nil, stats, err
	}

	for idx, result := range fetched {
		if idx >= len(allowedIndexes) {
			stats.ExtraResults += len(fetched) - idx
			break
		}

		if block, blocked := policy.Check(result.URL); blocked {
			results[allowedIndexes[idx]] = websiteBlockToExtractResult(result.URL, format, block)
			stats.ResultBlocked++
			continue
		}

		results[allowedIndexes[idx]] = result
	}

	for idx := len(fetched); idx < len(allowedIndexes); idx++ {
		allowedURLsValue := str.String(allowedURLs[idx])
		results[allowedIndexes[idx]] = webprovider.ExtractResult{
			URL:           allowedURLsValue.Trim(),
			ContentFormat: format,
			Error:         "web extraction provider returned no result",
		}
		stats.MissingResults++
	}

	return results, stats, nil
}

func websiteBlockToExtractResult(rawURL, format string, block guardrails.WebsiteBlock) webprovider.ExtractResult {
	if format == "" {
		format = "text"
	}
	rawURLValue2 := str.String(rawURL)
	return webprovider.ExtractResult{
		URL:           rawURLValue2.Trim(),
		ContentFormat: format,
		Error:         block.Message,
	}
}

func getFormat(format, extractMode string) (string, error) {
	formatValue := str.String(format)
	format = formatValue.Normalized()
	extractModeValue := str.String(extractMode)
	extractMode = extractModeValue.Normalized()

	if format != "" && extractMode != "" && format != extractMode {
		return "", fmt.Errorf("format and extract_mode must match when both are provided")
	}

	if format == "" {
		format = extractMode
	}

	if format == "" {
		return "", nil
	}

	if format != "text" && format != "markdown" {
		return "", fmt.Errorf("format must be text or markdown")
	}

	return format, nil
}

func getWebExtractOptions(options []Options) Options {
	if len(options) == 0 {
		return Options{}
	}

	return options[0]
}

func getRequestMaxChars(requested *int, configuredMax int) (int, error) {
	if requested == nil {
		return 0, nil
	}

	if *requested <= 0 {
		return 0, fmt.Errorf("max_chars must be greater than zero")
	}

	if configuredMax > 0 && *requested > configuredMax {
		return configuredMax, nil
	}

	return *requested, nil
}
