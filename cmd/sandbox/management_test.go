package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/profile"
	morphruntime "github.com/xymorphic/morph/internal/runtime"
)

func TestSetup_LocalPreservesInactiveDockerConfiguration(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	contractPath := writeSandboxManagementContract(t, profileHome, "original.json", sandboxManagementContract())
	image := "registry.example/sandbox@sha256:" + strings.Repeat("a", 64)
	writeSandboxManagementConfig(t, configPath, config.ExecutionBackendDocker, image, contractPath)

	var output bytes.Buffer
	err := NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "setup", "--backend", "local", "--yes",
	})

	require.NoError(t, err)
	cfg, loadErr := config.Load("", configPath)
	require.NoError(t, loadErr)
	require.Equal(t, config.ExecutionBackendLocal, cfg.Execution.Backend)
	require.Equal(t, image, cfg.Execution.Docker.Image)
	require.Equal(t, contractPath, cfg.Execution.Docker.Contract)
	require.Contains(t, output.String(), "Configured local execution")

	output.Reset()
	err = NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "setup", "--backend", "local", "--yes",
	})
	require.NoError(t, err)
	require.Contains(t, output.String(), "Already configured local execution")
}

func TestSetup_NonInteractiveRequiresCompleteInputs(t *testing.T) {
	setSandboxManagementProfile(t)

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "setup", "--non-interactive",
	})

	require.EqualError(t, err, "execution backend is required; use --backend local or --backend docker")
}

func TestSetup_InteractiveBackendDefaultsToConfiguredValue(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	contractPath := writeSandboxManagementContract(t, profileHome, "contract.json", sandboxManagementContract())
	writeSandboxManagementConfig(
		t,
		configPath,
		config.ExecutionBackendDocker,
		"registry.example/sandbox@sha256:"+strings.Repeat("a", 64),
		contractPath,
	)
	var output bytes.Buffer
	originalCheckCosign := checkSetupCosignPrerequisite
	checkSetupCosignPrerequisite = func() error {
		return errors.New("signature verification was selected")
	}
	t.Cleanup(func() { checkSetupCosignPrerequisite = originalCheckCosign })

	err := NewCommandWithIO(strings.NewReader("\n\n"), &output).Run(context.Background(), []string{
		"sandbox", "setup",
	})

	require.Error(t, err)
	require.NotContains(t, err.Error(), "signature verification was selected")
	require.Contains(t, output.String(), "Execution backend [local/docker] [docker]: ")
	require.Contains(
		t,
		output.String(),
		"Sandbox image or official release [registry.example/sandbox@sha256:"+strings.Repeat("a", 64)+"]: ",
	)
}

func TestGetSetupActiveContract_PreservesConfiguredContractForSameImage(t *testing.T) {
	profileHome := t.TempDir()
	image := "registry.example/sandbox@sha256:" + strings.Repeat("b", 64)
	releaseContract := sandboxManagementContract()
	configuredContract := releaseContract
	configuredContract.Shell = "/usr/bin/sh"
	configuredContract.Executables = map[string]string{"sh": "/usr/bin/sh"}
	contractPath := writeSandboxManagementContract(t, profileHome, "configured.json", configuredContract)
	cfg := config.NewProfileConfig()
	cfg.Execution.Docker.Image = image
	cfg.Execution.Docker.Contract = contractPath

	activeContract, err := getSetupActiveContract(cfg, image, releaseContract)

	require.NoError(t, err)
	require.Equal(t, configuredContract.Digest(), activeContract.Digest())
}

func TestGetSetupActiveContract_UsesReleaseContractForNewImage(t *testing.T) {
	profileHome := t.TempDir()
	configuredImage := "registry.example/sandbox@sha256:" + strings.Repeat("c", 64)
	selectedImage := "registry.example/sandbox@sha256:" + strings.Repeat("d", 64)
	releaseContract := sandboxManagementContract()
	configuredContract := releaseContract
	configuredContract.Shell = "/usr/bin/sh"
	configuredContract.Executables = map[string]string{"sh": "/usr/bin/sh"}
	contractPath := writeSandboxManagementContract(t, profileHome, "configured.json", configuredContract)
	cfg := config.NewProfileConfig()
	cfg.Execution.Docker.Image = configuredImage
	cfg.Execution.Docker.Contract = contractPath

	activeContract, err := getSetupActiveContract(cfg, selectedImage, releaseContract)

	require.NoError(t, err)
	require.Equal(t, releaseContract.Digest(), activeContract.Digest())
}

