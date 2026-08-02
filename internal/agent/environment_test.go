package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/mocks"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
)

func TestEnvironment_NewEnvironmentFactoryCreatesUsableRuntime(t *testing.T) {
	env := NewEnvironment(context.Background(), &config.Config{
		Models: config.ModelsConfig{
			Main: config.MainModelConfig{
				Provider: "openai",
				Name:     "gpt-4o-mini",
				API:      modelprovider.APIOpenAIResponses,
			},
			Summary: config.SummaryModelConfig{
				Provider: "openai",
				Name:     "gpt-4o-mini",
				API:      modelprovider.APIOpenAIResponses,
			},
		},
	})
	manager, err := statemanager.NewManager(
		&stateStoreStub{session: storage.Session{ID: storage.DefaultSessionID}},
		time.Hour,
		time.Hour,
	)
	require.NoError(t, err)
	env.SetStateManager(manager)
	env.SetModelClient(&mocks.ModelClientStub{})

	require.NoError(t, env.Prepare())
	definitions, err := env.Tools().Resolve(env.ToolPolicy())
	require.NoError(t, err)
	require.Greater(t, len(definitions), 0)
	require.Empty(t, env.NewTraceSession("default").ID())
	require.Positive(t, env.NewIterationBudget().Remaining())
}
