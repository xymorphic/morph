package webextract

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	instruct "github.com/xymorphic/morph/internal/instructions"
	models "github.com/xymorphic/morph/internal/model"
	webprovider "github.com/xymorphic/morph/internal/providers/web"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// Summarizer produces a concise summary for extracted web content.
type Summarizer interface {
	SummarizeExtract(context.Context, SummaryInput) (string, error)
}

// SummaryInput describes input for summary.
type SummaryInput struct {
	URL                  string
	Title                string
	Query                string
	Content              string
	MaxSummaryChars      int
	MaxSummaryChunkChars int
}

type summarizerContextKey struct{}

type summarizeOptions struct {
	Query                          string
	MinSummarizeChars              int
	MaxSummaryChars                int
	MaxSummaryChunkChars           int
	SummarizeRefusalThresholdChars int
}

// ExtractSummarizer describes extract summarizer.
type ExtractSummarizer struct {
	Client        models.Client
	Model         string
	API           string
	DebugRequests bool
}

// NewExtractSummarizer returns a summarizer backed by a model client.
func NewExtractSummarizer(client models.Client, cfg *config.Config) Summarizer {
	if client == nil || cfg == nil {
		return nil
	}

	normalized := *cfg
	normalized.Normalize()

	return ExtractSummarizer{
		Client:        client,
		Model:         normalized.SummaryModelEffective(),
		API:           normalized.SummaryModelAPIEffective(),
		DebugRequests: normalized.Debug.Requests,
	}
}

// WithSummarizer replaces the summarizer used by the web extract tool.
func WithSummarizer(ctx context.Context, summarizer Summarizer) context.Context {
	if summarizer == nil {
		return ctx
	}

	return context.WithValue(ctx, summarizerContextKey{}, summarizer)
}

func getSummarizerFromContext(ctx context.Context) Summarizer {
	if ctx == nil {
		return nil
	}

	summarizer, _ := ctx.Value(summarizerContextKey{}).(Summarizer)
	return summarizer
}

func (s ExtractSummarizer) SummarizeExtract(ctx context.Context, input SummaryInput) (string, error) {
	if s.Client == nil {
		return "", errors.New("web extract summarizer is not configured")
	}

	contentValue := str.String(input.Content)
	content := contentValue.Trim()
	if input.MaxSummaryChunkChars > 0 && getRuneLength(content) > input.MaxSummaryChunkChars {
		return s.summarizeChunked(ctx, input)
	}

	return s.completeSummary(
		ctx,
		instruct.BuildWebExtractSummary(input.MaxSummaryChars),
		renderSummaryPrompt(input),
		input.MaxSummaryChars,
	)
}

func (s ExtractSummarizer) summarizeChunked(ctx context.Context, input SummaryInput) (string, error) {
	chunks := splitIntoChunks(input.Content, input.MaxSummaryChunkChars)
	chunkSummaries := make([]string, 0, len(chunks))
	for idx, chunk := range chunks {
		summary, err := s.completeSummary(
			ctx,
			instruct.BuildWebExtractChunkSummary(input.MaxSummaryChars, idx+1, len(chunks)),
			renderChunkSummaryPrompt(input, chunk, idx+1, len(chunks)),
			input.MaxSummaryChars,
		)
		if err != nil {
			return "", err
		}

		chunkSummaries = append(chunkSummaries, summary)
	}

	return s.completeSummary(
		ctx,
		instruct.BuildWebExtractSynthesis(input.MaxSummaryChars),
		renderSynthesisPrompt(input, chunkSummaries),
		input.MaxSummaryChars,
	)
}

func (s ExtractSummarizer) completeSummary(
	ctx context.Context,
	instructions string,
	prompt string,
	maxSummaryChars int,
) (string, error) {
	resp, err := s.Client.Complete(ctx, models.Request{
		Model:        s.Model,
		API:          s.API,
		Instructions: instructions,
		Messages: []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: prompt,
		}},
		MaxOutputTokens: getMaxSummaryOutputTokens(maxSummaryChars),
		Temperature:     0,
		DebugRequests:   s.DebugRequests,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("web extract summary response is required")
	}
	if resp.RequiresToolCalls {
		return "", errors.New("web extract summary requested tool calls")
	}
	outputTextValue := str.String(resp.OutputText)
	if outputTextValue.Trim() == "" {
		return "", errors.New("web extract summary is empty")
	}
	outputTextValue2 := str.String(resp.OutputText)
	return outputTextValue2.Trim(), nil
}

