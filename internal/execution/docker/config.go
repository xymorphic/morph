package docker

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/wandxy/morph/internal/execution"
)

const (
	labelPrefix               = "io.xymorphic.morph.execution."
	LabelProfile              = labelPrefix + "profile"
	LabelScopeOwner           = labelPrefix + "scope-owner"
	LabelSecurityGeneration   = labelPrefix + "security-generation"
	LabelDaemonIncarnation    = labelPrefix + "daemon-incarnation"
	LabelContainerIncarnation = labelPrefix + "container-incarnation"
	LabelResourceKind         = labelPrefix + "resource-kind"
	LabelScope                = labelPrefix + "scope"
)

type ContainerInput struct {
	Spec                 execution.Spec
	Image                string
	Contract             execution.ImageContract
	DaemonIncarnation    string
	ContainerIncarnation string
	ResourceKind         string
	WorkspaceVolume      string
	NetworkName          string
	Command              []string
	OpenStdin            bool
}

func BuildContainerOptions(input ContainerInput) (client.ContainerCreateOptions, error) {
	owner := input.Spec.Owner()
	exposure := input.Spec.Exposure()
	if exposure.Backend() != execution.BackendDocker || strings.TrimSpace(input.Image) == "" {
		return client.ContainerCreateOptions{}, errors.New("docker container input is incomplete")
	}
	contract, err := input.Contract.Normalize()
	if err != nil {
		return client.ContainerCreateOptions{}, err
	}
	command, arguments, cwd, environment, planErr := getContainerCommand(input.Spec)
	if len(input.Command) > 0 {
		command = input.Command[0]
		arguments = slices.Clone(input.Command[1:])
		if planErr != nil {
			cwd = "/workspace"
			environment = nil
		}
	} else if planErr != nil {
		return client.ContainerCreateOptions{}, planErr
	}
	mounts, err := buildMounts(exposure, input.WorkspaceVolume)
	if err != nil {
		return client.ContainerCreateOptions{}, err
	}
	workingDirectory := mapWorkingDirectory(cwd, exposure)
	if cwd != "" && workingDirectory == "" {
		return client.ContainerCreateOptions{}, errors.New(
			"docker command working directory is outside configured mounts",
		)
	}
	limits := exposure.Limits()
	controlBytes := max(limits.ControlInputBytes, limits.OutputBytes*4)
	tmpfsOwner := getTmpfsOwner(contract.User)
	pids := limits.PIDs
	init := true
	stopSeconds := int(limits.StopGrace.Seconds())
	if stopSeconds < 1 {
		stopSeconds = 1
	}
	labels := map[string]string{
		LabelProfile:              owner.Profile,
		LabelScopeOwner:           getScopeOwner(owner, exposure.Scope()),
		LabelSecurityGeneration:   exposure.SecurityGeneration(),
		LabelDaemonIncarnation:    input.DaemonIncarnation,
		LabelContainerIncarnation: input.ContainerIncarnation,
		LabelResourceKind:         input.ResourceKind,
		LabelScope:                string(exposure.Scope()),
	}
	hostConfig := &container.HostConfig{
		LogConfig:       container.LogConfig{Type: "none"},
		NetworkMode:     getNetworkMode(exposure.Network(), input.NetworkName),
		RestartPolicy:   container.RestartPolicy{Name: container.RestartPolicyDisabled},
		CapDrop:         []string{"ALL"},
		ReadonlyRootfs:  true,
		SecurityOpt:     []string{"no-new-privileges"},
		Privileged:      false,
		PublishAllPorts: false,
		Init:            &init,
		Mounts:          mounts,
		Tmpfs: map[string]string{
			contract.HomePath: "rw,nosuid,nodev,noexec,mode=0700," + tmpfsOwner + "size=" + strconv.FormatInt(
				limits.TemporaryBytes,
				10,
			),
			contract.TemporaryPath: "rw,nosuid,nodev,noexec,mode=1777," + tmpfsOwner + "size=" + strconv.FormatInt(
				limits.TemporaryBytes,
				10,
			),
			contract.ControlPath: "rw,nosuid,nodev,noexec,mode=0700," + tmpfsOwner + "size=" + strconv.FormatInt(
				controlBytes,
				10,
			),
		},
		Resources: container.Resources{
			Memory:     limits.MemoryBytes,
			MemorySwap: limits.MemoryBytes,
			NanoCPUs:   limits.CPUMilli * 1_000_000,
			PidsLimit:  &pids,
			Ulimits: []*container.Ulimit{
				{
					Name: "nofile",
					Soft: limits.OpenFiles,
					Hard: limits.OpenFiles,
				},
			},
		},
	}
	config := &container.Config{
		User:         contract.User,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  input.OpenStdin,
		Tty:          false,
		OpenStdin:    input.OpenStdin,
		StdinOnce:    input.OpenStdin,
		Env:          slices.Clone(environment),
		Cmd:          append([]string{command}, arguments...),
		Image:        input.Image,
		WorkingDir:   workingDirectory,
		Labels:       labels,
		StopTimeout:  &stopSeconds,
	}
	return client.ContainerCreateOptions{
		Config:           config,
		HostConfig:       hostConfig,
		NetworkingConfig: getNetworkingConfig(exposure.Network(), input.NetworkName),
	}, nil
}

