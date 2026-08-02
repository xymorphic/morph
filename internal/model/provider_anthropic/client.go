package provider_anthropic

import (
	"context"
	"errors"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/pkg/str"
)

// AnthropicClient sends normalized model requests through the Anthropic Messages API.
type AnthropicClient struct {
	createMessage       func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error)
	createMessageStream func(context.Context, anthropic.MessageNewParams) *ssestream.Stream[anthropic.MessageStreamEventUnion]
	subscriptionAuth    bool
}

// newAnthropicMessageCaller builds the SDK caller for non-streaming messages requests.
var newAnthropicMessageCaller = func(opts ...option.RequestOption) func(
	context.Context,
	anthropic.MessageNewParams,
) (*anthropic.Message, error) {
	client := anthropic.NewClient(opts...)
	return func(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
		return client.Messages.New(ctx, params)
	}
}

// newAnthropicMessageStreamCaller builds the SDK caller for streaming messages requests.
var newAnthropicMessageStreamCaller = func(opts ...option.RequestOption) func(
	context.Context,
	anthropic.MessageNewParams,
) *ssestream.Stream[anthropic.MessageStreamEventUnion] {
	client := anthropic.NewClient(opts...)
	return func(ctx context.Context, params anthropic.MessageNewParams) *ssestream.Stream[anthropic.MessageStreamEventUnion] {
		return client.Messages.NewStreaming(ctx, params)
	}
}

// NewAnthropicClient returns a client configured with the supplied API key and SDK options.
func NewAnthropicClient(apiKey string, opts ...option.RequestOption) (*AnthropicClient, error) {
	clientOptions := make([]option.RequestOption, 0, len(opts)+1)
	apiKeyValue := str.String(apiKey)
	if trimmed := apiKeyValue.Trim(); trimmed != "" {
		clientOptions = append(clientOptions, option.WithAPIKey(trimmed))
	}
	clientOptions = append(clientOptions, opts...)

	return &AnthropicClient{
		createMessage:       newAnthropicMessageCaller(clientOptions...),
		createMessageStream: newAnthropicMessageStreamCaller(clientOptions...),
	}, nil
}

// SetSubscriptionAuth marks this client as using Anthropic subscription OAuth.
func (c *AnthropicClient) SetSubscriptionAuth(enabled bool) {
	if c == nil {
		return
	}

	c.subscriptionAuth = enabled
}

// SubscriptionAuthEnabled reports whether Anthropic subscription request shaping is enabled.
func (c *AnthropicClient) SubscriptionAuthEnabled() bool {
	return c != nil && c.subscriptionAuth
}

// Complete sends a request to Anthropic Messages and returns the normalized response.
func (c *AnthropicClient) Complete(ctx context.Context, req Request) (*Response, error) {
	return c.complete(ctx, req, false, nil)
}

// CompleteStream sends a streaming request and reports text or reasoning deltas as they arrive.
func (c *AnthropicClient) CompleteStream(ctx context.Context, req Request, onTextDelta func(StreamDelta)) (*Response, error) {
	return c.complete(ctx, req, true, onTextDelta)
}

// complete normalizes a provider-neutral request and dispatches it to Anthropic Messages.
func (c *AnthropicClient) complete(
	ctx context.Context,
	req Request,
	stream bool,
	onTextDelta func(StreamDelta),
) (resp *Response, err error) {
	if c == nil {
		return nil, errors.New("model client is required")
	}

	req.API = models.APIAnthropicMessages
	normalizedReq, err := normalizeGenerateRequest(req)
	if err != nil {
		return nil, err
	}
	normalizedReq.SubscriptionAuth = c.subscriptionAuth

	logModelClientRequestStarted(normalizedReq, stream)
	defer func() {
		if err != nil {
			logModelClientRequestFailed(normalizedReq, stream, err)
			return
		}

		logModelClientRequestCompleted(normalizedReq, stream, resp)
	}()

	params, err := buildMessagesRequest(normalizedReq)
	if err != nil {
		return nil, err
	}
	if normalizedReq.DebugRequests {
		logRequestDebugMetadata(normalizedReq)
	}

	if stream {
		if c.createMessageStream == nil {
			return nil, errors.New("model client is required")
		}

		return c.completeMessageStream(ctx, params, onTextDelta)
	}

	if c.createMessage == nil {
		return nil, errors.New("model client is required")
	}
	providerResp, callErr := c.createMessage(ctx, params)
	if callErr != nil {
		return nil, callErr
	}
	if providerResp == nil {
		return nil, errors.New("model response is required")
	}

	return extractMessageResponse(providerResp)
}
