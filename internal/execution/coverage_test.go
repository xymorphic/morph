package execution

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	commandplan "github.com/wandxy/morph/internal/command"
)

func TestOwner_NormalizeAndIncarnation(t *testing.T) {
	owner, err := (Owner{
		Profile:            " profile ",
		ActorKind:          " LOCAL_OWNER ",
		ActorID:            " actor ",
		Surface:            " TUI ",
		PublicSessionID:    " public ",
		EffectiveSessionID: " effective ",
		RunID:              " run ",
	}).Normalize()
	require.NoError(t, err)
	require.Equal(t, "profile", owner.Profile)
	require.Equal(t, "local_owner", owner.ActorKind)
	require.Equal(t, "actor", owner.ActorID)
	require.Equal(t, "tui", owner.Surface)
	require.Equal(t, "public", owner.PublicSessionID)
	require.Equal(t, "effective", owner.EffectiveSessionID)
	require.Equal(t, "run", owner.RunID)

	for _, invalid := range []Owner{
		{},
		{
			Profile: "profile",
		},
		{
			Profile:   "profile",
			ActorKind: "owner",
		},
		{
			Profile:   "profile",
			ActorKind: "owner",
			Surface:   "cli",
		},
		{
			Profile:         "profile",
			ActorKind:       "owner",
			Surface:         "cli",
			PublicSessionID: "public",
		},
	} {
		_, err := invalid.Normalize()
		require.EqualError(t, err, "execution owner identity is incomplete")
		require.Empty(t, invalid.Fingerprint())
	}

	incarnation, err := NewIncarnation()
	require.NoError(t, err)
	require.Len(t, incarnation, 32)

	original := randomRead
	randomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy failed")
	}
	t.Cleanup(func() {
		randomRead = original
	})
	_, err = NewIncarnation()
	require.EqualError(t, err, "entropy failed")
}

func TestProcessCodec_ValidatesAndClassifiesHandles(t *testing.T) {
	owner := testOwner()
	key := []byte("01234567890123456789012345678901")

	for _, test := range []struct {
		key        []byte
		generation string
		daemon     string
	}{
		{
			key:        []byte("short"),
			generation: "generation",
			daemon:     "daemon",
		},
		{
			key:    key,
			daemon: "daemon",
		},
		{
			key:        key,
			generation: "generation",
		},
	} {
		_, err := NewProcessCodec(test.key, test.generation, test.daemon)
		require.EqualError(t, err, "process identity codec configuration is incomplete")
	}

	codec, err := NewProcessCodec(key, " generation ", " daemon ")
	require.NoError(t, err)
	_, err = (*ProcessCodec)(nil).Encode(owner, "container", "token")
	require.ErrorIs(t, err, ErrInvalidProcessID)
	_, err = codec.Encode(Owner{}, "container", "token")
	require.ErrorIs(t, err, ErrInvalidProcessID)
	_, err = codec.Encode(owner, "", "token")
	require.ErrorIs(t, err, ErrInvalidProcessID)
	_, err = codec.Encode(owner, "container", "")
	require.ErrorIs(t, err, ErrInvalidProcessID)

	handle, err := codec.Encode(owner, " container ", " token ")
	require.NoError(t, err)
	identity, err := codec.Decode(handle, owner, "container")
	require.NoError(t, err)
	require.Equal(t, "token", identity.Token)
	_, err = codec.Decode(handle, owner, "other")
	require.ErrorIs(t, err, ErrProcessStale)
	_, err = codec.Decode("invalid", owner, "container")
	require.ErrorIs(t, err, ErrInvalidProcessID)
	_, err = codec.DecodeCurrent("invalid", owner)
	require.ErrorIs(t, err, ErrInvalidProcessID)
	_, err = (*ProcessCodec)(nil).Verify(handle)
	require.ErrorIs(t, err, ErrInvalidProcessID)

	for _, value := range []string{
		"",
		"one-part",
		"%%%.$$$",
		base64.RawURLEncoding.EncodeToString([]byte("payload")) + ".%%%",
		handle + "x",
	} {
		_, err := codec.Verify(value)
		require.ErrorIs(t, err, ErrInvalidProcessID)
	}

	for _, payload := range []ProcessIdentity{
		{
			Version: 2,
			Token:   "token",
		},
		{
			Version: 1,
		},
	} {
		_, err := codec.Verify(signProcessIdentity(t, codec, payload))
		require.ErrorIs(t, err, ErrInvalidProcessID)
	}

	invalidJSON := signProcessPayload(codec, []byte("not-json"))
	_, err = codec.Verify(invalidJSON)
	require.ErrorIs(t, err, ErrInvalidProcessID)
}

