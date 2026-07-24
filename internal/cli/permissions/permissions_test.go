package permissions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	permissiondomain "github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
)

func TestWriteRequestsAndGrants_RendersSafeStructuredFields(t *testing.T) {
	var buffer bytes.Buffer
	previous := SetOutput(&buffer)
	t.Cleanup(func() { SetOutput(previous) })
	originalLocal := time.Local
	time.Local = time.FixedZone("WAT", int(time.Hour/time.Second))
	t.Cleanup(func() { time.Local = originalLocal })
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	require.NoError(t, writeRequests([]permissiondomain.ApprovalRequest{
		{
			ID: "approval_1", Status: permissiondomain.ApprovalPending, Summary: "run_command · execute process",
			Effects: []permissiondomain.Effect{permissiondomain.EffectExecution}, ExpiresAt: now,
		},
	}))
	require.NoError(t, writeGrants([]permissiondomain.ApprovalGrant{
		{
			ID: "grant_1", Status: permissiondomain.GrantActive, Scope: permissiondomain.GrantSession,
			Operations: []string{"run_command · execute process", "run_command · read file workspace"},
			SessionID:  "session", ExpiresAt: now,
		},
		{
			ID: "grant_always", Status: permissiondomain.GrantActive, Scope: permissiondomain.GrantAlways,
		},
	}))
	require.Contains(t, buffer.String(), "approval_1")
	require.Contains(t, buffer.String(), "execution")
	require.Contains(t, buffer.String(), "grant_1")
	require.Contains(t, buffer.String(), "run_command · execute process; run_command · read file workspace")
	require.Contains(t, buffer.String(), "grant_always")
	require.Contains(t, buffer.String(), "never")
	require.Contains(t, buffer.String(), "2026-07-14 13:00 WAT")
	require.NotContains(t, buffer.String(), "2026-07-14 12:00 UTC")
	require.NotContains(t, buffer.String(), "fingerprint")
}

func TestWriteRequestsAndGrants_ReportsEmptyState(t *testing.T) {
	var buffer bytes.Buffer
	previous := SetOutput(&buffer)
	t.Cleanup(func() { SetOutput(previous) })
	require.NoError(t, writeRequests(nil))
	require.NoError(t, writeGrants(nil))
	require.Contains(t, buffer.String(), "No approval requests")
	require.Contains(t, buffer.String(), "No approval grants")
}

func TestPermissionCommands_RunApprovalLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	api := &permissionCommandAPIStub{
		requests: []permissiondomain.ApprovalRequest{
			{
				ID: "approval_1", Status: permissiondomain.ApprovalPending, Summary: "run command",
				Effects: []permissiondomain.Effect{permissiondomain.EffectExecution}, ExpiresAt: now,
			},
		},
		grants: []permissiondomain.ApprovalGrant{
			{
				ID: "grant_1", Status: permissiondomain.GrantActive, Scope: permissiondomain.GrantSession, ExpiresAt: now,
			},
		},
	}
	client := &permissionCommandClientStub{api: api}
	previousClient := getPermissionClient
	getPermissionClient = func(context.Context, *cli.Command) (permissionClient, error) { return client, nil }
	t.Cleanup(func() { getPermissionClient = previousClient })
	var buffer bytes.Buffer
	previousOutput := SetOutput(&buffer)
	t.Cleanup(func() { SetOutput(previousOutput) })

	for _, args := range [][]string{
		{"permissions", "list"},
		{"permissions", "pending"},
		{"permissions", "grants"},
		{"permissions", "prune", "--dry-run"},
		{"permissions", "approve", "--scope", "session", "approval_1"},
		{"permissions", "deny", "approval_1"},
		{"permissions", "revoke", "approval_1"},
		{"permissions", "delete", "approval_1"},
		{"permissions", "explain", "approval_1"},
	} {
		command := &cli.Command{Name: "permissions", Commands: []*cli.Command{
			NewListCommand(), NewPendingCommand(), NewGrantsCommand(), NewPruneCommand(), NewApproveCommand(), NewDenyCommand(), NewRevokeCommand(), NewDeleteCommand(), NewExplainCommand(),
		}}
		require.NoError(t, command.Run(context.Background(), args), args)
	}
	require.True(t, client.closed)
	require.Equal(t, permissiondomain.GrantSession, api.resolvedScope)
	require.Equal(t, "approval_1", api.revokedID)
	require.Equal(t, "approval_1", api.deletedID)
	require.Contains(t, buffer.String(), "approved approval_1")
	require.Contains(t, buffer.String(), "denied approval_1")
	require.Contains(t, buffer.String(), "revoked grant_1")
	require.Contains(t, buffer.String(), "deleted request approval_1 and linked grant grant_1")
	require.Contains(t, buffer.String(), "Effects: execution")
	require.Contains(t, buffer.String(), "0 requests and 0 grants eligible")
}

