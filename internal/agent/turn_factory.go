package agent

import (
	"context"

	"github.com/xymorphic/morph/internal/environment"
	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func (a *Agent) newTurn(
	runtimeEnv environment.Environment,
	invokeToolFn func(context.Context, environment.Environment, models.ToolCall) morphmsg.Message,
) *Turn {
	if invokeToolFn == nil {
		invokeToolFn = a.invokeToolWithEnvironment
	}

	sessionStore := NewSessionStore(a.stateMgr)
	toolRegistry := NewToolRegistry(runtimeEnv, func(
		ctx context.Context,
		env environment.Environment,
		toolCall models.ToolCall,
	) morphmsg.Message {
		return invokeToolFn(ctx, env, toolCall)
	})

	return NewTurnWithSessionStore(
		a.cfg,
		a.modelClient,
		a.summaryClient,
		a.stateMgr,
		sessionStore,
		sessionStore,
		toolRegistry,
		ToolPolicyFromEnvironment(runtimeEnv),
		NewPromptProvider(runtimeEnv),
		runtimeEnv,
		runtimeEnv,
		runtimeEnv,
		runtimeEnv,
		runtimeEnv,
		runtimeEnv,
		func(ctx context.Context, toolCall models.ToolCall) morphmsg.Message {
			return invokeToolFn(ctx, runtimeEnv, toolCall)
		},
	)
}
