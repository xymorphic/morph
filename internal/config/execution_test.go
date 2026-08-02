package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeExecution_NormalizesSecretCatalogMetadata(t *testing.T) {
	cfg := Config{
		Execution: ExecutionConfig{
			Docker: DockerExecutionConfig{
				Secrets: []ExecutionSecretConfig{
					{
						Name:        "  DEPLOYMENT_TOKEN  ",
						Env:         "  SANDBOX_DEPLOYMENT_TOKEN  ",
						Description: "  Deploy staging  ",
					},
				},
			},
		},
	}

	cfg.normalizeExecution()

	require.Equal(
		t,
		[]ExecutionSecretConfig{
			{
				Name:        "deployment_token",
				Env:         "SANDBOX_DEPLOYMENT_TOKEN",
				Description: "Deploy staging",
			},
		},
		cfg.Execution.Docker.Secrets,
	)
}

func TestValidateExecution_RequiresSecretDescription(t *testing.T) {
	cfg := Config{Execution: ExecutionConfig{
		Backend: ExecutionBackendDocker,
		Docker: DockerExecutionConfig{
			Scope:    ExecutionScopeSession,
			Endpoint: "/var/run/docker.sock",
			Image: "example.com/morph-sandbox@sha256:" + strings.Repeat(
				"a",
				64,
			),
			Contract: "/contract.json",
			Workspace: ExecutionWorkspaceConfig{
				Mode: ExecutionWorkspaceNone,
			},
			Network: ExecutionNetworkNone,
			Secrets: []ExecutionSecretConfig{
				{
					Name: "deployment_token",
					Env:  "SANDBOX_DEPLOYMENT_TOKEN",
				},
			},
		},
	}}

	require.EqualError(
		t,
		cfg.validateExecution(),
		`execution docker secret "deployment_token" is invalid`,
	)
}

func TestValidateExecution_AllowsAnyConfiguredSecretEnvironmentSource(t *testing.T) {
	cfg := Config{Execution: ExecutionConfig{
		Backend: ExecutionBackendDocker,
		Docker: DockerExecutionConfig{
			Scope:    ExecutionScopeSession,
			Endpoint: "/var/run/docker.sock",
			Image: "example.com/morph-sandbox@sha256:" + strings.Repeat(
				"a",
				64,
			),
			Contract: "/contract.json",
			Workspace: ExecutionWorkspaceConfig{
				Mode: ExecutionWorkspaceNone,
			},
			Network: ExecutionNetworkNone,
			Secrets: []ExecutionSecretConfig{
				{
					Name:        "morph_auth",
					Env:         "MORPH_AUTH_TOKEN",
					Description: "Use the configured Morph identity",
				},
				{
					Name:        "provider",
					Env:         "OPENAI_API_KEY",
					Description: "Call the configured model provider",
				},
			},
		},
	}}

	require.NoError(t, cfg.validateExecution())
}
