package sandbox

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/execution"
	dockerexec "github.com/xymorphic/morph/internal/execution/docker"
	"github.com/xymorphic/morph/internal/fileedit"
	morphruntime "github.com/xymorphic/morph/internal/runtime"
)

var (
	checkSetupCosignPrerequisite = dockerexec.CheckCosignPrerequisite
	probeSandboxRuntime          = morphruntime.Probe
	setupObservationInterval     = 100 * time.Millisecond
)

type setupResult struct {
	Changed            bool     `json:"changed"`
	Profile            string   `json:"profile"`
	Backend            string   `json:"backend"`
	Scope              string   `json:"scope,omitempty"`
	SelectedImage      string   `json:"selected_image,omitempty"`
	Image              string   `json:"image,omitempty"`
	Verification       string   `json:"verification,omitempty"`
	VerificationResult string   `json:"verification_result,omitempty"`
	Contract           string   `json:"contract,omitempty"`
	ContractDigest     string   `json:"contract_digest,omitempty"`
	ContractProvenance string   `json:"contract_provenance,omitempty"`
	WorkspaceMode      string   `json:"workspace_mode,omitempty"`
	Network            string   `json:"network,omitempty"`
	MountCount         int      `json:"mount_count"`
	SecretCount        int      `json:"secret_count"`
	DaemonApplication  string   `json:"daemon_application"`
	RecoveryPath       string   `json:"recovery_path,omitempty"`
	Checks             []string `json:"checks"`
	IntendedChanges    []string `json:"intended_changes"`
	Warnings           []string `json:"warnings"`
}

func newSetupCommand(input io.Reader, writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Configure a local or Docker command execution backend",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "backend", Usage: "Execution backend: local or docker"},
			&cli.StringFlag{Name: "release", Usage: "Official sandbox image release"},
			&cli.StringFlag{Name: "image", Usage: "Custom image reference; mutually exclusive with --release"},
			&cli.StringFlag{Name: "verification", Usage: "Image verification: signature or digest"},
			&cli.StringFlag{Name: "endpoint", Usage: "Local Docker socket or named pipe"},
			&cli.StringFlag{Name: "scope", Usage: "Docker environment scope: session or shared"},
			&cli.StringFlag{Name: "workspace-mode", Usage: "Workspace mode: none, ro, or rw"},
			&cli.StringFlag{Name: "workspace-source", Usage: "Absolute host workspace source for ro or rw mode"},
			&cli.StringFlag{Name: "network", Usage: "Docker network mode: none or bridge"},
			&cli.BoolFlag{Name: "accept-digest", Usage: "Accept the digest resolved from --release in digest mode"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Apply the displayed configuration without confirmation"},
			&cli.BoolFlag{Name: "non-interactive", Usage: "Fail instead of prompting for missing choices"},
			&cli.DurationFlag{Name: "timeout", Value: 5 * time.Minute, Usage: "Maximum time for Docker setup and daemon observation"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Duration("timeout") <= 0 {
				return errors.New("--timeout must be greater than zero")
			}
			if cmd.Bool("json") && (!cmd.Bool("non-interactive") || !cmd.Bool("yes")) {
				return errors.New("--json setup requires --non-interactive and --yes")
			}
			cfg, inputs, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			configSnapshot, err := fileedit.ReadSnapshot(inputs.ConfigPath, nil)
			if err != nil {
				return fmt.Errorf("read profile configuration: %w", err)
			}
			backend, err := resolveSetupBackend(input, writer, cmd, cfg.Execution.Backend)
			if err != nil {
				return err
			}
			if backend == config.ExecutionBackendLocal {
				if err := checkLocalSetupFlags(cmd); err != nil {
					return err
				}
				setupCtx, cancel := context.WithTimeout(ctx, cmd.Duration("timeout"))
				defer cancel()
				return runLocalSetup(setupCtx, input, writer, cmd, inputs, configSnapshot)
			}
			setupCtx, cancel := context.WithTimeout(ctx, cmd.Duration("timeout"))
			defer cancel()
			return runDockerSetup(setupCtx, input, writer, cmd, cfg, inputs, configSnapshot)
		},
	}
}

