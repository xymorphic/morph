package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wandxy/morph/internal/constants"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

const (
	defaultEmbeddingMaxInputsPerBatch = constants.DefaultEmbeddingMaxInputsPerBatch
	defaultEmbeddingMaxInputTextBytes = constants.DefaultEmbeddingMaxInputTextBytes
	defaultEmbeddingTimeout           = constants.DefaultEmbeddingTimeout
	defaultEmbeddingMaxRetries        = constants.DefaultEmbeddingMaxRetries
)

// EmbeddingProviderOptions controls embedding provider.
type EmbeddingProviderOptions struct {
	HTTPClient        *http.Client
	Provider          string
	API               string
	APIKey            string
	EndpointURL       string
	MaxInputsPerBatch int
	MaxInputTextBytes int
	Timeout           time.Duration
	MaxRetries        int
}

// EmbeddingProvider creates embeddings through the configured model client.
type EmbeddingProvider struct {
	client            *http.Client
	provider          string
	api               string
	registry          *modelprovider.Registry
	apiKey            string
	endpointURL       string
	maxInputsPerBatch int
	maxInputTextBytes int
	timeout           time.Duration
	maxRetries        int
}

// NewEmbeddingProvider returns an embedding provider selected from config.
func NewEmbeddingProvider(opts EmbeddingProviderOptions) (*EmbeddingProvider, error) {
	providerValue := str.String(opts.Provider)
	provider := providerValue.Normalized()
	if provider == "" {
		return nil, errors.New("embedding provider is required")
	}

	api := normalizeEmbeddingAPI(provider, opts.API)
	endpointURL := normalizeEmbeddingEndpointURL(api, opts.EndpointURL)
	if endpointURL == "" {
		return nil, errors.New("embedding endpoint URL is required")
	}
	aPIKeyValue := str.String(opts.APIKey)
	apiKey := aPIKeyValue.Trim()
	if apiKey == "" && api == modelprovider.APIOllamaEmbeddings {
		apiKey = constants.OllamaLocalAuthMarker
	}
	if apiKey == "" {
		return nil, errors.New("embedding API key is required")
	}

	maxInputs := opts.MaxInputsPerBatch
	if maxInputs <= 0 {
		maxInputs = defaultEmbeddingMaxInputsPerBatch
	}

	maxTextBytes := opts.MaxInputTextBytes
	if maxTextBytes <= 0 {
		maxTextBytes = defaultEmbeddingMaxInputTextBytes
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultEmbeddingTimeout
	}

	maxRetries := opts.MaxRetries
	if maxRetries < 0 {
		return nil, errors.New("embedding max retries must be non-negative")
	}
	if maxRetries == 0 {
		maxRetries = defaultEmbeddingMaxRetries
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	return &EmbeddingProvider{
		client:            client,
		provider:          provider,
		api:               api,
		registry:          modelprovider.DefaultRegistry(),
		apiKey:            apiKey,
		endpointURL:       endpointURL,
		maxInputsPerBatch: maxInputs,
		maxInputTextBytes: maxTextBytes,
		timeout:           timeout,
		maxRetries:        maxRetries,
	}, nil
}

func (p *EmbeddingProvider) Embed(ctx context.Context, req EmbeddingRequest) (EmbeddingResult, error) {
	if p == nil {
		return EmbeddingResult{}, errors.New("embedding provider is required")
	}
	if err := ValidateEmbeddingRequest(req); err != nil {
		return EmbeddingResult{}, err
	}
	if err := p.validateRequestLimits(req); err != nil {
		return EmbeddingResult{}, err
	}

	modelValue := str.String(req.Model)
	targetValue := str.String(req.Target)
	retrievalLog.Debug().
		Str("provider", p.provider).
		Str("embedding_model", modelValue.Trim()).
		Str("target", targetValue.Trim()).
		Str("source_kind", getEmbeddingRequestSourceKind(req.Inputs)).
		Str("input_id", getEmbeddingRequestSingleInputID(req.Inputs)).
		Int("input_count", len(req.Inputs)).
		Int("max_inputs_per_batch", p.maxInputsPerBatch).
		Msg("embedding provider request started")
	modelValue2 := str.String(req.Model)
	result := EmbeddingResult{
		Model: modelValue2.Trim(),
		Items: make([]Embedding, 0, len(req.Inputs)),
	}
	for start := 0; start < len(req.Inputs); start += p.maxInputsPerBatch {
		end := min(start+p.maxInputsPerBatch, len(req.Inputs))
		batchResult, err := p.embedBatch(ctx, req.Model, req.Inputs[start:end])
		if err != nil {
			modelValue3 := str.String(req.Model)
			targetValue2 := str.String(req.Target)
			retrievalLog.Debug().
				Str("error_kind", getEmbeddingProviderErrorKind(err)).
				Str("provider", p.provider).
				Str("embedding_model", modelValue3.Trim()).
				Str("target", targetValue2.Trim()).
				Str("source_kind", getEmbeddingRequestSourceKind(req.Inputs)).
				Int("input_count", len(req.Inputs)).
				Msg("embedding provider request failed")
			return EmbeddingResult{}, err
		}
		if result.Dimensions == 0 {
			result.Dimensions = batchResult.Dimensions
		} else if result.Dimensions != batchResult.Dimensions {
			err := errors.New("embedding dimensions changed between batches")
			modelValue4 := str.String(req.Model)
			targetValue3 := str.String(req.Target)
			retrievalLog.Debug().
				Str("error_kind", err.Error()).
				Str("provider", p.provider).
				Str("embedding_model", modelValue4.Trim()).
				Str("target", targetValue3.Trim()).
				Str("source_kind", getEmbeddingRequestSourceKind(req.Inputs)).
				Int("input_count", len(req.Inputs)).
				Msg("embedding provider request failed")
			return EmbeddingResult{}, err
		}
		result.Items = append(result.Items, batchResult.Items...)
	}
	targetValue4 := str.String(req.Target)
	retrievalLog.Debug().
		Str("provider", p.provider).
		Str("embedding_model", result.Model).
		Str("target", targetValue4.Trim()).
		Str("source_kind", getEmbeddingRequestSourceKind(req.Inputs)).
		Str("input_id", getEmbeddingRequestSingleInputID(req.Inputs)).
		Int("input_count", len(req.Inputs)).
		Int("embedding_count", len(result.Items)).
		Int("dimensions", result.Dimensions).
		Msg("embedding provider request completed")

	return result, nil
}

func (p *EmbeddingProvider) validateRequestLimits(req EmbeddingRequest) error {
	for _, input := range req.Inputs {
		if len([]byte(input.Text)) > p.maxInputTextBytes {
			return fmt.Errorf("embedding input %q exceeds %d bytes", input.ID, p.maxInputTextBytes)
		}
	}

	return nil
}

func (p *EmbeddingProvider) embedBatch(
	ctx context.Context,
	model string,
	inputs []EmbeddingInput,
) (EmbeddingResult, error) {
	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		modelValue5 := str.String(model)
		retrievalLog.Debug().
			Str("provider", p.provider).
			Str("embedding_model", modelValue5.Trim()).
			Str("source_kind", getEmbeddingRequestSourceKind(inputs)).
			Str("input_id", getEmbeddingRequestSingleInputID(inputs)).
			Int("input_count", len(inputs)).
			Int("attempt", attempt+1).
			Msg("embedding provider batch started")

		result, retry, err := p.embedBatchAttempt(ctx, model, inputs)
		if err == nil {
			modelValue6 := str.String(result.Model)
			retrievalLog.Debug().
				Str("provider", p.provider).
				Str("embedding_model", modelValue6.Trim()).
				Str("source_kind", getEmbeddingRequestSourceKind(inputs)).
				Str("input_id", getEmbeddingRequestSingleInputID(inputs)).
				Int("input_count", len(inputs)).
				Int("embedding_count", len(result.Items)).
				Int("dimensions", result.Dimensions).
				Int("attempt", attempt+1).
				Msg("embedding provider batch completed")
			return result, nil
		}
		lastErr = err
		modelValue7 := str.String(model)
		retrievalLog.Debug().
			Bool("retry", retry && attempt < p.maxRetries).
			Str("error_kind", getEmbeddingProviderErrorKind(err)).
			Str("provider", p.provider).
			Str("embedding_model", modelValue7.Trim()).
			Str("source_kind", getEmbeddingRequestSourceKind(inputs)).
			Str("input_id", getEmbeddingRequestSingleInputID(inputs)).
			Int("input_count", len(inputs)).
			Int("attempt", attempt+1).
			Msg("embedding provider batch failed")
		if !retry || attempt == p.maxRetries {
			break
		}
	}

	return EmbeddingResult{}, lastErr
}

