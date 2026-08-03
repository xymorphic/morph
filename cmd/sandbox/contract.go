package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/execution"
	dockerexec "github.com/xymorphic/morph/internal/execution/docker"
	"github.com/xymorphic/morph/internal/fileedit"
)

var runContractEditor = fileedit.RunEditor

var checkContractForActivation = checkContractAgainstConfig

type contractDetails struct {
	Provenance           execution.ContractProvenance `json:"provenance"`
	StructuralValidation contractValidationState      `json:"structural_validation"`
	ReleaseRelation      string                       `json:"release_relation"`
	Contract             execution.ImageContract      `json:"contract"`
}

type contractValidationState struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

type contractMutationResult struct {
	Command    string `json:"command"`
	Changed    bool   `json:"changed"`
	Path       string `json:"path,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Provenance string `json:"provenance,omitempty"`
}

func newContractCommand(input io.Reader, writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "contract",
		Usage: "Create, inspect, validate, and manage sandbox image contracts",
		Commands: []*cli.Command{
			newContractShowCommand(writer),
			newContractValidateCommand(writer),
			newContractCreateCommand(input, writer),
			newContractImportCommand(writer),
			newContractEditCommand(input, writer),
			newContractResetCommand(input, writer),
		},
	}
}

func newContractShowCommand(writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "show",
		Usage: "Show the active sandbox contract and its provenance",
		Action: func(_ context.Context, cmd *cli.Command) error {
			cfg, inputs, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			contract, err := dockerexec.LoadImageContract(cfg.Execution.Docker.Contract)
			if err != nil {
				return fmt.Errorf("load active sandbox contract: %w", err)
			}
			provenance, err := execution.NewContractStore(inputs.Profile.HomeDir).Describe(
				cfg.Execution.Docker.Image,
				cfg.Execution.Docker.Contract,
			)
			if err != nil {
				return err
			}
			validation := contractValidationState{Valid: true}
			if err := dockerexec.CheckImageContractCompatibility(contract); err != nil {
				validation.Valid = false
				validation.Error = err.Error()
			}
			relation := "preserved release unavailable"
			if provenance.OriginalDigest != "" {
				relation = "differs from preserved release"
				if provenance.ActiveDigest == provenance.OriginalDigest {
					relation = "matches preserved release"
				}
			}
			details := contractDetails{
				Provenance:           provenance,
				StructuralValidation: validation,
				ReleaseRelation:      relation,
				Contract:             contract,
			}
			if cmd.Bool("json") {
				return writeJSONTo(writer, details)
			}
			if _, err := fmt.Fprintf(
				writer,
				"provenance: %s\nrelation: %s\nstructurally valid: %t\nvalidation error: %s\nimage: %s\nactive digest: %s\noriginal digest: %s\nactive path: %s\noriginal path: %s\ncontract:\n",
				provenance.Kind,
				relation,
				validation.Valid,
				validation.Error,
				provenance.Image,
				provenance.ActiveDigest,
				provenance.OriginalDigest,
				provenance.ActivePath,
				provenance.OriginalPath,
			); err != nil {
				return err
			}
			return writeJSONTo(writer, contract)
		},
	}
}

func newContractValidateCommand(writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:      "validate",
		Usage:     "Validate an active or candidate sandbox contract",
		ArgsUsage: "[contract-path]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "structural-only", Usage: "Validate structure without inspecting the configured image"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			path := strings.TrimSpace(cmd.Args().First())
			if path == "" {
				path = cfg.Execution.Docker.Contract
			}
			contract, err := dockerexec.LoadImageContract(path)
			if err != nil {
				return err
			}
			if err := dockerexec.CheckImageContractCompatibility(contract); err != nil {
				return err
			}
			if !cmd.Bool("structural-only") {
				if err := checkContractAgainstConfig(ctx, cfg, contract); err != nil {
					return err
				}
			}
			if cmd.Bool("json") {
				return writeJSONTo(writer, map[string]any{
					"valid":        true,
					"digest":       contract.Digest(),
					"image_backed": !cmd.Bool("structural-only"),
				})
			}
			_, err = fmt.Fprintf(writer, "Contract valid: %s\n", contract.Digest())
			return err
		},
	}
}

func newContractImportCommand(writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Validate and activate a contract from a file",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "from", Usage: "Contract JSON file to import", Required: true},
			&cli.BoolFlag{Name: "structural-only", Usage: "Only valid with the non-mutating validate command"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("structural-only") {
				return errors.New("--structural-only cannot be used when activating a contract")
			}
			cfg, inputs, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			contract, err := dockerexec.LoadImageContract(cmd.String("from"))
			if err != nil {
				return err
			}
			configSnapshot, err := fileedit.ReadSnapshot(inputs.ConfigPath, nil)
			if err != nil {
				return err
			}
			if err := checkContractForActivation(ctx, cfg, contract); err != nil {
				return err
			}
			path, err := activateCustomContractFromSnapshot(cfg, inputs, contract, configSnapshot)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSONTo(writer, contractMutationResult{
					Command: "import", Changed: true, Path: path,
					Digest: contract.Digest(), Provenance: execution.ContractProvenanceCustom,
				})
			}
			_, err = fmt.Fprintf(writer, "Activated user-managed contract %s at %s\n", contract.Digest(), path)
			return err
		},
	}
}

func newContractEditCommand(input io.Reader, writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "edit",
		Usage: "Edit, validate, and activate a profile-local sandbox contract",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "editor", Usage: "Editor command; defaults to VISUAL, EDITOR, or the platform editor"},
			&cli.BoolFlag{Name: "structural-only", Usage: "Only valid with the non-mutating validate command"},
			&cli.BoolFlag{Name: "no-retry", Usage: "Do not offer to reopen an invalid candidate"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("structural-only") {
				return errors.New("--structural-only cannot be used when activating a contract")
			}
			if cmd.Bool("json") && !cmd.Bool("no-retry") {
				return errors.New("--json contract edit requires --no-retry")
			}
			cfg, inputs, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			activeSnapshot, err := fileedit.ReadSnapshot(cfg.Execution.Docker.Contract, nil)
			if err != nil {
				return fmt.Errorf("read active sandbox contract: %w", err)
			}
			configSnapshot, err := fileedit.ReadSnapshot(inputs.ConfigPath, nil)
			if err != nil {
				return fmt.Errorf("read profile configuration: %w", err)
			}
			identity := getContractConfigIdentity(cfg)
			workingDir := filepath.Join(inputs.Profile.HomeDir, "sandbox")
			if err := os.MkdirAll(workingDir, 0o700); err != nil {
				return fmt.Errorf("create contract edit directory: %w", err)
			}
			workingFile, err := os.CreateTemp(workingDir, ".contract-edit-*.json")
			if err != nil {
				return fmt.Errorf("create contract edit path: %w", err)
			}
			workingPath := workingFile.Name()
			if err := workingFile.Close(); err != nil {
				return fmt.Errorf("close contract edit path: %w", err)
			}
			if err := os.Remove(workingPath); err != nil {
				return fmt.Errorf("prepare contract edit path: %w", err)
			}
			result, err := fileedit.EditFile(ctx, fileedit.EditOptions{
				Path:        workingPath,
				DefaultData: activeSnapshot.Data,
				Editor:      cmd.String("editor"),
				RunEditor:   runContractEditor,
				Validate: func(candidatePath string) error {
					contract, loadErr := dockerexec.LoadImageContract(candidatePath)
					if loadErr != nil {
						return loadErr
					}
					return checkContractForActivation(ctx, cfg, contract)
				},
				Retry: func(validationErr error, candidatePath string) bool {
					if cmd.Bool("no-retry") {
						return false
					}
					return promptContractConfirmation(
						input,
						writer,
						fmt.Sprintf("Validation failed: %v\nCandidate: %s\nReopen candidate?", validationErr, candidatePath),
					)
				},
			})
			if err != nil {
				_ = os.Remove(workingPath)
				if result.CandidatePath != "" {
					return fmt.Errorf("%w; candidate preserved at %s", err, result.CandidatePath)
				}
				return err
			}
			if !result.Changed {
				_ = os.Remove(workingPath)
				activeContract, loadErr := dockerexec.LoadImageContract(cfg.Execution.Docker.Contract)
				if loadErr != nil {
					return loadErr
				}
				provenance, describeErr := execution.NewContractStore(inputs.Profile.HomeDir).Describe(
					cfg.Execution.Docker.Image,
					cfg.Execution.Docker.Contract,
				)
				if describeErr != nil {
					return describeErr
				}
				if cmd.Bool("json") {
					return writeJSONTo(writer, contractMutationResult{
						Command: "edit", Changed: false, Path: cfg.Execution.Docker.Contract,
						Digest: activeContract.Digest(), Provenance: provenance.Kind,
					})
				}
				_, err = fmt.Fprintln(writer, "Contract unchanged")
				return err
			}
			contract, err := dockerexec.LoadImageContract(workingPath)
			if err != nil {
				return fmt.Errorf("%w; candidate preserved at %s", err, workingPath)
			}
			if err := checkSnapshotUnchanged(activeSnapshot); err != nil {
				return fmt.Errorf("%w; candidate preserved at %s", err, workingPath)
			}
			if err := checkSnapshotUnchanged(configSnapshot); err != nil {
				return fmt.Errorf("profile configuration changed while contract was being edited: %w; candidate preserved at %s", err, workingPath)
			}
			reloaded, _, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return fmt.Errorf("reload profile configuration: %w; candidate preserved at %s", err, workingPath)
			}
			if getContractConfigIdentity(reloaded) != identity {
				return fmt.Errorf("sandbox image, verification, or contract path changed while contract was being edited; candidate preserved at %s", workingPath)
			}
			if err := checkContractForActivation(ctx, reloaded, contract); err != nil {
				return fmt.Errorf("candidate revalidation failed: %w; candidate preserved at %s", err, workingPath)
			}
			path, err := activateCustomContractFromSnapshot(reloaded, inputs, contract, configSnapshot)
			if err != nil {
				return fmt.Errorf("%w; candidate preserved at %s", err, workingPath)
			}
			_ = os.Remove(workingPath)
			if cmd.Bool("json") {
				return writeJSONTo(writer, contractMutationResult{
					Command: "edit", Changed: true, Path: path,
					Digest: contract.Digest(), Provenance: execution.ContractProvenanceCustom,
				})
			}
			_, err = fmt.Fprintf(writer, "Activated user-managed contract %s at %s\n", contract.Digest(), path)
			return err
		},
	}
}

func newContractResetCommand(input io.Reader, writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "reset",
		Usage: "Restore the preserved release contract for the configured image",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "yes", Usage: "Confirm contract reset"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") && !cmd.Bool("yes") {
				return errors.New("--json contract reset requires --yes")
			}
			cfg, inputs, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			configSnapshot, err := fileedit.ReadSnapshot(inputs.ConfigPath, nil)
			if err != nil {
				return err
			}
			if !cmd.Bool("yes") && !promptContractConfirmation(input, writer, "Restore the preserved release contract?") {
				return errors.New("contract reset cancelled")
			}
			path, contract, err := execution.NewContractStore(inputs.Profile.HomeDir).ResetActive(cfg.Execution.Docker.Image)
			if err != nil {
				return fmt.Errorf("load preserved release contract: %w", err)
			}
			if err := setActiveContractPathFromSnapshot(inputs, path, configSnapshot); err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSONTo(writer, contractMutationResult{
					Command: "reset", Changed: true, Path: path,
					Digest: contract.Digest(), Provenance: execution.ContractProvenanceRelease,
				})
			}
			_, err = fmt.Fprintf(writer, "Restored release-provided contract %s\n", contract.Digest())
			return err
		},
	}
}

func newContractCreateCommand(input io.Reader, writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "Create a contract candidate from the configured immutable image",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "shell", Usage: "Container shell path"},
			&cli.StringFlag{Name: "helper", Usage: "Morph sandbox helper path"},
			&cli.StringFlag{Name: "workspace-path", Usage: "Container workspace path"},
			&cli.StringFlag{Name: "home-path", Usage: "Container home path"},
			&cli.StringFlag{Name: "temporary-path", Usage: "Container temporary path"},
			&cli.StringFlag{Name: "control-path", Usage: "Container supervisor control path"},
			&cli.StringSliceFlag{Name: "path", Usage: "Container PATH entry"},
			&cli.StringSliceFlag{Name: "executable", Usage: "Declared executable as name=/absolute/path"},
			&cli.StringFlag{Name: "output", Usage: "Write candidate to this file instead of stdout"},
			&cli.BoolFlag{Name: "force", Usage: "Replace an existing output file"},
			&cli.BoolFlag{Name: "activate", Usage: "Activate the validated candidate for the selected profile"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Accept the displayed policy-bearing contract fields"},
			&cli.BoolFlag{Name: "non-interactive", Usage: "Fail instead of prompting for policy confirmation"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") && (!cmd.Bool("non-interactive") || !cmd.Bool("yes")) {
				return errors.New("--json contract create requires --non-interactive and --yes")
			}
			cfg, inputs, err := loadSandboxManagementConfig(cmd)
			if err != nil {
				return err
			}
			configSnapshot, err := fileedit.ReadSnapshot(inputs.ConfigPath, nil)
			if err != nil {
				return err
			}
			if err := dockerexec.ValidateImageReference(cfg.Execution.Docker.Image); err != nil {
				return err
			}
			if cfg.Execution.Docker.ImageVerification == config.ExecutionImageVerificationSignature {
				if err := dockerexec.CheckCosignPrerequisite(); err != nil {
					return err
				}
				if err := dockerexec.VerifyImageSignature(ctx, cfg.Execution.Docker.Image); err != nil {
					return err
				}
			} else if cfg.Execution.Docker.ImageVerification != config.ExecutionImageVerificationDigest {
				return fmt.Errorf("unsupported sandbox image verification mode %q", cfg.Execution.Docker.ImageVerification)
			}
			dockerClient, err := getLinuxDockerClient(ctx, cfg.Execution.Docker.Endpoint)
			if err != nil {
				return err
			}
			defer func() { _ = dockerClient.Close() }()
			if err := dockerexec.PullImage(ctx, dockerClient, cfg.Execution.Docker.Image); err != nil {
				return err
			}
			contract, err := dockerexec.InspectImageContract(ctx, dockerClient, cfg.Execution.Docker.Image)
			if err != nil {
				return err
			}
			if err := applyContractCreateOverrides(cmd, &contract); err != nil {
				return err
			}
			if err := checkContractAgainstClient(ctx, cfg, dockerClient, contract); err != nil {
				return err
			}
			if !cmd.Bool("yes") {
				if cmd.Bool("non-interactive") {
					return errors.New("--yes is required to accept policy-bearing contract fields non-interactively")
				}
				if err := writeContractCreatePreview(writer, contract); err != nil {
					return err
				}
				if !promptContractConfirmation(input, writer, "Accept these contract policy fields?") {
					return errors.New("contract creation cancelled")
				}
			}
			data, err := json.MarshalIndent(contract, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if path := strings.TrimSpace(cmd.String("output")); path != "" {
				if err := writeContractCandidate(path, data, cmd.Bool("force")); err != nil {
					return err
				}
				if !cmd.Bool("json") {
					if _, err := fmt.Fprintf(writer, "Created contract candidate %s\n", path); err != nil {
						return err
					}
				}
			} else if !cmd.Bool("activate") {
				if _, err := writer.Write(data); err != nil {
					return err
				}
			}
			if !cmd.Bool("activate") {
				return nil
			}
			path, err := activateCustomContractFromSnapshot(cfg, inputs, contract, configSnapshot)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSONTo(writer, contractMutationResult{
					Command: "create", Changed: true, Path: path,
					Digest: contract.Digest(), Provenance: execution.ContractProvenanceCustom,
				})
			}
			_, err = fmt.Fprintf(writer, "Activated user-managed contract %s at %s\n", contract.Digest(), path)
			return err
		},
	}
}

func loadSandboxManagementConfig(cmd *cli.Command) (*config.Config, morphcli.ConfigInputs, error) {
	cfg, inputs, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, morphcli.ConfigInputs{}, err
	}
	cfg.Normalize()
	return cfg, inputs, nil
}

func checkContractAgainstConfig(ctx context.Context, cfg *config.Config, contract execution.ImageContract) error {
	if err := dockerexec.CheckImageContractCompatibility(contract); err != nil {
		return err
	}
	if err := dockerexec.ValidateImageReference(cfg.Execution.Docker.Image); err != nil {
		return err
	}
	if cfg.Execution.Docker.ImageVerification == config.ExecutionImageVerificationSignature {
		if err := dockerexec.CheckCosignPrerequisite(); err != nil {
			return err
		}
		if err := dockerexec.VerifyImageSignature(ctx, cfg.Execution.Docker.Image); err != nil {
			return err
		}
	} else if cfg.Execution.Docker.ImageVerification != config.ExecutionImageVerificationDigest {
		return fmt.Errorf("unsupported sandbox image verification mode %q", cfg.Execution.Docker.ImageVerification)
	}
	dockerClient, err := getLinuxDockerClient(ctx, cfg.Execution.Docker.Endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = dockerClient.Close() }()
	if err := dockerexec.PullImage(ctx, dockerClient, cfg.Execution.Docker.Image); err != nil {
		return err
	}
	return dockerexec.CheckImageContract(ctx, dockerClient, cfg.Execution.Docker.Image, contract)
}

func checkContractAgainstClient(
	ctx context.Context,
	cfg *config.Config,
	dockerClient *dockerexec.Client,
	contract execution.ImageContract,
) error {
	return dockerexec.CheckImageContract(ctx, dockerClient, cfg.Execution.Docker.Image, contract)
}

func getLinuxDockerClient(ctx context.Context, endpoint string) (*dockerexec.Client, error) {
	if err := checkDockerSocket(endpoint); err != nil {
		return nil, err
	}
	dockerClient, err := dockerexec.NewClient(endpoint)
	if err != nil {
		return nil, err
	}
	ping, err := dockerClient.Ping(ctx)
	if err != nil {
		_ = dockerClient.Close()
		return nil, fmt.Errorf("connect to Docker engine: %w", err)
	}
	if !strings.EqualFold(ping.OSType, "linux") {
		_ = dockerClient.Close()
		return nil, fmt.Errorf("docker engine must use Linux containers, got %q", ping.OSType)
	}
	return dockerClient, nil
}

func checkDockerSocket(endpoint string) error {
	path := strings.TrimSpace(endpoint)
	if value, ok := strings.CutPrefix(path, "unix://"); ok {
		path = value
	}
	if !filepath.IsAbs(path) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Docker socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("docker endpoint %s is not a socket", path)
	}
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("docker socket %s is writable by other users", path)
	}
	return nil
}

func activateCustomContractFromSnapshot(
	cfg *config.Config,
	inputs morphcli.ConfigInputs,
	contract execution.ImageContract,
	snapshot fileedit.Snapshot,
) (string, error) {
	if err := dockerexec.CheckImageContractCompatibility(contract); err != nil {
		return "", err
	}
	path, err := execution.NewContractStore(inputs.Profile.HomeDir).SaveActive(cfg.Execution.Docker.Image, contract)
	if err != nil {
		return "", err
	}
	value := profileRelativePath(inputs.Profile.HomeDir, path)
	_, err = config.SetConfigValuesRelaxedIfUnchanged(
		inputs.EnvPath,
		inputs.ConfigPath,
		[]config.ConfigUpdate{{Path: "execution.docker.contract", Value: value}},
		snapshot,
	)
	if err != nil {
		return "", err
	}
	return path, nil
}

type contractConfigIdentity struct {
	Image        string
	Verification string
	Contract     string
}

func getContractConfigIdentity(cfg *config.Config) contractConfigIdentity {
	return contractConfigIdentity{
		Image:        cfg.Execution.Docker.Image,
		Verification: cfg.Execution.Docker.ImageVerification,
		Contract:     cfg.Execution.Docker.Contract,
	}
}

func checkSnapshotUnchanged(snapshot fileedit.Snapshot) error {
	current, err := fileedit.ReadSnapshot(snapshot.Path, snapshot.Data)
	if err != nil {
		return err
	}
	if current.Exists != snapshot.Exists || current.Digest != snapshot.Digest {
		return fmt.Errorf("%s changed while it was being edited", snapshot.Path)
	}
	return nil
}

func setActiveContractPathFromSnapshot(
	inputs morphcli.ConfigInputs,
	path string,
	snapshot fileedit.Snapshot,
) error {
	value := profileRelativePath(inputs.Profile.HomeDir, path)
	_, err := config.SetConfigValuesRelaxedIfUnchanged(
		inputs.EnvPath,
		inputs.ConfigPath,
		[]config.ConfigUpdate{{Path: "execution.docker.contract", Value: value}},
		snapshot,
	)
	return err
}

func profileRelativePath(profileHome string, path string) string {
	relative, err := filepath.Rel(profileHome, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return path
	}
	return filepath.ToSlash(relative)
}

func promptContractConfirmation(input io.Reader, writer io.Writer, message string) bool {
	if _, err := fmt.Fprintf(writer, "%s [y/N] ", message); err != nil {
		return false
	}
	answer, err := readInputLine(input)
	if err != nil && answer == "" {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func applyContractCreateOverrides(cmd *cli.Command, contract *execution.ImageContract) error {
	if value := strings.TrimSpace(cmd.String("shell")); value != "" {
		contract.Shell = value
	}
	if value := strings.TrimSpace(cmd.String("helper")); value != "" {
		contract.Helper = value
	}
	if value := strings.TrimSpace(cmd.String("workspace-path")); value != "" {
		contract.WorkspacePath = value
	}
	if value := strings.TrimSpace(cmd.String("home-path")); value != "" {
		contract.HomePath = value
	}
	if value := strings.TrimSpace(cmd.String("temporary-path")); value != "" {
		contract.TemporaryPath = value
	}
	if value := strings.TrimSpace(cmd.String("control-path")); value != "" {
		contract.ControlPath = value
	}
	if cmd.IsSet("path") {
		contract.PATH = cmd.StringSlice("path")
	}
	for _, value := range cmd.StringSlice("executable") {
		name, path, ok := strings.Cut(value, "=")
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if !ok || name == "" || path == "" {
			return fmt.Errorf("invalid executable %q; expected name=/absolute/path", value)
		}
		if contract.Executables == nil {
			contract.Executables = map[string]string{}
		}
		contract.Executables[name] = path
	}
	normalized, err := contract.Normalize()
	if err != nil {
		return err
	}
	*contract = normalized
	return nil
}

func writeContractCreatePreview(writer io.Writer, contract execution.ImageContract) error {
	_, err := fmt.Fprintf(
		writer,
		"Contract policy\n  workspace: %s\n  home: %s\n  temporary: %s\n  control: %s\n  shell: %s\n  helper: %s\n  PATH entries: %d\n  declared executables: %d\n",
		contract.WorkspacePath,
		contract.HomePath,
		contract.TemporaryPath,
		contract.ControlPath,
		contract.Shell,
		contract.Helper,
		len(contract.PATH),
		len(contract.Executables),
	)
	return err
}

func writeContractCandidate(path string, data []byte, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("contract output already exists: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	snapshot, err := fileedit.ReadSnapshot(path, nil)
	if err != nil {
		return err
	}
	_, err = fileedit.ReplaceIfUnchanged(snapshot, data)
	return err
}
