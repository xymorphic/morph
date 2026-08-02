package trace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/e2e"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func Test_E2E_TraceCommand_GeneratedTracesAreReadable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "morph-home")
	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Trace.Enabled = true

	h, err := e2e.NewDefaultRPCHarness(context.Background(), home, e2e.NewClient(e2e.Step{
		Response: &models.Response{OutputText: "trace reply"},
	}), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	result, err := h.Send(context.Background(), e2e.RootChatRequest{Message: "hello trace"})
	require.NoError(t, err)
	assert.Equal(t, "trace reply", result.Reply)

	listenAddr := reserveListenAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- NewCommand().Run(ctx, []string{
			"trace", "view",
			"--trace-dir", h.Config().Trace.Disk.Dir,
			"--listen", listenAddr,
		})
	}()

	baseURL := "http://" + listenAddr
	waitForServer(t, baseURL+"/api/sessions")

	t.Run("Trace files are written", func(t *testing.T) {
		traceFiles, globErr := filepath.Glob(filepath.Join(h.Config().Trace.Disk.Dir, "*.jsonl"))
		require.NoError(t, globErr)
		require.NotEmpty(t, traceFiles)
		require.Len(t, traceFiles, 1)
	})

	t.Run("Session list is readable", func(t *testing.T) {
		resp, getErr := http.Get(baseURL + "/api/sessions")
		require.NoError(t, getErr)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var listPayload struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&listPayload))
		require.NotEmpty(t, listPayload.Sessions)
		assert.Equal(t, result.SessionID, listPayload.Sessions[0].ID)
	})

	t.Run("Session detail is readable", func(t *testing.T) {
		resp, getErr := http.Get(baseURL + "/api/sessions/" + result.SessionID)
		require.NoError(t, getErr)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var detailPayload struct {
			Summary struct {
				ID          string `json:"id"`
				FinalStatus string `json:"final_status"`
			} `json:"summary"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&detailPayload))
		assert.Equal(t, result.SessionID, detailPayload.Summary.ID)
		assert.Equal(t, "completed", detailPayload.Summary.FinalStatus)
	})

	t.Run("Basic auth protects endpoints when configured", func(t *testing.T) {
		authListenAddr := reserveListenAddress(t)
		authCtx, authCancel := context.WithCancel(context.Background())
		defer authCancel()

		authErrCh := make(chan error, 1)
		go func() {
			authErrCh <- NewCommand().Run(authCtx, []string{
				"trace", "view",
				"--trace-dir", h.Config().Trace.Disk.Dir,
				"--listen", authListenAddr,
				"--username", "viewer",
				"--password", "secret",
			})
		}()

		authBaseURL := "http://" + authListenAddr
		waitForServer(t, authBaseURL+"/api/sessions")

		req, reqErr := http.NewRequest(http.MethodGet, authBaseURL+"/api/sessions", nil)
		require.NoError(t, reqErr)
		resp, getErr := http.DefaultClient.Do(req)
		require.NoError(t, getErr)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		req, reqErr = http.NewRequest(http.MethodGet, authBaseURL+"/api/sessions", nil)
		require.NoError(t, reqErr)
		req.SetBasicAuth("viewer", "secret")
		resp, getErr = http.DefaultClient.Do(req)
		require.NoError(t, getErr)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		authCancel()
		require.NoError(t, <-authErrCh)
	})

	cancel()
	require.NoError(t, <-errCh)
}
