package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/permissions"
)

func TestLoad_ParsesPermissionPolicy(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
permissions:
  preset: custom
  default: deny
  requestRetention: 720h
  grantRetention: 1440h
  cleanupInterval: 30m
  cleanupBatchSize: 75
  approvalRateLimit: 12
  approvalRateWindow: 2m
  surfaceKinds:
    gateway: deny
  surfaces:
    cli: ask
  rules:
    - name: owner workspace writes
      profiles: [work]
      actors: [local_owner]
      actorIds: [owner-1]
      surfaceKinds: [local]
      surfaces: [cli]
      tools: [write_file]
      resources: [file]
      actions: [update]
      effects: [write]
      targetScopes: [workspace]
      targetPrefixes: [workspace/]
      decision: allow
      reason: trusted workspace write
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.Equal(t, permissions.PresetCustom, cfg.Permissions.Preset)
	require.Equal(t, permissions.DecisionDeny, cfg.Permissions.SurfaceKindDefaults[permissions.SurfaceKindGateway])
	require.Equal(t, permissions.DecisionAsk, cfg.Permissions.SurfaceDefaults[permissions.SurfaceCLI])
	require.Equal(t, 30*24*time.Hour, cfg.Permissions.RequestRetention)
	require.Equal(t, 60*24*time.Hour, cfg.Permissions.GrantRetention)
	require.Equal(t, 30*time.Minute, cfg.Permissions.CleanupInterval)
	require.Equal(t, 75, cfg.Permissions.CleanupBatchSize)
	require.Equal(t, 12, cfg.Permissions.ApprovalRateLimit)
	require.Equal(t, 2*time.Minute, cfg.Permissions.ApprovalRateWindow)
	require.Equal(t, permissions.Rule{
		Name:           "owner workspace writes",
		Profiles:       []string{"work"},
		ActorKinds:     []permissions.ActorKind{permissions.ActorLocalOwner},
		ActorIDs:       []string{"owner-1"},
		SurfaceKinds:   []permissions.SurfaceKind{permissions.SurfaceKindLocal},
		Surfaces:       []permissions.Surface{permissions.SurfaceCLI},
		Tools:          []string{"write_file"},
		Resources:      []permissions.Resource{permissions.ResourceFile},
		Actions:        []permissions.Action{permissions.ActionUpdate},
		Effects:        []permissions.Effect{permissions.EffectWrite},
		TargetScopes:   []permissions.TargetScope{permissions.TargetScopeWorkspace},
		TargetPrefixes: []string{"workspace/"},
		Decision:       permissions.DecisionAllow,
		Reason:         "trusted workspace write",
	}, cfg.Permissions.Rules[0])
}

func TestConfig_NormalizePermissions(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Permissions = permissions.Policy{
		SurfaceDefaults: map[permissions.Surface]permissions.Decision{" CLI ": " ASK "},
		Rules:           []permissions.Rule{{Name: " owner ", Decision: " ALLOW "}},
	}

	cfg.Normalize()
	require.Equal(t, permissions.DecisionDeny, cfg.Permissions.Default)
	require.Equal(t, permissions.DecisionAsk, cfg.Permissions.SurfaceDefaults[permissions.SurfaceCLI])
	require.Equal(t, "owner", cfg.Permissions.Rules[0].Name)
	require.NoError(t, cfg.Permissions.Validate())
}

func TestConfig_ValidateRejectsInvalidPermissionPresetAndTargetScope(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Permissions.Preset = "automatic"
	require.EqualError(
		t,
		cfg.ValidateRelaxed(),
		"permission preset must be one of: ask, approve, full_access, custom",
	)

	cfg = NewDefaultConfig()
	cfg.Permissions.Rules = []permissions.Rule{{
		Name:         "invalid target scope",
		TargetScopes: []permissions.TargetScope{"computer"},
		Decision:     permissions.DecisionAllow,
	}}
	require.EqualError(t, cfg.ValidateRelaxed(), "permission rule contains an invalid target scope")
}

func TestConfig_ValidateRejectsInvalidPermissionRetention(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*permissions.Policy)
		want   string
	}{
		{name: "request retention", mutate: func(policy *permissions.Policy) { policy.RequestRetention = -time.Second }, want: "permission request retention must be greater than or equal to zero"},
		{name: "grant retention", mutate: func(policy *permissions.Policy) { policy.GrantRetention = -time.Second }, want: "permission grant retention must be greater than or equal to zero"},
		{name: "cleanup interval", mutate: func(policy *permissions.Policy) { policy.CleanupInterval = -time.Second }, want: "permission cleanup interval must be greater than zero"},
		{name: "cleanup batch", mutate: func(policy *permissions.Policy) { policy.CleanupBatchSize = -1 }, want: "permission cleanup batch size must be greater than zero"},
		{name: "approval rate limit", mutate: func(policy *permissions.Policy) { policy.ApprovalRateLimit = -1 }, want: "permission approval rate limit must be greater than zero"},
		{name: "approval rate window", mutate: func(policy *permissions.Policy) { policy.ApprovalRateWindow = -time.Second }, want: "permission approval rate window must be greater than zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := NewDefaultConfig()
			test.mutate(&cfg.Permissions)
			require.EqualError(t, cfg.ValidateRelaxed(), test.want)
		})
	}
}