func TestPermissionCommands_ValidateIDsAndPropagateClientFailure(t *testing.T) {
	for _, name := range []string{"approve", "deny", "revoke", "delete", "explain"} {
		command := &cli.Command{Name: "permissions", Commands: []*cli.Command{
			NewApproveCommand(), NewDenyCommand(), NewRevokeCommand(), NewDeleteCommand(), NewExplainCommand(),
		}}
		err := command.Run(context.Background(), []string{"permissions", name})
		require.EqualError(t, err, "approval or grant id is required")
	}
	expected := errors.New("daemon unavailable")
	previous := getPermissionClient
	getPermissionClient = func(context.Context, *cli.Command) (permissionClient, error) { return nil, expected }
	t.Cleanup(func() { getPermissionClient = previous })
	for _, args := range [][]string{
		{"permissions", "list"},
		{"permissions", "pending"},
		{"permissions", "grants"},
		{"permissions", "prune"},
		{"permissions", "approve", "approval"},
		{"permissions", "deny", "approval"},
		{"permissions", "revoke", "grant"},
		{"permissions", "delete", "grant"},
		{"permissions", "explain", "approval"},
	} {
		err := newPermissionTestCommand().Run(context.Background(), args)
		require.ErrorIs(t, err, expected, args)
	}
}

func TestApproveCommand_AcceptsAlwaysAndRejectsLegacyDurableScope(t *testing.T) {
	api := &permissionCommandAPIStub{}
	previous := getPermissionClient
	getPermissionClient = func(context.Context, *cli.Command) (permissionClient, error) {
		return &permissionCommandClientStub{api: api}, nil
	}
	t.Cleanup(func() { getPermissionClient = previous })
	previousOutput := SetOutput(io.Discard)
	t.Cleanup(func() { SetOutput(previousOutput) })

	require.NoError(t, newPermissionTestCommand().Run(context.Background(), []string{
		"permissions", "approve", "--scope", "always", "approval",
	}))
	require.Equal(t, permissiondomain.GrantAlways, api.resolvedScope)
	err := newPermissionTestCommand().Run(context.Background(), []string{
		"permissions", "approve", "--scope", "durable", "approval",
	})
	require.EqualError(t, err, "approval scope must be one of: once, session, always")
}

func TestPermissionListCommands_ApplyFiltersAndPagination(t *testing.T) {
	api := &permissionCommandAPIStub{}
	previous := getPermissionClient
	getPermissionClient = func(context.Context, *cli.Command) (permissionClient, error) {
		return &permissionCommandClientStub{api: api}, nil
	}
	t.Cleanup(func() { getPermissionClient = previous })
	previousOutput := SetOutput(io.Discard)
	t.Cleanup(func() { SetOutput(previousOutput) })

	require.NoError(t, newPermissionTestCommand().Run(context.Background(), []string{
		"permissions", "list", "--status", "denied", "--limit", "25", "--offset", "5",
	}))
	require.Equal(t, permissiondomain.ApprovalQuery{
		Status: permissiondomain.ApprovalDenied, Limit: 25, Offset: 5,
	}, api.requestQuery)
	require.NoError(t, newPermissionTestCommand().Run(context.Background(), []string{
		"permissions", "grants", "--status", "revoked", "--limit", "10", "--offset", "2",
	}))
	require.Equal(t, permissiondomain.GrantQuery{
		Status: permissiondomain.GrantRevoked, Limit: 10, Offset: 2,
	}, api.grantQuery)
}

