package memory

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestApplyRunProvenance_AddsSessionMetadataAndSourceLinks(t *testing.T) {
	parentID := nanoid.MustFromSeed(runcontext.SessionIDPrefix, "parent", "MemoryLineageTestSeed")
	childID := nanoid.MustFromSeed(runcontext.SessionIDPrefix, "child", "MemoryLineageTestSeed")
	parent, err := runcontext.NewParent(parentID)
	require.NoError(t, err)
	child, err := parent.NewChild(runcontext.ChildOptions{
		ChildSessionID:  childID,
		RunID:           "run_memory",
		PersonalityName: "researcher",
		StateMode:       runcontext.StateModeReadonly,
		ProfileName:     "work",
	})
	require.NoError(t, err)

	item := ApplyRunProvenance(MemoryItem{
		Metadata:    map[string]string{"custom": "value"},
		SourceLinks: []SourceLink{{MessageIDs: []uint{1}}},
	}, child, "tool_write")

	require.Equal(t, "value", item.Metadata["custom"])
	require.Equal(t, parentID, item.Metadata[MemoryMetadataSourceSessionID])
	require.Equal(t, parentID, item.SourceLinks[0].SessionID)
	require.Equal(t, parentID, item.SourceLinks[0].ParentSessionID)
	require.Equal(t, childID, item.SourceLinks[0].ChildSessionID)
	require.Equal(t, "run_memory", item.SourceLinks[0].RunID)
	require.Equal(t, "researcher", item.SourceLinks[0].SourcePersonality)
	require.Equal(t, "tool_write", item.SourceLinks[0].SourceTrigger)
}

func TestApplyRunProvenance_AddsMissingSourceLinkAndStaysIdempotent(t *testing.T) {
	parentID := nanoid.MustFromSeed(runcontext.SessionIDPrefix, "parent", "MemoryMissingSourceLinkSeed")
	parent, err := runcontext.NewParent(parentID)
	require.NoError(t, err)

	item := ApplyRunProvenance(MemoryItem{}, parent, "tool_write")
	item = ApplyRunProvenance(item, parent, "tool_write")

	require.Equal(t, parentID, item.Metadata[MemoryMetadataSourceSessionID])
	require.Len(t, item.SourceLinks, 1)
	require.Equal(t, parentID, item.SourceLinks[0].SessionID)
	require.Equal(t, "tool_write", item.SourceLinks[0].SourceTrigger)
	require.True(t, HasSourceProvenance(item))
}

func TestApplyRunProvenance_SkipsInvalidRunContext(t *testing.T) {
	item := MemoryItem{Metadata: map[string]string{"existing": "value"}}

	result := ApplyRunProvenance(item, runcontext.Context{Session: runcontext.Session{PublicID: "session-1"}}, "tool_write")

	require.Equal(t, map[string]string{"existing": "value"}, result.Metadata)
}

func TestApplyRunProvenance_PreservesExistingSourceLinkFields(t *testing.T) {
	parentID := nanoid.MustFromSeed(runcontext.SessionIDPrefix, "parent", "PreserveSourceLinkSeed")
	childID := nanoid.MustFromSeed(runcontext.SessionIDPrefix, "child", "PreserveSourceLinkSeed")
	parent, err := runcontext.NewParent(parentID)
	require.NoError(t, err)
	child, err := parent.NewChild(runcontext.ChildOptions{
		ChildSessionID:  childID,
		RunID:           "run_memory",
		PersonalityName: "researcher",
		StateMode:       runcontext.StateModeReadonly,
		ProfileName:     "work",
	})
	require.NoError(t, err)

	item := ApplyRunProvenance(MemoryItem{
		SourceLinks: []SourceLink{{
			SourceProfile:     "existing_profile",
			SourcePersonality: "existing_personality",
			ParentSessionID:   "existing_parent",
			ChildSessionID:    "existing_child",
			RunID:             "existing_run",
			StateMode:         "existing_state",
			SourceTrigger:     "existing_trigger",
		}},
	}, child, "tool_write")

	require.Len(t, item.SourceLinks, 1)
	require.Equal(t, parentID, item.SourceLinks[0].SessionID)
	require.Equal(t, "existing_profile", item.SourceLinks[0].SourceProfile)
	require.Equal(t, "existing_personality", item.SourceLinks[0].SourcePersonality)
	require.Equal(t, "existing_parent", item.SourceLinks[0].ParentSessionID)
	require.Equal(t, "existing_child", item.SourceLinks[0].ChildSessionID)
	require.Equal(t, "existing_run", item.SourceLinks[0].RunID)
	require.Equal(t, "existing_state", item.SourceLinks[0].StateMode)
	require.Equal(t, "existing_trigger", item.SourceLinks[0].SourceTrigger)
}

func TestGetRunChildSessionID_FallsBackToEffectiveSessionOnlyForChildren(t *testing.T) {
	childID := nanoid.MustFromSeed(runcontext.SessionIDPrefix, "child", "ChildSessionFallbackSeed")

	require.Empty(t, getRunChildSessionID(runcontext.Context{
		Session: runcontext.Session{EffectiveID: childID},
	}))
	require.Equal(t, childID, getRunChildSessionID(runcontext.Context{
		Session: runcontext.Session{EffectiveID: childID},
		Lineage: runcontext.Lineage{ParentSessionID: runcontext.DefaultSessionID},
	}))
}

func TestFillSourceLinkProvenance_IgnoresNilLinks(t *testing.T) {
	require.NotPanics(t, func() {
		fillSourceLinkProvenance(nil, runcontext.Context{}, "tool_write")
	})
}

func TestHasSourceProvenance_AcceptsSourceSessionMetadataAndSourceLinks(t *testing.T) {
	require.True(t, HasSourceProvenance(MemoryItem{
		Metadata: map[string]string{MemoryMetadataSourceSessionID: "default"},
	}))
	require.True(t, HasSourceProvenance(MemoryItem{
		SourceLinks: []SourceLink{{SummaryID: "summary_1"}},
	}))
	require.False(t, HasSourceProvenance(MemoryItem{
		Metadata:    map[string]string{"other": "value"},
		SourceLinks: []SourceLink{{CreatedBy: "tool"}},
	}))
}
