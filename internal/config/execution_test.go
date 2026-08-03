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

func TestNormalizeExecution_DefaultsAndNormalizesImageVerification(t *testing.T) {
	cfg := Config{}

	cfg.normalizeExecution()

	require.Equal(
		t,
		ExecutionImageVerificationSignature,
		cfg.Execution.Docker.ImageVerification,
	)

	cfg.Execution.Docker.ImageVerification = "  DIGEST  "
	cfg.normalizeExecution()

	require.Equal(
		t,
		ExecutionImageVerificationDigest,
		cfg.Execution.Docker.ImageVerification,
	)
}

func TestValidateExecution_RejectsUnknownImageVerification(t *testing.T) {
	cfg := validDockerExecutionConfig()
	cfg.Execution.Docker.ImageVerification = "checksum"

	require.EqualError(
		t,
		cfg.validateExecution(),
		"execution docker image verification must be signature or digest",
	)
}

func TestValidateExecution_AcceptsSupportedImageVerification(t *testing.T) {
	for _, mode := range []string{
		ExecutionImageVerificationSignature,
		ExecutionImageVerificationDigest,
	} {
		t.Run(mode, func(t *testing.T) {
			cfg := validDockerExecutionConfig()
			cfg.Execution.Docker.ImageVerification = mode

			require.NoError(t, cfg.validateExecution())
		})
	}
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
			ImageVerification: ExecutionImageVerificationSignature,
			Contract:          "/contract.json",
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
			ImageVerification: ExecutionImageVerificationSignature,
			Contract:          "/contract.json",
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

func validDockerExecutionConfig() Config {
	return Config{Execution: ExecutionConfig{
		Backend: ExecutionBackendDocker,
		Docker: DockerExecutionConfig{
			Scope:             ExecutionScopeSession,
			Endpoint:          "/var/run/docker.sock",
			Image:             "example.com/morph-sandbox@sha256:" + strings.Repeat("a", 64),
			ImageVerification: ExecutionImageVerificationSignature,
			Contract:          "/contract.json",
			Workspace: ExecutionWorkspaceConfig{
				Mode: ExecutionWorkspaceNone,
			},
			Network: ExecutionNetworkNone,
		},
	}}
}