func resolveSetupBackend(
	input io.Reader,
	writer io.Writer,
	cmd *cli.Command,
	configuredBackend string,
) (string, error) {
	backend := strings.TrimSpace(strings.ToLower(cmd.String("backend")))
	if backend == "" && !cmd.Bool("non-interactive") {
		defaultBackend := strings.TrimSpace(strings.ToLower(configuredBackend))
		if defaultBackend != config.ExecutionBackendLocal && defaultBackend != config.ExecutionBackendDocker {
			defaultBackend = config.ExecutionBackendLocal
		}
		value, err := promptSetupValue(input, writer, "Execution backend [local/docker]", defaultBackend)
		if err != nil {
			return "", err
		}
		backend = strings.ToLower(value)
	}
	if backend == "" {
		return "", errors.New("execution backend is required; use --backend local or --backend docker")
	}
	if backend != config.ExecutionBackendLocal && backend != config.ExecutionBackendDocker {
		return "", errors.New("execution backend must be local or docker")
	}
	return backend, nil
}

func runLocalSetup(
	ctx context.Context,
	input io.Reader,
	writer io.Writer,
	cmd *cli.Command,
	inputs morphcli.ConfigInputs,
	configSnapshot fileedit.Snapshot,
) error {
	if !cmd.Bool("yes") {
		if cmd.Bool("non-interactive") {
			return errors.New("--yes is required to apply setup non-interactively")
		}
		if !promptContractConfirmation(input, writer, "Set the active execution backend to local?") {
			return errors.New("sandbox setup cancelled")
		}
	}
	oldValues, err := morphcli.GetConfigValues(inputs.EnvPath, inputs.ConfigPath, []string{"execution.backend"})
	if err != nil {
		return err
	}
	changed := len(oldValues) == 0 || oldValues[0].Value != config.ExecutionBackendLocal
	probeBefore := probeSandboxRuntime(ctx, inputs.Profile)
	recoveryPath := ""
	if changed {
		recoveryPath, err = preserveSetupRecovery(inputs)
		if err != nil {
			return err
		}
		if _, err := config.SetConfigValuesRelaxedIfUnchanged(
			inputs.EnvPath,
			inputs.ConfigPath,
			[]config.ConfigUpdate{{Path: "execution.backend", Value: config.ExecutionBackendLocal}},
			configSnapshot,
		); err != nil {
			return err
		}
	}
	daemonApplication, err := getDaemonApplication(ctx, inputs, changed, probeBefore, recoveryPath)
	if err != nil {
		return err
	}
	result := setupResult{
		Changed:           changed,
		Profile:           inputs.Profile.Name,
		Backend:           config.ExecutionBackendLocal,
		DaemonApplication: daemonApplication,
		RecoveryPath:      recoveryPath,
		Checks:            []string{"profile configuration valid"},
		IntendedChanges:   getConfigUpdatePaths([]config.ConfigUpdate{{Path: "execution.backend"}}),
		Warnings:          getSetupReloadWarnings(changed),
	}
	return writeSetupResult(writer, cmd.Bool("json"), result)
}