func getTmpfsOwner(user string) string {
	parts := strings.Split(strings.TrimSpace(user), ":")
	if len(parts) != 2 {
		return ""
	}
	if _, err := strconv.ParseUint(parts[0], 10, 32); err != nil {
		return ""
	}
	if _, err := strconv.ParseUint(parts[1], 10, 32); err != nil {
		return ""
	}
	return "uid=" + parts[0] + ",gid=" + parts[1] + ","
}

func getContainerCommand(spec execution.Spec) (string, []string, string, []string, error) {
	operation := spec.Operation()
	var planCommand interface {
		ExecutionCommand() (string, []string, string, []string, error)
	}
	switch {
	case operation.Command != nil:
		planCommand = operation.Command
	case operation.Process != nil && operation.Process.Plan != nil:
		planCommand = operation.Process.Plan
	default:
		return "", nil, "", nil, errors.New("docker operation has no command plan")
	}
	return planCommand.ExecutionCommand()
}

func getScopeOwner(owner execution.Owner, scope execution.Scope) string {
	if scope == execution.ScopeShared {
		return owner.Profile
	}
	return owner.EffectiveSessionID
}

func mapWorkingDirectory(cwd string, exposure execution.Exposure) string {
	if cwd == "" {
		return "/workspace"
	}
	cleaned := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(cwd)), "/")
	if cleaned == "/workspace" || strings.HasPrefix(cleaned, "/workspace/") ||
		strings.HasPrefix(cleaned, "/mnt/") {
		return cleaned
	}
	for _, mount := range exposure.Mounts() {
		source := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(mount.SourceIdentity)), "/")
		if cleaned == source {
			return mount.Target
		}
		if strings.HasPrefix(cleaned, source+"/") {
			return strings.TrimSuffix(mount.Target, "/") + strings.TrimPrefix(cleaned, source)
		}
	}
	if !strings.HasPrefix(cleaned, "/") {
		return cwd
	}
	return ""
}

func getNetworkMode(mode execution.NetworkMode, networkName string) container.NetworkMode {
	if mode == execution.NetworkNone {
		return container.NetworkMode("none")
	}
	return container.NetworkMode(networkName)
}

func getNetworkingConfig(mode execution.NetworkMode, networkName string) *network.NetworkingConfig {
	if mode == execution.NetworkNone || networkName == "" {
		return nil
	}
	return &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{networkName: {}},
	}
}

func validateContainerOptions(options client.ContainerCreateOptions) error {
	if options.Config == nil || options.HostConfig == nil {
		return errors.New("docker container configuration is incomplete")
	}
	if options.HostConfig.Privileged || !options.HostConfig.ReadonlyRootfs ||
		!slices.Contains(options.HostConfig.CapDrop, "ALL") {
		return fmt.Errorf("docker container hardening is incomplete")
	}
	return nil
}