func (p *EmbeddingProvider) embedBatchAttempt(
	ctx context.Context,
	model string,
	inputs []EmbeddingInput,
) (EmbeddingResult, bool, error) {
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	if p.api == modelprovider.APIOllamaEmbeddings {
		return p.embedOllamaBatchAttempt(ctx, model, inputs)
	}

	payload := embeddingProviderRequest{
		Model:          p.getEmbeddingProviderModelID(model),
		Input:          getEmbeddingTexts(inputs),
		EncodingFormat: "float",
	}
	body, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpointURL, bytes.NewReader(body))
	if err != nil {
		return EmbeddingResult{}, false, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	if p.shouldSendAuthorization() {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return EmbeddingResult{}, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := getProviderErrorMessage(resp)
		return EmbeddingResult{}, retryableEmbeddingStatus(resp.StatusCode),
			fmt.Errorf("embedding request failed: %s", message)
	}

	var decoded embeddingProviderResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return EmbeddingResult{}, false, err
	}
	modelValue8 := str.String(model)
	result, err := openAIEmbeddingResponseToEmbeddingResult(modelValue8.Trim(), inputs, decoded)
	if err != nil {
		return EmbeddingResult{}, false, err
	}

	return result, false, nil
}

func (p *EmbeddingProvider) embedOllamaBatchAttempt(
	ctx context.Context,
	model string,
	inputs []EmbeddingInput,
) (EmbeddingResult, bool, error) {
	items := make([]Embedding, 0, len(inputs))
	dimensions := 0
	for _, input := range inputs {
		vector, retry, err := p.embedOllamaInput(ctx, model, input)
		if err != nil {
			return EmbeddingResult{}, retry, err
		}
		if dimensions == 0 {
			dimensions = len(vector)
		} else if dimensions != len(vector) {
			return EmbeddingResult{}, false, errors.New("embedding vector dimensions do not match result dimensions")
		}
		items = append(items, Embedding{
			ID:          input.ID,
			ContentHash: VectorContentHash(input.Text),
			Vector:      vector,
		})
	}
	modelValue9 := str.String(model)
	return EmbeddingResult{
		Model:      modelValue9.Trim(),
		Items:      items,
		Dimensions: dimensions,
	}, false, nil
}

