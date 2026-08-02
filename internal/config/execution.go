package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	ExecutionBackendLocal  = "local"
	ExecutionBackendDocker = "docker"
	ExecutionScopeSession  = "session"
	ExecutionScopeShared   = "shared"
	ExecutionWorkspaceNone = "none"
	ExecutionWorkspaceRO   = "ro"
	ExecutionWorkspaceRW   = "rw"
	ExecutionNetworkNone   = "none"
	ExecutionNetworkBridge = "bridge"
)

var (
	executionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	imageDigestPattern   = regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`)
)

type ExecutionConfig struct {
	Backend string                `yaml:"backend"`
	Docker  DockerExecutionConfig `yaml:"docker"`
}

type DockerExecutionConfig struct {
	Scope                   string                   `yaml:"scope"`
	Endpoint                string                   `yaml:"endpoint"`
	Image                   string                   `yaml:"image"`
	Contract                string                   `yaml:"contract"`
	Workspace               ExecutionWorkspaceConfig `yaml:"workspace"`
	Mounts                  []ExecutionMountConfig   `yaml:"mounts"`
	Network                 string                   `yaml:"network"`
	Secrets                 []ExecutionSecretConfig  `yaml:"secrets"`
	Limits                  ExecutionLimitsConfig    `yaml:"limits"`
	EnvironmentIdleExpiry   time.Duration            `yaml:"environmentIdleExpiry"`
	SharedDisabledRetention time.Duration            `yaml:"sharedDisabledRetention"`
	MaximumEnvironments     int                      `yaml:"maximumEnvironments"`
	MaximumVolumes          int                      `yaml:"maximumVolumes"`
	ReservedFreeBytes       int64                    `yaml:"reservedFreeBytes"`
}

type ExecutionWorkspaceConfig struct {
	Mode   string `yaml:"mode"`
	Source string `yaml:"source"`
}

type ExecutionMountConfig struct {
	Name    string `yaml:"name"`
	Source  string `yaml:"source"`
	Mode    string `yaml:"mode"`
	Create  bool   `yaml:"create"`
	Purpose string `yaml:"purpose"`
}

func (m ExecutionMountConfig) Target() string {
	return "/mnt/" + strings.TrimSpace(strings.ToLower(m.Name))
}

type ExecutionSecretConfig struct {
	Name        string `yaml:"name"`
	Env         string `yaml:"env"`
	Description string `yaml:"description"`
}

type ExecutionLimitsConfig struct {
	MemoryBytes       int64         `yaml:"memoryBytes"`
	CPUMilli          int64         `yaml:"cpuMilli"`
	PIDs              int64         `yaml:"pids"`
	OpenFiles         int64         `yaml:"openFiles"`
	TemporaryBytes    int64         `yaml:"temporaryBytes"`
	OutputBytes       int64         `yaml:"outputBytes"`
	ControlInputBytes int64         `yaml:"controlInputBytes"`
	Runtime           time.Duration `yaml:"runtime"`
	StopGrace         time.Duration `yaml:"stopGrace"`
}

func defaultExecutionConfig() ExecutionConfig {
	endpoint := "/var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = `//./pipe/docker_engine`
	}
	return ExecutionConfig{
		Backend: ExecutionBackendLocal,
		Docker: DockerExecutionConfig{
			Scope:                   ExecutionScopeSession,
			Endpoint:                endpoint,
			Workspace:               ExecutionWorkspaceConfig{Mode: ExecutionWorkspaceNone},
			Network:                 ExecutionNetworkNone,
			EnvironmentIdleExpiry:   30 * time.Minute,
			SharedDisabledRetention: 7 * 24 * time.Hour,
			MaximumEnvironments:     32,
			MaximumVolumes:          128,
			ReservedFreeBytes:       2 << 30,
			Limits: ExecutionLimitsConfig{
				MemoryBytes:       1 << 30,
				CPUMilli:          1000,
				PIDs:              256,
				OpenFiles:         1024,
				TemporaryBytes:    64 << 20,
				OutputBytes:       1 << 20,
				ControlInputBytes: 1 << 20,
				Runtime:           2 * time.Minute,
				StopGrace:         3 * time.Second,
			},
		},
	}
}