func TestLoadOrCreateIdentityKey(t *testing.T) {
	_, err := LoadOrCreateIdentityKey("")
	require.EqualError(t, err, "execution identity key path is required")

	path := filepath.Join(t.TempDir(), "nested", "identity.key")
	created, err := LoadOrCreateIdentityKey(path)
	require.NoError(t, err)
	require.Len(t, created, 32)
	loaded, err := LoadOrCreateIdentityKey(path)
	require.NoError(t, err)
	require.Equal(t, created, loaded)

	shortPath := filepath.Join(t.TempDir(), "short.key")
	require.NoError(t, os.WriteFile(shortPath, []byte("short"), 0o600))
	_, err = LoadOrCreateIdentityKey(shortPath)
	require.EqualError(t, err, "execution identity key is invalid")

	broadPath := filepath.Join(t.TempDir(), "broad.key")
	require.NoError(t, os.WriteFile(broadPath, make([]byte, 32), 0o644))
	_, err = LoadOrCreateIdentityKey(broadPath)
	require.EqualError(t, err, "execution identity key permissions are too broad")

	directoryPath := t.TempDir()
	_, err = LoadOrCreateIdentityKey(directoryPath)
	require.Error(t, err)

	blockedParent := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockedParent, []byte("value"), 0o600))
	_, err = LoadOrCreateIdentityKey(filepath.Join(blockedParent, "identity.key"))
	require.Error(t, err)
}

func TestLoadOrCreateIdentityKey_HandlesSystemFailures(t *testing.T) {
	t.Run("stat", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		readIdentityKey = func(string) ([]byte, error) {
			return make([]byte, 32), nil
		}
		statIdentityKey = func(string) (os.FileInfo, error) {
			return nil, errors.New("stat failed")
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.EqualError(t, err, "stat failed")
	})

	t.Run("mkdir", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		readIdentityKey = missingIdentityKey
		makeIdentityKeyDirs = func(string, os.FileMode) error {
			return errors.New("mkdir failed")
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.EqualError(t, err, "mkdir failed")
	})

	t.Run("entropy", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		readIdentityKey = missingIdentityKey
		readIdentityEntropy = func([]byte) (int, error) {
			return 0, errors.New("entropy failed")
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.EqualError(t, err, "entropy failed")
	})

	t.Run("concurrent creation", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		reads := 0
		readIdentityKey = func(string) ([]byte, error) {
			reads++
			if reads == 1 {
				return nil, os.ErrNotExist
			}
			return make([]byte, 32), nil
		}
		statIdentityKey = func(string) (os.FileInfo, error) {
			return os.Stat(filepath.Join(t.TempDir(), "missing"))
		}
		openIdentityKey = func(string, int, os.FileMode) (identityKeyFile, error) {
			return nil, os.ErrExist
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.Error(t, err)
		require.Equal(t, 2, reads)
	})

	t.Run("open", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		readIdentityKey = missingIdentityKey
		openIdentityKey = func(string, int, os.FileMode) (identityKeyFile, error) {
			return nil, errors.New("open failed")
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.EqualError(t, err, "open failed")
	})

	t.Run("write", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		readIdentityKey = missingIdentityKey
		removed := false
		file := &identityKeyFileStub{
			writeErr: errors.New("write failed"),
		}
		openIdentityKey = func(string, int, os.FileMode) (identityKeyFile, error) {
			return file, nil
		}
		removeIdentityKey = func(string) error {
			removed = true
			return nil
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.EqualError(t, err, "write failed")
		require.True(t, file.closed)
		require.True(t, removed)
	})

	t.Run("close", func(t *testing.T) {
		restoreIdentityKeyDependencies(t)
		readIdentityKey = missingIdentityKey
		removed := false
		file := &identityKeyFileStub{
			closeErr: errors.New("close failed"),
		}
		openIdentityKey = func(string, int, os.FileMode) (identityKeyFile, error) {
			return file, nil
		}
		removeIdentityKey = func(string) error {
			removed = true
			return nil
		}

		_, err := LoadOrCreateIdentityKey("identity.key")
		require.EqualError(t, err, "close failed")
		require.True(t, removed)
	})
}