func TestContract_ImportShowAndResetLifecycle(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	image := "ghcr.io/xymorphic/morph-sandbox@sha256:" + strings.Repeat("b", 64)
	release := sandboxManagementContract()
	store := execution.NewContractStore(profileHome)
	releasePath, err := store.SaveRelease(image, release)
	require.NoError(t, err)
	writeSandboxManagementConfig(t, configPath, config.ExecutionBackendLocal, image, releasePath)

	custom := release
	custom.Shell = "/usr/bin/sh"
	custom.Executables = map[string]string{"sh": "/usr/bin/sh"}
	customPath := writeSandboxManagementContract(t, profileHome, "candidate.json", custom)
	originalCheck := checkContractForActivation
	checkContractForActivation = func(context.Context, *config.Config, execution.ImageContract) error { return nil }
	t.Cleanup(func() { checkContractForActivation = originalCheck })
	var output bytes.Buffer
	err = NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "contract", "import", "--from", customPath,
	})
	require.NoError(t, err)
	require.Contains(t, output.String(), "Activated user-managed contract")

	output.Reset()
	err = NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "--json", "contract", "show",
	})
	require.NoError(t, err)
	var details contractDetails
	require.NoError(t, json.Unmarshal(output.Bytes(), &details))
	require.Equal(t, execution.ContractProvenanceCustom, details.Provenance.Kind)
	require.Equal(t, custom.Digest(), details.Provenance.ActiveDigest)
	require.Equal(t, release.Digest(), details.Provenance.OriginalDigest)

	output.Reset()
	err = NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "contract", "reset", "--yes",
	})
	require.NoError(t, err)
	cfg, loadErr := config.Load("", configPath)
	require.NoError(t, loadErr)
	require.NotEqual(t, releasePath, cfg.Execution.Docker.Contract)
	resetContract, loadErr := loadSandboxManagementContract(cfg.Execution.Docker.Contract)
	require.NoError(t, loadErr)
	require.Equal(t, release.Digest(), resetContract.Digest())
	require.Contains(t, output.String(), "Restored release-provided contract")
}

func TestContract_MutatingCommandsRejectStructuralOnlyValidation(t *testing.T) {
	profileHome, _ := setSandboxManagementProfile(t)
	candidate := writeSandboxManagementContract(t, profileHome, "candidate.json", sandboxManagementContract())

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "contract", "import", "--from", candidate, "--structural-only",
	})
	require.EqualError(t, err, "--structural-only cannot be used when activating a contract")

	err = NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "contract", "edit", "--structural-only",
	})
	require.EqualError(t, err, "--structural-only cannot be used when activating a contract")
}

func TestContract_ImportValidationFailureLeavesActiveContractUnchanged(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	image := "registry.example/sandbox@sha256:" + strings.Repeat("e", 64)
	activePath := writeSandboxManagementContract(t, profileHome, "active.json", sandboxManagementContract())
	writeSandboxManagementConfig(t, configPath, config.ExecutionBackendDocker, image, activePath)
	candidate := sandboxManagementContract()
	candidate.Shell = "/usr/bin/sh"
	candidate.Executables = map[string]string{"sh": "/usr/bin/sh"}
	candidatePath := writeSandboxManagementContract(t, profileHome, "candidate.json", candidate)
	originalCheck := checkContractForActivation
	checkContractForActivation = func(context.Context, *config.Config, execution.ImageContract) error {
		return errors.New("image-backed validation failed")
	}
	t.Cleanup(func() { checkContractForActivation = originalCheck })

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "contract", "import", "--from", candidatePath,
	})

	require.EqualError(t, err, "image-backed validation failed")
	cfg, loadErr := config.Load("", configPath)
	require.NoError(t, loadErr)
	require.Equal(t, activePath, cfg.Execution.Docker.Contract)
}

func TestContract_ValidateRejectsUnsupportedCompatibilityVersion(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	contract := sandboxManagementContract()
	contract.Version = "2"
	contractPath := writeSandboxManagementContract(t, profileHome, "candidate.json", contract)
	writeSandboxManagementConfig(
		t,
		configPath,
		config.ExecutionBackendLocal,
		"registry.example/sandbox@sha256:"+strings.Repeat("c", 64),
		contractPath,
	)

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "contract", "validate", contractPath, "--structural-only",
	})

	require.ErrorContains(t, err, "runtime compatibility \"2\" is unsupported")
}

func TestSetup_JSONRequiresExplicitNonInteractiveConfirmation(t *testing.T) {
	setSandboxManagementProfile(t)

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "--json", "setup", "--backend", "local", "--yes",
	})

	require.EqualError(t, err, "--json setup requires --non-interactive and --yes")
}

func TestSetup_JSONLocalResultContainsNoPrompts(t *testing.T) {
	_, configPath := setSandboxManagementProfile(t)
	writeSandboxManagementConfig(t, configPath, config.ExecutionBackendLocal, "", "")
	var output bytes.Buffer

	err := NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "--json", "setup", "--backend", "local", "--non-interactive", "--yes",
	})

	require.NoError(t, err)
	var result setupResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.False(t, result.Changed)
	require.Equal(t, config.ExecutionBackendLocal, result.Backend)
	require.Equal(t, "no reload needed", result.DaemonApplication)
	require.Empty(t, result.Warnings)
}

func TestSetup_RejectsNonPositiveTimeout(t *testing.T) {
	setSandboxManagementProfile(t)

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "setup", "--backend", "local", "--yes", "--timeout", "0s",
	})

	require.EqualError(t, err, "--timeout must be greater than zero")
}