func (c *Config) normalizeExecution() {
	defaults := defaultExecutionConfig()
	c.Execution.Backend = strings.TrimSpace(strings.ToLower(c.Execution.Backend))
	if c.Execution.Backend == "" {
		c.Execution.Backend = defaults.Backend
	}
	docker := &c.Execution.Docker
	docker.Scope = strings.TrimSpace(strings.ToLower(docker.Scope))
	if docker.Scope == "" {
		docker.Scope = defaults.Docker.Scope
	}
	docker.Endpoint = strings.TrimSpace(docker.Endpoint)
	if docker.Endpoint == "" {
		docker.Endpoint = defaults.Docker.Endpoint
	}
	docker.Image = strings.TrimSpace(docker.Image)
	docker.Contract = strings.TrimSpace(docker.Contract)
	docker.Workspace.Mode = strings.TrimSpace(strings.ToLower(docker.Workspace.Mode))
	if docker.Workspace.Mode == "" {
		docker.Workspace.Mode = defaults.Docker.Workspace.Mode
	}
	docker.Workspace.Source = strings.TrimSpace(docker.Workspace.Source)
	docker.Network = strings.TrimSpace(strings.ToLower(docker.Network))
	if docker.Network == "" {
		docker.Network = defaults.Docker.Network
	}
	for index := range docker.Mounts {
		docker.Mounts[index].Name = strings.TrimSpace(strings.ToLower(docker.Mounts[index].Name))
		docker.Mounts[index].Source = strings.TrimSpace(docker.Mounts[index].Source)
		docker.Mounts[index].Mode = strings.TrimSpace(strings.ToLower(docker.Mounts[index].Mode))
		if docker.Mounts[index].Mode == "" {
			docker.Mounts[index].Mode = ExecutionWorkspaceRO
		}
		docker.Mounts[index].Purpose = strings.TrimSpace(docker.Mounts[index].Purpose)
	}
	slices.SortFunc(
		docker.Mounts,
		func(left, right ExecutionMountConfig) int { return strings.Compare(left.Name, right.Name) },
	)
	for index := range docker.Secrets {
		docker.Secrets[index].Name = strings.TrimSpace(strings.ToLower(docker.Secrets[index].Name))
		docker.Secrets[index].Env = strings.TrimSpace(docker.Secrets[index].Env)
		docker.Secrets[index].Description = strings.TrimSpace(docker.Secrets[index].Description)
	}
	slices.SortFunc(
		docker.Secrets,
		func(left, right ExecutionSecretConfig) int { return strings.Compare(left.Name, right.Name) },
	)
	applyExecutionLimitDefaults(docker, defaults.Docker)
}

func applyExecutionLimitDefaults(value *DockerExecutionConfig, defaults DockerExecutionConfig) {
	if value.EnvironmentIdleExpiry <= 0 {
		value.EnvironmentIdleExpiry = defaults.EnvironmentIdleExpiry
	}
	if value.SharedDisabledRetention <= 0 {
		value.SharedDisabledRetention = defaults.SharedDisabledRetention
	}
	if value.MaximumEnvironments <= 0 {
		value.MaximumEnvironments = defaults.MaximumEnvironments
	}
	if value.MaximumVolumes <= 0 {
		value.MaximumVolumes = defaults.MaximumVolumes
	}
	if value.ReservedFreeBytes <= 0 {
		value.ReservedFreeBytes = defaults.ReservedFreeBytes
	}
	if value.Limits.MemoryBytes <= 0 {
		value.Limits.MemoryBytes = defaults.Limits.MemoryBytes
	}
	if value.Limits.CPUMilli <= 0 {
		value.Limits.CPUMilli = defaults.Limits.CPUMilli
	}
	if value.Limits.PIDs <= 0 {
		value.Limits.PIDs = defaults.Limits.PIDs
	}
	if value.Limits.OpenFiles <= 0 {
		value.Limits.OpenFiles = defaults.Limits.OpenFiles
	}
	if value.Limits.TemporaryBytes <= 0 {
		value.Limits.TemporaryBytes = defaults.Limits.TemporaryBytes
	}
	if value.Limits.OutputBytes <= 0 {
		value.Limits.OutputBytes = defaults.Limits.OutputBytes
	}
	if value.Limits.ControlInputBytes <= 0 {
		value.Limits.ControlInputBytes = defaults.Limits.ControlInputBytes
	}
	if value.Limits.Runtime <= 0 {
		value.Limits.Runtime = defaults.Limits.Runtime
	}
	if value.Limits.StopGrace <= 0 {
		value.Limits.StopGrace = defaults.Limits.StopGrace
	}
}

