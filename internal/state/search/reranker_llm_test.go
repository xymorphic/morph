package search

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
)

type llmRerankerModelStub struct {
	requests  []models.Request
	responses []*models.Response
	errs      []error
}

func (s *llmRerankerModelStub) Complete(_ context.Context, req models.Request) (*models.Response, error) {
	s.requests = append(s.requests, req)
	idx := len(s.requests) - 1
	if idx < len(s.errs) && s.errs[idx] != nil {
		return nil, s.errs[idx]
	}
	if idx < len(s.responses) {
		return s.responses[idx], nil
	}

	return nil, errors.New("unexpected model request")
}

func (s *llmRerankerModelStub) CompleteStream(
	context.Context,
	models.Request,
	func(models.StreamDelta),
) (*models.Response, error) {
	return nil, errors.New("unexpected stream call")
}

func TestNewLLMReranker_DisabledByDefault(t *testing.T) {
	client := &llmRerankerModelStub{}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:   client,
		Model:    "openai/gpt-4.1-mini",
		Fallback: NoopReranker{},
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{
			testSessionCandidate("candidate-a", 0, 0, 0.3, time.Time{}),
			testSessionCandidate("candidate-b", 0, 0, 0.7, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-a", "candidate-b"}, rerankIDs(result))
	require.Empty(t, client.requests)
}

func TestLLMReranker_RunsOnlyWhenEnabled(t *testing.T) {
	client := &llmRerankerModelStub{
		responses: []*models.Response{llmRerankModelResponse("candidate-b", "candidate-a")},
	}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:          client,
		Model:           "openai/gpt-4.1-mini",
		API:             models.APIOpenAIResponses,
		MaxCandidates:   2,
		MaxOutputTokens: 128,
		Enabled:         true,
		DebugRequests:   true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Query:      "rank this",
		Caller:     "session_search",
		TraceID:    "trace-1",
		SourceKind: SourceKindSessionMessage,
		Candidates: []Candidate{
			testSessionCandidate("candidate-a", 0, 0, 0.3, time.Time{}),
			testSessionCandidate("candidate-b", 0, 0, 0.7, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []RerankItem{
		{CandidateID: "candidate-b", Score: 1},
		{CandidateID: "candidate-a", Score: 0.5},
	}, result.Items)
	require.Len(t, client.requests, 1)
	require.Equal(t, "openai/gpt-4.1-mini", client.requests[0].Model)
	require.Equal(t, models.APIOpenAIResponses, client.requests[0].API)
	require.Equal(t, int64(128), client.requests[0].MaxOutputTokens)
	require.True(t, client.requests[0].DebugRequests)
	require.NotNil(t, client.requests[0].StructuredOutput)
	require.Contains(t, client.requests[0].Instructions, "Do not rewrite candidate text or metadata.")
	require.Len(t, client.requests[0].Messages, 1)
	require.JSONEq(t, `{
		"query": "rank this",
		"caller": "session_search",
		"trace_id": "trace-1",
		"source_kind": "session_message",
		"candidates": [
			{
				"id": "candidate-a",
				"source_kind": "session_message",
				"text": "session text",
				"lexical_score": 0,
				"vector_score": 0,
				"fused_score": 0.3
			},
			{
				"id": "candidate-b",
				"source_kind": "session_message",
				"text": "session text",
				"lexical_score": 0,
				"vector_score": 0,
				"fused_score": 0.7
			}
		]
	}`, client.requests[0].Messages[0].Content)
}

func TestLLMReranker_FallsBackWithoutRequiredModelDependencies(t *testing.T) {
	result, err := LLMReranker{options: LLMRerankerOptions{
		Enabled:  true,
		Fallback: NoopReranker{},
	}}.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{testSessionCandidate("candidate-a", 0, 0, 0.4, time.Time{})},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-a"}, rerankIDs(result))
	require.IsType(t, DeterministicReranker{}, fallbackReranker(nil))
}

func TestLLMReranker_ReturnsEmptyResultForEmptyCandidates(t *testing.T) {
	client := &llmRerankerModelStub{}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:  client,
		Model:   "openai/gpt-4.1-mini",
		Enabled: true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{})

	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.Empty(t, client.requests)
}

func TestLLMReranker_RespectsExplicitZeroCandidateBound(t *testing.T) {
	client := &llmRerankerModelStub{}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:           client,
		Model:            "openai/gpt-4.1-mini",
		MaxCandidates:    0,
		MaxCandidatesSet: true,
		Enabled:          true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{testSessionCandidate("candidate-a", 0, 0, 1, time.Time{})},
	})

	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.Empty(t, client.requests)
}