func TestPermissionListCommands_RejectInvalidPaginationAndFilters(t *testing.T) {
	for _, args := range [][]string{
		{"permissions", "list", "--status", "unknown"},
		{"permissions", "grants", "--status", "unknown"},
		{"permissions", "list", "--limit", "0"},
		{"permissions", "grants", "--limit", "501"},
		{"permissions", "pending", "--offset", "-1"},
	} {
		err := newPermissionTestCommand().Run(context.Background(), args)
		require.Error(t, err, args)
	}
}

func TestPermissionCommands_PropagateAPIFailures(t *testing.T) {
	expected := errors.New("permission api failed")
	tests := []struct {
		name string
		args []string
		api  *permissionCommandAPIStub
	}{
		{name: "list requests", args: []string{"permissions", "list"}, api: &permissionCommandAPIStub{requestErr: expected}},
		{name: "grants", args: []string{"permissions", "grants"}, api: &permissionCommandAPIStub{grantErr: expected}},
		{name: "prune", args: []string{"permissions", "prune"}, api: &permissionCommandAPIStub{pruneErr: expected}},
		{name: "pending", args: []string{"permissions", "pending"}, api: &permissionCommandAPIStub{requestErr: expected}},
		{name: "approve", args: []string{"permissions", "approve", "approval"}, api: &permissionCommandAPIStub{resolveErr: expected}},
		{name: "deny", args: []string{"permissions", "deny", "approval"}, api: &permissionCommandAPIStub{resolveErr: expected}},
		{name: "revoke", args: []string{"permissions", "revoke", "grant"}, api: &permissionCommandAPIStub{revokeErr: expected}},
		{name: "delete", args: []string{"permissions", "delete", "grant"}, api: &permissionCommandAPIStub{deleteErr: expected}},
		{name: "explain", args: []string{"permissions", "explain", "approval"}, api: &permissionCommandAPIStub{getErr: expected}},
	}
	previous := getPermissionClient
	t.Cleanup(func() { getPermissionClient = previous })
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getPermissionClient = func(context.Context, *cli.Command) (permissionClient, error) {
				return &permissionCommandClientStub{api: test.api}, nil
			}
			err := newPermissionTestCommand().Run(context.Background(), test.args)
			require.ErrorIs(t, err, expected)
		})
	}
}

func TestPermissionCommands_ReportMissingRequestAndOutputFailure(t *testing.T) {
	api := &permissionCommandAPIStub{found: boolPointer(false)}
	client := &permissionCommandClientStub{api: api}
	previousClient := getPermissionClient
	getPermissionClient = func(context.Context, *cli.Command) (permissionClient, error) { return client, nil }
	t.Cleanup(func() { getPermissionClient = previousClient })

	err := newPermissionTestCommand().Run(context.Background(), []string{"permissions", "explain", "missing"})
	require.EqualError(t, err, "approval request not found")

	api.found = nil
	api.requests = []permissiondomain.ApprovalRequest{{ID: "approval", Status: permissiondomain.ApprovalPending}}
	previousOutput := SetOutput(errorWriter{err: errors.New("write failed")})
	t.Cleanup(func() { SetOutput(previousOutput) })
	err = newPermissionTestCommand().Run(context.Background(), []string{"permissions", "list"})
	require.EqualError(t, err, "write failed")
}

