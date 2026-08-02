package tool

import (
	"context"

	"github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/agent/model"
)

type Registry interface {
	Resolve(Policy) ([]Definition, error)
	Invoke(context.Context, Call) message.Message
}

type Call struct {
	ID     string
	Name   string
	Input  string
	Source string
}

type Definition struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	ParallelSafe bool
	Groups       []string
	Requires     Capabilities
	Platforms    []string
}

type Group struct {
	Name     string
	Tools    []string
	Includes []string
}

type Policy struct {
	GroupNames   []string
	Capabilities Capabilities
	Platform     string
}

type Capabilities struct {
	Filesystem bool
	Network    bool
	Exec       bool
	Browser    bool
	Memory     bool
}

func DefinitionFromModel(definition model.ToolDefinition) Definition {
	return Definition{
		Name:         definition.Name,
		Description:  definition.Description,
		InputSchema:  definition.InputSchema,
		ParallelSafe: definition.ParallelSafe,
	}
}

func DefinitionToModel(definition Definition) model.ToolDefinition {
	return model.ToolDefinition{
		Name:         definition.Name,
		Description:  definition.Description,
		InputSchema:  definition.InputSchema,
		ParallelSafe: definition.ParallelSafe,
	}
}

func DefinitionsToModel(definitions []Definition) []model.ToolDefinition {
	if len(definitions) == 0 {
		return nil
	}

	modelDefinitions := make([]model.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		modelDefinitions = append(modelDefinitions, DefinitionToModel(definition))
	}

	return modelDefinitions
}

func CallFromModel(call model.ToolCall) Call {
	return Call{
		ID:     call.ID,
		Name:   call.Name,
		Input:  call.Input,
		Source: "model",
	}
}

func CallToModel(call Call) model.ToolCall {
	return model.ToolCall{
		ID:    call.ID,
		Name:  call.Name,
		Input: call.Input,
	}
}
