package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	instruct "github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/mocks"
	agentprompt "github.com/xymorphic/morph/pkg/agent/prompt"
)

func TestPromptProvider_LoadBaseInstructionsConvertsEnvironmentInstructions(t *testing.T) {
	provider := NewPromptProvider(&mocks.EnvironmentStub{
		InstructionsList: instruct.Instructions{
			{Name: "one", Value: "first"},
			{Name: "two", Value: "second"},
		},
	})

	instructions, err := provider.LoadBaseInstructions(context.Background(), agentprompt.RunContext{})

	require.NoError(t, err)
	require.Equal(t, agentprompt.Instructions{
		{Name: "one", Value: "first"},
		{Name: "two", Value: "second"},
	}, instructions)
}

func TestPromptProvider_EmptyEnvironmentReturnsNoInstructions(t *testing.T) {
	instructions, err := (*PromptProvider)(nil).LoadBaseInstructions(context.Background(), agentprompt.RunContext{})

	require.NoError(t, err)
	require.Nil(t, instructions)
	require.Nil(t, promptInstructionsFromInstructions(nil))

	empty, err := NewPromptProvider(nil).BuildEnvironmentInstruction(context.Background(), agentprompt.EnvironmentInput{})
	require.NoError(t, err)
	require.Equal(t, agentprompt.Instruction{}, empty)
}