func TestGetClient_LoadsConfiguredEndpoint(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("{}\n"), 0o600))
	var envPath string
	rootConfigPath := configPath
	command := &cli.Command{
		Name:  "morph",
		Flags: morphcli.RootFlags(&envPath, &rootConfigPath),
		Commands: []*cli.Command{{
			Name: "probe",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				client, err := getClient(ctx, cmd)
				if client != nil {
					_ = client.Close()
				}
				return err
			},
		}},
	}
	stub := &permissionCommandClientStub{api: &permissionCommandAPIStub{}}
	previousNewClient := newClient
	newClient = func(_ context.Context, cfg *config.Config) (permissionClient, error) {
		require.Equal(t, "127.0.0.1", cfg.RPC.Address)
		require.Equal(t, 55123, cfg.RPC.Port)
		return stub, nil
	}
	t.Cleanup(func() { newClient = previousNewClient })
	require.NoError(t, command.Run(context.Background(), []string{
		"morph", "--config", configPath, "--rpc.address", "127.0.0.1", "--rpc.port", "55123", "probe",
	}))
	require.True(t, stub.closed)
}

func TestGetClient_PropagatesConfigAndRuntimeErrors(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte("invalid: [\n"), 0o600))
		var envPath string
		rootConfigPath := configPath
		command := &cli.Command{
			Name:  "morph",
			Flags: morphcli.RootFlags(&envPath, &rootConfigPath),
			Action: func(ctx context.Context, cmd *cli.Command) error {
				_, err := getClient(ctx, cmd)
				return err
			},
		}
		require.Error(t, command.Run(context.Background(), []string{"morph", "--config", configPath}))
	})

	t.Run("runtime", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte("{}\n"), 0o600))
		expected := errors.New("runtime failed")
		previousResolveRPC := resolveRPC
		resolveRPC = func(context.Context, *cli.Command, *config.Config) (config.RPCConfig, error) {
			return config.RPCConfig{}, expected
		}
		t.Cleanup(func() { resolveRPC = previousResolveRPC })
		var envPath string
		rootConfigPath := configPath
		command := &cli.Command{
			Name:  "morph",
			Flags: morphcli.RootFlags(&envPath, &rootConfigPath),
			Action: func(ctx context.Context, cmd *cli.Command) error {
				_, err := getClient(ctx, cmd)
				return err
			},
		}
		err := command.Run(context.Background(), []string{"morph", "--config", configPath})
		require.ErrorIs(t, err, expected)
	})
}

func TestDefaultNewClient_CreatesPermissionClient(t *testing.T) {
	client, err := newClient(context.Background(), &config.Config{RPC: config.RPCConfig{
		Address: "127.0.0.1",
		Port:    1,
	}})
	require.NoError(t, err)
	require.NotNil(t, client.PermissionAPI())
	require.NoError(t, client.Close())
}

func TestSetOutput_HandlesNilWriter(t *testing.T) {
	previous := SetOutput(nil)
	t.Cleanup(func() { SetOutput(previous) })
}

