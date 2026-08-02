package client

import (
	"context"
	"fmt"
	"strings"

	agentapi "github.com/xymorphic/morph/internal/agent"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *ModelService) ListProviders(ctx context.Context) (ProviderList, error) {
	client, err := s.getClient()
	if err != nil {
		return ProviderList{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.ListProviders(ctx, &morphpb.ListProvidersRequest{})
	if err != nil {
		return ProviderList{}, err
	}

	providers := make([]ProviderOption, 0, len(resp.GetProviders()))
	for _, provider := range resp.GetProviders() {
		providers = append(providers, protoProviderOptionToProviderOption(provider))
	}

	return ProviderList{Providers: providers}, nil
}

func (s *ModelService) RuntimeModel(ctx context.Context) (ModelRuntime, error) {
	client, err := s.getClient()
	if err != nil {
		return ModelRuntime{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.RuntimeModel(ctx, &morphpb.RuntimeModelRequest{})
	if err != nil {
		return ModelRuntime{}, err
	}

	return protoRuntimeModelToModelRuntime(resp), nil
}

func (s *ModelService) ListModels(ctx context.Context, opts ...ModelListOptions) (ModelList, error) {
	client, err := s.getClient()
	if err != nil {
		return ModelList{}, err
	}

	listOpts := getModelListOptions(opts...)
	prepareRPCConnection(s.reconnector)
	providerValue2 := str.String(listOpts.Provider)
	resp, err := client.ListModels(ctx, &morphpb.ListModelsRequest{Provider: providerValue2.Trim()})
	if err != nil {
		return ModelList{}, err
	}

	models := make([]ModelOption, 0, len(resp.GetModels()))
	for _, model := range resp.GetModels() {
		models = append(models, protoModelOptionToModelOption(model))
	}
	provider := str.String(resp.GetProvider())
	authType := str.String(resp.GetAuthType())
	return ModelList{
		Provider: provider.Trim(),
		AuthType: authType.Trim(),
		Models:   models,
	}, nil
}

func getModelListOptions(opts ...ModelListOptions) ModelListOptions {
	if len(opts) == 0 {
		return ModelListOptions{}
	}

	return opts[0]
}

func getModelSelectOptions(opts ...agentapi.ModelSelectOptions) agentapi.ModelSelectOptions {
	if len(opts) == 0 {
		return agentapi.ModelSelectOptions{}
	}

	return opts[0]
}

func (s *ModelService) SelectModel(
	ctx context.Context,
	id string,
	opts ...agentapi.ModelSelectOptions,
) (ModelOption, error) {
	client, err := s.getClient()
	if err != nil {
		return ModelOption{}, err
	}

	selectOpts := getModelSelectOptions(opts...)
	prepareRPCConnection(s.reconnector)
	modelID := str.String(id)
	provider := str.String(selectOpts.Provider)
	api := str.String(selectOpts.API)
	resp, err := client.SelectModel(ctx, &morphpb.SelectModelRequest{
		Id:       modelID.Trim(),
		Provider: provider.Trim(),
		Api:      api.Trim(),
	})
	if err != nil {
		return ModelOption{}, err
	}

	return protoModelOptionToModelOption(resp.GetModel()), nil
}

func (s *ModelService) SetProviderAPIKey(ctx context.Context, provider string, apiKey string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}

	prepareRPCConnection(s.reconnector)
	providerValue := str.String(provider)
	apiKeyValue := str.String(apiKey)
	_, err = client.SetProviderAPIKey(ctx, &morphpb.SetProviderAPIKeyRequest{
		Provider: providerValue.Trim(),
		ApiKey:   apiKeyValue.Trim(),
	})
	return err
}

func (s *ModelService) getClient() (morphpb.ModelServiceClient, error) {
	if s != nil && s.client != nil {
		return s.client, nil
	}

	return nil, fmt.Errorf("morph: model service client is required")
}

func protoProviderOptionToProviderOption(option *morphpb.ProviderOption) ProviderOption {
	if option == nil {
		return ProviderOption{}
	}

	id := str.String(option.GetId())
	name := str.String(option.GetName())
	optionType := str.String(option.GetType())
	authType := str.String(option.GetAuthType())
	return ProviderOption{
		ID:             id.Trim(),
		Name:           name.Trim(),
		Type:           optionType.Trim(),
		ModelCount:     int(option.GetModelCount()),
		SupportsAPIKey: option.GetSupportsApiKey(),
		SupportsOAuth:  option.GetSupportsOauth(),
		AuthType:       authType.Trim(),
		Current:        option.GetCurrent(),
	}
}

func protoModelOptionToModelOption(option *morphpb.ModelOption) ModelOption {
	if option == nil {
		return ModelOption{}
	}

	id := str.String(option.GetId())
	name := str.String(option.GetName())
	provider := str.String(option.GetProvider())
	api := str.String(option.GetApi())
	return ModelOption{
		ID:            id.Trim(),
		Name:          name.Trim(),
		Provider:      provider.Trim(),
		API:           api.Trim(),
		ContextWindow: int(option.GetContextWindow()),
		MaxTokens:     int(option.GetMaxTokens()),
		Input:         append([]string(nil), option.GetInput()...),
		Reasoning:     option.GetReasoning(),
		SupportsOAuth: option.GetSupportsOauth(),
		Current:       option.GetCurrent(),
	}
}

func protoRuntimeModelToModelRuntime(runtime *morphpb.RuntimeModelResponse) ModelRuntime {
	if runtime == nil {
		return ModelRuntime{}
	}

	provider := str.String(runtime.GetProvider())
	api := str.String(runtime.GetApi())
	model := str.String(runtime.GetModel())
	baseURL := str.String(runtime.GetBaseUrl())
	return ModelRuntime{
		Provider:      provider.Trim(),
		API:           api.Trim(),
		Model:         model.Trim(),
		BaseURL:       strings.TrimRight(baseURL.Trim(), "/"),
		ContextLength: int(runtime.GetContextLength()),
	}
}
