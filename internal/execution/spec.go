package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	commandplan "github.com/xymorphic/morph/internal/command"
)

type Exposure struct {
	input  ExposureInput
	digest string
}

func NewExposure(input ExposureInput) (Exposure, error) {
	normalized, err := normalizeExposureInput(input)
	if err != nil {
		return Exposure{}, err
	}

	encoded, _ := json.Marshal(exposureCanonicalFromInput(normalized))
	sum := sha256.Sum256(encoded)

	return Exposure{
		input:  normalized,
		digest: hex.EncodeToString(sum[:]),
	}, nil
}

func (e Exposure) Digest() string {
	return e.digest
}

func (e Exposure) Backend() Backend {
	return e.input.Backend
}

func (e Exposure) Scope() Scope {
	return e.input.Scope
}

func (e Exposure) WorkspaceIdentity() string {
	return e.input.WorkspaceIdentity
}

func (e Exposure) WorkspaceMode() WorkspaceMode {
	return e.input.WorkspaceMode
}

func (e Exposure) Mounts() []Mount {
	return slices.Clone(e.input.Mounts)
}

func (e Exposure) Network() NetworkMode {
	return e.input.Network
}

func (e Exposure) SecretReferences() []string {
	return slices.Clone(e.input.SecretReferences)
}

func (e Exposure) ImageDigest() string {
	return e.input.ImageDigest
}

func (e Exposure) ImageContractDigest() string {
	return e.input.ImageContractDigest
}

func (e Exposure) PolicyHash() string {
	return e.input.PolicyHash
}

func (e Exposure) SecurityGeneration() string {
	return e.input.SecurityGeneration
}

func (e Exposure) Limits() Limits {
	return e.input.Limits
}

func (e Exposure) EnvironmentIdleExpiry() time.Duration {
	return e.input.EnvironmentIdleExpiry
}

func (e Exposure) SharedDisabledRetention() time.Duration {
	return e.input.SharedDisabledRetention
}

func (e Exposure) MarshalJSON() ([]byte, error) {
	if e.digest == "" {
		return []byte("null"), nil
	}
	return json.Marshal(exposureCanonicalFromInput(e.input))
}

type exposureCanonical struct {
	Backend              Backend       `json:"backend"`
	Scope                Scope         `json:"scope"`
	WorkspaceIdentity    string        `json:"workspace_identity"`
	WorkspaceMode        WorkspaceMode `json:"workspace_mode"`
	Mounts               []Mount       `json:"mounts,omitempty"`
	Network              NetworkMode   `json:"network"`
	SecretReferences     []string      `json:"secret_references,omitempty"`
	ImageDigest          string        `json:"image_digest,omitempty"`
	ImageContractDigest  string        `json:"image_contract_digest,omitempty"`
	PolicyHash           string        `json:"policy_hash,omitempty"`
	SecurityGeneration   string        `json:"security_generation,omitempty"`
	Limits               Limits        `json:"limits"`
	EnvironmentIdleNanos int64         `json:"environment_idle_nanos"`
	SharedRetentionNanos int64         `json:"shared_retention_nanos"`
}

func exposureCanonicalFromInput(input ExposureInput) exposureCanonical {
	return exposureCanonical{
		Backend:              input.Backend,
		Scope:                input.Scope,
		WorkspaceIdentity:    input.WorkspaceIdentity,
		WorkspaceMode:        input.WorkspaceMode,
		Mounts:               slices.Clone(input.Mounts),
		Network:              input.Network,
		SecretReferences:     slices.Clone(input.SecretReferences),
		ImageDigest:          input.ImageDigest,
		ImageContractDigest:  input.ImageContractDigest,
		PolicyHash:           input.PolicyHash,
		SecurityGeneration:   input.SecurityGeneration,
		Limits:               input.Limits,
		EnvironmentIdleNanos: int64(input.EnvironmentIdleExpiry),
		SharedRetentionNanos: int64(input.SharedDisabledRetention),
	}
}

