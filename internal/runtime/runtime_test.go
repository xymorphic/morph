package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/profile"
)

func TestWriteAndLoadActive(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	now = func() time.Time { return time.Date(2026, 5, 12, 1, 2, 3, 0, time.UTC) }
	processPID = func() int { return 12345 }

	metadata, err := WriteActive("127.0.0.1", 50052)
	require.NoError(t, err)
	require.Equal(t, "work", metadata.Profile)
	require.Equal(t, 12345, metadata.PID)
	require.Equal(t, RPC{Address: "127.0.0.1", Port: 50052}, metadata.RPC)

	data, err := os.ReadFile(filepath.Join(home, "runtime.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"profile": "work"`)
	require.Contains(t, string(data), `"port": 50052`)

	loaded, err := LoadActive()
	require.NoError(t, err)
	require.Equal(t, metadata, loaded)
}

func TestWrite_IsolatesProfiles(t *testing.T) {
	resetRuntimeHooks(t)
	workHome := t.TempDir()
	personalHome := t.TempDir()
	processPID = func() int { return 12345 }

	workProfile := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: workHome})
	personalProfile := profile.WithMetadataPaths(profile.Profile{Name: "personal", HomeDir: personalHome})
	work, err := Write(workProfile, "127.0.0.1", 50052)
	require.NoError(t, err)
	personal, err := Write(personalProfile, "127.0.0.1", 50053)
	require.NoError(t, err)

	require.Equal(t, 50052, work.RPC.Port)
	require.Equal(t, 50053, personal.RPC.Port)
	require.FileExists(t, filepath.Join(workHome, "runtime.json"))
	require.FileExists(t, filepath.Join(personalHome, "runtime.json"))
	require.NotEqual(t, filepath.Join(workHome, "runtime.json"), filepath.Join(personalHome, "runtime.json"))
	loadedWork, err := Load(workProfile)
	require.NoError(t, err)
	require.Equal(t, work, loadedWork)
	loadedPersonal, err := Load(personalProfile)
	require.NoError(t, err)
	require.Equal(t, personal, loadedPersonal)
}

func TestWriteDefaultsBlankProfileName(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	processPID = func() int { return 12345 }

	metadata, err := Write(profile.WithMetadataPaths(profile.Profile{HomeDir: home}), "127.0.0.1", 50052)

	require.NoError(t, err)
	require.Equal(t, profile.DefaultName, metadata.Profile)
}

func TestWriteRejectsIncompleteEndpoint(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())

	_, err := WriteActive("", 50052)
	require.EqualError(t, err, "runtime rpc address is required")

	_, err = WriteActive("127.0.0.1", 0)
	require.EqualError(t, err, "runtime rpc port must be greater than zero")
}

func TestWriteRequiresRuntimePath(t *testing.T) {
	resetRuntimeHooks(t)

	_, err := Write(profile.Profile{Name: "work"}, "127.0.0.1", 50052)

	require.EqualError(t, err, "profile runtime path is required")
}

func TestWriteReturnsMarshalError(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())
	marshalJSON = func(any, string, string) ([]byte, error) {
		return nil, errors.New("marshal failed")
	}

	_, err := WriteActive("127.0.0.1", 50052)

	require.EqualError(t, err, "marshal failed")
}

func TestWriteReturnsCreateDirError(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())
	mkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}

	_, err := WriteActive("127.0.0.1", 50052)

	require.EqualError(t, err, "create runtime metadata dir: mkdir failed")
}

func TestWriteReturnsFileWriteError(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())
	writeFile = func(string, []byte, os.FileMode) error {
		return errors.New("write failed")
	}

	_, err := WriteActive("127.0.0.1", 50052)

	require.EqualError(t, err, "write runtime metadata: write failed")
}

func TestLoadRejectsInvalidRuntimeFile(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "runtime.json"), []byte(`{"rpc":{"address":"","port":0}}`), 0o600))

	_, err := LoadActive()
	require.EqualError(t, err, "runtime rpc address is required")
}

func TestLoadRejectsInvalidRuntimePort(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "runtime.json"), []byte(`{"rpc":{"address":"127.0.0.1","port":0}}`), 0o600))

	_, err := LoadActive()

	require.EqualError(t, err, "runtime rpc port must be greater than zero")
}

func TestLoadRejectsMalformedRuntimeFile(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "runtime.json"), []byte(`{`), 0o600))

	_, err := LoadActive()

	require.ErrorContains(t, err, "parse runtime metadata")
}

func TestLoadRequiresRuntimePath(t *testing.T) {
	resetRuntimeHooks(t)

	_, err := Load(profile.Profile{Name: "work"})

	require.EqualError(t, err, "profile runtime path is required")
}

