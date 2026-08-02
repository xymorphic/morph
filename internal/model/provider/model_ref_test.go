package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/constants"
)

func TestParseLocalModelRef(t *testing.T) {
	ref, ok := ParseLocalModelRef(" Ollama / llama3.1:8b ")

	require.True(t, ok)
	require.Equal(t, ModelRef{Provider: constants.ModelProviderOllama, Model: "llama3.1:8b"}, ref)
	require.Equal(t, "ollama/llama3.1:8b", ref.String())
}

func TestModelRef_StringReturnsEmptyForIncompleteRef(t *testing.T) {
	require.Empty(t, ModelRef{Provider: constants.ModelProviderOllama}.String())
	require.Empty(t, ModelRef{Model: "llama3.1:8b"}.String())
}

func TestParseLocalModelRefRejectsInvalidRefs(t *testing.T) {
	for _, value := range []string{
		"",
		"llama3.1:8b",
		"ollama/",
		"/llama3.1:8b",
		"openai/gpt-4o",
	} {
		_, ok := ParseLocalModelRef(value)
		require.False(t, ok, value)
	}
}

func TestIsLocalProviderID(t *testing.T) {
	require.True(t, IsLocalProviderID("OLLAMA"))
	require.True(t, IsLocalProviderID(constants.ModelProviderVLLM))
	require.True(t, IsLocalProviderID(constants.ModelProviderSGLang))
	require.True(t, IsLocalProviderID(constants.ModelProviderCustomLocal))
	require.False(t, IsLocalProviderID(constants.ModelProviderOpenAI))
}