func normalizeExposureInput(input ExposureInput) (ExposureInput, error) {
	input.Backend = Backend(strings.TrimSpace(strings.ToLower(string(input.Backend))))
	input.Scope = Scope(strings.TrimSpace(strings.ToLower(string(input.Scope))))
	input.WorkspaceMode = WorkspaceMode(
		strings.TrimSpace(strings.ToLower(string(input.WorkspaceMode))),
	)
	input.WorkspaceIdentity = strings.TrimSpace(input.WorkspaceIdentity)
	input.Network = NetworkMode(strings.TrimSpace(strings.ToLower(string(input.Network))))
	input.ImageDigest = strings.TrimSpace(input.ImageDigest)
	input.ImageContractDigest = strings.TrimSpace(input.ImageContractDigest)
	input.PolicyHash = strings.TrimSpace(input.PolicyHash)
	input.SecurityGeneration = strings.TrimSpace(input.SecurityGeneration)

	if input.Backend != BackendLocal && input.Backend != BackendDocker {
		return ExposureInput{}, errors.New("execution exposure backend is invalid")
	}
	if input.Scope != ScopeSession && input.Scope != ScopeShared {
		return ExposureInput{}, errors.New("execution exposure scope is invalid")
	}
	if input.WorkspaceMode != WorkspaceNone && input.WorkspaceMode != WorkspaceReadOnly &&
		input.WorkspaceMode != WorkspaceReadWrite {
		return ExposureInput{}, errors.New("execution exposure workspace mode is invalid")
	}
	if input.WorkspaceIdentity == "" {
		return ExposureInput{}, errors.New("execution exposure workspace identity is required")
	}
	if input.Network != NetworkNone && input.Network != NetworkBridge {
		return ExposureInput{}, errors.New("execution exposure network mode is invalid")
	}
	if input.SecurityGeneration == "" {
		return ExposureInput{}, errors.New("execution exposure security generation is required")
	}
	if input.EnvironmentIdleExpiry <= 0 || input.SharedDisabledRetention < 0 {
		return ExposureInput{}, errors.New("execution exposure retention is invalid")
	}

	input.Mounts = slices.Clone(input.Mounts)
	for index := range input.Mounts {
		mount := &input.Mounts[index]
		mount.Name = strings.TrimSpace(strings.ToLower(mount.Name))
		mount.SourceIdentity = strings.TrimSpace(mount.SourceIdentity)
		mount.Target = strings.TrimSpace(mount.Target)
		mount.Mode = MountMode(strings.TrimSpace(strings.ToLower(string(mount.Mode))))
		mount.Purpose = strings.TrimSpace(mount.Purpose)
		if mount.Name == "" || mount.SourceIdentity == "" || mount.Target == "" {
			return ExposureInput{}, errors.New("execution exposure mount identity is incomplete")
		}
		if mount.Mode != MountReadOnly && mount.Mode != MountReadWrite {
			return ExposureInput{}, errors.New("execution exposure mount mode is invalid")
		}
	}
	sort.Slice(
		input.Mounts,
		func(i, j int) bool { return input.Mounts[i].Name < input.Mounts[j].Name },
	)
	for index := 1; index < len(input.Mounts); index++ {
		if input.Mounts[index-1].Name == input.Mounts[index].Name {
			return ExposureInput{}, errors.New("execution exposure mount names must be unique")
		}
	}

	input.SecretReferences = normalizeNames(input.SecretReferences)
	if slices.Contains(input.SecretReferences, "") {
		return ExposureInput{}, errors.New("execution exposure secret references cannot be empty")
	}
	if len(input.SecretReferences) != len(slices.Compact(slices.Clone(input.SecretReferences))) {
		return ExposureInput{}, errors.New("execution exposure secret references must be unique")
	}
	if input.Backend == BackendDocker &&
		(input.ImageDigest == "" || input.ImageContractDigest == "") {
		return ExposureInput{}, errors.New(
			"docker execution exposure requires image and contract digests",
		)
	}

	return input, nil
}

