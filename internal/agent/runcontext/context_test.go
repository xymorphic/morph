package runcontext

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestNewParent_DefaultsPublicAndEffectiveSession(t *testing.T) {
	runCtx, err := NewParent("")

	require.NoError(t, err)
	require.Equal(t, DefaultSessionID, runCtx.Session.PublicID)
	require.Equal(t, DefaultSessionID, runCtx.Session.EffectiveID)
	require.Equal(t, DefaultSessionID, runCtx.StateSessionID())
}

func TestNewChild_KeepsPublicSessionAndUsesChildStateSession(t *testing.T) {
	parentID := nanoid.MustFromSeed(SessionIDPrefix, "parent", "RunContextTestSeed")
	childID := nanoid.MustFromSeed(SessionIDPrefix, "child", "RunContextTestSeed")
	spawnedAt := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)

	parent, err := NewParent(parentID)
	require.NoError(t, err)
	child, err := parent.NewChild(ChildOptions{
		ChildSessionID:  childID,
		RunID:           "run_1",
		PersonalityName: "researcher",
		StateMode:       StateModeIsolated,
		ProfileName:     "work",
		SpawnedAt:       spawnedAt,
	})

	require.NoError(t, err)
	require.Equal(t, parentID, child.Session.PublicID)
	require.Equal(t, childID, child.Session.EffectiveID)
	require.Equal(t, parentID, child.Lineage.ParentSessionID)
	require.Equal(t, childID, child.Lineage.ChildSessionID)
	require.Equal(t, "run_1", child.Lineage.RunID)
	require.Equal(t, "researcher", child.Personality.Name)
	require.Equal(t, StateModeIsolated, child.State.Mode)
	require.Equal(t, "work", child.ProfileName)
	require.Equal(t, spawnedAt, child.Lineage.SpawnedAt)
}

func TestNewChild_RequiresChildSession(t *testing.T) {
	parent, err := NewParent(DefaultSessionID)
	require.NoError(t, err)

	_, err = parent.NewChild(ChildOptions{})
	require.ErrorContains(t, err, "child session id is required")
}

func TestNewChild_RejectsInvalidParent(t *testing.T) {
	childID := nanoid.MustFromSeed(SessionIDPrefix, "child", "InvalidParentSeed")

	_, err := (Context{Session: Session{PublicID: "session-1"}}).NewChild(ChildOptions{
		ChildSessionID: childID,
	})

	require.ErrorContains(t, err, "session id must be a valid ses_ nanoid")
}

func TestNewChild_InheritsParentDefaults(t *testing.T) {
	parentID := nanoid.MustFromSeed(SessionIDPrefix, "parent", "ChildDefaultsSeed")
	childID := nanoid.MustFromSeed(SessionIDPrefix, "child", "ChildDefaultsSeed")
	parent, err := (Context{
		ProfileName: "work",
		Session: Session{
			PublicID: parentID,
		},
	}).NewPersonality(PersonalityOptions{
		PersonalityName: "researcher",
	})
	require.NoError(t, err)

	child, err := parent.NewChild(ChildOptions{ChildSessionID: childID})

	require.NoError(t, err)
	require.Equal(t, "work", child.ProfileName)
	require.Equal(t, "researcher", child.Personality.Name)
	require.Equal(t, StateModeShared, child.State.Mode)
}

func TestNewPersonality_KeepsPublicAndEffectiveSession(t *testing.T) {
	sessionID := nanoid.MustFromSeed(SessionIDPrefix, "named", "RunContextTestSeed")
	parent, err := NewParent(sessionID)
	require.NoError(t, err)

	runCtx, err := parent.NewPersonality(PersonalityOptions{
		PersonalityName: "researcher",
		StateMode:       StateModeReadonly,
		ProfileName:     "work",
	})

	require.NoError(t, err)
	require.Equal(t, sessionID, runCtx.Session.PublicID)
	require.Equal(t, sessionID, runCtx.Session.EffectiveID)
	require.Equal(t, "researcher", runCtx.Personality.Name)
	require.Equal(t, StateModeReadonly, runCtx.State.Mode)
	require.Equal(t, "work", runCtx.ProfileName)
	require.Empty(t, runCtx.Lineage.ParentSessionID)
}

