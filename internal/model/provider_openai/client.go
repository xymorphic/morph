package provider_openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"

	models "github.com/wandxy/morph/internal/model"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

// OpenAIClient sends normalized model requests through OpenAI-compatible APIs.
type OpenAIClient struct {
	provider             string
	registry             *modelprovider.Registry
	api                  string
	forceResponsesStream bool
	createChatCompletion func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
	createChatStream     func(context.Context, openai.ChatCompletionNewParams) *ssestream.Stream[openai.ChatCompletionChunk]
	createResponse       func(context.Context, responses.ResponseNewParams) (*responses.Response, error)
	createResponseStream func(context.Context, responses.ResponseNewParams) *ssestream.Stream[responses.ResponseStreamEventUnion]
}

// openAIAPIHandler converts and dispatches one supported OpenAI-compatible API shape.
type openAIAPIHandler interface {
	Complete(context.Context, *OpenAIClient, normalizedGenerateRequest, bool, func(StreamDelta)) (*Response, error)
}

// newOpenAICompletionCaller builds the SDK caller for non-streaming chat completions requests.
var newOpenAICompletionCaller = func(opts ...option.RequestOption) func(
	context.Context,
	openai.ChatCompletionNewParams,
) (*openai.ChatCompletion, error) {
	client := openai.NewClient(opts...)
	return func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return client.Chat.Completions.New(ctx, params)
	}
}

// newOpenAIResponseCaller builds the SDK caller for non-streaming responses requests.
var newOpenAIResponseCaller = func(opts ...option.RequestOption) func(
	context.Context,
	responses.ResponseNewParams,
) (*responses.Response, error) {
	client := openai.NewClient(opts...)
	return func(ctx context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
		return client.Responses.New(ctx, params)
	}
}

// newOpenAICompletionStreamCaller builds the SDK caller for streaming chat completions requests.
var newOpenAICompletionStreamCaller = func(opts ...option.RequestOption) func(
	context.Context,
	openai.ChatCompletionNewParams,
) *ssestream.Stream[openai.ChatCompletionChunk] {
	client := openai.NewClient(opts...)
	return func(ctx context.Context, params openai.ChatCompletionNewParams) *ssestream.Stream[openai.ChatCompletionChunk] {
		return client.Chat.Completions.NewStreaming(ctx, params)
	}
}

// newOpenAIResponseStreamCaller builds the SDK caller for streaming responses requests.
var newOpenAIResponseStreamCaller = func(opts ...option.RequestOption) func(
	context.Context,
	responses.ResponseNewParams,
) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	client := openai.NewClient(opts...)
	return func(ctx context.Context, params responses.ResponseNewParams) *ssestream.Stream[responses.ResponseStreamEventUnion] {
		return client.Responses.NewStreaming(ctx, params)
	}
}

// NewOpenAIClient returns a client configured with the supplied API route, API key, and SDK options.
func NewOpenAIClient(apiKey, api string, opts ...option.RequestOption) (*OpenAIClient, error) {
	return NewOpenAIProviderClient(apiKey, api, "openai", nil, opts...)
}

// NewOpenAIProviderClient returns an OpenAI-compatible client for a concrete provider.
func NewOpenAIProviderClient(
	apiKey string,
	api string,
	provider string,
	registry *modelprovider.Registry,
	opts ...option.RequestOption,
) (*OpenAIClient, error) {
	normalizedAPI, err := normalizeRequestAPI(api)
	if err != nil {
		return nil, err
	}

	clientOptions := make([]option.RequestOption, 0, len(opts)+1)
	apiKeyValue := str.String(apiKey)
	if trimmed := apiKeyValue.Trim(); trimmed != "" {
		clientOptions = append(clientOptions, option.WithAPIKey(trimmed))
	}
	clientOptions = append(clientOptions, opts...)

	return &OpenAIClient{
		provider:             normalizeProvider(provider),
		registry:             registry,
		api:                  normalizedAPI,
		createChatCompletion: newOpenAICompletionCaller(clientOptions...),
		createChatStream:     newOpenAICompletionStreamCaller(clientOptions...),
		createResponse:       newOpenAIResponseCaller(clientOptions...),
		createResponseStream: newOpenAIResponseStreamCaller(clientOptions...),
	}, nil
}

// Complete sends a request to the configured OpenAI-compatible API and returns the normalized response.
func (c *OpenAIClient) Complete(ctx context.Context, req Request) (*Response, error) {
	return c.complete(ctx, req, false, nil)
}

