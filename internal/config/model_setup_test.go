package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestModelSetupEmbeddingUpdates(t *testing.T) {
	require.Equal(t, []ConfigUpdate{
		{Path: "models.embedding.provider", Value: constants.ModelProviderOpenAI},
		{Path: "models.embedding.name", Value: constants.DefaultProfileEmbeddingModel},
		{Path: "models.embedding.api", Value: modelprovider.APIOpenAIEmbeddings},
		{Path: "models.embedding.baseUrl", Value: ""},
	}, ModelSetupEmbeddingUpdates(constants.ModelProviderOpenAI))

	require.Equal(t, []ConfigUpdate{
		{Path: "models.embedding.provider", Value: constants.ModelProviderOpenRouter},
		{Path: "models.embedding.name", Value: constants.DefaultProfileEmbeddingModel},
		{Path: "models.embedding.api", Value: modelprovider.APIOpenRouterEmbeddings},
		{Path: "models.embedding.baseUrl", Value: ""},
	}, ModelSetupEmbeddingUpdates(constants.ModelProviderOpenRouter))

	require.Equal(t, []ConfigUpdate{
		{Path: "models.embedding.provider", Value: constants.ModelProviderOllama},
		{Path: "models.embedding.name", Value: constants.DefaultOllamaEmbeddingModel},
		{Path: "models.embedding.api", Value: modelprovider.APIOllamaEmbeddings},
		{Path: "models.embedding.baseUrl", Value: "http://127.0.0.1:11434"},
	}, ModelSetupEmbeddingUpdates(constants.ModelProviderOllama, "http://127.0.0.1:11434"))

	require.Equal(t, []ConfigUpdate{
		{Path: "models.embedding.provider", Value: constants.ModelProviderOllama},
		{Path: "models.embedding.name", Value: constants.DefaultOllamaEmbeddingModel},
		{Path: "models.embedding.api", Value: modelprovider.APIOllamaEmbeddings},
	}, ModelSetupEmbeddingUpdates(constants.ModelProviderOllama))

	require.Equal(t, []ConfigUpdate{
		{Path: "search.vector.enabled", Value: "false"},
	}, ModelSetupEmbeddingUpdates(constants.ModelProviderOpenAICodex))
}
