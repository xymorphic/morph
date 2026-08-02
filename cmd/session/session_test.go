package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	"github.com/xymorphic/morph/internal/profile"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/runtime"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
)

func TestNewCommandSessionNewCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{CreatedSession: storage.Session{ID: "project-a"}}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "new", "project-a"})

	require.NoError(t, err)
	require.Equal(t, "project-a\n", output.String())
}

func TestNewCommandSessionListCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{Sessions: []storage.Session{
		{
			ID:          "default",
			Origin:      storage.SessionOrigin{Source: storage.SessionOriginSourceCLI},
			Title:       "Daily Planning",
			TitleSource: storage.SessionTitleSourceGenerated,
			UpdatedAt:   time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
		},
		{ID: "project-a"},
	}}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{
		"session",
		"list",
		"--source",
		storage.SessionOriginSourceCLI,
	})

	require.NoError(t, err)
	require.Equal(t, storage.SessionOriginSourceCLI, stub.ListOptions.OriginSource)
	require.True(t, strings.HasPrefix(output.String(), "ID"))
	require.Contains(t, output.String(), "TITLE")
	require.Contains(t, output.String(), "default    Daily Planning")
	require.Contains(t, output.String(), "cli     2026-07-05T08:00:00Z")
	require.Contains(t, output.String(), "project-a")
}

func TestNewCommandSessionListUsesProfileRuntimeEndpoint(t *testing.T) {
	originalNewClient := newClient
	originalOutput := sessionOutput
	originalProfile := profile.Active()
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
		profile.SetActive(originalProfile)
	})

	home := t.TempDir()
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte("models:\n"), 0o600))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: profileHome}))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})
	port := listener.Addr().(*net.TCPAddr).Port
	_, err = runtime.WriteActive("127.0.0.1", port)
	require.NoError(t, err)

	sessionOutput = io.Discard
	stub := &agentstub.AgentServiceStub{Sessions: []storage.Session{{ID: "default"}}}
	var got *config.Config
	newClient = func(_ context.Context, cfg *config.Config) (sessionClient, error) {
		got = cfg
		return stub, nil
	}

	err = NewCommand().Run(context.Background(), []string{"session", "list"})

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "127.0.0.1", got.RPC.Address)
	require.Equal(t, port, got.RPC.Port)
}

func TestNewCommandSessionCurrentCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{CurrentSessionResult: storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Main conversation",
	}}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "current"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "Session\n")
	require.Contains(t, output.String(), "ID:                  "+storage.DefaultSessionID)
	require.Contains(t, output.String(), "Title:               Main conversation")
}

func TestNewCommandSessionUseCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "use", "project-a"})

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.UsedSessionID)
	require.Equal(t, "project-a\n", output.String())
}

func TestNewCommandSessionUnarchiveCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "unarchive", "project-a"})

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.UnarchivedSessionID)
	require.Equal(t, "project-a\n", output.String())
}

func TestNewCommandSessionUnarchiveReturnsRPCErrors(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	expected := errors.New("unarchive failed")
	sessionOutput = io.Discard

	stub := &agentstub.AgentServiceStub{UnarchiveSessionErr: expected}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "unarchive", "project-a"})

	require.ErrorIs(t, err, expected)
	require.Equal(t, "project-a", stub.UnarchivedSessionID)
	require.True(t, stub.Closed)
}

func TestNewCommandSessionUnarchiveReturnsClientErrors(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	expected := errors.New("client failed")
	sessionOutput = io.Discard
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return nil, expected
	}

	err := NewCommand().Run(context.Background(), []string{"session", "unarchive", "project-a"})

	require.ErrorIs(t, err, expected)
}

func TestNewCommandSessionUnarchiveReturnsOutputErrors(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	expected := errors.New("write failed")
	sessionOutput = failingSessionWriter{err: expected}

	stub := &agentstub.AgentServiceStub{}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "unarchive", "project-a"})

	require.ErrorIs(t, err, expected)
	require.Equal(t, "project-a", stub.UnarchivedSessionID)
	require.True(t, stub.Closed)
}

func TestNewCommandSessionCompactCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{CompactResult: rpcclient.CompactSessionResult{
		SessionID:            "project-a",
		SourceEndOffset:      12,
		SourceMessageCount:   20,
		UpdatedAt:            time.Unix(123, 0).UTC(),
		CurrentContextLength: 4000,
		TotalContextLength:   128000,
	}}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "compact", "project-a"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "Compaction\n")
	require.Contains(t, output.String(), "Session ID:          project-a")
	require.Contains(t, output.String(), "Source end offset:   12")
	require.Contains(t, output.String(), "Source messages:     20")
	require.Contains(t, output.String(), "Updated at:          1970-01-01T00:02:03Z")
	require.Contains(t, output.String(), "Current context:     4000")
	require.Contains(t, output.String(), "Total context:       128000")
}

func TestNewCommandSessionRepairCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	stub := &agentstub.AgentServiceStub{RepairResult: search.VectorRepairResult{
		SessionsScanned:    2,
		MessagesScanned:    3,
		RowsScanned:        4,
		MissingRows:        5,
		StaleRows:          6,
		UnchangedRows:      7,
		RebuiltRows:        8,
		DeletedSources:     9,
		Batches:            10,
		AttemptedSources:   11,
		RecoveredSources:   12,
		StillFailedSources: 13,
	}}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "repair", "--full", "project-a"})

	require.NoError(t, err)
	require.Equal(t, "project-a", stub.RepairOptions.SessionID)
	require.True(t, stub.RepairOptions.Full)
	require.Contains(t, output.String(), "Session repair\n")
	for _, expected := range []string{
		"Sessions scanned:    2",
		"Messages scanned:    3",
		"Rows scanned:        4",
		"Missing rows:        5",
		"Stale rows:          6",
		"Unchanged rows:      7",
		"Rebuilt rows:        8",
		"Deleted sources:     9",
		"Batches:             10",
		"Attempted sources:   11",
		"Recovered sources:   12",
		"Still failed sources: 13",
	} {
		require.Contains(t, output.String(), expected)
	}
}

func TestNewCommandSessionStatusCallsRPC(t *testing.T) {
	setSessionTestProfile(t)
	originalNewClient := newClient
	originalOutput := sessionOutput
	t.Cleanup(func() {
		newClient = originalNewClient
		sessionOutput = originalOutput
	})

	var output bytes.Buffer
	sessionOutput = &output

	created := time.Date(2024, 5, 1, 8, 0, 0, 0, time.UTC)
	updated := time.Date(2024, 5, 2, 9, 0, 0, 0, time.UTC)
	stub := &agentstub.AgentServiceStub{StatusResult: rpcclient.ContextStatus{
		SessionID:        "project-a",
		Offset:           12,
		Size:             20,
		Length:           128000,
		Used:             64000,
		Remaining:        64000,
		UsedPct:          0.5,
		RemainingPct:     0.5,
		CreatedAt:        created,
		UpdatedAt:        updated,
		CompactionStatus: "succeeded",
	}}
	newClient = func(context.Context, *config.Config) (sessionClient, error) {
		return stub, nil
	}

	err := NewCommand().Run(context.Background(), []string{"session", "status", "project-a"})

	require.NoError(t, err)
	for _, expected := range []string{
		"Session\n",
		"ID:                  project-a",
		"Created at:          2024-05-01T08:00:00Z",
		"Updated at:          2024-05-02T09:00:00Z",
		"Compaction status:   succeeded",
		"Context\n",
		"Offset:              12",
		"Size:                20",
		"Length:              128000",
		"Used:                64000",
		"Remaining:           64000",
		"Used percent:        50.00%",
		"Remaining percent:   50.00%",
	} {
		require.Contains(t, output.String(), expected)
	}
}

type failingSessionWriter struct {
	err error
}

func (w failingSessionWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func setSessionTestProfile(t *testing.T) {
	t.Helper()
	t.Setenv("MORPH_RPC_ADDRESS", "")
	t.Setenv("MORPH_RPC_PORT", "")

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: t.TempDir()}))
}
