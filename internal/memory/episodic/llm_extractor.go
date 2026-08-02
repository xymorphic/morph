package episodic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/xymorphic/morph/internal/instructions"
	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultLLMExtractorMaxOutputTokens int64 = 1600

// NewLLMExtractor validates the model dependency once at provider setup time so
// extraction calls can assume the model client and model name are available.
func NewLLMExtractor(options LLMExtractorOptions) (*LLMExtractor, error) {
	if options.Client == nil {
		return nil, errors.New("memory episode extractor model client is required")
	}

	modelValue := str.String(options.Model)
	if modelValue.Trim() == "" {
		return nil, errors.New("memory episode extractor model is required")
	}
	if options.MaxOutputTokensEnabled != nil && !*options.MaxOutputTokensEnabled {
		options.MaxOutputTokens = 0
	} else if options.MaxOutputTokens <= 0 {
		options.MaxOutputTokens = defaultLLMExtractorMaxOutputTokens
	}
	return &LLMExtractor{options: options}, nil
}

// ExtractCandidates sends one bounded source window to the model and returns
// proposals plus explicit model-side rejections. The service still owns
// deterministic admission, provenance, dedupe, and writes.
func (e *LLMExtractor) ExtractCandidates(ctx context.Context, req CandidateRequest) (CandidateResult, error) {
	if e == nil || e.options.Client == nil {
		return CandidateResult{}, errors.New("memory episode extractor model client is required")
	}

	// The request already contains trimmed message evidence and trace evidence.
	// Sending JSON keeps the prompt compact and makes debug payloads easier to
	// compare across runs.
	payload, _ := json.Marshal(req)
	resp, err := e.options.Client.Complete(ctx, models.Request{
		Model:            e.options.Model,
		API:              e.options.API,
		Instructions:     instructions.BuildEpisodicExtractionInstructions(),
		Messages:         []morphmsg.Message{{Role: morphmsg.RoleUser, Content: string(payload)}},
		StructuredOutput: getLLMExtractorStructuredOutput(),
		MaxOutputTokens:  e.options.MaxOutputTokens,
		DebugRequests:    e.options.DebugRequests,
	})
	if err != nil {
		return CandidateResult{}, err
	}
	return llmExtractorResponseToCandidateResult(resp)
}

type llmExtractorResponse struct {
	Candidates []llmExtractorCandidate `json:"candidates"`
	Rejections []candidateRejection    `json:"rejections"`
}
type llmExtractorCandidate struct {
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Text       string            `json:"text"`
	Confidence float64           `json:"confidence"`
	Metadata   map[string]string `json:"metadata"`
}

func llmExtractorResponseToCandidateResult(resp *models.Response) (CandidateResult, error) {
	if resp == nil {
		return CandidateResult{}, errors.New("memory episode extractor response is required")
	}

	var parsed llmExtractorResponse
	if err := json.Unmarshal([]byte(normalizeLLMExtractorJSON(resp.OutputText)), &parsed); err != nil {
		return CandidateResult{}, err
	}

	result := CandidateResult{Rejections: parsed.Rejections}
	for _, candidate := range parsed.Candidates {
		kindValue :=
			// Model candidates are normalized only enough for downstream provider
			// logic. IDs, source links, tags, and provenance are constructed by the
			// service from trusted window evidence.

			str.String(candidate.Kind)
		titleValue := str.String(candidate.Title)
		textValue := str.String(candidate.Text)
		result.Candidates = append(result.Candidates, episodeCandidate{
			Kind:       kindValue.Trim(),
			Title:      titleValue.Trim(),
			Text:       textValue.Trim(),
			Confidence: candidate.Confidence,
			Metadata:   candidate.Metadata,
		})
	}

	return result, nil
}

func normalizeLLMExtractorJSON(raw string) string {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if !strings.HasPrefix(raw, "```") {
		return raw
	}

	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	rawValue2 := str.String(raw)
	raw = strings.TrimSuffix(rawValue2.Trim(), "```")
	rawValue3 := str.String(raw)
	return rawValue3.Trim()
}

// getLLMExtractorStructuredOutput constrains the extractor to known candidate
// kinds and known metadata keys. Unknown metadata would make admission behavior
// harder to reason about, so the schema is strict.
func getLLMExtractorStructuredOutput() *models.StructuredOutput {
	return &models.StructuredOutput{
		Name:        "episodic_memory_candidates",
		Description: "Curated episodic memory candidates",
		Strict:      true,
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"candidates": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"kind": map[string]any{
								"type": "string",
								"enum": getEpisodeCandidateKinds(),
							},
							"title":      map[string]any{"type": "string"},
							"text":       map[string]any{"type": "string"},
							"confidence": map[string]any{"type": "number"},
							"metadata":   getLLMExtractorMetadataSchema(),
						},
						"required": []string{"kind", "title", "text", "confidence", "metadata"},
					},
				},
				"rejections": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"kind":   map[string]any{"type": "string"},
							"reason": map[string]any{"type": "string"},
						},
						"required": []string{"kind", "reason"},
					},
				},
			},
			"required": []string{"candidates", "rejections"},
		},
	}
}

func getLLMExtractorMetadataSchema() map[string]any {
	fields := getEpisodeMetadataFieldKeys()
	properties := make(map[string]any, len(fields))
	for _, field := range fields {
		properties[field] = map[string]any{"type": "string"}
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             fields,
	}
}