func TestAcquireManager_ValidatesAndPropagatesFailures(t *testing.T) {
	_, err := AcquireManager("", func() (Service, error) {
		return &managedServiceStub{}, nil
	})
	require.EqualError(t, err, "execution manager configuration is incomplete")
	_, err = AcquireManager("key", nil)
	require.EqualError(t, err, "execution manager configuration is incomplete")
	_, err = AcquireManager(t.Name(), func() (Service, error) {
		return nil, errors.New("build failed")
	})
	require.EqualError(t, err, "build failed")

	var nilManager *Manager
	require.NoError(t, nilManager.Close(context.Background()))
	require.NoError(t, (&Manager{}).Close(context.Background()))

	registry := &managerRegistry{
		entries: map[string]*managerEntry{},
	}
	manager := &Manager{
		key:      "missing",
		registry: registry,
	}
	require.NoError(t, manager.Close(context.Background()))
}

func TestImageContract_NormalizeAndDigest(t *testing.T) {
	contract := validImageContract()
	contract.Architectures = []string{" ARM64 ", "amd64", "arm64"}
	contract.Executables = map[string]string{
		" sh ":   " /bin/sh ",
		"patch":  "/usr/bin/patch",
		"printf": "/usr/bin/printf",
	}

	normalized, err := contract.Normalize()
	require.NoError(t, err)
	require.Equal(t, []string{"amd64", "arm64"}, normalized.Architectures)
	require.Equal(t, "/bin/sh", normalized.Executables["sh"])
	require.True(t, normalized.SupportsArchitecture(" AMD64 "))
	require.True(t, normalized.SupportsArchitecture("arm64"))
	require.False(t, normalized.SupportsArchitecture("s390x"))
	require.NotEmpty(t, normalized.Digest())

	contract.Executables["sh"] = "/changed"
	require.Equal(t, "/bin/sh", normalized.Executables["sh"])

	invalid := validImageContract()
	invalid.GOOS = "windows"
	_, err = invalid.Normalize()
	require.EqualError(t, err, "sandbox image contract is incomplete")
	require.Empty(t, invalid.Digest())

	invalid = validImageContract()
	invalid.PATH = []string{"relative"}
	_, err = invalid.Normalize()
	require.EqualError(t, err, "sandbox image PATH contains a non-absolute entry")

	invalid = validImageContract()
	invalid.Executables = map[string]string{"": "/bin/sh"}
	_, err = invalid.Normalize()
	require.EqualError(t, err, "sandbox image executable identity is invalid")

	invalid = validImageContract()
	invalid.Executables = map[string]string{"shell": "/bin/sh"}
	_, err = invalid.Normalize()
	require.EqualError(t, err, "sandbox image executable name does not match its path")

	require.False(t, isAbsoluteContractPath("/"))
	require.Nil(t, cloneStringMap(nil))
}