func runDockerSetup(
	ctx context.Context,
	input io.Reader,
	writer io.Writer,
	cmd *cli.Command,
	cfg *config.Config,
	inputs morphcli.ConfigInputs,
	configSnapshot fileedit.Snapshot,
) error {
	if strings.TrimSpace(cmd.String("release")) != "" && strings.TrimSpace(cmd.String("image")) != "" {
		return errors.New("--release and --image are mutually exclusive")
	}
	selectedImage, digestAcceptanceRequired, err := resolveSetupImage(
		input,
		writer,
		cmd,
		cfg.Execution.Docker.Image,
	)
	if err != nil {
		return err
	}
	verification, err := resolveSetupVerification(cmd, cfg.Execution.Docker.ImageVerification)
	if err != nil {
		return err
	}
	if verification == config.ExecutionImageVerificationSignature {
		if err := checkSetupCosignPrerequisite(); err != nil {
			return fmt.Errorf("image verification prerequisite failed: %w", err)
		}
	}
	endpoint := strings.TrimSpace(cmd.String("endpoint"))
	if endpoint == "" {
		endpoint = cfg.Execution.Docker.Endpoint
	}
	if err := dockerexec.CheckLocalEndpoint(endpoint); err != nil {
		return fmt.Errorf("docker preflight failed: %w", err)
	}
	dockerClient, err := getLinuxDockerClient(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("docker preflight failed: %w", err)
	}
	defer func() { _ = dockerClient.Close() }()

	resolvedImage, err := dockerexec.ResolveImageReference(ctx, dockerClient, selectedImage)
	if err != nil {
		return fmt.Errorf("release resolution failed: %w", err)
	}
	if verification == config.ExecutionImageVerificationDigest && digestAcceptanceRequired && !cmd.Bool("accept-digest") {
		if cmd.Bool("non-interactive") {
			return fmt.Errorf("--accept-digest is required for resolved digest %s", resolvedImage)
		}
		if !promptContractConfirmation(input, writer, "Trust resolved digest "+resolvedImage+"?") {
			return errors.New("sandbox digest was not accepted")
		}
	}
	if verification == config.ExecutionImageVerificationSignature {
		if err := dockerexec.VerifyImageSignature(ctx, resolvedImage); err != nil {
			return fmt.Errorf("image verification failed: %w", err)
		}
	}
	if err := dockerexec.PullImage(ctx, dockerClient, resolvedImage); err != nil {
		return err
	}
	contractData, err := dockerexec.ExtractImageFile(ctx, dockerClient, resolvedImage, dockerexec.SandboxContractPath)
	if err != nil {
		return fmt.Errorf("contract extraction failed: %w", err)
	}
	var contract execution.ImageContract
	if err := json.Unmarshal(contractData, &contract); err != nil {
		return fmt.Errorf("parse embedded sandbox contract: %w", err)
	}
	contract, err = contract.Normalize()
	if err != nil {
		return fmt.Errorf("contract validation failed: %w", err)
	}
	if err := dockerexec.CheckImageContractCompatibility(contract); err != nil {
		return fmt.Errorf("contract validation failed: %w", err)
	}
	if err := dockerexec.CheckImageContract(ctx, dockerClient, resolvedImage, contract); err != nil {
		return fmt.Errorf("contract validation failed: %w", err)
	}

	store := execution.NewContractStore(inputs.Profile.HomeDir)
	_, err = store.SaveRelease(resolvedImage, contract)
	if err != nil {
		return err
	}
	activeContract, err := getSetupActiveContract(cfg, resolvedImage, contract)
	if err != nil {
		return err
	}
	if activeContract.Digest() != contract.Digest() {
		if err := dockerexec.CheckImageContract(ctx, dockerClient, resolvedImage, activeContract); err != nil {
			return fmt.Errorf("configured contract validation failed: %w", err)
		}
	}
	contractPath, err := store.ActivePath(resolvedImage, activeContract)
	if err != nil {
		return err
	}
	updates, effective, err := getDockerSetupUpdates(cmd, cfg, endpoint, resolvedImage, verification, inputs, contractPath)
	if err != nil {
		return err
	}
	configChanged, err := configUpdatesChangeValues(inputs, updates)
	if err != nil {
		return err
	}
	activeReady := isActiveContractReady(contractPath, activeContract)
	changed := configChanged || !activeReady
	if !cmd.Bool("yes") {
		if cmd.Bool("non-interactive") {
			return errors.New("--yes is required to apply setup non-interactively")
		}
		if err := writeDockerSetupPreview(writer, selectedImage, resolvedImage, verification, activeContract, effective); err != nil {
			return err
		}
		if !promptContractConfirmation(input, writer, "Apply this sandbox configuration?") {
			return errors.New("sandbox setup cancelled")
		}
	}
	recoveryPath := ""
	probeBefore := probeSandboxRuntime(ctx, inputs.Profile)
	if configChanged {
		recoveryPath, err = preserveSetupRecovery(inputs)
		if err != nil {
			return err
		}
	}
	if !activeReady {
		if _, err := store.SaveActive(resolvedImage, activeContract); err != nil {
			return fmt.Errorf("activate sandbox contract: %w", err)
		}
	}
	if configChanged {
		if _, err := config.SetConfigValuesRelaxedIfUnchanged(
			inputs.EnvPath,
			inputs.ConfigPath,
			updates,
			configSnapshot,
		); err != nil {
			return fmt.Errorf("activate sandbox configuration: %w", err)
		}
	}
	daemonApplication, err := getDaemonApplication(ctx, inputs, configChanged, probeBefore, recoveryPath)
	if err != nil {
		return err
	}
	provenance, err := store.Describe(resolvedImage, contractPath)
	if err != nil {
		return fmt.Errorf("describe sandbox contract provenance: %w", err)
	}
	result := setupResult{
		Changed:            changed,
		Profile:            inputs.Profile.Name,
		Backend:            config.ExecutionBackendDocker,
		Scope:              effective.Scope,
		SelectedImage:      selectedImage,
		Image:              resolvedImage,
		Verification:       verification,
		VerificationResult: getVerificationResult(verification),
		Contract:           contractPath,
		ContractDigest:     activeContract.Digest(),
		ContractProvenance: provenance.Kind,
		WorkspaceMode:      effective.Workspace.Mode,
		Network:            effective.Network,
		MountCount:         len(effective.Mounts),
		SecretCount:        len(effective.Secrets),
		DaemonApplication:  daemonApplication,
		RecoveryPath:       recoveryPath,
		Checks: []string{
			"Docker engine reachable with Linux containers",
			"image resolved to immutable digest",
			"image trust verified",
			"embedded contract extracted and validated",
			"profile configuration valid",
		},
		IntendedChanges: getConfigUpdatePaths(updates),
		Warnings: append(
			getDockerSetupWarnings(effective, verification),
			getSetupReloadWarnings(configChanged)...,
		),
	}
	return writeSetupResult(writer, cmd.Bool("json"), result)
}

