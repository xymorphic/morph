package guardrails

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExactValueStream_RedactsAcrossChunkBoundaries(t *testing.T) {
	stream := NewExactValueStream("super-secret-value")

	first := stream.Redact([]byte("before super-sec"), false)
	second := stream.Redact([]byte("ret-value after"), false)
	last := stream.Flush()

	require.Equal(t, "before ", string(first))
	require.Equal(t, "[REDACTED] after", string(second))
	require.Empty(t, last)
}

func TestExactValueStream_PreservesIncompleteNonSecretTextOnFlush(t *testing.T) {
	stream := NewExactValueStream("secret")

	output := stream.Redact([]byte("a sec"), false)
	flushed := stream.Flush()

	require.Equal(t, "a ", string(output))
	require.Equal(t, "sec", string(flushed))
}

func TestExactValueStream_RedactsLongestRegisteredValue(t *testing.T) {
	stream := NewExactValueStream("token", "token-value")

	output := stream.Redact([]byte("token-value token"), true)

	require.Equal(t, "[REDACTED] [REDACTED]", string(output))
}

func TestExactValueStream_IgnoresEmptyAndDuplicateValues(t *testing.T) {
	stream := NewExactValueStream("", "secret", "secret")

	require.Equal(t, "[REDACTED]", string(stream.Redact([]byte("secret"), true)))
}
