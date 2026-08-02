package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/xymorphic/morph/internal/constants"
	instruct "github.com/xymorphic/morph/internal/instructions"
	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultLLMRerankerMaxCandidates       = constants.DefaultLLMRerankerMaxCandidates
	defaultLLMRerankerMaxCandidateTextLen = constants.DefaultLLMRerankerMaxCandidateTextLen
)

// LLMRerankerOptions controls llm reranker.
type LLMRerankerOptions struct {
	Fallback                 Reranker
	Client                   models.Client
	Model                    string
	API                      string
	MaxCandidates            int
	MaxCandidatesSet         bool
	MaxCandidateTextChars    int
	MaxCandidateTextCharsSet bool
	MaxOutputTokens          int64
	Enabled                  bool
	DebugRequests            bool
}

// LLMReranker reranks llm candidates.
type LLMReranker struct {
	options LLMRerankerOptions
}

func (LLMReranker) Name() string {
	return RerankerLLM
}

// NewLLMReranker returns a reranker backed by an LLM.
func NewLLMReranker(options LLMRerankerOptions) Reranker {
	if !options.Enabled {
		retrievalLog.Trace().
			Str("fallback", fallbackReranker(options.Fallback).Name()).
			Msg("llm reranker disabled, using fallback reranker")
		return fallbackReranker(options.Fallback)
	}

	retrievalLog.Trace().
		Str("reranker", RerankerLLM).
		Int("max_candidates", options.MaxCandidates).
		Int("max_candidate_text_chars", options.MaxCandidateTextChars).
		Msg("llm reranker enabled for retrieval ranking")

	return LLMReranker{options: normalizeLLMRerankerOptions(options)}
}

func (r LLMReranker) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	options := normalizeLLMRerankerOptions(r.options)
	modelValue := str.String(options.Model)
	if !options.Enabled || options.Client == nil || modelValue.Trim() == "" {
		modelValue2 := str.String(options.Model)
		rerankDebugLogEvent(req, RerankerLLM).
			Bool("enabled", options.Enabled).
			Bool("has_client", options.Client != nil).
			Bool("has_model", modelValue2.Trim() != "").
			Msg("llm rerank unavailable, using fallback")
		return options.Fallback.Rerank(ctx, req)
	}

	maxCandidates, err := getEffectiveLLMMaxCandidates(options.MaxCandidates, req.Options.MaxCandidates)
	if err != nil {
		rerankTraceLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank candidate bound failed")
		return RerankResult{}, err
	}

	candidates := req.Candidates
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}
	if len(candidates) == 0 {
		rerankTraceLogEvent(req, RerankerLLM).Msg("llm rerank skipped without candidates")
		return RerankResult{}, nil
	}
	for _, candidate := range candidates {
		if err := ValidateCandidate(candidate); err != nil {
			rerankTraceLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank candidate validation failed")
			return RerankResult{}, err
		}
	}

	rerankTraceLogEvent(req, RerankerLLM).
		Str("model", options.Model).
		Str("api", options.API).
		Int("candidate_count", len(req.Candidates)).
		Int("bounded_candidate_count", len(candidates)).
		Int("max_candidate_text_chars", options.MaxCandidateTextChars).
		Msg("llm rerank model request started to order retrieval candidates")

	modelReq := r.modelRequest(req, candidates, true)
	resp, err := options.Client.Complete(ctx, modelReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			rerankDebugLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank timed out, using fallback")
			return options.Fallback.Rerank(ctx, rerankRequestWithCandidateSet(req, candidates))
		}

		rerankDebugLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank structured request failed, retrying without structured output")
		modelReq.StructuredOutput = nil
		resp, err = options.Client.Complete(ctx, modelReq)
	}
	if err != nil {
		rerankDebugLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank model request failed, using fallback")
		return options.Fallback.Rerank(ctx, rerankRequestWithCandidateSet(req, candidates))
	}

	result, err := parseLLMRerankResponse(resp)
	if err != nil {
		rerankDebugLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank response parse failed, using fallback")
		return options.Fallback.Rerank(ctx, rerankRequestWithCandidateSet(req, candidates))
	}
	if err := ValidateRerankResult(candidates, result); err != nil {
		rerankDebugLogEvent(req, RerankerLLM).Err(err).Msg("llm rerank result rejected, using fallback")
		return options.Fallback.Rerank(ctx, rerankRequestWithCandidateSet(req, candidates))
	}

	rerankTraceLogEvent(req, RerankerLLM).
		Int("bounded_candidate_count", len(candidates)).
		Int("result_count", len(result.Items)).
		Msg("llm rerank completed for retrieval candidates")

	result.Reranker = RerankerLLM
	return result, nil
}