// CompleteStream sends a streaming request and reports text or reasoning deltas as they arrive.
func (c *OpenAIClient) CompleteStream(ctx context.Context, req Request, onTextDelta func(StreamDelta)) (*Response, error) {
	return c.complete(ctx, req, true, onTextDelta)
}

// SetForceResponsesStream makes Responses Complete calls use the streaming transport.
func (c *OpenAIClient) SetForceResponsesStream(enabled bool) {
	if c != nil {
		c.forceResponsesStream = enabled
	}
}

// ForceResponsesStreamEnabled reports whether Responses Complete calls use the streaming transport.
func (c *OpenAIClient) ForceResponsesStreamEnabled() bool {
	return c != nil && c.forceResponsesStream
}

// complete normalizes a provider-neutral request and routes it to the selected API handler.
func (c *OpenAIClient) complete(
	ctx context.Context,
	req Request,
	stream bool,
	onTextDelta func(StreamDelta),
) (resp *Response, err error) {
	if c == nil {
		return nil, errors.New("model client is required")
	}

	req.API = c.api
	req.Model = c.getProviderModelID(req.Model)

	normalizedReq, err := normalizeGenerateRequest(req)
	if err != nil {
		return nil, err
	}

	stream = stream || normalizedReq.API == models.APIOpenAIResponses && c.forceResponsesStream
	logModelClientRequestStarted(normalizedReq, stream)

	defer func() {
		if err != nil {
			logModelClientRequestFailed(normalizedReq, stream, err)
			err = enrichModelClientError(err)
			return
		}

		logModelClientRequestCompleted(normalizedReq, stream, resp)
	}()

	var handler openAIAPIHandler = responsesHandler{}
	if normalizedReq.API == models.APIOpenAICompletions {
		handler = chatCompletionsHandler{}
	}

	return handler.Complete(ctx, c, normalizedReq, stream, onTextDelta)
}

func enrichModelClientError(err error) error {
	detail := getModelClientProviderErrorDetail(err)
	if detail == "" || strings.Contains(err.Error(), detail) {
		return err
	}

	return fmt.Errorf("%w: %s", err, detail)
}

func getModelClientProviderErrorDetail(err error) string {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || apiErr == nil {
		return ""
	}
	rawJSONValue := str.String(apiErr.RawJSON())
	if raw := rawJSONValue.Trim(); raw != "" {
		return truncateProviderErrorDetail(raw)
	}
	if body := readOpenAIErrorResponseBody(apiErr); body != "" {
		return truncateProviderErrorDetail(body)
	}
	messageValue := str.String(apiErr.Message)
	return truncateProviderErrorDetail(messageValue.Trim())
}

func readOpenAIErrorResponseBody(apiErr *openai.Error) string {
	if apiErr == nil || apiErr.Response == nil || apiErr.Response.Body == nil {
		return ""
	}

	body, err := io.ReadAll(apiErr.Response.Body)
	apiErr.Response.Body = io.NopCloser(bytes.NewBuffer(body))
	if err != nil {
		return ""
	}
	bodyValue := str.String(string(body))
	return bodyValue.Trim()
}

func truncateProviderErrorDetail(detail string) string {
	const maxProviderErrorDetailChars = 2048
	detailValue := str.String(detail)
	detail = detailValue.Trim()
	if len(detail) <= maxProviderErrorDetailChars {
		return detail
	}

	return detail[:maxProviderErrorDetailChars] + "...[truncated]"
}

// getProviderModelID converts Morph's neutral model ID to the provider's routed ID.
func (c *OpenAIClient) getProviderModelID(model string) string {
	modelValue := str.String(model)
	model = modelValue.Trim()
	if normalizeProvider(c.provider) == "openai" {
		return strings.TrimPrefix(model, "openai/")
	}

	return model
}

func (c *OpenAIClient) getModelOwner(model string) string {
	if c == nil {
		return ""
	}

	modelDef, ok := c.registryOrDefault().GetModel(c.provider, model)
	if !ok {
		return ""
	}
	ownerValue := str.String(modelDef.Owner)
	return ownerValue.Trim()
}

func (c *OpenAIClient) isReasoningModel(model string) bool {
	if c == nil {
		return false
	}

	modelDef, ok := c.registryOrDefault().GetModel(c.provider, model)
	return ok && modelDef.Reasoning
}

func normalizeProvider(provider string) string {
	providerValue := str.String(provider)
	return providerValue.Normalized()
}

func (c *OpenAIClient) registryOrDefault() *modelprovider.Registry {
	if c == nil || c.registry == nil {
		return modelprovider.DefaultRegistry()
	}

	return c.registry
}