func (c *Config) validateExecution() error {
	if c.Execution.Backend != ExecutionBackendLocal &&
		c.Execution.Backend != ExecutionBackendDocker {
		return errors.New("execution backend must be local or docker")
	}
	if c.Execution.Backend == ExecutionBackendLocal {
		return nil
	}
	docker := c.Execution.Docker
	if docker.Scope != ExecutionScopeSession && docker.Scope != ExecutionScopeShared {
		return errors.New("execution docker scope must be session or shared")
	}
	if !isLocalDockerEndpoint(docker.Endpoint) {
		return errors.New(
			"execution docker endpoint must be an explicit local socket or named pipe",
		)
	}
	if !imageDigestPattern.MatchString(docker.Image) {
		return errors.New("execution docker image must be pinned by sha256 digest")
	}
	if docker.Contract == "" {
		return errors.New("execution docker contract path is required")
	}
	if docker.Workspace.Mode != ExecutionWorkspaceNone &&
		docker.Workspace.Mode != ExecutionWorkspaceRO &&
		docker.Workspace.Mode != ExecutionWorkspaceRW {
		return errors.New("execution docker workspace mode must be none, ro, or rw")
	}
	if docker.Workspace.Mode == ExecutionWorkspaceNone && docker.Workspace.Source != "" {
		return errors.New("execution docker workspace source is not allowed in none mode")
	}
	if docker.Workspace.Mode != ExecutionWorkspaceNone && !filepath.IsAbs(docker.Workspace.Source) {
		return errors.New("execution docker workspace source must be absolute")
	}
	if docker.Network != ExecutionNetworkNone && docker.Network != ExecutionNetworkBridge {
		return errors.New("execution docker network must be none or bridge")
	}
	seenMounts := map[string]struct{}{}
	for _, mount := range docker.Mounts {
		if !executionNamePattern.MatchString(mount.Name) || !filepath.IsAbs(mount.Source) ||
			(mount.Mode != ExecutionWorkspaceRO && mount.Mode != ExecutionWorkspaceRW) {
			return fmt.Errorf("execution docker mount %q is invalid", mount.Name)
		}
		if _, exists := seenMounts[mount.Name]; exists {
			return fmt.Errorf("execution docker mount %q is duplicated", mount.Name)
		}
		seenMounts[mount.Name] = struct{}{}
	}
	seenSecrets := map[string]struct{}{}
	for _, secret := range docker.Secrets {
		if !executionNamePattern.MatchString(secret.Name) || secret.Env == "" ||
			secret.Description == "" {
			return fmt.Errorf("execution docker secret %q is invalid", secret.Name)
		}
		if _, exists := seenSecrets[secret.Name]; exists {
			return fmt.Errorf("execution docker secret %q is duplicated", secret.Name)
		}
		seenSecrets[secret.Name] = struct{}{}
	}
	return nil
}

func isLocalDockerEndpoint(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if strings.HasPrefix(endpoint, "unix://") {
		return filepath.IsAbs(strings.TrimPrefix(endpoint, "unix://"))
	}
	if strings.HasPrefix(endpoint, "/") {
		return true
	}
	lower := strings.ToLower(endpoint)
	return strings.HasPrefix(lower, `//./pipe/`) || strings.HasPrefix(lower, `npipe:////./pipe/`) ||
		strings.HasPrefix(lower, `\\.\pipe\`)
}
