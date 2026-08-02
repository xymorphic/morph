package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	appcredential "github.com/xymorphic/morph/internal/credential"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestSessionTitleHelpersNormalizeAndFallback(t *testing.T) {
	contextText, fallback := getSessionTitleContext([]morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: " "},
		{Role: morphmsg.RoleUser, Content: "  Help me fix parallel tools, please! "},
		{Role: morphmsg.RoleAssistant, Content: "Sure."},
	})

	require.Equal(t, "User: Help me fix parallel tools, please!\nAssistant: Sure.", contextText)
	require.Equal(t, "Help me fix parallel tools, please", fallback)
	require.Equal(t, "Specific Project", normalizeGeneratedSessionTitle("  `Specific   Project!`  "))
	require.Empty(t, normalizeGeneratedSessionTitle("Conversation about code"))
	require.Empty(t, normalizeGeneratedSessionTitle(""))
	require.Equal(t, "hello world", fallbackSessionTitleFromUserMessage("hello world!!!"))
	require.Equal(t, "one two three four five six seven eight", fallbackSessionTitleFromUserMessage("one two three four five six seven eight nine"))
	require.Empty(t, fallbackSessionTitleFromUserMessage("   "))
	require.True(t, hasBannedSessionTitleWord("A chat about tools"))
	require.False(t, hasBannedSessionTitleWord("Parallel tools"))
	require.Equal(t, "abc", trimTitleRunes("abcdef", 3))
	require.Empty(t, trimTitleRunes("abcdef", 0))
	require.True(t, isSameModelClient((*mocks.ModelClientStub)(nil), (*mocks.ModelClientStub)(nil)))
	require.False(t, isSameModelClient(nil, &mocks.ModelClientStub{}))
	require.False(t, isSameModelClient(&mocks.ModelClientStub{}, &nonComparableModelClient{}))
}

func TestAgent_MaybeGenerateSessionTitleUsesSummaryModel(t *testing.T) {
	store := &stateStoreStub{
		session: storage.Session{ID: "default"},
		messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "please help me test title generation"},
			{Role: morphmsg.RoleAssistant, Content: "I can help."},
		},
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)

	summaryClient := &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "Useful Title."}}}
	core := &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{
			Main:    config.MainModelConfig{Name: "main", API: models.APIOpenAIResponses},
			Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses},
		}},
		modelClient:   &mocks.ModelClientStub{},
		summaryClient: summaryClient,
		stateMgr:      manager,
	}

	core.maybeGenerateSessionTitle(context.Background(), "default")

	session, ok, err := store.Get(context.Background(), "default", storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Useful Title", session.Title)
	require.Equal(t, storage.SessionTitleSourceGenerated, session.TitleSource)
	require.Len(t, summaryClient.Requests, 1)
	require.Equal(t, int64(24), summaryClient.Requests[0].MaxOutputTokens)
}

func TestAgent_MaybeGenerateSessionTitleFallsBackWhenModelTitleInvalid(t *testing.T) {
	store := &stateStoreStub{
		session: storage.Session{ID: "default"},
		messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "fix memory flush before compaction please"},
		},
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)

	core := &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{
			Main:    config.MainModelConfig{Name: "main", API: models.APIOpenAIResponses},
			Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses},
		}},
		modelClient:   &mocks.ModelClientStub{},
		summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "Conversation"}}},
		stateMgr:      manager,
	}

	core.maybeGenerateSessionTitle(context.Background(), "default")

	session, ok, err := store.Get(context.Background(), "default", storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "fix memory flush before compaction please", session.Title)
}

func TestSessionTitleGenerationSkipsInvalidInputs(t *testing.T) {
	core := &Agent{}
	core.maybeGenerateSessionTitle(context.Background(), "default")
	core = &Agent{
		cfg:           &config.Config{},
		modelClient:   &mocks.ModelClientStub{},
		summaryClient: &mocks.ModelClientStub{},
	}
	core.maybeGenerateSessionTitle(context.Background(), "default")

	client := &mocks.ModelClientStub{Err: context.Canceled}
	core = &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{
			Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses},
		}},
		summaryClient: client,
	}
	require.Empty(t, core.generateSessionTitle(context.Background(), "context"))

	require.Empty(t, (&Agent{
		cfg:           &config.Config{},
		summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{nil}},
	}).generateSessionTitle(context.Background(), "context"))
	require.Empty(t, getSessionTitleContextStringFallbackOnly([]morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "hello"}}))
}

func TestAgent_MaybeGenerateSessionTitleSkipsExistingMissingAndMessageErrors(t *testing.T) {
	tests := []struct {
		name          string
		store         *stateStoreStub
		expectedTitle string
	}{
		{name: "existing title", store: &stateStoreStub{session: storage.Session{ID: "default", Title: "Manual"}}, expectedTitle: "Manual"},
		{name: "missing session", store: &stateStoreStub{}},
		{name: "get error", store: &stateStoreStub{session: storage.Session{ID: "default"}, getErr: context.Canceled}},
		{name: "messages error", store: &stateStoreStub{session: storage.Session{ID: "default"}, messagesErr: context.Canceled}},
		{name: "no messages", store: &stateStoreStub{session: storage.Session{ID: "default"}}},
		{name: "no user", store: &stateStoreStub{session: storage.Session{ID: "default"}, messages: []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "hello"}}}},
		{name: "save error", store: &stateStoreStub{session: storage.Session{ID: "default"}, messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}, saveErr: context.Canceled}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, err := statemanager.NewManager(test.store, time.Hour, time.Hour)
			require.NoError(t, err)
			core := &Agent{
				cfg: &config.Config{Models: config.ModelsConfig{
					Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses},
				}},
				modelClient:   &mocks.ModelClientStub{},
				summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "Generated"}}},
				stateMgr:      manager,
			}

			core.maybeGenerateSessionTitle(context.Background(), "default")
			require.Equal(t, test.expectedTitle, test.store.session.Title)
		})
	}
}

func TestAgent_GenerateSessionTitleOmitsMaxOutputTokensForOpenAISubscription(t *testing.T) {
	setSessionTitleTestProfileHome(t, t.TempDir())
	require.NoError(t, appcredential.NewFileStore("").Set(constants.ModelProviderOpenAICodex, appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: "subscription-token",
	}))

	summaryClient := &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "Useful Title"}}}
	core := &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{
			Main: config.MainModelConfig{
				Name:     "gpt-5.4",
				Provider: constants.ModelProviderOpenAICodex,
				API:      models.APIOpenAIResponses,
			},
		}},
		summaryClient: summaryClient,
	}

	require.Equal(t, "Useful Title", core.generateSessionTitle(context.Background(), "context"))
	require.Len(t, summaryClient.Requests, 1)
	require.Zero(t, summaryClient.Requests[0].MaxOutputTokens)
}

func getSessionTitleContextStringFallbackOnly(messages []morphmsg.Message) string {
	_, fallback := getSessionTitleContext(messages)
	return fallback
}

func setSessionTitleTestProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}

type nonComparableModelClient struct {
	_ []string
}

func (c *nonComparableModelClient) Complete(context.Context, models.Request) (*models.Response, error) {
	return nil, nil
}

func (c *nonComparableModelClient) CompleteStream(
	context.Context,
	models.Request,
	func(models.StreamDelta),
) (*models.Response, error) {
	return nil, nil
}