func normalizeNames(values []string) []string {
	values = slices.Clone(values)
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	sort.Strings(values)
	return values
}

type Operation struct {
	Kind             OperationKind
	SecretReferences []string
	Command          *commandplan.Plan
	Process          *ProcessOperation
	Filesystem       *FilesystemOperation
}

type ProcessOperation struct {
	Action            ProcessAction
	Plan              *commandplan.Plan
	ProcessID         string
	Label             string
	OutputBufferBytes int
	StdoutCursor      *int
	StderrCursor      *int
}

type FilesystemOperation struct {
	Action        FilesystemAction
	Path          PreparedPath
	Paths         []PreparedPath
	Creates       []bool
	Data          []byte
	Query         string
	Recursive     bool
	IncludeHidden bool
	CaseSensitive bool
	Strip         int
}

func NewSpec(owner Owner, exposure Exposure, operation Operation) (Spec, error) {
	owner, err := owner.Normalize()
	if err != nil {
		return Spec{}, err
	}
	if exposure.digest == "" {
		return Spec{}, errors.New("execution exposure is required")
	}
	operation, err = normalizeOperation(operation)
	if err != nil {
		return Spec{}, err
	}

	operationDigest := getOperationDigest(operation)
	sum := sha256.Sum256(
		[]byte(owner.Fingerprint() + "\x00" + exposure.Digest() + "\x00" + operationDigest),
	)
	return Spec{
		owner:           owner,
		exposure:        exposure,
		operation:       operation,
		operationDigest: operationDigest,
		digest:          hex.EncodeToString(sum[:]),
	}, nil
}

type Spec struct {
	owner           Owner
	exposure        Exposure
	operation       Operation
	operationDigest string
	digest          string
}

func (s Spec) Owner() Owner            { return s.owner }
func (s Spec) Exposure() Exposure      { return s.exposure }
func (s Spec) Operation() Operation    { return cloneOperation(s.operation) }
func (s Spec) OperationDigest() string { return s.operationDigest }
func (s Spec) Digest() string          { return s.digest }

func (s Spec) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Owner           Owner    `json:"owner"`
		Exposure        Exposure `json:"exposure"`
		OperationKind   string   `json:"operation_kind"`
		OperationDigest string   `json:"operation_digest"`
		Digest          string   `json:"digest"`
	}{s.owner, s.exposure, string(s.operation.Kind), s.operationDigest, s.digest})
}