func TestPresetCommand_ShowsAndUpdatesProfilePreset(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    provider: ollama
    name: test
    api: ollama-native
search:
  vector:
    enabled: false
storage:
  backend: memory
permissions:
  preset: ask
  rules:
    - name: allow automation clock
      actors: [automation]
      resources: [clock]
      actions: [read]
      decision: allow
`), 0o600))
	var buffer bytes.Buffer
	previousOutput := SetOutput(&buffer)
	t.Cleanup(func() { SetOutput(previousOutput) })

	command := newPresetTestCommand(configPath)
	require.NoError(t, command.Run(context.Background(), []string{
		"morph", "--config", configPath, "preset",
	}))
	require.Contains(t, buffer.String(), "Ask for approval (customized) (ask)")

	buffer.Reset()
	command = newPresetTestCommand(configPath)
	require.NoError(t, command.Run(context.Background(), []string{
		"morph", "--config", configPath, "preset", "approve",
	}))
	require.Contains(t, buffer.String(), "permission preset set to Approve for me (customized)")
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, permissiondomain.PresetApproveForMe, cfg.Permissions.EffectivePreset())
	require.Len(t, cfg.Permissions.Rules, 1)
	require.Equal(t, "allow automation clock", cfg.Permissions.Rules[0].Name)
}

func TestPresetCommand_RequiresConfirmationForFullAccess(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    provider: ollama
    name: test
    api: ollama-native
search:
  vector:
    enabled: false
storage:
  backend: memory
`), 0o600))
	previousOutput := SetOutput(io.Discard)
	t.Cleanup(func() { SetOutput(previousOutput) })

	err := newPresetTestCommand(configPath).Run(context.Background(), []string{
		"morph", "--config", configPath, "preset", "full-access",
	})
	require.EqualError(t, err, "full access is unsafe; rerun with --yes to confirm")

	require.NoError(t, newPresetTestCommand(configPath).Run(context.Background(), []string{
		"morph", "--config", configPath, "preset", "--yes", "full-access",
	}))
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, permissiondomain.PresetFullAccess, cfg.Permissions.EffectivePreset())
	require.Equal(t, permissiondomain.PresetFullAccess, cfg.Permissions.EffectivePreset())
}

func TestPresetCommand_ReportsInvalidInputAndConfigurationFailures(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("{}\n"), 0o600))

	err := newPresetTestCommand(configPath).Run(context.Background(), []string{
		"morph", "--config", configPath, "preset", "automatic",
	})
	require.EqualError(t, err, "permission preset must be one of: ask, approve, full_access, custom")

	err = newPresetTestCommand(configPath).Run(context.Background(), []string{
		"morph", "--config", configPath, "preset", "ask",
	})
	require.EqualError(t, err, "model is required")

	malformedPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(malformedPath, []byte("permissions: ["), 0o600))
	err = newPresetTestCommand(malformedPath).Run(context.Background(), []string{
		"morph", "--config", malformedPath, "preset",
	})
	require.Error(t, err)
}