func TestResolveRPC_UsesRuntimeWhenConfigIsDefault(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	writeRuntimeFile(t, home, Metadata{
		Profile:   "work",
		PID:       123,
		RPC:       RPC{Address: "127.0.0.9", Port: 50090},
		StartedAt: time.Now().UTC(),
	})
	checkPID = func(pid int) error {
		require.Equal(t, 123, pid)
		return nil
	}
	dialRuntime = func(_ context.Context, address string, port int) error {
		require.Equal(t, "127.0.0.9", address)
		require.Equal(t, 50090, port)
		return nil
	}

	endpoint, err := ResolveRPC(context.Background(), nil, defaultRuntimeConfig())

	require.NoError(t, err)
	require.Equal(t, config.RPCConfig{Address: "127.0.0.9", Port: 50090}, endpoint)
}

func TestResolveRPC_FallsBackToConfigWhenRuntimeMissing(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())

	endpoint, err := ResolveRPC(context.Background(), nil, defaultRuntimeConfig())

	require.NoError(t, err)
	require.Equal(t, config.RPCConfig{Address: constants.DefaultRPCAddress, Port: constants.DefaultRPCPort}, endpoint)
}

func TestResolveRPC_ReturnsRuntimeLoadError(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "runtime.json"), []byte(`{`), 0o600))

	_, err := ResolveRPC(context.Background(), nil, defaultRuntimeConfig())

	require.ErrorContains(t, err, "parse runtime metadata")
}

func TestResolveRPC_KeepsExplicitRPCConfig(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())
	cfg := defaultRuntimeConfig()
	cfg.RPC = config.RPCConfig{Address: "127.0.0.8", Port: 50080}

	endpoint, err := ResolveRPC(context.Background(), nil, cfg)

	require.NoError(t, err)
	require.Equal(t, cfg.RPC, endpoint)
}

func TestResolveRPC_KeepsExplicitRPCFlags(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())
	cfg := defaultRuntimeConfig()
	cmd := &cli.Command{
		Flags: []cli.Flag{&cli.IntFlag{Name: "rpc.port"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			endpoint, err := ResolveRPC(ctx, cmd, cfg)
			require.NoError(t, err)
			require.Equal(t, cfg.RPC, endpoint)
			return nil
		},
	}

	require.NoError(t, cmd.Run(context.Background(), []string{"morph", "--rpc.port", "50051"}))
}

func TestResolveRPC_KeepsExplicitRPCEnv(t *testing.T) {
	resetRuntimeHooks(t)
	setRuntimeProfile(t, "work", t.TempDir())
	t.Setenv("MORPH_RPC_PORT", "50051")
	cfg := defaultRuntimeConfig()

	endpoint, err := ResolveRPC(context.Background(), nil, cfg)

	require.NoError(t, err)
	require.Equal(t, cfg.RPC, endpoint)
}

func TestResolveRPC_ReturnsConfigError(t *testing.T) {
	resetRuntimeHooks(t)

	_, err := ResolveRPC(context.Background(), nil, nil)

	require.EqualError(t, err, "config is required")
}

func TestResolveRPC_RemovesStaleProcessMetadataAndFallsBackToConfig(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	runtimePath := writeRuntimeFile(t, home, Metadata{Profile: "work", PID: 999, RPC: RPC{Address: "127.0.0.1", Port: 50051}})
	checkPID = func(int) error { return errors.New("missing process") }

	endpoint, err := ResolveRPC(context.Background(), nil, defaultRuntimeConfig())

	require.NoError(t, err)
	require.Equal(t, config.RPCConfig{Address: constants.DefaultRPCAddress, Port: constants.DefaultRPCPort}, endpoint)
	require.NoFileExists(t, runtimePath)
}

func TestResolveRPC_RemovesUnreachableRuntimeMetadataAndFallsBackToConfig(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	setRuntimeProfile(t, "work", home)
	runtimePath := writeRuntimeFile(t, home, Metadata{Profile: "work", PID: 999, RPC: RPC{Address: "127.0.0.1", Port: 50051}})
	checkPID = func(int) error { return nil }
	dialRuntime = func(context.Context, string, int) error { return errors.New("connection refused") }

	endpoint, err := ResolveRPC(context.Background(), nil, defaultRuntimeConfig())

	require.NoError(t, err)
	require.Equal(t, config.RPCConfig{Address: constants.DefaultRPCAddress, Port: constants.DefaultRPCPort}, endpoint)
	require.NoFileExists(t, runtimePath)
}

func TestProbe_ReadOnlyRuntimeProbe(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	runtimePath := writeRuntimeFile(t, home, Metadata{
		Profile: "work",
		PID:     123,
		RPC:     RPC{Address: "127.0.0.9", Port: 50090},
	})
	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: home})
	checkPID = func(pid int) error {
		require.Equal(t, 123, pid)
		return nil
	}
	dialRuntime = func(_ context.Context, address string, port int) error {
		require.Equal(t, "127.0.0.9", address)
		require.Equal(t, 50090, port)
		return nil
	}

	result := Probe(context.Background(), active)

	require.NoError(t, result.Err)
	require.Equal(t, ProbeStateReady, result.State)
	require.Equal(t, "work", result.Metadata.Profile)
	require.FileExists(t, runtimePath)
}