func getSetupActiveContract(
	cfg *config.Config,
	resolvedImage string,
	releaseContract execution.ImageContract,
) (execution.ImageContract, error) {
	if strings.TrimSpace(cfg.Execution.Docker.Image) != resolvedImage ||
		strings.TrimSpace(cfg.Execution.Docker.Contract) == "" {
		return releaseContract, nil
	}
	configuredContract, err := dockerexec.LoadImageContract(cfg.Execution.Docker.Contract)
	if err != nil {
		return execution.ImageContract{}, fmt.Errorf("load configured sandbox contract: %w", err)
	}
	if err := dockerexec.CheckImageContractCompatibility(configuredContract); err != nil {
		return execution.ImageContract{}, fmt.Errorf("configured contract validation failed: %w", err)
	}
	return configuredContract, nil
}

func resolveSetupImage(
	input io.Reader,
	writer io.Writer,
	cmd *cli.Command,
	configuredImage string,
) (string, bool, error) {
	release := strings.TrimSpace(cmd.String("release"))
	image := strings.TrimSpace(cmd.String("image"))
	if release == "" && image == "" && !cmd.Bool("non-interactive") {
		configuredImage = strings.TrimSpace(configuredImage)
		label := "Official sandbox release"
		if configuredImage != "" {
			label = "Sandbox image or official release"
		}
		value, err := promptSetupValue(input, writer, label, configuredImage)
		if err != nil {
			return "", false, err
		}
		if value == configuredImage || strings.ContainsAny(value, "/@:") {
			image = value
		} else {
			release = value
		}
	}
	if release != "" {
		if strings.ContainsAny(release, "@/ \t\r\n") {
			return "", false, fmt.Errorf("invalid sandbox release %q", release)
		}
		return dockerexec.SandboxRepository + ":" + release, true, nil
	}
	if image == "" {
		return "", false, errors.New("sandbox release or image is required; use --release or --image")
	}
	return image, dockerexec.ValidateImageReference(image) != nil, nil
}

