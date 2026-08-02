package agent

import (
	"context"

	"github.com/xymorphic/morph/internal/environment"
	models "github.com/xymorphic/morph/internal/model"
	morphtools "github.com/xymorphic/morph/internal/tools"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
)

// ToolInvoker executes a model tool call against the prepared environment.
type ToolInvoker func(context.Context, environment.Environment, models.ToolCall) morphmsg.Message

// ToolRegistry adapts the environment tool registry into the core agent tool
// interface.
type ToolRegistry struct {
	env    environment.Environment
	invoke ToolInvoker
}

// NewToolRegistry returns a tool registry adapter backed by env.
func NewToolRegistry(env environment.Environment, invoke ToolInvoker) *ToolRegistry {
	return &ToolRegistry{env: env, invoke: invoke}
}

// Resolve loads model-visible tool definitions allowed by policy.
func (r *ToolRegistry) Resolve(policy agenttool.Policy) ([]agenttool.Definition, error) {
	if r == nil || r.env == nil || r.env.Tools() == nil {
		return nil, nil
	}

	if isEmptyToolPolicy(policy) {
		policy = ToolPolicyFromEnvironment(r.env)
	}

	definitions, err := r.env.Tools().Resolve(toolsPolicyFromAgentPolicy(policy))
	if err != nil {
		return nil, err
	}

	return agentDefinitionsFromToolsDefinitions(definitions), nil
}

// ListGroups returns the environment tool groups exposed to the agent layer.
func (r *ToolRegistry) ListGroups() []agenttool.Group {
	if r == nil || r.env == nil || r.env.Tools() == nil {
		return nil
	}

	groups := r.env.Tools().ListGroups()
	if len(groups) == 0 {
		return nil
	}

	result := make([]agenttool.Group, 0, len(groups))
	for _, group := range groups {
		result = append(result, agenttool.Group{
			Name:     group.Name,
			Tools:    append([]string(nil), group.Tools...),
			Includes: append([]string(nil), group.Includes...),
		})
	}

	return result
}

// Invoke executes a model tool call and returns the resulting tool message.
func (r *ToolRegistry) Invoke(ctx context.Context, call agenttool.Call) morphmsg.Message {
	if r == nil || r.invoke == nil {
		return morphmsg.Message{
			Role:       morphmsg.RoleTool,
			Name:       call.Name,
			ToolCallID: call.ID,
			Content:    `{"error":"tool invocation is required"}`,
		}
	}

	return r.invoke(ctx, r.env, agenttool.CallToModel(call))
}

// ToolPolicyFromEnvironment converts the active environment tool policy into
// the core agent policy shape.
func ToolPolicyFromEnvironment(env environment.Environment) agenttool.Policy {
	if env == nil {
		return agenttool.Policy{}
	}

	return agentPolicyFromToolsPolicy(env.ToolPolicy())
}

func agentPolicyFromToolsPolicy(policy morphtools.Policy) agenttool.Policy {
	return agenttool.Policy{
		GroupNames:   append([]string(nil), policy.GroupNames...),
		Capabilities: agentCapabilitiesFromToolsCapabilities(policy.Capabilities),
		Platform:     policy.Platform,
	}
}

func toolsPolicyFromAgentPolicy(policy agenttool.Policy) morphtools.Policy {
	return morphtools.Policy{
		GroupNames:   append([]string(nil), policy.GroupNames...),
		Capabilities: toolsCapabilitiesFromAgentCapabilities(policy.Capabilities),
		Platform:     policy.Platform,
	}
}

func agentDefinitionsFromToolsDefinitions(definitions morphtools.Definitions) []agenttool.Definition {
	if len(definitions) == 0 {
		return nil
	}

	result := make([]agenttool.Definition, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, agentDefinitionFromToolsDefinition(definition))
	}

	return result
}

func agentDefinitionFromToolsDefinition(definition morphtools.Definition) agenttool.Definition {
	return agenttool.Definition{
		Name:         definition.Name,
		Description:  definition.Description,
		InputSchema:  definition.InputSchema,
		ParallelSafe: definition.ParallelSafe,
		Groups:       append([]string(nil), definition.Groups...),
		Requires:     agentCapabilitiesFromToolsCapabilities(definition.Requires),
		Platforms:    append([]string(nil), definition.Platforms...),
	}
}

func agentCapabilitiesFromToolsCapabilities(capabilities morphtools.Capabilities) agenttool.Capabilities {
	return agenttool.Capabilities{
		Filesystem: capabilities.Filesystem,
		Network:    capabilities.Network,
		Exec:       capabilities.Exec,
		Browser:    capabilities.Browser,
		Memory:     capabilities.Memory,
	}
}

func toolsCapabilitiesFromAgentCapabilities(capabilities agenttool.Capabilities) morphtools.Capabilities {
	return morphtools.Capabilities{
		Filesystem: capabilities.Filesystem,
		Network:    capabilities.Network,
		Exec:       capabilities.Exec,
		Browser:    capabilities.Browser,
		Memory:     capabilities.Memory,
	}
}

func isEmptyToolPolicy(policy agenttool.Policy) bool {
	return len(policy.GroupNames) == 0 &&
		policy.Platform == "" &&
		!policy.Capabilities.Filesystem &&
		!policy.Capabilities.Network &&
		!policy.Capabilities.Exec &&
		!policy.Capabilities.Browser &&
		!policy.Capabilities.Memory
}