func TestPreparedPath_NormalizesAndValidates(t *testing.T) {
	path, err := NewPreparedPath(PreparedPathInput{
		LogicalPath:        " ./folder/file.txt ",
		HostSourceIdentity: " /host/file.txt ",
		ContainerPath:      " /workspace/folder/file.txt ",
		Grant:              " WORKSPACE ",
		Mode:               "RW",
		Action:             "WRITE",
		SecurityGeneration: " generation ",
	})
	require.NoError(t, err)
	require.Equal(t, "folder/file.txt", path.LogicalPath())
	require.Equal(t, "/host/file.txt", path.HostSourceIdentity())
	require.Equal(t, "/workspace/folder/file.txt", path.ContainerPath())
	require.Equal(t, "workspace", path.Grant())
	require.Equal(t, MountReadWrite, path.Mode())
	require.Equal(t, FilesystemWrite, path.Action())
	require.Equal(t, "generation", path.SecurityGeneration())
	require.NotEmpty(t, path.Digest())

	for _, test := range []struct {
		input PreparedPathInput
		err   string
	}{
		{
			input: PreparedPathInput{},
			err:   "execution logical path is required",
		},
		{
			input: PreparedPathInput{
				LogicalPath: "file",
			},
			err: "execution container path must be absolute",
		},
		{
			input: PreparedPathInput{
				LogicalPath:   "file",
				ContainerPath: "/file",
			},
			err: "execution path mode is invalid",
		},
		{
			input: PreparedPathInput{
				LogicalPath:   "file",
				ContainerPath: "/file",
				Mode:          MountReadOnly,
			},
			err: "execution path security generation is required",
		},
		{
			input: PreparedPathInput{
				LogicalPath:        "file",
				ContainerPath:      "/file",
				Mode:               MountReadOnly,
				SecurityGeneration: "generation",
			},
			err: "execution path action is invalid",
		},
		{
			input: PreparedPathInput{
				LogicalPath:        "file",
				ContainerPath:      "/file",
				Mode:               MountReadOnly,
				Action:             FilesystemPatch,
				SecurityGeneration: "generation",
			},
			err: "execution path is read-only",
		},
	} {
		_, err := NewPreparedPath(test.input)
		require.EqualError(t, err, test.err)
	}

	for _, action := range []FilesystemAction{
		FilesystemRead,
		FilesystemWrite,
		FilesystemPatch,
		FilesystemList,
		FilesystemSearch,
	} {
		require.True(t, isFilesystemAction(action))
	}
	require.False(t, isFilesystemAction("invalid"))
}

func TestCommandTarget_Resolve(t *testing.T) {
	target := CommandTarget{
		Executables: map[string]string{
			"echo": "/usr/bin/echo",
		},
	}

	value, err := target.Resolve(" echo ")
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/echo", value)
	value, err = target.Resolve("/usr/bin/../bin/echo")
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/echo", value)
	_, err = target.Resolve("missing")
	require.EqualError(t, err, "command executable is absent from the sandbox contract")
	_, err = target.Resolve("/usr/bin/missing")
	require.EqualError(t, err, "command executable is absent from the sandbox contract")
}

func TestExposure_NormalizesAndExposesImmutableValues(t *testing.T) {
	input := testExposureInput()
	input.Mounts = []Mount{
		{
			Name:           " B ",
			SourceIdentity: " /b ",
			Target:         " /mount/b ",
			Mode:           "RO",
			Purpose:        " test ",
		},
		{
			Name:           "a",
			SourceIdentity: "/a",
			Target:         "/mount/a",
			Mode:           MountReadWrite,
		},
	}
	input.SecretReferences = []string{" beta ", "alpha"}
	exposure, err := NewExposure(input)
	require.NoError(t, err)
	require.Equal(t, BackendDocker, exposure.Backend())
	require.Equal(t, ScopeSession, exposure.Scope())
	require.Equal(t, "default:session:default", exposure.WorkspaceIdentity())
	require.Equal(t, WorkspaceNone, exposure.WorkspaceMode())
	require.Equal(t, []string{"a", "b"}, []string{
		exposure.Mounts()[0].Name,
		exposure.Mounts()[1].Name,
	})
	require.Equal(t, NetworkNone, exposure.Network())
	require.Equal(t, []string{"alpha", "beta"}, exposure.SecretReferences())
	require.Equal(t, input.ImageDigest, exposure.ImageDigest())
	require.Equal(t, input.ImageContractDigest, exposure.ImageContractDigest())
	require.Equal(t, input.PolicyHash, exposure.PolicyHash())
	require.Equal(t, input.SecurityGeneration, exposure.SecurityGeneration())
	require.Equal(t, input.Limits, exposure.Limits())
	require.Equal(t, input.EnvironmentIdleExpiry, exposure.EnvironmentIdleExpiry())
	require.Equal(t, input.SharedDisabledRetention, exposure.SharedDisabledRetention())

	mounts := exposure.Mounts()
	mounts[0].Name = "changed"
	secrets := exposure.SecretReferences()
	secrets[0] = "changed"
	require.Equal(t, "a", exposure.Mounts()[0].Name)
	require.Equal(t, "alpha", exposure.SecretReferences()[0])

	encoded, err := json.Marshal(exposure)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "\"backend\":\"docker\"")
	encoded, err = json.Marshal(Exposure{})
	require.NoError(t, err)
	require.Equal(t, "null", string(encoded))
}