func summarizeExtractResults(
	ctx context.Context,
	results []webprovider.ExtractResult,
	options summarizeOptions,
) ([]webprovider.ExtractResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	summarizer := getSummarizerFromContext(ctx)
	summarized := make([]webprovider.ExtractResult, len(results))
	copy(summarized, results)
	for idx := range summarized {
		result := &summarized[idx]
		errorValue := str.String(result.Error)
		contentValue2 := str.String(result.Content)
		if errorValue.Trim() != "" || contentValue2.Trim() == "" {
			continue
		}

		sourceChars := getRuneLength(result.Content)
		if sourceChars < options.MinSummarizeChars {
			continue
		}
		result.SourceContentChars = sourceChars
		if options.SummarizeRefusalThresholdChars > 0 && sourceChars > options.SummarizeRefusalThresholdChars {
			result.SummaryRefused = true
			errorValue2 := str.String(result.Error)
			if errorValue2.Trim() == "" {
				result.Error = "content exceeds summarization threshold"
			}
			continue
		}
		if summarizer == nil {
			return nil, errors.New("web extract summarizer is not configured")
		}

		summary, err := summarizer.SummarizeExtract(ctx, SummaryInput{
			URL:                  result.URL,
			Title:                result.Title,
			Query:                options.Query,
			Content:              result.Content,
			MaxSummaryChars:      options.MaxSummaryChars,
			MaxSummaryChunkChars: options.MaxSummaryChunkChars,
		})
		if err != nil {
			return nil, err
		}

		summary, truncated := truncateToChars(summary, options.MaxSummaryChars)
		result.Content = summary
		result.ContentFormat = "summary"
		result.Summarized = true
		result.SummaryChars = getRuneLength(summary)
		result.Truncated = result.Truncated || truncated
	}

	return summarized, nil
}

func renderSummaryPrompt(input SummaryInput) string {
	uRLValue := str.String(input.URL)
	titleValue := str.String(input.Title)
	parts := []string{
		"URL: " + uRLValue.Trim(),
		"Title: " + titleValue.Trim(),
	}
	queryValue := str.String(input.Query)
	if query := queryValue.Trim(); query != "" {
		parts = append(parts, "Query: "+query)
	}
	contentValue3 := str.String(input.Content)
	parts = append(parts, "Content:\n"+contentValue3.Trim())

	return strings.Join(parts, "\n\n")
}

func renderChunkSummaryPrompt(input SummaryInput, chunk string, chunkIndex, chunkCount int) string {
	uRLValue2 := str.String(input.URL)
	titleValue2 := str.String(input.Title)
	parts := []string{
		"URL: " + uRLValue2.Trim(),
		"Title: " + titleValue2.Trim(),
		"Chunk: " + strconv.Itoa(chunkIndex) + " of " + strconv.Itoa(chunkCount),
	}
	queryValue2 := str.String(input.Query)
	if query := queryValue2.Trim(); query != "" {
		parts = append(parts, "Query: "+query)
	}
	chunkValue := str.String(chunk)
	parts = append(parts, "Chunk Content:\n"+chunkValue.Trim())

	return strings.Join(parts, "\n\n")
}

func renderSynthesisPrompt(input SummaryInput, chunkSummaries []string) string {
	uRLValue3 := str.String(input.URL)
	titleValue3 := str.String(input.Title)
	parts := []string{
		"URL: " + uRLValue3.Trim(),
		"Title: " + titleValue3.Trim(),
	}
	queryValue3 := str.String(input.Query)
	if query := queryValue3.Trim(); query != "" {
		parts = append(parts, "Query: "+query)
	}

	sections := make([]string, 0, len(chunkSummaries))
	for idx, summary := range chunkSummaries {
		summaryValue := str.String(summary)
		sections = append(sections, "Chunk "+strconv.Itoa(idx+1)+" Summary:\n"+summaryValue.Trim())
	}
	parts = append(parts, "Chunk Summaries:\n"+strings.Join(sections, "\n\n"))

	return strings.Join(parts, "\n\n")
}

func splitIntoChunks(content string, chunkChars int) []string {
	contentValue4 := str.String(content)
	content = contentValue4.Trim()
	if content == "" {
		return nil
	}
	if chunkChars <= 0 {
		return []string{content}
	}

	runes := []rune(content)
	chunks := make([]string, 0, (len(runes)+chunkChars-1)/chunkChars)
	for start := 0; start < len(runes); start += chunkChars {
		end := min(start+chunkChars, len(runes))
		trimmedValue := str.String(string(runes[start:end]))
		chunk := trimmedValue.Trim()
		if chunk == "" {
			continue
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

func getMaxSummaryOutputTokens(maxSummaryChars int) int64 {
	if maxSummaryChars <= 0 {
		return 0
	}

	return int64(maxSummaryChars/4 + 128)
}

func truncateToChars(value string, maxChars int) (string, bool) {
	valueText := str.String(value).Trim()
	if valueText == "" || maxChars <= 0 {
		return valueText, false
	}

	runes := []rune(valueText)
	if len(runes) <= maxChars {
		return valueText, false
	}
	trimmedValue2 := str.String(string(runes[:maxChars]))
	return trimmedValue2.Trim(), true
}

func getRuneLength(value string) int {
	value2 := str.String(value)
	return len([]rune(value2.Trim()))
}
