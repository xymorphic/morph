package episodic

import (
	"testing"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
)

func TestCheckEpisodeCandidateAdmissionRejection_RejectsExecutionDetail(t *testing.T) {
	reason := checkEpisodeCandidateAdmissionRejection(storage.MemoryItem{
		Metadata: map[string]string{
			"memory_granularity": " execution_detail ",
		},
	})

	require.Equal(t, "execution_detail", reason)
}