func TestPresetCommand_PropagatesOutputFailures(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    provider: ollama
    name: test
    api: ollama-native
search:
  vector:
    enabled: false
storage:
  backend: memory
permissions:
  preset: ask
`), 0o600))
	expected := errors.New("write failed")
	previousOutput := SetOutput(errorWriter{err: expected})
	t.Cleanup(func() { SetOutput(previousOutput) })

	err := newPresetTestCommand(configPath).Run(context.Background(), []string{
		"morph", "--config", configPath, "preset",
	})
	require.ErrorIs(t, err, expected)

	err = newPresetTestCommand(configPath).Run(context.Background(), []string{
		"morph", "--config", configPath, "preset", "approve",
	})
	require.ErrorIs(t, err, expected)
}

type permissionCommandClientStub struct {
	api    rpcclient.PermissionAPI
	closed bool
}

func (s *permissionCommandClientStub) Close() error {
	s.closed = true
	return nil
}
func (s *permissionCommandClientStub) PermissionAPI() rpcclient.PermissionAPI { return s.api }

type permissionCommandAPIStub struct {
	requests      []permissiondomain.ApprovalRequest
	grants        []permissiondomain.ApprovalGrant
	resolvedScope permissiondomain.GrantScope
	revokedID     string
	deletedID     string
	requestErr    error
	grantErr      error
	pruneErr      error
	resolveErr    error
	revokeErr     error
	deleteErr     error
	getErr        error
	found         *bool
	requestQuery  permissiondomain.ApprovalQuery
	grantQuery    permissiondomain.GrantQuery
}

func (s *permissionCommandAPIStub) ListApprovalRequests(
	_ context.Context,
	query permissiondomain.ApprovalQuery,
) ([]permissiondomain.ApprovalRequest, error) {
	s.requestQuery = query
	if s.requestErr != nil {
		return nil, s.requestErr
	}
	if query.Status == "" {
		return s.requests, nil
	}
	result := make([]permissiondomain.ApprovalRequest, 0, len(s.requests))
	for _, request := range s.requests {
		if request.Status == query.Status {
			result = append(result, request)
		}
	}

	return result, nil
}

func (s *permissionCommandAPIStub) GetApprovalRequest(
	context.Context,
	string,
) (permissiondomain.ApprovalRequest, bool, error) {
	if s.getErr != nil {
		return permissiondomain.ApprovalRequest{}, false, s.getErr
	}
	if s.found != nil && !*s.found {
		return permissiondomain.ApprovalRequest{}, false, nil
	}

	return s.requests[0], true, nil
}

func (s *permissionCommandAPIStub) ResolveApprovalRequest(
	_ context.Context,
	id string,
	approved bool,
	scope permissiondomain.GrantScope,
) (permissiondomain.ApprovalRequest, error) {
	if s.resolveErr != nil {
		return permissiondomain.ApprovalRequest{}, s.resolveErr
	}

	if approved {
		s.resolvedScope = scope
	}
	status := permissiondomain.ApprovalDenied
	if approved {
		status = permissiondomain.ApprovalApproved
	}
	return permissiondomain.ApprovalRequest{ID: id, Status: status, Scope: scope}, nil
}

func (s *permissionCommandAPIStub) ListApprovalGrants(
	_ context.Context,
	query permissiondomain.GrantQuery,
) ([]permissiondomain.ApprovalGrant, error) {
	s.grantQuery = query
	if s.grantErr != nil {
		return nil, s.grantErr
	}
	return s.grants, nil
}

func (s *permissionCommandAPIStub) RevokeApprovalGrant(
	_ context.Context,
	id string,
) (permissiondomain.ApprovalGrant, error) {
	if s.revokeErr != nil {
		return permissiondomain.ApprovalGrant{}, s.revokeErr
	}

	s.revokedID = id
	return permissiondomain.ApprovalGrant{ID: "grant_1", Status: permissiondomain.GrantRevoked}, nil
}

func (s *permissionCommandAPIStub) DeleteApprovalRecord(
	_ context.Context,
	id string,
) (permissiondomain.ApprovalDeleteResult, error) {
	if s.deleteErr != nil {
		return permissiondomain.ApprovalDeleteResult{}, s.deleteErr
	}

	s.deletedID = id
	return permissiondomain.ApprovalDeleteResult{
		ID: id, Kind: permissiondomain.ApprovalRecordRequest, LinkedGrantID: "grant_1",
	}, nil
}

func (s *permissionCommandAPIStub) PruneApprovals(
	_ context.Context,
	dryRun bool,
) (permissiondomain.ApprovalPruneResult, error) {
	if s.pruneErr != nil {
		return permissiondomain.ApprovalPruneResult{}, s.pruneErr
	}

	return permissiondomain.ApprovalPruneResult{DryRun: dryRun}, nil
}

func newPermissionTestCommand() *cli.Command {
	return &cli.Command{Name: "permissions", Commands: []*cli.Command{
		NewListCommand(), NewPendingCommand(), NewGrantsCommand(), NewPruneCommand(), NewApproveCommand(), NewDenyCommand(), NewRevokeCommand(), NewDeleteCommand(), NewExplainCommand(),
	}}
}

func newPresetTestCommand(configPath string) *cli.Command {
	var envPath string
	rootConfigPath := configPath
	return &cli.Command{
		Name:     "morph",
		Flags:    morphcli.RootFlags(&envPath, &rootConfigPath),
		Commands: []*cli.Command{NewPresetCommand()},
	}
}

func boolPointer(value bool) *bool {
	return &value
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