func TestSetup_LocalRejectsDockerOnlyFlags(t *testing.T) {
	setSandboxManagementProfile(t)

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "setup", "--backend", "local", "--release", "v1", "--yes",
	})

	require.EqualError(t, err, "--release is only valid with --backend docker")
}

func TestContract_JSONMutationsRequireNonPromptingFlags(t *testing.T) {
	setSandboxManagementProfile(t)

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "--json", "contract", "create",
	})
	require.EqualError(t, err, "--json contract create requires --non-interactive and --yes")

	err = NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "--json", "contract", "edit",
	})
	require.EqualError(t, err, "--json contract edit requires --no-retry")

	err = NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "--json", "contract", "reset",
	})
	require.EqualError(t, err, "--json contract reset requires --yes")
}

func TestSetup_DoesNotTreatSameDaemonGenerationAsApplied(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	contractPath := writeSandboxManagementContract(t, profileHome, "original.json", sandboxManagementContract())
	image := "registry.example/sandbox@sha256:" + strings.Repeat("d", 64)
	writeSandboxManagementConfig(t, configPath, config.ExecutionBackendDocker, image, contractPath)
	originalProbe := probeSandboxRuntime
	originalInterval := setupObservationInterval
	startedAt := time.Now().UTC()
	probeSandboxRuntime = func(context.Context, profile.Profile) morphruntime.ProbeResult {
		return morphruntime.ProbeResult{
			State: morphruntime.ProbeStateReady,
			Metadata: morphruntime.Metadata{
				StartedAt: startedAt,
			},
		}
	}
	setupObservationInterval = time.Millisecond
	t.Cleanup(func() {
		probeSandboxRuntime = originalProbe
		setupObservationInterval = originalInterval
	})

	err := NewCommandWithIO(strings.NewReader(""), &bytes.Buffer{}).Run(context.Background(), []string{
		"sandbox", "setup", "--backend", "local", "--yes", "--timeout", "10ms",
	})

	require.ErrorContains(t, err, "daemon observation failed: context deadline exceeded")
}

func TestContract_ValidateSupportsStructuralOnlyCandidate(t *testing.T) {
	profileHome, configPath := setSandboxManagementProfile(t)
	contract := sandboxManagementContract()
	contractPath := writeSandboxManagementContract(t, profileHome, "candidate.json", contract)
	writeSandboxManagementConfig(
		t,
		configPath,
		config.ExecutionBackendLocal,
		"registry.example/sandbox@sha256:"+strings.Repeat("c", 64),
		contractPath,
	)

	var output bytes.Buffer
	err := NewCommandWithIO(strings.NewReader(""), &output).Run(context.Background(), []string{
		"sandbox", "contract", "validate", contractPath, "--structural-only",
	})

	require.NoError(t, err)
	require.Equal(t, "Contract valid: "+contract.Digest()+"\n", output.String())
}

func setSandboxManagementProfile(t *testing.T) (string, string) {
	t.Helper()
	original := profile.Active()
	profileHome := t.TempDir()
	resolved := profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: profileHome})
	profile.SetActive(resolved)
	t.Cleanup(func() { profile.SetActive(original) })
	return profileHome, resolved.ConfigPath
}

func writeSandboxManagementConfig(
	t *testing.T,
	configPath string,
	backend string,
	image string,
	contractPath string,
) {
	t.Helper()
	cfg := config.NewProfileConfig()
	cfg.Name = "test"
	cfg.Execution.Backend = backend
	cfg.Execution.Docker.Image = image
	cfg.Execution.Docker.ImageVerification = config.ExecutionImageVerificationDigest
	cfg.Execution.Docker.Contract = contractPath
	require.NoError(t, config.SaveYAML(configPath, cfg))
}

func writeSandboxManagementContract(
	t *testing.T,
	profileHome string,
	name string,
	contract execution.ImageContract,
) string {
	t.Helper()
	path := filepath.Join(profileHome, name)
	data, err := json.MarshalIndent(contract, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
	return path
}

func loadSandboxManagementContract(path string) (execution.ImageContract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return execution.ImageContract{}, err
	}
	var contract execution.ImageContract
	if err := json.Unmarshal(data, &contract); err != nil {
		return execution.ImageContract{}, err
	}
	return contract.Normalize()
}

func sandboxManagementContract() execution.ImageContract {
	return execution.ImageContract{
		Version:       "1",
		GOOS:          "linux",
		Architectures: []string{"amd64", "arm64"},
		User:          "65532:65532",
		Shell:         "/bin/sh",
		PATH:          []string{"/bin", "/usr/bin"},
		Executables:   map[string]string{"sh": "/bin/sh"},
		Helper:        "/usr/local/bin/morph-sandbox",
		WorkspacePath: "/workspace",
		HomePath:      "/home/morph",
		TemporaryPath: "/tmp",
		ControlPath:   "/run/morph",
	}
}