func TestProbe_ReturnsStaleWithoutDeletingRuntimeMetadata(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	runtimePath := writeRuntimeFile(t, home, Metadata{
		Profile: "work",
		PID:     999,
		RPC:     RPC{Address: "127.0.0.1", Port: 50051},
	})
	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: home})
	checkPID = func(int) error { return errors.New("missing process") }

	result := Probe(context.Background(), active)

	require.ErrorContains(t, result.Err, "missing process")
	require.Equal(t, ProbeStateStale, result.State)
	require.FileExists(t, runtimePath)
}

func TestProbe_ReturnsStaleWhenEndpointIsUnreachable(t *testing.T) {
	resetRuntimeHooks(t)
	home := t.TempDir()
	runtimePath := writeRuntimeFile(t, home, Metadata{
		Profile: "work",
		PID:     999,
		RPC:     RPC{Address: "127.0.0.1", Port: 50051},
	})
	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: home})
	checkPID = func(int) error { return nil }
	dialRuntime = func(context.Context, string, int) error { return errors.New("connection refused") }

	result := Probe(context.Background(), active)

	require.ErrorContains(t, result.Err, "connection refused")
	require.Equal(t, ProbeStateStale, result.State)
	require.FileExists(t, runtimePath)
}

func TestProbe_ReturnsInvalidWhenRuntimePathIsMissing(t *testing.T) {
	resetRuntimeHooks(t)

	result := Probe(context.Background(), profile.Profile{Name: "work"})

	require.EqualError(t, result.Err, "profile runtime path is required")
	require.Equal(t, ProbeStateInvalid, result.State)
}

func TestProbe_ReturnsMissingAndInvalidStates(t *testing.T) {
	resetRuntimeHooks(t)
	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()})

	result := Probe(context.Background(), active)
	require.ErrorContains(t, result.Err, "runtime metadata is not present")
	require.Equal(t, ProbeStateMissing, result.State)

	require.NoError(t, os.WriteFile(active.RuntimePath, []byte(`{`), 0o600))
	result = Probe(context.Background(), active)
	require.ErrorContains(t, result.Err, "parse runtime metadata")
	require.Equal(t, ProbeStateInvalid, result.State)
}

func TestCheckProcessAcceptsCurrentProcess(t *testing.T) {
	require.NoError(t, checkProcess(os.Getpid()))
	require.EqualError(t, checkProcess(0), "runtime pid is required")
}

func TestCheckProcessReturnsFindError(t *testing.T) {
	resetRuntimeHooks(t)
	findProcess = func(int) (*os.Process, error) {
		return nil, errors.New("find failed")
	}

	err := checkProcess(123)

	require.EqualError(t, err, "runtime pid 123 cannot be inspected: find failed")
}

func TestCheckProcessReturnsSignalError(t *testing.T) {
	resetRuntimeHooks(t)
	findProcess = func(int) (*os.Process, error) {
		return &os.Process{}, nil
	}
	signalProcess = func(*os.Process, os.Signal) error {
		return errors.New("signal failed")
	}

	err := checkProcess(123)

	require.EqualError(t, err, "runtime pid 123 is not running: signal failed")
}

func TestDialRuntimeEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})

	err = dialRuntimeEndpoint(context.Background(), "127.0.0.1", listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, err)

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedPort := closed.Addr().(*net.TCPAddr).Port
	require.NoError(t, closed.Close())

	err = dialRuntimeEndpoint(context.Background(), "127.0.0.1", closedPort)
	require.Error(t, err)
}

func resetRuntimeHooks(t *testing.T) {
	t.Helper()
	t.Setenv("MORPH_RPC_ADDRESS", "")
	t.Setenv("MORPH_RPC_PORT", "")

	originalNow := now
	originalPID := processPID
	originalCheckPID := checkPID
	originalDial := dialRuntime
	originalMarshal := marshalJSON
	originalMkdirAll := mkdirAll
	originalWriteFile := writeFile
	originalReadFile := readFile
	originalRemoveFile := removeFile
	originalFindProcess := findProcess
	originalSignalProcess := signalProcess
	originalProfile := profile.Active()
	t.Cleanup(func() {
		now = originalNow
		processPID = originalPID
		checkPID = originalCheckPID
		dialRuntime = originalDial
		marshalJSON = originalMarshal
		mkdirAll = originalMkdirAll
		writeFile = originalWriteFile
		readFile = originalReadFile
		removeFile = originalRemoveFile
		findProcess = originalFindProcess
		signalProcess = originalSignalProcess
		profile.SetActive(originalProfile)
	})
}

func setRuntimeProfile(t *testing.T, name string, home string) {
	t.Helper()

	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: name, HomeDir: home}))
}

func writeRuntimeFile(t *testing.T, home string, metadata Metadata) string {
	t.Helper()

	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	path := filepath.Join(home, "runtime.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	return path
}

func defaultRuntimeConfig() *config.Config {
	return &config.Config{RPC: config.RPCConfig{Address: constants.DefaultRPCAddress, Port: constants.DefaultRPCPort}}
}
