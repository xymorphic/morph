package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/execution"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	"github.com/xymorphic/morph/internal/profile"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
)

type nilAPIClient struct {
	closed bool
}

func (c *nilAPIClient) Close() error {
	c.closed = true
	return nil
}

func (c *nilAPIClient) SessionAPI() rpcclient.SessionAPI {
	return nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestNewCommand_ListEnvironments(t *testing.T) {
	stub, buffer := setSandboxTestClient(t)
	stub.ExecutionEnvironments = []execution.EnvironmentDetails{
		{
			Status: execution.EnvironmentStatus{
				ID:      "sandbox-1",
				Backend: execution.BackendDocker,
				Scope:   execution.ScopeSession,
				State:   execution.EnvironmentReady,
			},
		},
	}

	err := NewCommand().Run(
		context.Background(),
		[]string{"sandbox", "--session", "work", "list"},
	)

	require.NoError(t, err)
	require.Equal(t, "sandbox-1\tdocker\tsession\tready\n", buffer.String())
	require.True(t, stub.Closed)
}

func TestNewCommand_ListEnvironmentsAsJSON(t *testing.T) {
	stub, buffer := setSandboxTestClient(t)
	stub.ExecutionEnvironments = []execution.EnvironmentDetails{
		{
			Status: execution.EnvironmentStatus{
				ID:    "local",
				State: execution.EnvironmentReady,
			},
		},
	}

	err := NewCommand().Run(
		context.Background(),
		[]string{"sandbox", "--json", "list"},
	)

	require.NoError(t, err)
	require.Contains(t, buffer.String(), "\"ID\": \"local\"")
	require.Contains(t, buffer.String(), "\"State\": \"ready\"")
}

func TestNewCommand_ExplainEnvironment(t *testing.T) {
	stub, buffer := setSandboxTestClient(t)
	stub.ExecutionEnvironments = []execution.EnvironmentDetails{
		{
			Status: execution.EnvironmentStatus{
				ID:                 "sandbox-1",
				Backend:            execution.BackendDocker,
				Scope:              execution.ScopeShared,
				State:              execution.EnvironmentRunning,
				WorkspaceMode:      execution.WorkspaceReadWrite,
				Network:            execution.NetworkBridge,
				ImageDigest:        "sha256:image",
				SecurityGeneration: "generation",
			},
		},
	}

	err := NewCommand().Run(
		context.Background(),
		[]string{"sandbox", "explain", "sandbox-1"},
	)

	require.NoError(t, err)
	require.Equal(
		t,
		"id: sandbox-1\nbackend: docker\nscope: shared\nstate: running\n"+
			"workspace: rw\nnetwork: bridge\nimage: sha256:image\n"+
			"security generation: generation\n",
		buffer.String(),
	)
}

func TestNewCommand_ExplainEnvironmentAsJSON(t *testing.T) {
	stub, buffer := setSandboxTestClient(t)
	stub.ExecutionEnvironments = []execution.EnvironmentDetails{
		{
			Status: execution.EnvironmentStatus{
				ID: "sandbox-1",
			},
			PolicyHash: "policy",
		},
	}

	err := NewCommand().Run(
		context.Background(),
		[]string{"sandbox", "--json", "explain", "sandbox-1"},
	)

	require.NoError(t, err)
	require.Contains(t, buffer.String(), "\"PolicyHash\": \"policy\"")
}

func TestNewCommand_ReportsInputAndRPCErrors(t *testing.T) {
	t.Run("missing environment ID", func(t *testing.T) {
		setSandboxTestClient(t)

		err := NewCommand().Run(context.Background(), []string{"sandbox", "explain"})

		require.EqualError(t, err, "execution environment id is required")
	})

	t.Run("list RPC", func(t *testing.T) {
		stub, _ := setSandboxTestClient(t)
		stub.Err = errors.New("list failed")

		err := NewCommand().Run(context.Background(), []string{"sandbox", "list"})

		require.EqualError(t, err, "list failed")
	})

	t.Run("explain RPC", func(t *testing.T) {
		stub, _ := setSandboxTestClient(t)
		stub.Err = errors.New("explain failed")

		err := NewCommand().Run(
			context.Background(),
			[]string{"sandbox", "explain", "missing"},
		)

		require.EqualError(t, err, "explain failed")
	})

	t.Run("client creation", func(t *testing.T) {
		setSandboxTestProfile(t)
		original := newClient
		t.Cleanup(func() {
			newClient = original
		})
		newClient = func(context.Context, *config.Config) (sandboxClient, error) {
			return nil, errors.New("connect failed")
		}

		err := NewCommand().Run(context.Background(), []string{"sandbox", "list"})

		require.EqualError(t, err, "connect failed")
	})

	t.Run("explain client creation", func(t *testing.T) {
		setSandboxTestProfile(t)
		original := newClient
		t.Cleanup(func() {
			newClient = original
		})
		newClient = func(context.Context, *config.Config) (sandboxClient, error) {
			return nil, errors.New("connect failed")
		}

		err := NewCommand().Run(
			context.Background(),
			[]string{"sandbox", "explain", "sandbox-1"},
		)

		require.EqualError(t, err, "connect failed")
	})

	t.Run("missing session API", func(t *testing.T) {
		setSandboxTestProfile(t)
		original := newClient
		t.Cleanup(func() {
			newClient = original
		})
		client := &nilAPIClient{}
		newClient = func(context.Context, *config.Config) (sandboxClient, error) {
			return client, nil
		}

		err := NewCommand().Run(context.Background(), []string{"sandbox", "list"})

		require.EqualError(t, err, "session RPC client is unavailable")
		require.True(t, client.closed)
	})

	t.Run("configuration", func(t *testing.T) {
		setSandboxTestProfileConfig(t, ": invalid")

		err := NewCommand().Run(context.Background(), []string{"sandbox", "list"})

		require.Error(t, err)
	})
}

func TestOutputErrorsAreReturned(t *testing.T) {
	original := output
	output = failingWriter{}
	t.Cleanup(func() {
		output = original
	})

	require.EqualError(t, writeJSON(map[string]string{"value": "test"}), "write failed")
	require.EqualError(
		t,
		writeDetails(execution.EnvironmentDetails{}),
		"write failed",
	)

	stub, _ := setSandboxTestClientWithWriter(t, failingWriter{})
	stub.ExecutionEnvironments = []execution.EnvironmentDetails{
		{
			Status: execution.EnvironmentStatus{
				ID: "sandbox-1",
			},
		},
	}
	err := NewCommand().Run(context.Background(), []string{"sandbox", "list"})
	require.EqualError(t, err, "write failed")

	stub.ExecutionEnvironments = []execution.EnvironmentDetails{
		{
			Status: execution.EnvironmentStatus{
				ID: "sandbox-1",
			},
		},
	}
	err = NewCommand().Run(
		context.Background(),
		[]string{"sandbox", "--json", "explain", "sandbox-1"},
	)
	require.EqualError(t, err, "write failed")
}

func TestNewCommand_ShowsHelpWithoutSubcommand(t *testing.T) {
	setSandboxTestProfile(t)

	err := NewCommand().Run(context.Background(), []string{"sandbox"})

	require.NoError(t, err)
}

func TestDefaultClientFactory(t *testing.T) {
	cfg := &config.Config{}
	cfg.Normalize()

	client, err := newClient(context.Background(), cfg)
	if err == nil {
		require.NoError(t, client.Close())
	}
}

func setSandboxTestClient(t *testing.T) (*agentstub.AgentServiceStub, *bytes.Buffer) {
	t.Helper()
	buffer := &bytes.Buffer{}
	stub, _ := setSandboxTestClientWithWriter(t, buffer)
	return stub, buffer
}

func setSandboxTestClientWithWriter(
	t *testing.T,
	writer io.Writer,
) (*agentstub.AgentServiceStub, io.Writer) {
	t.Helper()
	setSandboxTestProfile(t)
	originalClient := newClient
	originalOutput := output
	stub := &agentstub.AgentServiceStub{}
	newClient = func(context.Context, *config.Config) (sandboxClient, error) {
		return stub, nil
	}
	output = writer
	t.Cleanup(func() {
		newClient = originalClient
		output = originalOutput
	})
	return stub, writer
}

func setSandboxTestProfile(t *testing.T) {
	t.Helper()
	setSandboxTestProfileConfig(t, "models:\n")
}

func setSandboxTestProfileConfig(t *testing.T, content string) {
	t.Helper()
	original := profile.Active()
	profileHome := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(content), 0o600),
	)
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    "test",
		HomeDir: profileHome,
	}))
	t.Cleanup(func() {
		profile.SetActive(original)
	})
}
