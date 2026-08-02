package webextract

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	models "github.com/xymorphic/morph/internal/model"
	webprovider "github.com/xymorphic/morph/internal/providers/web"
)

type modelClientStub struct {
	requests  []models.Request
	responses []*models.Response
	response  *models.Response
	err       error
}

func (s *modelClientStub) Complete(_ context.Context, req models.Request) (*models.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.responses) > 0 {
		idx := len(s.requests) - 1
		if idx >= len(s.responses) {
			return nil, errors.New("unexpected model request")
		}
		return s.responses[idx], nil
	}
	return s.response, s.err
}

func (s *modelClientStub) CompleteStream(context.Context, models.Request, func(models.StreamDelta)) (*models.Response, error) {
	return nil, errors.New("unexpected stream call")
}

func TestNewExtractSummarizer_ReturnsNilWithoutDependencies(t *testing.T) {
	require.Nil(t, NewExtractSummarizer(nil, &config.Config{}))
	require.Nil(t, NewExtractSummarizer(&modelClientStub{}, nil))
}

func TestNewExtractSummarizer_UsesSummaryModelEffective(t *testing.T) {
	summarizer := NewExtractSummarizer(&modelClientStub{}, &config.Config{
		Models: config.ModelsConfig{
			Main:    config.MainModelConfig{Name: "openai/gpt-4o-mini", Provider: "openrouter", APIKey: "key"},
			Summary: config.SummaryModelConfig{Name: "openai/gpt-4.1-mini"},
		},
	})

	modelSummarizer, ok := summarizer.(ExtractSummarizer)
	require.True(t, ok)
	require.Equal(t, "openai/gpt-4.1-mini", modelSummarizer.Model)
	require.Equal(t, models.APIOpenAIResponses, modelSummarizer.API)
}

func TestNewExtractSummarizer_FallsBackToMainModel(t *testing.T) {
	summarizer := NewExtractSummarizer(&modelClientStub{}, &config.Config{
		Models: config.ModelsConfig{
			Main: config.MainModelConfig{Name: "openai/gpt-4o-mini", Provider: "openrouter", APIKey: "key"},
		},
	})

	modelSummarizer, ok := summarizer.(ExtractSummarizer)
	require.True(t, ok)
	require.Equal(t, "openai/gpt-4o-mini", modelSummarizer.Model)
}

func TestWithSummarizer_ReturnsOriginalContextWhenSummarizerIsNil(t *testing.T) {
	ctx := context.Background()

	require.True(t, ctx == WithSummarizer(ctx, nil))
}

func TestSummarizerFromContext_ReturnsNilWithoutSummarizer(t *testing.T) {
	var nilContext context.Context
	require.Nil(t, getSummarizerFromContext(nilContext))
	require.Nil(t, getSummarizerFromContext(context.Background()))
	require.Nil(t, getSummarizerFromContext(context.WithValue(context.Background(), summarizerContextKey{}, "not a summarizer")))
}

func TestExtractSummarizer_SummarizeExtractBuildsModelRequest(t *testing.T) {
	client := &modelClientStub{response: &models.Response{OutputText: " concise summary "}}
	summarizer := ExtractSummarizer{
		Client:        client,
		Model:         "openai/gpt-4o-mini",
		API:           models.APIOpenAIResponses,
		DebugRequests: true,
	}

	summary, err := summarizer.SummarizeExtract(context.Background(), SummaryInput{
		URL:             "https://example.com",
		Title:           "Example",
		Query:           "pricing",
		Content:         "Long content",
		MaxSummaryChars: 500,
	})

	require.NoError(t, err)
	require.Equal(t, "concise summary", summary)
	require.Len(t, client.requests, 1)
	require.Equal(t, "openai/gpt-4o-mini", client.requests[0].Model)
	require.Equal(t, models.APIOpenAIResponses, client.requests[0].API)
	require.Contains(t, client.requests[0].Instructions, "# Web Extract Summary")
	require.Contains(t, client.requests[0].Instructions, "under 500 characters")
	require.Len(t, client.requests[0].Messages, 1)
	require.Contains(t, client.requests[0].Messages[0].Content, "URL: https://example.com")
	require.Contains(t, client.requests[0].Messages[0].Content, "Query: pricing")
	require.Contains(t, client.requests[0].Messages[0].Content, "Long content")
	require.Equal(t, int64(253), client.requests[0].MaxOutputTokens)
	require.True(t, client.requests[0].DebugRequests)
}

func TestExtractSummarizer_SummarizeExtractChunksAndSynthesizesLargeContent(t *testing.T) {
	client := &modelClientStub{responses: []*models.Response{
		{OutputText: "chunk one"},
		{OutputText: "chunk two"},
		{OutputText: "chunk three"},
		{OutputText: " final summary "},
	}}
	summarizer := ExtractSummarizer{
		Client: client,
		Model:  "openai/gpt-4o-mini",
		API:    models.APIOpenAIResponses,
	}

	summary, err := summarizer.SummarizeExtract(context.Background(), SummaryInput{
		URL:                  "https://example.com",
		Title:                "Example",
		Query:                "pricing",
		Content:              "abcdefghi",
		MaxSummaryChars:      500,
		MaxSummaryChunkChars: 3,
	})

	require.NoError(t, err)
	require.Equal(t, "final summary", summary)
	require.Len(t, client.requests, 4)
	require.Contains(t, client.requests[0].Instructions, "# Web Extract Chunk Summary")
	require.Contains(t, client.requests[0].Instructions, "chunk 1 of 3")
	require.Contains(t, client.requests[0].Messages[0].Content, "Chunk: 1 of 3")
	require.Contains(t, client.requests[0].Messages[0].Content, "Chunk Content:\nabc")
	require.Contains(t, client.requests[1].Messages[0].Content, "Chunk Content:\ndef")
	require.Contains(t, client.requests[2].Messages[0].Content, "Chunk Content:\nghi")
	require.Contains(t, client.requests[3].Instructions, "# Web Extract Summary Synthesis")
	require.Contains(t, client.requests[3].Messages[0].Content, "Chunk 1 Summary:\nchunk one")
	require.Contains(t, client.requests[3].Messages[0].Content, "Chunk 2 Summary:\nchunk two")
	require.Contains(t, client.requests[3].Messages[0].Content, "Chunk 3 Summary:\nchunk three")
}

