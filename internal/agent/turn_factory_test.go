package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/environment"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentprompt "github.com/xymorphic/morph/pkg/agent/prompt"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
)

func TestTurnFactory_NewTurnWiresRuntimeDependencies(t *testing.T) {
	runtimeEnv := &mocks.EnvironmentStub{}
	manager, err := statemanager.NewManager(
		&stateStoreStub{session: storage.Session{ID: storage.DefaultSessionID}},
		time.Hour,
		time.Hour,
	)
	require.NoError(t, err)
	core := &Agent{
		cfg:           &config.Config{},
		modelClient:   &mocks.ModelClientStub{},
		summaryClient: &mocks.ModelClientStub{},
		stateMgr:      manager,
		env:           runtimeEnv,
	}
	var capturedEnv any
	var capturedCall models.ToolCall

	turn := core.newTurn(runtimeEnv, func(
		_ context.Context,
		env environment.Environment,
		toolCall models.ToolCall,
	) morphmsg.Message {
		capturedEnv = env
		capturedCall = toolCall
		return toolExecutionTestMessage(toolCall, `{"ok":true}`)
	})

	require.Same(t, core.cfg, turn.cfg)
	require.Same(t, core.modelClient, turn.modelClient)
	require.Same(t, core.summaryClient, turn.summaryClient)
	session, err := turn.sessionStore.Resolve(context.Background(), agentsession.DefaultID)
	require.NoError(t, err)
	require.Equal(t, agentsession.DefaultID, session.ID)
	definitions, err := turn.toolRegistry.Resolve(agenttool.Policy{})
	require.NoError(t, err)
	require.Nil(t, definitions)
	instructions, err := turn.promptProvider.LoadBaseInstructions(
		context.Background(),
		agentprompt.RunContext{SessionID: agentsession.DefaultID},
	)
	require.NoError(t, err)
	require.Empty(t, instructions)

	message := turn.invokeTool(context.Background(), models.ToolCall{ID: "call", Name: "time", Input: "{}"})
	require.Equal(t, `{"ok":true}`, message.Content)
	require.Same(t, runtimeEnv, capturedEnv)
	require.Equal(t, models.ToolCall{ID: "call", Name: "time", Input: "{}"}, capturedCall)

	message = turn.toolRegistry.Invoke(context.Background(), agenttool.Call{ID: "call-2", Name: "time", Input: "{}"})
	require.Equal(t, `{"ok":true}`, message.Content)
	require.Equal(t, models.ToolCall{ID: "call-2", Name: "time", Input: "{}"}, capturedCall)

	defaultTurn := core.newTurn(runtimeEnv, nil)
	message = defaultTurn.invokeTool(context.Background(), models.ToolCall{ID: "call", Name: "time", Input: "{}"})
	require.JSONEq(t, `{"name":"time","error":"tool registry is required"}`, message.Content)
}
