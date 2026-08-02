package search

import (
	"testing"

	"github.com/stretchr/testify/require"

	statememory "github.com/xymorphic/morph/internal/state/core"
)

func TestMemoryVectorTags(t *testing.T) {
	item := statememory.MemoryItem{
		Kind:      statememory.MemoryKindProcedural,
		Status:    statememory.MemoryStatusActive,
		Tags:      []string{"Go Style", " ", "Daemon-Log"},
		Reflected: true,
		SourceLinks: []statememory.MemorySourceLink{{
			SessionID: " source-session ",
		}},
	}

	require.Equal(t, []string{
		"memory_kind:procedural",
		"memory_reflected:true",
		"memory_session:source-session",
		"memory_status:active",
		"memory_tag:daemon-log",
		"memory_tag:go style",
	}, MemoryVectorTags(item))
}

func TestMemoryVectorTags_PrefersMetadataSessionID(t *testing.T) {
	item := statememory.MemoryItem{
		Kind:      statememory.MemoryKindSemantic,
		Status:    statememory.MemoryStatusCandidate,
		Reflected: false,
		Metadata: map[string]string{
			"source_session_id": " metadata-session ",
		},
		SourceLinks: []statememory.MemorySourceLink{{
			SessionID: "source-session",
		}},
	}

	require.Equal(t, "metadata-session", MemoryVectorSessionID(item))
	require.Equal(t, []string{
		"memory_kind:semantic",
		"memory_reflected:false",
		"memory_session:metadata-session",
		"memory_status:candidate",
	}, MemoryVectorTags(item))
}

func TestMemoryVectorTags_OmitsEmptyKindStatusSessionAndMemoryTags(t *testing.T) {
	item := statememory.MemoryItem{
		Tags: []string{" ", "\t"},
	}

	require.Empty(t, MemoryVectorSessionID(item))
	require.Equal(t, []string{"memory_reflected:false"}, MemoryVectorTags(item))
}

func TestMemoryVectorTag(t *testing.T) {
	require.Equal(t, "memory_kind:semantic", MemoryVectorTag(" memory_kind ", " semantic "))
	require.Equal(t, ":", MemoryVectorTag(" ", " "))
}