func TestExposure_RejectsInvalidInputs(t *testing.T) {
	valid := testExposureInput()
	tests := []struct {
		name string
		edit func(*ExposureInput)
		err  string
	}{
		{
			name: "backend",
			edit: func(input *ExposureInput) { input.Backend = "invalid" },
			err:  "execution exposure backend is invalid",
		},
		{
			name: "scope",
			edit: func(input *ExposureInput) { input.Scope = "invalid" },
			err:  "execution exposure scope is invalid",
		},
		{
			name: "workspace mode",
			edit: func(input *ExposureInput) { input.WorkspaceMode = "invalid" },
			err:  "execution exposure workspace mode is invalid",
		},
		{
			name: "workspace identity",
			edit: func(input *ExposureInput) { input.WorkspaceIdentity = "" },
			err:  "execution exposure workspace identity is required",
		},
		{
			name: "network",
			edit: func(input *ExposureInput) { input.Network = "invalid" },
			err:  "execution exposure network mode is invalid",
		},
		{
			name: "generation",
			edit: func(input *ExposureInput) { input.SecurityGeneration = "" },
			err:  "execution exposure security generation is required",
		},
		{
			name: "expiry",
			edit: func(input *ExposureInput) { input.EnvironmentIdleExpiry = 0 },
			err:  "execution exposure retention is invalid",
		},
		{
			name: "mount identity",
			edit: func(input *ExposureInput) {
				input.Mounts = []Mount{
					{
						Mode: MountReadOnly,
					},
				}
			},
			err: "execution exposure mount identity is incomplete",
		},
		{
			name: "mount mode",
			edit: func(input *ExposureInput) {
				input.Mounts = []Mount{
					{
						Name:           "mount",
						SourceIdentity: "/source",
						Target:         "/target",
						Mode:           "invalid",
					},
				}
			},
			err: "execution exposure mount mode is invalid",
		},
		{
			name: "duplicate mounts",
			edit: func(input *ExposureInput) {
				input.Mounts = []Mount{
					{
						Name:           "same",
						SourceIdentity: "/one",
						Target:         "/one",
						Mode:           MountReadOnly,
					},
					{
						Name:           "same",
						SourceIdentity: "/two",
						Target:         "/two",
						Mode:           MountReadOnly,
					},
				}
			},
			err: "execution exposure mount names must be unique",
		},
		{
			name: "empty secret",
			edit: func(input *ExposureInput) { input.SecretReferences = []string{""} },
			err:  "execution exposure secret references cannot be empty",
		},
		{
			name: "duplicate secrets",
			edit: func(input *ExposureInput) {
				input.SecretReferences = []string{"same", "same"}
			},
			err: "execution exposure secret references must be unique",
		},
		{
			name: "image",
			edit: func(input *ExposureInput) { input.ImageDigest = "" },
			err:  "docker execution exposure requires image and contract digests",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.edit(&input)
			_, err := NewExposure(input)
			require.EqualError(t, err, test.err)
		})
	}
}

