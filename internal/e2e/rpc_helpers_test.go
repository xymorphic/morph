package e2e

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
)

func TestNewDefaultRPCHarness_UsesDefaultSpecAndConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "morph-home")

	h, err := NewDefaultRPCHarness(context.Background(), home, NewTextClient("hello"), nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	assert.Equal(t, "127.0.0.1", h.Address())
	assert.NotZero(t, h.Port())
}

func TestReserveRPCPort(t *testing.T) {
	t.Run("returns available port", func(t *testing.T) {
		port, err := ReserveRPCPort()
		require.NoError(t, err)
		assert.NotZero(t, port)
	})

	t.Run("returns listen error", func(t *testing.T) {
		originalListen := rpcHelperListen
		rpcHelperListen = func(string, string) (net.Listener, error) {
			return nil, errors.New("listen failed")
		}
		t.Cleanup(func() {
			rpcHelperListen = originalListen
		})

		port, err := ReserveRPCPort()
		require.Error(t, err)
		assert.Zero(t, port)
		assert.EqualError(t, err, "listen failed")
	})

	t.Run("requires tcp listener", func(t *testing.T) {
		originalListen := rpcHelperListen
		rpcHelperListen = func(string, string) (net.Listener, error) {
			return stubListener{addr: stubAddr("pipe")}, nil
		}
		t.Cleanup(func() {
			rpcHelperListen = originalListen
		})

		port, err := ReserveRPCPort()
		require.Error(t, err)
		assert.Zero(t, port)
		assert.EqualError(t, err, "rpc helper listener must be tcp")
	})
}

func TestWaitForRPC(t *testing.T) {
	t.Run("returns client when rpc becomes ready", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "morph-home")

		h, err := NewDefaultRPCHarness(context.Background(), home, NewTextClient("hello"), nil)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, h.Close())
		})

		client, err := WaitForRPC(h.Address(), h.Port(), 2*time.Second, rpcclient.Options{
			PermissionSurface: permissions.SurfaceCLI,
			AuthAudience:      h.authAudience,
			AuthKey:           h.authKey,
			AuthOwnerID:       h.authOwnerID,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, client.Close())
		})

		current, err := client.Session.Current(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "default", current.ID)
	})

	t.Run("times out when client cannot connect", func(t *testing.T) {
		_, err := WaitForRPC("127.0.0.1", 1, 150*time.Millisecond)
		require.Error(t, err)
		assert.EqualError(t, err, "rpc server did not become ready on 127.0.0.1:1")
	})

	t.Run("retries when current session probe fails", func(t *testing.T) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		server := grpc.NewServer()
		defer server.Stop()

		go func() {
			_ = server.Serve(lis)
		}()

		port := lis.Addr().(*net.TCPAddr).Port
		client, err := WaitForRPC("127.0.0.1", port, 150*time.Millisecond)
		require.Error(t, err)
		assert.Nil(t, client)
		assert.EqualError(t, err, "rpc server did not become ready on 127.0.0.1:"+strconv.Itoa(port))
	})
}

func TestWriteRPCConfigFile_WritesExpectedContent(t *testing.T) {
	dir := t.TempDir()

	path, err := WriteRPCConfigFile(dir, "127.0.0.1", 8123, RPCConfigOptions{
		Name:     "rpc-agent",
		Stream:   true,
		Instruct: "be brief",
		NoColor:  true,
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "config.yaml"), path)
	assert.Contains(t, string(raw), "name: rpc-agent")
	assert.Contains(t, string(raw), "stream: true")
	assert.Contains(t, string(raw), "address: 127.0.0.1")
	assert.Contains(t, string(raw), "port: 8123")
	assert.Contains(t, string(raw), "noColor: true")
	assert.Contains(t, string(raw), "session:")
	assert.Contains(t, string(raw), "instruct: be brief")
}

func TestWriteRPCConfigFile_DefaultsName(t *testing.T) {
	path, err := WriteRPCConfigFile(t.TempDir(), "127.0.0.1", 9000, RPCConfigOptions{})
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "name: yaml-agent")
	assert.NotContains(t, string(raw), "instruct:")
}

func TestWriteRPCConfigFile_ReturnsWriteError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	_, err := WriteRPCConfigFile(file, "127.0.0.1", 9000, RPCConfigOptions{})
	require.Error(t, err)
}