func resolveSetupVerification(cmd *cli.Command, configuredVerification string) (string, error) {
	verification := strings.TrimSpace(strings.ToLower(cmd.String("verification")))
	if verification == "" {
		verification = strings.TrimSpace(strings.ToLower(configuredVerification))
		if verification == "" {
			verification = config.ExecutionImageVerificationSignature
		}
	}
	if verification != config.ExecutionImageVerificationSignature && verification != config.ExecutionImageVerificationDigest {
		return "", errors.New("image verification must be signature or digest")
	}
	return verification, nil
}

func getDockerSetupUpdates(
	cmd *cli.Command,
	cfg *config.Config,
	endpoint string,
	image string,
	verification string,
	inputs morphcli.ConfigInputs,
	contractPath string,
) ([]config.ConfigUpdate, config.DockerExecutionConfig, error) {
	effective := cfg.Execution.Docker
	newDockerConfiguration := strings.TrimSpace(effective.Image) == ""
	if newDockerConfiguration {
		effective.Scope = config.ExecutionScopeSession
		effective.Workspace = config.ExecutionWorkspaceConfig{Mode: config.ExecutionWorkspaceNone}
		effective.Network = config.ExecutionNetworkNone
		effective.Mounts = nil
		effective.Secrets = nil
	}
	if cmd.IsSet("scope") {
		effective.Scope = strings.TrimSpace(strings.ToLower(cmd.String("scope")))
	}
	if cmd.IsSet("workspace-mode") {
		effective.Workspace.Mode = strings.TrimSpace(strings.ToLower(cmd.String("workspace-mode")))
	}
	if cmd.IsSet("workspace-source") {
		effective.Workspace.Source = strings.TrimSpace(cmd.String("workspace-source"))
	}
	if cmd.IsSet("network") {
		effective.Network = strings.TrimSpace(strings.ToLower(cmd.String("network")))
	}
	if effective.Workspace.Mode == config.ExecutionWorkspaceNone {
		effective.Workspace.Source = ""
	}

	updates := []config.ConfigUpdate{
		{Path: "execution.backend", Value: config.ExecutionBackendDocker},
		{Path: "execution.docker.endpoint", Value: endpoint},
		{Path: "execution.docker.image", Value: image},
		{Path: "execution.docker.imageVerification", Value: verification},
		{Path: "execution.docker.contract", Value: profileRelativePath(inputs.Profile.HomeDir, contractPath)},
		{Path: "execution.docker.scope", Value: effective.Scope},
		{Path: "execution.docker.workspace.mode", Value: effective.Workspace.Mode},
		{Path: "execution.docker.workspace.source", Value: effective.Workspace.Source},
		{Path: "execution.docker.network", Value: effective.Network},
	}
	if newDockerConfiguration {
		updates = append(updates,
			config.ConfigUpdate{Path: "execution.docker.mounts", Value: "[]"},
			config.ConfigUpdate{Path: "execution.docker.secrets", Value: "[]"},
		)
	}

	proposed := *cfg
	proposed.Execution.Backend = config.ExecutionBackendDocker
	proposed.Execution.Docker = effective
	proposed.Execution.Docker.Endpoint = endpoint
	proposed.Execution.Docker.Image = image
	proposed.Execution.Docker.ImageVerification = verification
	proposed.Execution.Docker.Contract = contractPath
	if err := proposed.ValidateRelaxed(); err != nil {
		return nil, config.DockerExecutionConfig{}, err
	}
	return updates, effective, nil
}

