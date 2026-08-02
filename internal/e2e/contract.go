package e2e

import (
	"context"
	"errors"

	"github.com/xymorphic/morph/pkg/str"
)

// Entrypoint identifies the execution boundary a scenario uses.
type Entrypoint string

const (
	// EntrypointDirectAgent is the preferred path for most scenarios.
	EntrypointDirectAgent Entrypoint = "direct_agent"
	// EntrypointCommandRPC is the smoke/full-stack path for command and RPC wiring.
	EntrypointCommandRPC Entrypoint = "command_rpc"
)

// Validate checks whether the entrypoint is one of the supported Phase 0 values.
func (e Entrypoint) Validate() error {
	switch e {
	case EntrypointDirectAgent, EntrypointCommandRPC:
		return nil
	case "":
		return errors.New("e2e entrypoint is required")
	default:
		return errors.New("unsupported e2e entrypoint")
	}
}

// RecommendedPrimaryEntrypoint returns the preferred Phase 0 harness path.
func RecommendedPrimaryEntrypoint() Entrypoint {
	return EntrypointDirectAgent
}

// RecommendedSecondaryEntrypoint returns the secondary smoke/full-stack path.
func RecommendedSecondaryEntrypoint() Entrypoint {
	return EntrypointCommandRPC
}

// ConfigMode describes how a scenario supplies runtime config.
type ConfigMode string

const (
	ConfigModeRealInput ConfigMode = "real_input"
	ConfigModeInMemory  ConfigMode = "in_memory"
)

// ConfigInput describes the config source for an e2e scenario.
type ConfigInput struct {
	EnvFilePath    string
	ConfigFilePath string
	Env            map[string]string
	AllowInMemory  bool
}

// Mode returns the effective config-loading mode for the scenario.
func (c ConfigInput) Mode() ConfigMode {
	envFilePathValue := str.String(c.EnvFilePath)
	configFilePathValue := str.String(c.ConfigFilePath)
	if envFilePathValue.Trim() != "" || configFilePathValue.Trim() != "" || len(c.Env) > 0 {
		return ConfigModeRealInput
	}
	return ConfigModeInMemory
}

// Validate enforces the Phase 0 rule:
// use real YAML/env loading when inputs exist, otherwise allow explicit in-memory fallback.
func (c ConfigInput) Validate() error {
	if c.Mode() == ConfigModeRealInput {
		return nil
	}
	if c.AllowInMemory {
		return nil
	}

	return errors.New("e2e config input requires real inputs or explicit in-memory fallback")
}

// Isolation defines the per-test resources that must stay isolated.
type Isolation struct {
	WorkspaceDir string
	DataDir      string
	StoragePath  string
	TraceDir     string
}

// Validate ensures the required isolated resources are configured.
func (i Isolation) Validate() error {
	workspaceDirValue := str.String(i.WorkspaceDir)
	if workspaceDirValue.Trim() == "" {
		return errors.New("e2e workspace dir is required")
	}
	dataDirValue := str.String(i.DataDir)
	if dataDirValue.Trim() == "" {
		return errors.New("e2e data dir is required")
	}
	storagePathValue := str.String(i.StoragePath)
	if storagePathValue.Trim() == "" {
		return errors.New("e2e storage path is required")
	}
	return nil
}

// HarnessSpec is the Phase 0 contract for the e2e harness.
type HarnessSpec struct {
	PrimaryEntrypoint   Entrypoint
	SecondaryEntrypoint Entrypoint
	Config              ConfigInput
	Isolation           Isolation
}

// Validate enforces the agreed Phase 0 design contract.
func (s HarnessSpec) Validate() error {
	if err := s.PrimaryEntrypoint.Validate(); err != nil {
		return err
	}
	if err := s.SecondaryEntrypoint.Validate(); err != nil {
		return err
	}
	if s.PrimaryEntrypoint == s.SecondaryEntrypoint {
		return errors.New("e2e primary and secondary entrypoints must differ")
	}
	if s.PrimaryEntrypoint != RecommendedPrimaryEntrypoint() {
		return errors.New("e2e primary entrypoint must use the direct agent path")
	}
	if err := s.Config.Validate(); err != nil {
		return err
	}
	if err := s.Isolation.Validate(); err != nil {
		return err
	}

	return nil
}

// Event is a user-visible stream event emitted during a root chat scenario.
type Event struct {
	Channel string
	Text    string
}

// RootChatRequest is the minimum request contract for the first e2e adapter.
type RootChatRequest struct {
	Message   string
	SessionID string
	Instruct  string
	Stream    *bool
}

// Validate enforces the Phase 0 root chat request contract.
func (r RootChatRequest) Validate() error {
	messageValue := str.String(r.Message)
	if messageValue.Trim() == "" {
		return errors.New("e2e root chat message is required")
	}
	return nil
}

// RootChatResult is the minimum response contract for the first e2e adapter.
type RootChatResult struct {
	Reply     string
	SessionID string
	Events    []Event
}

// RootChatAdapter defines the first interface adapter contract for e2e scenarios.
type RootChatAdapter interface {
	Send(context.Context, RootChatRequest) (RootChatResult, error)
}