func TestSpec_NormalizesEveryOperationKind(t *testing.T) {
	exposure, err := NewExposure(testExposureInput())
	require.NoError(t, err)
	plan := testCommandPlan(t)
	path := testPreparedPath(t, FilesystemPatch, "generation")
	second := testPreparedPath(t, FilesystemPatch, "generation")
	stdoutCursor := 4
	stderrCursor := 8

	operations := []Operation{
		{
			Kind:             " COMMAND ",
			SecretReferences: []string{" beta ", "alpha"},
			Command:          &plan,
		},
		{
			Kind: OperationProcess,
			Process: &ProcessOperation{
				Action:            " START ",
				Plan:              &plan,
				OutputBufferBytes: 1024,
				StdoutCursor:      &stdoutCursor,
				StderrCursor:      &stderrCursor,
			},
		},
		{
			Kind: OperationProcess,
			Process: &ProcessOperation{
				Action:    ProcessStatus,
				ProcessID: "process",
			},
		},
		{
			Kind: OperationFilesystem,
			Filesystem: &FilesystemOperation{
				Action:        FilesystemPatch,
				Path:          path,
				Paths:         []PreparedPath{second},
				Creates:       []bool{true},
				Data:          []byte("patch"),
				Query:         "query",
				Recursive:     true,
				IncludeHidden: true,
				CaseSensitive: true,
				Strip:         1,
			},
		},
	}

	for _, operation := range operations {
		spec, err := NewSpec(testOwner(), exposure, operation)
		require.NoError(t, err)
		require.Equal(t, testOwner(), spec.Owner())
		require.Equal(t, exposure.Digest(), spec.Exposure().Digest())
		require.NotEmpty(t, spec.OperationDigest())
		require.NotEmpty(t, spec.Digest())
		encoded, err := json.Marshal(spec)
		require.NoError(t, err)
		require.Contains(t, string(encoded), "operation_digest")

		clone := spec.Operation()
		clone.SecretReferences = append(clone.SecretReferences, "changed")
		if clone.Command != nil {
			clone.Command = nil
		}
		if clone.Process != nil {
			clone.Process.ProcessID = "changed"
			if clone.Process.StdoutCursor != nil {
				*clone.Process.StdoutCursor = 99
			}
		}
		if clone.Filesystem != nil {
			clone.Filesystem.Data[0] = 'X'
			clone.Filesystem.Paths = nil
			clone.Filesystem.Creates = nil
		}
		require.NotEqual(t, clone, spec.Operation())
	}
}

func TestSpec_RejectsInvalidOperations(t *testing.T) {
	exposure, err := NewExposure(testExposureInput())
	require.NoError(t, err)
	plan := testCommandPlan(t)
	path := testPreparedPath(t, FilesystemRead, "generation")
	otherGeneration := testPreparedPath(t, FilesystemRead, "other")

	_, err = NewSpec(Owner{}, exposure, Operation{})
	require.EqualError(t, err, "execution owner identity is incomplete")
	_, err = NewSpec(testOwner(), Exposure{}, Operation{})
	require.EqualError(t, err, "execution exposure is required")

	tests := []struct {
		name      string
		operation Operation
		err       string
	}{
		{
			name:      "empty secret",
			operation: Operation{SecretReferences: []string{""}},
			err:       "execution operation secret references must be non-empty and unique",
		},
		{
			name: "duplicate secret",
			operation: Operation{
				SecretReferences: []string{"same", "same"},
			},
			err: "execution operation secret references must be non-empty and unique",
		},
		{
			name:      "missing payload",
			operation: Operation{
				Kind: OperationCommand,
			},
			err:       "execution operation requires exactly one typed payload",
		},
		{
			name: "multiple payloads",
			operation: Operation{
				Kind:    OperationCommand,
				Command: &plan,
				Process: &ProcessOperation{},
			},
			err: "execution operation requires exactly one typed payload",
		},
		{
			name: "invalid command plan",
			operation: Operation{
				Kind:    OperationCommand,
				Command: &commandplan.Plan{},
			},
			err: "execution command operation requires a prepared plan",
		},
		{
			name: "invalid process action",
			operation: Operation{
				Kind:    OperationProcess,
				Process: &ProcessOperation{
					Action: "invalid",
				},
			},
			err: "execution process action is invalid",
		},
		{
			name: "process start plan",
			operation: Operation{
				Kind:    OperationProcess,
				Process: &ProcessOperation{
					Action: ProcessStart,
				},
			},
			err: "execution process start requires a prepared plan",
		},
		{
			name: "process ID",
			operation: Operation{
				Kind:    OperationProcess,
				Process: &ProcessOperation{
					Action: ProcessRead,
				},
			},
			err: "execution process action requires a process ID",
		},
		{
			name: "invalid filesystem action",
			operation: Operation{
				Kind:       OperationFilesystem,
				Filesystem: &FilesystemOperation{
					Action: "invalid",
				},
			},
			err: "execution filesystem action is invalid",
		},
		{
			name: "missing filesystem path",
			operation: Operation{
				Kind: OperationFilesystem,
				Filesystem: &FilesystemOperation{
					Action: FilesystemRead,
				},
			},
			err: "execution filesystem operation requires a prepared path",
		},
		{
			name: "invalid additional path",
			operation: Operation{
				Kind: OperationFilesystem,
				Filesystem: &FilesystemOperation{
					Action: FilesystemRead,
					Path:   path,
					Paths:  []PreparedPath{{}},
				},
			},
			err: "execution filesystem operation contains an invalid prepared path",
		},
		{
			name: "different path generation",
			operation: Operation{
				Kind: OperationFilesystem,
				Filesystem: &FilesystemOperation{
					Action: FilesystemRead,
					Path:   path,
					Paths:  []PreparedPath{otherGeneration},
				},
			},
			err: "execution filesystem operation contains an invalid prepared path",
		},
		{
			name: "invalid kind",
			operation: Operation{
				Kind:    "invalid",
				Command: &plan,
			},
			err: "execution operation kind is invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSpec(testOwner(), exposure, test.operation)
			require.EqualError(t, err, test.err)
		})
	}
}