func configUpdatesChangeValues(inputs morphcli.ConfigInputs, updates []config.ConfigUpdate) (bool, error) {
	paths := make([]string, 0, len(updates))
	for _, update := range updates {
		paths = append(paths, update.Path)
	}
	values, err := morphcli.GetConfigValues(inputs.EnvPath, inputs.ConfigPath, paths)
	if err != nil {
		return false, err
	}
	if len(values) != len(updates) {
		return true, nil
	}
	for index := range updates {
		expected := updates[index].Value
		if updates[index].Path == "execution.docker.contract" && !filepath.IsAbs(expected) {
			expected = filepath.Join(inputs.Profile.HomeDir, filepath.FromSlash(expected))
		}
		if values[index].Value != expected {
			return true, nil
		}
	}
	return false, nil
}

func writeDockerSetupPreview(
	writer io.Writer,
	selectedImage string,
	resolvedImage string,
	verification string,
	contract execution.ImageContract,
	effective config.DockerExecutionConfig,
) error {
	_, err := fmt.Fprintf(
		writer,
		"Sandbox configuration\n  selected image: %s\n  immutable image: %s\n  verification: %s\n  contract: %s\n  scope: %s\n  workspace: %s\n  network: %s\n  mounts: %d\n  secrets: %d\nA running daemon will reload services and may interrupt active work.\n",
		selectedImage,
		resolvedImage,
		verification,
		contract.Digest(),
		effective.Scope,
		effective.Workspace.Mode,
		effective.Network,
		len(effective.Mounts),
		len(effective.Secrets),
	)
	return err
}