func (p *EmbeddingProvider) embedOllamaInput(
	ctx context.Context,
	model string,
	input EmbeddingInput,
) ([]float64, bool, error) {
	payload := ollamaEmbeddingProviderRequest{
		Model:  p.getEmbeddingProviderModelID(model),
		Prompt: input.Text,
	}
	body, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	if p.shouldSendAuthorization() {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := getProviderErrorMessage(resp)
		return nil, retryableEmbeddingStatus(resp.StatusCode),
			fmt.Errorf("embedding request failed: %s", message)
	}

	var decoded ollamaEmbeddingProviderResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, false, err
	}
	if len(decoded.Embedding) == 0 {
		return nil, false, errors.New("embedding vector is required")
	}

	return append([]float64(nil), decoded.Embedding...), false, nil
}

func openAIEmbeddingResponseToEmbeddingResult(
	model string,
	inputs []EmbeddingInput,
	response embeddingProviderResponse,
) (EmbeddingResult, error) {
	modelValue10 := str.String(response.Model)
	if modelValue10.Trim() == "" {
		return EmbeddingResult{}, errors.New("embedding result model is required")
	}
	if !checkEmbeddingModelNamesMatch(model, response.Model) {
		return EmbeddingResult{}, errors.New("embedding result model must match request model")
	}
	if len(response.Data) != len(inputs) {
		return EmbeddingResult{}, errors.New("embedding result count must match input count")
	}

	seen := make(map[int]struct{}, len(response.Data))
	items := make([]Embedding, len(inputs))
	dimensions := 0
	for _, item := range response.Data {
		if item.Index < 0 || item.Index >= len(inputs) {
			return EmbeddingResult{}, fmt.Errorf("embedding response index %d is out of range", item.Index)
		}
		if _, ok := seen[item.Index]; ok {
			return EmbeddingResult{}, fmt.Errorf("embedding response index %d is duplicated", item.Index)
		}

		seen[item.Index] = struct{}{}
		if len(item.Embedding) == 0 {
			return EmbeddingResult{}, errors.New("embedding vector is required")
		}
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		} else if dimensions != len(item.Embedding) {
			return EmbeddingResult{}, errors.New("embedding vector dimensions do not match result dimensions")
		}

		input := inputs[item.Index]
		items[item.Index] = Embedding{
			ID:          input.ID,
			ContentHash: VectorContentHash(input.Text),
			Vector:      append([]float64(nil), item.Embedding...),
		}
	}
	modelValue11 := str.String(model)
	return EmbeddingResult{
		Model:      modelValue11.Trim(),
		Items:      items,
		Dimensions: dimensions,
	}, nil
}