func TestExtractSummarizer_SummarizeExtractReturnsChunkError(t *testing.T) {
	client := &modelClientStub{responses: []*models.Response{{OutputText: "chunk one"}}}
	summarizer := ExtractSummarizer{Client: client}

	_, err := summarizer.SummarizeExtract(context.Background(), SummaryInput{
		Content:              "abcdef",
		MaxSummaryChars:      500,
		MaxSummaryChunkChars: 3,
	})

	require.EqualError(t, err, "unexpected model request")
	require.Len(t, client.requests, 2)
}

func TestExtractSummarizer_ReturnsModelErrors(t *testing.T) {
	summarizer := ExtractSummarizer{Client: &modelClientStub{err: errors.New("model failed")}}

	_, err := summarizer.SummarizeExtract(context.Background(), SummaryInput{Content: "content"})

	require.EqualError(t, err, "model failed")
}

func TestExtractSummarizer_RejectsInvalidResponses(t *testing.T) {
	_, err := (ExtractSummarizer{}).SummarizeExtract(context.Background(), SummaryInput{Content: "content"})
	require.EqualError(t, err, "web extract summarizer is not configured")

	_, err = (ExtractSummarizer{Client: &modelClientStub{}}).SummarizeExtract(context.Background(), SummaryInput{Content: "content"})
	require.EqualError(t, err, "web extract summary response is required")

	_, err = (ExtractSummarizer{Client: &modelClientStub{
		response: &models.Response{RequiresToolCalls: true},
	}}).SummarizeExtract(context.Background(), SummaryInput{Content: "content"})
	require.EqualError(t, err, "web extract summary requested tool calls")

	_, err = (ExtractSummarizer{Client: &modelClientStub{
		response: &models.Response{OutputText: "   "},
	}}).SummarizeExtract(context.Background(), SummaryInput{Content: "content"})
	require.EqualError(t, err, "web extract summary is empty")
}

func TestSummarizeResults_ReturnsEmptyResults(t *testing.T) {
	results, err := summarizeExtractResults(context.Background(), nil, summarizeOptions{})

	require.NoError(t, err)
	require.Nil(t, results)
}

func TestSummarizeResults_SkipsErroredAndBlankResults(t *testing.T) {
	summarizer := &stubSummarizer{output: "summary"}
	results, err := summarizeExtractResults(
		WithSummarizer(context.Background(), summarizer),
		[]webprovider.ExtractResult{
			{URL: "https://bad.example", Content: "long enough", Error: "failed"},
			{URL: "https://blank.example", Content: "   "},
		},
		summarizeOptions{MinSummarizeChars: 3, MaxSummaryChars: 20},
	)

	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "long enough", results[0].Content)
	require.Equal(t, "failed", results[0].Error)
	require.Equal(t, "   ", results[1].Content)
	require.Empty(t, summarizer.inputs)
}

func TestSummarizeResults_ReturnsSummarizerError(t *testing.T) {
	summarizer := &stubSummarizer{err: errors.New("summary failed")}
	_, err := summarizeExtractResults(
		WithSummarizer(context.Background(), summarizer),
		[]webprovider.ExtractResult{{URL: "https://example.com", Content: "long enough"}},
		summarizeOptions{MinSummarizeChars: 3, MaxSummaryChars: 20},
	)

	require.EqualError(t, err, "summary failed")
}

func TestSplitIntoChunks_SplitsByRuneCount(t *testing.T) {
	require.Nil(t, splitIntoChunks("   ", 3))
	require.Equal(t, []string{"abc"}, splitIntoChunks(" abc ", 0))
	require.Equal(t, []string{"åß", "cd", "ef"}, splitIntoChunks("åßcdef", 2))
	require.Equal(t, []string{"ab", "cd", "e"}, splitIntoChunks("abcde", 2))
	require.Equal(t, []string{"ab", "cd"}, splitIntoChunks("ab    cd", 2))
}

func TestTruncateToChars_TrimsAndReportsTruncation(t *testing.T) {
	content, truncated := truncateToChars(" abcdef ", 3)
	require.Equal(t, "abc", content)
	require.True(t, truncated)

	content, truncated = truncateToChars(" abc ", 10)
	require.Equal(t, "abc", content)
	require.False(t, truncated)
}

func TestTruncateToChars_ReturnsOriginalWhenEmptyOrLimitDisabled(t *testing.T) {
	content, truncated := truncateToChars("   ", 3)
	require.Empty(t, content)
	require.False(t, truncated)

	content, truncated = truncateToChars(" abc ", 0)
	require.Equal(t, "abc", content)
	require.False(t, truncated)
}
