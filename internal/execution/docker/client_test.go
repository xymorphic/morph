package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeEndpoint_WindowsNamedPipe(t *testing.T) {
	endpoint, err := normalizeEndpoint(`//./pipe/docker_engine`)
	require.NoError(t, err)
	require.Equal(t, `npipe:////./pipe/docker_engine`, endpoint)
}

func FuzzNormalizeEndpoint(f *testing.F) {
	f.Add("/var/run/docker.sock")
	f.Add("tcp://example.com:2375")
	f.Add(`//./pipe/docker_engine`)
	f.Fuzz(func(t *testing.T, endpoint string) {
		normalized, err := normalizeEndpoint(endpoint)
		if err == nil {
			require.True(
				t,
				strings.HasPrefix(normalized, "unix://") ||
					strings.HasPrefix(normalized, "npipe://"),
			)
		}
	})
}