func checkEmbeddingModelNamesMatch(requested string, returned string) bool {
	requestedValue := str.String(requested)
	requested = requestedValue.Trim()
	returnedValue := str.String(returned)
	returned = returnedValue.Trim()
	if requested == returned {
		return true
	}

	return trimModelProviderPrefix(requested) == trimModelProviderPrefix(returned)
}

func trimModelProviderPrefix(model string) string {
	modelValue12 := str.String(model)
	model = modelValue12.Trim()
	if _, suffix, ok := strings.Cut(model, "/"); ok {
		return suffix
	}

	return model
}

func (p *EmbeddingProvider) getEmbeddingProviderModelID(model string) string {
	modelValue13 := str.String(model)
	model = modelValue13.Trim()
	switch p.provider {
	case "openai":
		return strings.TrimPrefix(model, "openai/")
	case "openrouter":
		registry := p.registry
		if registry == nil {
			registry = modelprovider.DefaultRegistry()
		}
		modelDef, ok := registry.GetModelForAPI(p.provider, p.api, model)
		if !ok {
			return model
		}
		ownerValue := str.String(modelDef.Owner)
		if owner := ownerValue.Trim(); owner != "" && !strings.Contains(model, "/") {
			return owner + "/" + model
		}
	}

	return model
}

func (p *EmbeddingProvider) shouldSendAuthorization() bool {
	if p == nil {
		return false
	}
	if p.api != modelprovider.APIOllamaEmbeddings {
		apiKeyValue := str.String(p.apiKey)
		return apiKeyValue.Trim() != ""
	}

	apiKeyValue2 := str.String(p.apiKey)
	switch apiKeyValue2.Trim() {
	case constants.OllamaLocalAuthMarker, constants.LocalProviderAuthMarker, "":
		return false
	default:
		return true
	}
}

func normalizeEmbeddingAPI(provider string, api string) string {
	apiValue := str.String(api)
	api = apiValue.Normalized()
	if api != "" {
		return api
	}

	switch provider {
	case constants.ModelProviderOllama:
		return modelprovider.APIOllamaEmbeddings
	case constants.ModelProviderOpenRouter:
		return modelprovider.APIOpenRouterEmbeddings
	default:
		return modelprovider.APIOpenAIEmbeddings
	}
}

func normalizeEmbeddingEndpointURL(api string, value string) string {
	valueText := strings.TrimRight(str.String(value).Trim(), "/")
	if api != modelprovider.APIOllamaEmbeddings || valueText == "" {
		return valueText
	}

	lower := strings.ToLower(valueText)
	if strings.HasSuffix(lower, "/api/embeddings") {
		return valueText
	}
	if strings.HasSuffix(lower, "/v1") {
		valueText = strings.TrimRight(valueText[:len(valueText)-len("/v1")], "/")
	}

	return valueText + "/api/embeddings"
}

func getEmbeddingTexts(inputs []EmbeddingInput) []string {
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		values = append(values, input.Text)
	}

	return values
}

func getEmbeddingRequestSourceKind(inputs []EmbeddingInput) string {
	if len(inputs) == 0 {
		return ""
	}

	sourceKindValue := str.String(string(inputs[0].SourceKind))
	sourceKind := sourceKindValue.Trim()
	if sourceKind == "" {
		return ""
	}

	for _, input := range inputs[1:] {
		sourceKindValue2 := str.String(string(input.SourceKind))
		if sourceKindValue2.Trim() != sourceKind {
			return "mixed"
		}
	}

	return sourceKind
}

func getEmbeddingRequestSingleInputID(inputs []EmbeddingInput) string {
	if len(inputs) != 1 {
		return ""
	}

	iDValue := str.String(inputs[0].ID)
	return iDValue.Trim()
}

func getProviderErrorMessage(resp *http.Response) string {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	dataValue := str.String(string(data))
	message := dataValue.Trim()
	if message == "" {
		message = resp.Status
	}

	return message
}

func retryableEmbeddingStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func getEmbeddingProviderErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "embedding request failed"):
		return "provider_request_failed"
	case strings.Contains(value, "json"):
		return "decode_failed"
	case strings.Contains(value, "model"):
		return "model_mismatch"
	case strings.Contains(value, "dimensions"):
		return "dimension_mismatch"
	case strings.Contains(value, "timeout"):
		return "timeout"
	default:
		return "operation_failed"
	}
}

type embeddingProviderRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
}

type embeddingProviderResponse struct {
	Model string                          `json:"model"`
	Data  []embeddingProviderResponseData `json:"data"`
}

type embeddingProviderResponseData struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type ollamaEmbeddingProviderRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingProviderResponse struct {
	Embedding []float64 `json:"embedding"`
}

var _ Embedder = (*EmbeddingProvider)(nil)