func TestMissingTools(t *testing.T) {
	assert.NoError(t, MissingTools("run_command")(models.Request{
		Tools: []models.ToolDefinition{{Name: "read_file"}},
	}))

	err := MissingTools("read_file")(models.Request{
		Tools: []models.ToolDefinition{{Name: "read_file"}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool "read_file" to be unavailable, got tools [read_file]`)
}

func TestCombineChecks(t *testing.T) {
	assert.NoError(t, CombineChecks(nil, func(models.Request) error { return nil })(models.Request{}))

	err := CombineChecks(
		func(models.Request) error { return errors.New("first failure") },
		func(models.Request) error { return errors.New("second failure") },
	)(models.Request{})
	require.Error(t, err)
	assert.EqualError(t, err, "first failure")
}

func TestToolMessagePresent(t *testing.T) {
	assert.NoError(t, ToolMessagePresent("call-1", "time")(models.Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "call-1"}},
	}))

	err := ToolMessagePresent("call-1", "time")(models.Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleTool, Name: "clock", ToolCallID: "call-1"}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool message name "time"`)

	err = ToolMessagePresent("call-1", "time")(models.Request{})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool message for tool call "call-1"`)

	err = ToolMessagePresent("call-1", "time")(models.Request{
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleAssistant, Name: "time", ToolCallID: "call-1"},
			{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "other-call"},
		},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool message for tool call "call-1"`)
}

func TestToolOutputString(t *testing.T) {
	assert.NoError(t, ToolOutputString("call-1", "time", func(output string) error {
		if output != "2026-01-01T00:00:00Z" {
			return errors.New("unexpected output")
		}

		return nil
	})(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "time",
			ToolCallID: "call-1",
			Content:    `{"name":"time","output":"2026-01-01T00:00:00Z"}`,
		}},
	}))

	err := ToolOutputString("call-1", "time", func(string) error { return errors.New("bad output") })(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "time",
			ToolCallID: "call-1",
			Content:    `{"name":"time","output":"2026-01-01T00:00:00Z"}`,
		}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, "bad output")
}

func TestToolOutputJSON(t *testing.T) {
	assert.NoError(t, ToolOutputJSON("call-1", "write_file", func(payload map[string]any) error {
		if fmt.Sprint(payload["path"]) != "drafts/out.txt" {
			return errors.New("unexpected path")
		}

		return nil
	})(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "write_file",
			ToolCallID: "call-1",
			Content:    `{"name":"write_file","output":"{\"path\":\"drafts/out.txt\",\"created\":true}"}`,
		}},
	}))

	err := ToolOutputJSON("call-1", "write_file", func(map[string]any) error { return nil })(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "write_file",
			ToolCallID: "call-1",
			Content:    `{"name":"write_file","output":"{"}`,
		}},
	})
	require.Error(t, err)
}

func TestToolError(t *testing.T) {
	assert.NoError(t, ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "read_file",
			ToolCallID: "call-1",
			Content:    `{"name":"read_file","error":{"code":"path_outside_roots","message":"path is outside allowed roots"}}`,
		}},
	}))

	err := ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "clock",
			ToolCallID: "call-1",
			Content:    `{"name":"clock","error":{"code":"path_outside_roots","message":"path is outside allowed roots"}}`,
		}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool message name "read_file"`)

	err = ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "read_file",
			ToolCallID: "call-1",
			Content:    `{`,
		}},
	})
	require.Error(t, err)

	err = ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "read_file",
			ToolCallID: "call-1",
			Content:    `{"name":"wrong","error":{"code":"path_outside_roots","message":"path is outside allowed roots"}}`,
		}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool payload name "read_file"`)

	err = ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "read_file",
			ToolCallID: "call-1",
			Content:    `{"name":"read_file","error":{"code":"wrong","message":"path is outside allowed roots"}}`,
		}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool error code "path_outside_roots", got "wrong"`)

	err = ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "read_file",
			ToolCallID: "call-1",
			Content:    `{"name":"read_file","error":{"code":"path_outside_roots","message":"wrong"}}`,
		}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool error message "path is outside allowed roots", got "wrong"`)

	err = ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool error for tool call "call-1"`)

	err = ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots")(models.Request{
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleAssistant, Name: "read_file", ToolCallID: "call-1"},
			{Role: morphmsg.RoleTool, Name: "read_file", ToolCallID: "other-call", Content: `{"name":"read_file","error":{"code":"path_outside_roots","message":"path is outside allowed roots"}}`},
		},
	})
	require.Error(t, err)
	assert.EqualError(t, err, `expected tool error for tool call "call-1"`)
}