func testOwner() Owner {
	return Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		ActorID:            "actor",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}
}

func validImageContract() ImageContract {
	return ImageContract{
		Version:       "1",
		GOOS:          "linux",
		Architecture:  "amd64",
		User:          "65532:65532",
		Shell:         "/bin/sh",
		PATH:          []string{"/usr/bin", "/bin"},
		Executables:   map[string]string{"sh": "/bin/sh"},
		Helper:        "/usr/local/bin/morph-sandbox",
		WorkspacePath: "/workspace",
		HomePath:      "/home/morph",
		TemporaryPath: "/tmp",
		ControlPath:   "/run/morph",
	}
}

func testCommandPlan(t *testing.T) commandplan.Plan {
	t.Helper()
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          "echo",
		Args:             []string{"hello"},
		CWD:              "/workspace",
		Environment:      map[string]string{"PATH": "/usr/bin"},
		CleanEnvironment: true,
		LookPath: func(string) (string, error) {
			return "/usr/bin/echo", nil
		},
	})
	require.NoError(t, err)
	return plan
}

func testPreparedPath(
	t *testing.T,
	action FilesystemAction,
	generation string,
) PreparedPath {
	t.Helper()
	path, err := NewPreparedPath(PreparedPathInput{
		LogicalPath:        "/workspace/file",
		HostSourceIdentity: "/host/file",
		ContainerPath:      "/workspace/file",
		Mode:               MountReadWrite,
		Action:             action,
		SecurityGeneration: generation,
	})
	require.NoError(t, err)
	return path
}

func signProcessIdentity(
	t *testing.T,
	codec *ProcessCodec,
	identity ProcessIdentity,
) string {
	t.Helper()
	payload, err := json.Marshal(identity)
	require.NoError(t, err)
	return signProcessPayload(codec, payload)
}

func signProcessPayload(codec *ProcessCodec, payload []byte) string {
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type identityKeyFileStub struct {
	writeErr error
	closeErr error
	closed   bool
}

func (f *identityKeyFileStub) Write(value []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(value), nil
}

func (f *identityKeyFileStub) Close() error {
	f.closed = true
	return f.closeErr
}

func missingIdentityKey(string) ([]byte, error) {
	return nil, os.ErrNotExist
}

func restoreIdentityKeyDependencies(t *testing.T) {
	t.Helper()
	originalRead := readIdentityKey
	originalStat := statIdentityKey
	originalMkdir := makeIdentityKeyDirs
	originalEntropy := readIdentityEntropy
	originalOpen := openIdentityKey
	originalRemove := removeIdentityKey
	t.Cleanup(func() {
		readIdentityKey = originalRead
		statIdentityKey = originalStat
		makeIdentityKeyDirs = originalMkdir
		readIdentityEntropy = originalEntropy
		openIdentityKey = originalOpen
		removeIdentityKey = originalRemove
	})
}