func normalizeOperation(operation Operation) (Operation, error) {
	operation.Kind = OperationKind(strings.TrimSpace(strings.ToLower(string(operation.Kind))))
	operation.SecretReferences = normalizeNames(operation.SecretReferences)
	if slices.Contains(operation.SecretReferences, "") ||
		len(
			operation.SecretReferences,
		) != len(
			slices.Compact(slices.Clone(operation.SecretReferences)),
		) {
		return Operation{}, errors.New(
			"execution operation secret references must be non-empty and unique",
		)
	}
	nonNil := 0
	if operation.Command != nil {
		nonNil++
	}
	if operation.Process != nil {
		nonNil++
	}
	if operation.Filesystem != nil {
		nonNil++
	}
	if nonNil != 1 {
		return Operation{}, errors.New("execution operation requires exactly one typed payload")
	}

	switch operation.Kind {
	case OperationCommand:
		if operation.Command == nil || operation.Command.Digest() == "" {
			return Operation{}, errors.New("execution command operation requires a prepared plan")
		}
	case OperationProcess:
		operation.Process.Action = ProcessAction(
			strings.TrimSpace(strings.ToLower(string(operation.Process.Action))),
		)
		if !slices.Contains(
			[]ProcessAction{ProcessStart, ProcessStatus, ProcessRead, ProcessStop, ProcessList},
			operation.Process.Action,
		) {
			return Operation{}, errors.New("execution process action is invalid")
		}
		if operation.Process.Action == ProcessStart &&
			(operation.Process.Plan == nil || operation.Process.Plan.Digest() == "") {
			return Operation{}, errors.New("execution process start requires a prepared plan")
		}
		if slices.Contains(
			[]ProcessAction{ProcessStatus, ProcessRead, ProcessStop},
			operation.Process.Action,
		) &&
			strings.TrimSpace(operation.Process.ProcessID) == "" {
			return Operation{}, errors.New("execution process action requires a process ID")
		}
	case OperationFilesystem:
		operation.Filesystem.Action = FilesystemAction(
			strings.TrimSpace(strings.ToLower(string(operation.Filesystem.Action))),
		)
		if !slices.Contains(
			[]FilesystemAction{
				FilesystemRead,
				FilesystemWrite,
				FilesystemPatch,
				FilesystemList,
				FilesystemSearch,
			},
			operation.Filesystem.Action,
		) {
			return Operation{}, errors.New("execution filesystem action is invalid")
		}
		if operation.Filesystem.Path.digest == "" {
			return Operation{}, errors.New(
				"execution filesystem operation requires a prepared path",
			)
		}
		operation.Filesystem.Paths = slices.Clone(operation.Filesystem.Paths)
		operation.Filesystem.Creates = slices.Clone(operation.Filesystem.Creates)
		for _, path := range operation.Filesystem.Paths {
			if path.digest == "" ||
				path.SecurityGeneration() != operation.Filesystem.Path.SecurityGeneration() {
				return Operation{}, errors.New(
					"execution filesystem operation contains an invalid prepared path",
				)
			}
		}
		operation.Filesystem.Data = slices.Clone(operation.Filesystem.Data)
	default:
		return Operation{}, errors.New("execution operation kind is invalid")
	}

	return cloneOperation(operation), nil
}

func cloneOperation(operation Operation) Operation {
	clone := operation
	clone.SecretReferences = slices.Clone(operation.SecretReferences)
	if operation.Command != nil {
		plan := operation.Command.Clone()
		clone.Command = &plan
	}
	if operation.Process != nil {
		value := *operation.Process
		if value.Plan != nil {
			plan := value.Plan.Clone()
			value.Plan = &plan
		}
		if value.StdoutCursor != nil {
			value.StdoutCursor = new(*value.StdoutCursor)
		}
		if value.StderrCursor != nil {
			value.StderrCursor = new(*value.StderrCursor)
		}
		clone.Process = &value
	}
	if operation.Filesystem != nil {
		value := *operation.Filesystem
		value.Data = slices.Clone(value.Data)
		value.Paths = slices.Clone(value.Paths)
		value.Creates = slices.Clone(value.Creates)
		clone.Filesystem = &value
	}
	return clone
}

func getOperationDigest(operation Operation) string {
	values := []string{string(operation.Kind), strings.Join(operation.SecretReferences, ",")}
	if operation.Command != nil {
		values = append(values, operation.Command.Digest())
	}
	if operation.Process != nil {
		values = append(
			values,
			string(operation.Process.Action),
			operation.Process.ProcessID,
			operation.Process.Label,
		)
		if operation.Process.Plan != nil {
			values = append(values, operation.Process.Plan.Digest())
		}
		values = append(values, fmt.Sprint(operation.Process.OutputBufferBytes))
	}
	if operation.Filesystem != nil {
		data := sha256.Sum256(operation.Filesystem.Data)
		pathDigests := make([]string, len(operation.Filesystem.Paths))
		for index, path := range operation.Filesystem.Paths {
			pathDigests[index] = path.Digest()
		}
		values = append(values,
			string(operation.Filesystem.Action),
			operation.Filesystem.Path.Digest(),
			hex.EncodeToString(data[:]),
			operation.Filesystem.Query,
			fmt.Sprint(operation.Filesystem.Recursive),
			fmt.Sprint(operation.Filesystem.IncludeHidden),
			fmt.Sprint(operation.Filesystem.CaseSensitive),
			strings.Join(pathDigests, ","),
			fmt.Sprint(operation.Filesystem.Creates),
			fmt.Sprint(operation.Filesystem.Strip),
		)
	}
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}