func TestConfig_ValidateAcceptsFullAccessPreset(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Permissions.Preset = permissions.PresetFullAccess

	require.NoError(t, cfg.ValidateRelaxed())
}

func TestLoad_IgnoresUnknownPermissionMode(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
permissions:
  mode: full_access
  preset: ask
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, permissions.PresetAskForApproval, cfg.Permissions.Preset)
}

func TestNewDefaultConfig_ClonesPermissions(t *testing.T) {
	original := DefaultConfig.Permissions
	t.Cleanup(func() {
		DefaultConfig.Permissions = original
	})
	DefaultConfig.Permissions = permissions.Policy{
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{
			permissions.SurfaceKindLocal: permissions.DecisionAsk,
		},
		SurfaceDefaults: map[permissions.Surface]permissions.Decision{permissions.SurfaceCLI: permissions.DecisionAsk},
		Rules: []permissions.Rule{{
			Name:             "owner",
			Profiles:         []string{"work"},
			ActorKinds:       []permissions.ActorKind{permissions.ActorLocalOwner},
			ActorIDs:         []string{"owner-1"},
			ParentActorKinds: []permissions.ActorKind{permissions.ActorLocalOwner},
			SurfaceKinds:     []permissions.SurfaceKind{permissions.SurfaceKindLocal},
			Surfaces:         []permissions.Surface{permissions.SurfaceCLI},
			Tools:            []string{"read_file"},
			Resources:        []permissions.Resource{permissions.ResourceFile},
			Actions:          []permissions.Action{permissions.ActionRead},
			Effects:          []permissions.Effect{permissions.EffectRead},
			TargetScopes:     []permissions.TargetScope{permissions.TargetScopeWorkspace},
			TargetPrefixes:   []string{"workspace/"},
			Decision:         permissions.DecisionAllow,
		}},
	}

	first := NewDefaultConfig()
	second := NewDefaultConfig()
	first.Permissions.SurfaceDefaults[permissions.SurfaceCLI] = permissions.DecisionDeny
	first.Permissions.SurfaceKindDefaults[permissions.SurfaceKindLocal] = permissions.DecisionDeny
	first.Permissions.Rules[0].Profiles[0] = "other"
	first.Permissions.Rules[0].ActorKinds[0] = permissions.ActorGatewayUser
	first.Permissions.Rules[0].ActorIDs[0] = "owner-2"
	first.Permissions.Rules[0].ParentActorKinds[0] = permissions.ActorGatewayUser
	first.Permissions.Rules[0].SurfaceKinds[0] = permissions.SurfaceKindGateway
	first.Permissions.Rules[0].Surfaces[0] = permissions.SurfaceSlack
	first.Permissions.Rules[0].Tools[0] = "memory_search"
	first.Permissions.Rules[0].Resources[0] = permissions.ResourceMemory
	first.Permissions.Rules[0].Actions[0] = permissions.ActionDelete
	first.Permissions.Rules[0].Effects[0] = permissions.EffectDestructive
	first.Permissions.Rules[0].TargetScopes[0] = permissions.TargetScopeExternal
	first.Permissions.Rules[0].TargetPrefixes[0] = "outside/"

	require.Equal(t, permissions.DecisionAsk, second.Permissions.SurfaceDefaults[permissions.SurfaceCLI])
	require.Equal(t, permissions.DecisionAsk, second.Permissions.SurfaceKindDefaults[permissions.SurfaceKindLocal])
	require.Equal(t, "work", second.Permissions.Rules[0].Profiles[0])
	require.Equal(t, permissions.ActorLocalOwner, second.Permissions.Rules[0].ActorKinds[0])
	require.Equal(t, "owner-1", second.Permissions.Rules[0].ActorIDs[0])
	require.Equal(t, permissions.ActorLocalOwner, second.Permissions.Rules[0].ParentActorKinds[0])
	require.Equal(t, permissions.SurfaceKindLocal, second.Permissions.Rules[0].SurfaceKinds[0])
	require.Equal(t, permissions.SurfaceCLI, second.Permissions.Rules[0].Surfaces[0])
	require.Equal(t, "read_file", second.Permissions.Rules[0].Tools[0])
	require.Equal(t, permissions.ResourceFile, second.Permissions.Rules[0].Resources[0])
	require.Equal(t, permissions.ActionRead, second.Permissions.Rules[0].Actions[0])
	require.Equal(t, permissions.EffectRead, second.Permissions.Rules[0].Effects[0])
	require.Equal(t, permissions.TargetScopeWorkspace, second.Permissions.Rules[0].TargetScopes[0])
	require.Equal(t, "workspace/", second.Permissions.Rules[0].TargetPrefixes[0])
}
