package agent

import (
	"context"

	"github.com/xymorphic/morph/internal/environment"
	instruct "github.com/xymorphic/morph/internal/instructions"
	agentprompt "github.com/xymorphic/morph/pkg/agent/prompt"
)

// PromptProvider adapts environment instructions into the core agent prompt
// provider interface.
type PromptProvider struct {
	env environment.Environment
}

// NewPromptProvider returns a prompt provider backed by env.
func NewPromptProvider(env environment.Environment) *PromptProvider {
	return &PromptProvider{env: env}
}

// LoadBaseInstructions returns static environment instructions for a model run.
func (p *PromptProvider) LoadBaseInstructions(
	context.Context,
	agentprompt.RunContext,
) (agentprompt.Instructions, error) {
	if p == nil || p.env == nil {
		return nil, nil
	}

	return promptInstructionsFromInstructions(p.env.Instructions()), nil
}

// BuildEnvironmentInstruction is reserved for dynamic environment prompt
// material. Host currently supplies environment context through base
// instructions and turn assembly, so this returns an empty instruction.
func (p *PromptProvider) BuildEnvironmentInstruction(
	context.Context,
	agentprompt.EnvironmentInput,
) (agentprompt.Instruction, error) {
	return agentprompt.Instruction{}, nil
}

func promptInstructionsFromInstructions(instructions instruct.Instructions) agentprompt.Instructions {
	if len(instructions) == 0 {
		return nil
	}

	result := make(agentprompt.Instructions, 0, len(instructions))
	for _, instruction := range instructions {
		result = append(result, agentprompt.Instruction{
			Name:  instruction.Name,
			Value: instruction.Value,
		})
	}

	return result
}