func writeSetupResult(writer io.Writer, jsonOutput bool, result setupResult) error {
	if jsonOutput {
		return writeJSONTo(writer, result)
	}
	status := "Configured"
	if !result.Changed {
		status = "Already configured"
	}
	if _, err := fmt.Fprintf(writer, "%s %s execution for profile %s\n", status, result.Backend, result.Profile); err != nil {
		return err
	}
	if result.Image != "" {
		if _, err := fmt.Fprintf(
			writer,
			"image: %s\nverification: %s (%s)\ncontract: %s (%s)\nscope: %s\nworkspace: %s\nnetwork: %s\nmounts: %d\nsecrets: %d\n",
			result.Image,
			result.Verification,
			result.VerificationResult,
			result.ContractDigest,
			result.ContractProvenance,
			result.Scope,
			result.WorkspaceMode,
			result.Network,
			result.MountCount,
			result.SecretCount,
		); err != nil {
			return err
		}
	}
	if result.RecoveryPath != "" {
		if _, err := fmt.Fprintf(writer, "recovery: %s\n", result.RecoveryPath); err != nil {
			return err
		}
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintf(writer, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "daemon: %s\n", result.DaemonApplication)
	return err
}

func getVerificationResult(verification string) string {
	if verification == config.ExecutionImageVerificationDigest {
		return "user-accepted immutable digest"
	}
	return "publisher signature verified"
}

func getDaemonApplication(
	ctx context.Context,
	inputs morphcli.ConfigInputs,
	changed bool,
	before morphruntime.ProbeResult,
	recoveryPath string,
) (string, error) {
	if !changed {
		return "no reload needed", nil
	}
	if before.State != morphruntime.ProbeStateReady {
		return "configuration written; applies on next daemon start", nil
	}
	for {
		after := probeSandboxRuntime(ctx, inputs.Profile)
		if after.State == morphruntime.ProbeStateReady &&
			after.Metadata.StartedAt.After(before.Metadata.StartedAt) {
			return "configuration written; running daemon observed ready after the change", nil
		}
		select {
		case <-ctx.Done():
			recovery := "restoring the previous profile configuration manually"
			if recoveryPath != "" {
				recovery = "restoring " + recoveryPath
			}
			return "", fmt.Errorf(
				"daemon observation failed: %w; configuration remains at %s; recover by %s",
				ctx.Err(), inputs.ConfigPath, recovery,
			)
		case <-time.After(setupObservationInterval):
		}
	}
}

func isActiveContractReady(path string, expected execution.ImageContract) bool {
	contract, err := dockerexec.LoadImageContract(path)
	return err == nil && contract.Digest() == expected.Digest()
}

func getConfigUpdatePaths(updates []config.ConfigUpdate) []string {
	paths := make([]string, 0, len(updates))
	for _, update := range updates {
		paths = append(paths, update.Path)
	}
	return paths
}

func getDockerSetupWarnings(effective config.DockerExecutionConfig, verification string) []string {
	warnings := make([]string, 0, 5)
	if verification == config.ExecutionImageVerificationDigest {
		warnings = append(warnings, "digest verification trusts the selected immutable digest without authenticating its publisher")
	}
	if effective.Scope == config.ExecutionScopeShared {
		warnings = append(warnings, "shared scope reuses one environment across sessions")
	}
	if effective.Workspace.Mode != config.ExecutionWorkspaceNone {
		warnings = append(warnings, "the sandbox can access the configured host workspace")
	}
	if effective.Network != config.ExecutionNetworkNone {
		warnings = append(warnings, "the sandbox has outbound network access")
	}
	if len(effective.Mounts) > 0 || len(effective.Secrets) > 0 {
		warnings = append(warnings, "the sandbox receives explicitly configured mounts or secrets")
	}
	return warnings
}

func getSetupReloadWarnings(configChanged bool) []string {
	if !configChanged {
		return []string{}
	}
	return []string{"a running daemon reload may interrupt active work"}
}

func checkLocalSetupFlags(cmd *cli.Command) error {
	for _, name := range []string{
		"release", "image", "verification", "endpoint", "scope", "workspace-mode",
		"workspace-source", "network", "accept-digest",
	} {
		if cmd.IsSet(name) {
			return fmt.Errorf("--%s is only valid with --backend docker", name)
		}
	}
	return nil
}

func promptSetupValue(input io.Reader, writer io.Writer, label string, defaultValue string) (string, error) {
	if defaultValue == "" {
		if _, err := fmt.Fprintf(writer, "%s: ", label); err != nil {
			return "", err
		}
	} else if _, err := fmt.Fprintf(writer, "%s [%s]: ", label, defaultValue); err != nil {
		return "", err
	}
	value, err := readInputLine(input)
	if err != nil && value == "" {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultValue
	}
	return value, nil
}

func readInputLine(input io.Reader) (string, error) {
	if reader, ok := input.(*bufio.Reader); ok {
		return reader.ReadString('\n')
	}
	return bufio.NewReader(input).ReadString('\n')
}

func preserveSetupRecovery(inputs morphcli.ConfigInputs) (string, error) {
	data, err := os.ReadFile(inputs.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read setup recovery source: %w", err)
	}
	digest := sha256.Sum256(data)
	path := filepath.Join(
		inputs.Profile.HomeDir,
		"sandbox",
		"recovery",
		fmt.Sprintf("config-%x.yaml", digest[:]),
	)
	snapshot, err := fileedit.ReadSnapshot(path, nil)
	if err != nil {
		return "", err
	}
	if snapshot.Exists {
		if snapshot.Digest != digest {
			return "", fmt.Errorf("setup recovery digest collision at %s", path)
		}
		return path, nil
	}
	if _, err := fileedit.ReplaceIfUnchanged(snapshot, data); err != nil {
		return "", fmt.Errorf("preserve setup recovery: %w", err)
	}
	return path, nil
}