func (r LLMReranker) modelRequest(req RerankRequest, candidates []Candidate, structuredOutput bool) models.Request {
	options := normalizeLLMRerankerOptions(r.options)
	queryValue := str.String(req.Query)
	callerValue := str.String(req.Caller)
	traceIDValue := str.String(req.TraceID)
	sourceKindValue := str.String(string(req.SourceKind))
	payload := llmRerankPayload{
		Query:      queryValue.Trim(),
		Caller:     callerValue.Trim(),
		TraceID:    traceIDValue.Trim(),
		SourceKind: sourceKindValue.Trim(),
		Candidates: candidatesToLLMRerankCandidates(candidates, options.MaxCandidateTextChars),
	}
	data, _ := json.Marshal(payload)

	modelReq := models.Request{
		Model:           options.Model,
		API:             options.API,
		Instructions:    instruct.BuildRetrievalRerank(),
		Messages:        []morphmsg.Message{{Role: morphmsg.RoleUser, Content: string(data)}},
		MaxOutputTokens: options.MaxOutputTokens,
		DebugRequests:   options.DebugRequests,
	}
	if structuredOutput {
		modelReq.StructuredOutput = getLLMRerankerStructuredOutput()
	}

	return modelReq
}

func normalizeLLMRerankerOptions(options LLMRerankerOptions) LLMRerankerOptions {
	if options.Fallback == nil {
		options.Fallback = DeterministicReranker{}
	}
	if !options.MaxCandidatesSet && options.MaxCandidates == 0 {
		options.MaxCandidates = defaultLLMRerankerMaxCandidates
	}
	if !options.MaxCandidateTextCharsSet && options.MaxCandidateTextChars <= 0 {
		options.MaxCandidateTextChars = defaultLLMRerankerMaxCandidateTextLen
	}

	return options
}

func fallbackReranker(fallback Reranker) Reranker {
	if fallback == nil {
		return DeterministicReranker{}
	}

	return fallback
}

func rerankRequestWithCandidateSet(req RerankRequest, candidates []Candidate) RerankRequest {
	req.Candidates = candidates
	req.Options.MaxCandidates = 0
	return req
}

func getEffectiveLLMMaxCandidates(optionsMax int, requestMax int) (int, error) {
	if optionsMax < 0 || requestMax < 0 {
		return 0, errors.New("max candidates must be greater than or equal to zero")
	}
	if requestMax == 0 || optionsMax <= requestMax {
		return optionsMax, nil
	}

	return requestMax, nil
}

type llmRerankPayload struct {
	Query      string               `json:"query"`
	Caller     string               `json:"caller,omitempty"`
	TraceID    string               `json:"trace_id,omitempty"`
	SourceKind string               `json:"source_kind,omitempty"`
	Candidates []llmRerankCandidate `json:"candidates"`
}

type llmRerankCandidate struct {
	ID           string  `json:"id"`
	SourceKind   string  `json:"source_kind"`
	Text         string  `json:"text"`
	LexicalScore float64 `json:"lexical_score"`
	VectorScore  float64 `json:"vector_score"`
	FusedScore   float64 `json:"fused_score"`
}

type llmRerankResponse struct {
	Items []llmRerankItem `json:"items"`
}

type llmRerankItem struct {
	CandidateID string  `json:"candidate_id"`
	Score       float64 `json:"score"`
}

func candidatesToLLMRerankCandidates(candidates []Candidate, maxTextChars int) []llmRerankCandidate {
	items := make([]llmRerankCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, llmRerankCandidate{
			ID:           candidate.ID,
			SourceKind:   string(candidate.SourceKind),
			Text:         truncateString(candidate.Text, maxTextChars),
			LexicalScore: candidate.LexicalScore,
			VectorScore:  candidate.VectorScore,
			FusedScore:   candidate.FusedScore,
		})
	}

	return items
}

func parseLLMRerankResponse(resp *models.Response) (RerankResult, error) {
	if resp == nil {
		return RerankResult{}, errors.New("llm rerank response is required")
	}
	if resp.RequiresToolCalls {
		return RerankResult{}, errors.New("llm rerank requested tool calls")
	}

	outputTextValue := str.String(resp.OutputText)
	if outputTextValue.Trim() == "" {
		return RerankResult{}, errors.New("llm rerank response is empty")
	}

	var payload llmRerankResponse
	if err := json.Unmarshal([]byte(resp.OutputText), &payload); err != nil {
		return RerankResult{}, fmt.Errorf("parse llm rerank response: %w", err)
	}
	if len(payload.Items) == 0 {
		return RerankResult{}, errors.New("llm rerank response items are empty")
	}

	items := make([]RerankItem, 0, len(payload.Items))
	for _, item := range payload.Items {
		items = append(items, RerankItem(item))
	}

	return RerankResult{Items: items}, nil
}

func getLLMRerankerStructuredOutput() *models.StructuredOutput {
	return &models.StructuredOutput{
		Name:        "retrieval_rerank",
		Description: "Ranked retrieval candidate IDs.",
		Strict:      true,
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"items"},
			"properties": map[string]any{
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"candidate_id", "score"},
						"properties": map[string]any{
							"candidate_id": map[string]any{"type": "string"},
							"score":        map[string]any{"type": "number"},
						},
					},
				},
			},
		},
	}
}

func truncateString(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}

	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}

	return string(runes[:maxChars])
}