func TestLLMReranker_TruncatesCandidateTextAndBoundsModelInput(t *testing.T) {
	client := &llmRerankerModelStub{
		responses: []*models.Response{llmRerankModelResponse("candidate-a")},
	}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:                client,
		Model:                 "openai/gpt-4.1-mini",
		MaxCandidates:         1,
		MaxCandidateTextChars: 4,
		Enabled:               true,
	})
	candidate := testSessionCandidate("candidate-a", 0, 0, 1, time.Time{})
	candidate.Text = "abcdef"

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{
			candidate,
			testSessionCandidate("candidate-b", 0, 0, 0.5, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-a"}, rerankIDs(result))
	require.Len(t, client.requests, 1)

	var payload llmRerankPayload
	require.NoError(t, json.Unmarshal([]byte(client.requests[0].Messages[0].Content), &payload))
	require.Len(t, payload.Candidates, 1)
	require.Equal(t, "abcd", payload.Candidates[0].Text)
	require.Empty(t, truncateString("abcdef", 0))

	client = &llmRerankerModelStub{
		responses: []*models.Response{llmRerankModelResponse("candidate-a")},
	}
	reranker = NewLLMReranker(LLMRerankerOptions{
		Client:                   client,
		Model:                    "openai/gpt-4.1-mini",
		MaxCandidates:            1,
		MaxCandidateTextChars:    0,
		MaxCandidateTextCharsSet: true,
		Enabled:                  true,
	})

	result, err = reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{candidate},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-a"}, rerankIDs(result))
	require.Len(t, client.requests, 1)
	require.NoError(t, json.Unmarshal([]byte(client.requests[0].Messages[0].Content), &payload))
	require.Empty(t, payload.Candidates[0].Text)
}

func TestLLMReranker_UsesSmallerRequestCandidateBound(t *testing.T) {
	client := &llmRerankerModelStub{
		responses: []*models.Response{llmRerankModelResponse("candidate-a")},
	}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:        client,
		Model:         "openai/gpt-4.1-mini",
		MaxCandidates: 3,
		Enabled:       true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Options: RerankOptions{MaxCandidates: 1},
		Candidates: []Candidate{
			testSessionCandidate("candidate-a", 0, 0, 0.2, time.Time{}),
			testSessionCandidate("candidate-b", 0, 0, 0.8, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-a"}, rerankIDs(result))
	require.Len(t, client.requests, 1)

	var payload llmRerankPayload
	require.NoError(t, json.Unmarshal([]byte(client.requests[0].Messages[0].Content), &payload))
	require.Len(t, payload.Candidates, 1)
}

func TestLLMReranker_RetriesWithoutStructuredOutputWhenStructuredRequestFails(t *testing.T) {
	client := &llmRerankerModelStub{
		errs:      []error{errors.New("structured output unsupported"), nil},
		responses: []*models.Response{nil, llmRerankModelResponse("candidate-b", "candidate-a")},
	}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:  client,
		Model:   "openai/gpt-4.1-mini",
		Enabled: true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{
			testSessionCandidate("candidate-a", 0, 0, 0.2, time.Time{}),
			testSessionCandidate("candidate-b", 0, 0, 0.8, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-b", "candidate-a"}, rerankIDs(result))
	require.Len(t, client.requests, 2)
	require.NotNil(t, client.requests[0].StructuredOutput)
	require.Nil(t, client.requests[1].StructuredOutput)
}

func TestLLMReranker_FallsBackWithoutRetryOnTimeout(t *testing.T) {
	client := &llmRerankerModelStub{
		errs: []error{context.DeadlineExceeded},
	}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:  client,
		Model:   "openai/gpt-4.1-mini",
		Enabled: true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{
			testSessionCandidate("candidate-a", 0, 0, 0.2, time.Time{}),
			testSessionCandidate("candidate-b", 0, 0, 0.8, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-b", "candidate-a"}, rerankIDs(result))
	require.Len(t, client.requests, 1)
}

func TestLLMReranker_FallsBackOnModelFailureOrMalformedOutput(t *testing.T) {
	candidates := []Candidate{
		testSessionCandidate("candidate-a", 0, 0, 0.2, time.Time{}),
		testSessionCandidate("candidate-b", 0, 0, 0.8, time.Time{}),
	}

	tests := []struct {
		name      string
		responses []*models.Response
		errs      []error
	}{
		{
			name: "model failure",
			errs: []error{errors.New("structured failed"), errors.New("plain json failed")},
		},
		{
			name:      "nil response",
			responses: []*models.Response{nil},
		},
		{
			name:      "tool calls",
			responses: []*models.Response{{RequiresToolCalls: true}},
		},
		{
			name:      "empty output",
			responses: []*models.Response{{OutputText: "  "}},
		},
		{
			name:      "malformed json",
			responses: []*models.Response{{OutputText: `{"items":`}},
		},
		{
			name:      "empty items",
			responses: []*models.Response{{OutputText: `{"items":[]}`}},
		},
		{
			name:      "unknown candidate id",
			responses: []*models.Response{llmRerankModelResponse("candidate-missing")},
		},
		{
			name: "non-finite score",
			responses: []*models.Response{{
				OutputText: `{"items":[{"candidate_id":"candidate-a","score":NaN}]}`,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &llmRerankerModelStub{responses: tt.responses, errs: tt.errs}
			reranker := NewLLMReranker(LLMRerankerOptions{
				Client:  client,
				Model:   "openai/gpt-4.1-mini",
				Enabled: true,
			})

			result, err := reranker.Rerank(context.Background(), RerankRequest{Candidates: candidates})

			require.NoError(t, err)
			require.Equal(t, []string{"candidate-b", "candidate-a"}, rerankIDs(result))
		})
	}
}

func TestLLMReranker_FallbackUsesBoundedCandidates(t *testing.T) {
	client := &llmRerankerModelStub{
		responses: []*models.Response{llmRerankModelResponse("candidate-missing")},
	}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:        client,
		Model:         "openai/gpt-4.1-mini",
		MaxCandidates: 1,
		Enabled:       true,
	})

	result, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{
			testSessionCandidate("candidate-a", 0, 0, 0.2, time.Time{}),
			testSessionCandidate("candidate-b", 0, 0, 0.8, time.Time{}),
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"candidate-a"}, rerankIDs(result))
}

func TestLLMReranker_ValidationErrors(t *testing.T) {
	client := &llmRerankerModelStub{}
	reranker := NewLLMReranker(LLMRerankerOptions{
		Client:        client,
		Model:         "openai/gpt-4.1-mini",
		MaxCandidates: -1,
		Enabled:       true,
	})

	_, err := reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{testSessionCandidate("candidate-a", 0, 0, 0, time.Time{})},
	})
	require.EqualError(t, err, "max candidates must be greater than or equal to zero")

	reranker = NewLLMReranker(LLMRerankerOptions{
		Client:  client,
		Model:   "openai/gpt-4.1-mini",
		Enabled: true,
	})
	_, err = reranker.Rerank(context.Background(), RerankRequest{
		Options:    RerankOptions{MaxCandidates: -1},
		Candidates: []Candidate{testSessionCandidate("candidate-a", 0, 0, 0, time.Time{})},
	})
	require.EqualError(t, err, "max candidates must be greater than or equal to zero")

	invalid := testSessionCandidate("candidate-a", 0, 0, 0, time.Time{})
	invalid.VectorScore = math.NaN()
	reranker = NewLLMReranker(LLMRerankerOptions{
		Client:  client,
		Model:   "openai/gpt-4.1-mini",
		Enabled: true,
	})

	_, err = reranker.Rerank(context.Background(), RerankRequest{
		Candidates: []Candidate{invalid},
	})
	require.EqualError(t, err, "candidate vector score must be finite")
}

func llmRerankModelResponse(ids ...string) *models.Response {
	items := make([]map[string]any, 0, len(ids))
	for idx, id := range ids {
		items = append(items, map[string]any{
			"candidate_id": id,
			"score":        float64(len(ids)-idx) / float64(len(ids)),
		})
	}
	data, _ := json.Marshal(map[string]any{"items": items})

	return &models.Response{OutputText: string(data)}
}