func TestNewPersonality_RequiresName(t *testing.T) {
	parent, err := NewParent(DefaultSessionID)
	require.NoError(t, err)

	_, err = parent.NewPersonality(PersonalityOptions{})

	require.ErrorContains(t, err, "personality name is required")
}

func TestNewPersonality_RejectsInvalidParent(t *testing.T) {
	_, err := (Context{Session: Session{PublicID: "session-1"}}).NewPersonality(PersonalityOptions{
		PersonalityName: "researcher",
	})

	require.ErrorContains(t, err, "session id must be a valid ses_ nanoid")
}

func TestNewPersonality_InheritsParentProfile(t *testing.T) {
	sessionID := nanoid.MustFromSeed(SessionIDPrefix, "named", "PersonalityDefaultsSeed")
	parent := Context{
		ProfileName: "work",
		Session: Session{
			PublicID: sessionID,
		},
	}

	runCtx, err := parent.NewPersonality(PersonalityOptions{PersonalityName: "researcher"})

	require.NoError(t, err)
	require.Equal(t, "work", runCtx.ProfileName)
	require.Equal(t, StateModeShared, runCtx.State.Mode)
}

func TestContext_NormalizeRejectsInvalidPublicOrEffectiveSession(t *testing.T) {
	_, err := Context{Session: Session{PublicID: "session-1"}}.Normalize()
	require.ErrorContains(t, err, "session id must be a valid ses_ nanoid")

	_, err = Context{Session: Session{PublicID: DefaultSessionID, EffectiveID: "session-2"}}.Normalize()
	require.ErrorContains(t, err, "session id must be a valid ses_ nanoid")
}

func TestContext_NormalizeRejectsInvalidLineageSessions(t *testing.T) {
	_, err := Context{
		Session: Session{PublicID: DefaultSessionID},
		Lineage: Lineage{
			ParentSessionID: "session-1",
		},
	}.Normalize()
	require.ErrorContains(t, err, "session id must be a valid ses_ nanoid")

	_, err = Context{
		Session: Session{PublicID: DefaultSessionID},
		Lineage: Lineage{
			ChildSessionID: "session-2",
		},
	}.Normalize()
	require.ErrorContains(t, err, "session id must be a valid ses_ nanoid")
}

func TestContext_StateSessionIDFallsBackToPublicAndDefault(t *testing.T) {
	sessionID := nanoid.MustFromSeed(SessionIDPrefix, "public", "StateSessionSeed")

	require.Equal(t, sessionID, Context{Session: Session{PublicID: " " + sessionID + " "}}.StateSessionID())
	require.Equal(t, DefaultSessionID, Context{}.StateSessionID())
}

func TestFromContext_ReturnsNormalizedRunContext(t *testing.T) {
	ctx := WithContext(context.Background(), Context{Session: Session{PublicID: " default "}})
	runCtx, ok := FromContext(ctx)

	require.True(t, ok)
	require.Equal(t, DefaultSessionID, runCtx.Session.PublicID)
	require.Equal(t, DefaultSessionID, runCtx.Session.EffectiveID)
}

func TestFromContext_HandlesNilMissingAndInvalidContexts(t *testing.T) {
	var nilContext context.Context
	ctx := WithContext(nilContext, Context{Session: Session{PublicID: DefaultSessionID}})
	runCtx, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, DefaultSessionID, runCtx.StateSessionID())

	invalidCtx := WithContext(context.Background(), Context{Session: Session{PublicID: "session-1"}})
	_, ok = FromContext(invalidCtx)
	require.False(t, ok)

	_, ok = FromContext(nilContext)
	require.False(t, ok)

	_, ok = FromContext(context.Background())
	require.False(t, ok)

	_, ok = FromContext(context.WithValue(context.Background(), contextKey{}, Context{
		Session: Session{PublicID: "session-1"},
	}))
	require.False(t, ok)
}
