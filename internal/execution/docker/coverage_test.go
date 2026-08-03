package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	processenv "github.com/xymorphic/morph/internal/environment/process"
	"github.com/xymorphic/morph/internal/execution"
)

type fakeDockerServer struct {
	client           *Client
	listener         net.Listener
	server           *http.Server
	mu               sync.Mutex
	failure          string
	failureMethod    string
	driverStatus     [][]string
	imageOS          string
	imageArch        string
	imageUser        string
	imageEntry       []string
	containers       []map[string]any
	networks         []map[string]any
	volumes          []map[string]any
	waitStatus       int
	waitError        string
	attachOutput     string
	oomKilled        bool
	volumeExists     bool
	containerRunning bool
	stopStatus       int
	removeStatus     int
	inspectStatus    int
	holdAttachOpen   bool
	attachDelay      time.Duration
	closeAttach      bool
	rawAttach        []byte
	waitDelay        time.Duration
}

func newFakeDockerServer(t *testing.T) *fakeDockerServer {
	t.Helper()
	socketFile, err := os.CreateTemp("", "morph-docker-*.sock")
	require.NoError(t, err)
	socket := socketFile.Name()
	require.NoError(t, socketFile.Close())
	require.NoError(t, os.Remove(socket))
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	server := &fakeDockerServer{
		listener:  listener,
		imageOS:   "linux",
		imageArch: "amd64",
		imageUser: "65532:65532",
		imageEntry: []string{
			"/usr/local/bin/morph-sandbox",
		},
		attachOutput:     "output",
		containerRunning: true,
	}
	server.server = &http.Server{
		Handler: http.HandlerFunc(server.handle),
	}
	go func() {
		_ = server.server.Serve(listener)
	}()
	server.client, err = NewClient(socket)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.client.Close())
		require.NoError(t, server.server.Close())
		err := os.Remove(socket)
		require.True(t, err == nil || errors.Is(err, os.ErrNotExist))
	})
	return server
}

func (s *fakeDockerServer) setFailure(path string) {
	s.mu.Lock()
	s.failure = path
	s.failureMethod = ""
	s.mu.Unlock()
}

func (s *fakeDockerServer) setMethodFailure(method string, path string) {
	s.mu.Lock()
	s.failure = path
	s.failureMethod = method
	s.mu.Unlock()
}

func (s *fakeDockerServer) clearFailure() {
	s.setFailure("")
}

func (s *fakeDockerServer) handle(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	failure := s.failure
	failureMethod := s.failureMethod
	driverStatus := s.driverStatus
	imageOS := s.imageOS
	imageArch := s.imageArch
	imageUser := s.imageUser
	imageEntry := s.imageEntry
	containers := s.containers
	networks := s.networks
	volumes := s.volumes
	waitStatus := s.waitStatus
	waitError := s.waitError
	attachOutput := s.attachOutput
	oomKilled := s.oomKilled
	volumeExists := s.volumeExists
	containerRunning := s.containerRunning
	stopStatus := s.stopStatus
	removeStatus := s.removeStatus
	inspectStatus := s.inspectStatus
	holdAttachOpen := s.holdAttachOpen
	attachDelay := s.attachDelay
	closeAttach := s.closeAttach
	rawAttach := append([]byte(nil), s.rawAttach...)
	waitDelay := s.waitDelay
	s.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	if failure != "" && strings.Contains(request.URL.Path, failure) &&
		(failureMethod == "" || failureMethod == request.Method) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"message":"docker API failed"}`))
		return
	}
	switch {
	case strings.HasSuffix(request.URL.Path, "/_ping"):
		writer.Header().Set("API-Version", "1.52")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("OK"))
	case strings.HasSuffix(request.URL.Path, "/containers/json"):
		writeDockerJSON(writer, containers)
	case strings.HasSuffix(request.URL.Path, "/containers/create"):
		writeDockerJSON(writer, map[string]any{
			"Id":       "container",
			"Warnings": []string{},
		})
	case strings.Contains(request.URL.Path, "/containers/") &&
		strings.HasSuffix(request.URL.Path, "/attach"):
		hijackDockerStream(
			writer,
			request,
			attachOutput,
			holdAttachOpen,
			attachDelay,
			closeAttach,
			rawAttach,
		)
	case strings.Contains(request.URL.Path, "/containers/") &&
		strings.HasSuffix(request.URL.Path, "/wait"):
		if waitDelay > 0 {
			time.Sleep(waitDelay)
		}
		payload := map[string]any{
			"StatusCode": waitStatus,
		}
		if waitError != "" {
			payload["Error"] = map[string]string{
				"Message": waitError,
			}
		}
		writeDockerJSON(writer, payload)
	case strings.Contains(request.URL.Path, "/containers/") &&
		strings.HasSuffix(request.URL.Path, "/json"):
		if inspectStatus != 0 {
			writeDockerStatus(writer, inspectStatus)
			return
		}
		writeDockerJSON(writer, map[string]any{
			"Id": "container",
			"State": map[string]any{
				"Running":   containerRunning,
				"OOMKilled": oomKilled,
			},
		})
	case strings.Contains(request.URL.Path, "/containers/") &&
		strings.HasSuffix(request.URL.Path, "/start"):
		writer.WriteHeader(http.StatusNoContent)
	case strings.Contains(request.URL.Path, "/containers/") &&
		strings.HasSuffix(request.URL.Path, "/stop"):
		if stopStatus != 0 {
			writeDockerStatus(writer, stopStatus)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case strings.Contains(request.URL.Path, "/containers/") &&
		request.Method == http.MethodDelete:
		if removeStatus != 0 {
			writeDockerStatus(writer, removeStatus)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case strings.Contains(request.URL.Path, "/containers/") &&
		strings.HasSuffix(request.URL.Path, "/exec"):
		writeDockerJSON(writer, map[string]string{
			"Id": "exec",
		})
	case strings.HasSuffix(request.URL.Path, "/exec/exec/start"):
		hijackDockerStream(
			writer,
			request,
			attachOutput,
			holdAttachOpen,
			attachDelay,
			closeAttach,
			rawAttach,
		)
	case strings.HasSuffix(request.URL.Path, "/exec/exec/json"):
		writeDockerJSON(writer, map[string]any{
			"ExitCode": waitStatus,
			"Running":  false,
		})
	case strings.HasSuffix(request.URL.Path, "/networks") && request.Method == http.MethodGet:
		writeDockerJSON(writer, networks)
	case strings.HasSuffix(request.URL.Path, "/networks/create"):
		writeDockerJSON(writer, map[string]string{
			"Id": "network",
		})
	case strings.Contains(request.URL.Path, "/networks/") && request.Method == http.MethodGet:
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"not found"}`))
	case strings.Contains(request.URL.Path, "/networks/") && request.Method == http.MethodDelete:
		writer.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(request.URL.Path, "/volumes") && request.Method == http.MethodGet:
		writeDockerJSON(writer, map[string]any{
			"Volumes":  volumes,
			"Warnings": []string{},
		})
	case strings.HasSuffix(request.URL.Path, "/volumes/create"):
		writeDockerJSON(writer, map[string]string{
			"Name": "workspace",
		})
	case strings.Contains(request.URL.Path, "/volumes/") && request.Method == http.MethodGet:
		if volumeExists {
			writeDockerJSON(writer, map[string]string{
				"Name": "workspace",
			})
			return
		}
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"not found"}`))
	case strings.Contains(request.URL.Path, "/volumes/") && request.Method == http.MethodDelete:
		writer.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(request.URL.Path, "/info"):
		writeDockerJSON(writer, map[string]any{
			"DriverStatus": driverStatus,
		})
	case strings.Contains(request.URL.Path, "/images/") &&
		strings.HasSuffix(request.URL.Path, "/json"):
		writeDockerJSON(writer, map[string]any{
			"Os":           imageOS,
			"Architecture": imageArch,
			"Config": map[string]any{
				"User":       imageUser,
				"Entrypoint": imageEntry,
			},
		})
	default:
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"not found"}`))
	}
}

func writeDockerJSON(writer http.ResponseWriter, value any) {
	_ = json.NewEncoder(writer).Encode(value)
}

func writeDockerStatus(writer http.ResponseWriter, status int) {
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`{"message":"docker status failure"}`))
}

func hijackDockerStream(
	writer http.ResponseWriter,
	request *http.Request,
	output string,
	holdOpen bool,
	delay time.Duration,
	closeImmediately bool,
	raw []byte,
) {
	_, _ = io.Copy(io.Discard, request.Body)
	_ = request.Body.Close()

	connection, buffer, err := writer.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer func() {
		_ = connection.Close()
	}()
	_, _ = buffer.WriteString(
		"HTTP/1.1 101 UPGRADED\r\n" +
			"Content-Type: application/vnd.docker.raw-stream\r\n" +
			"Connection: Upgrade\r\n" +
			"Upgrade: tcp\r\n\r\n",
	)
	_ = buffer.Flush()
	if closeImmediately {
		return
	}
	if len(raw) > 0 {
		_, _ = connection.Write(raw)
		return
	}
	if output == "" {
		return
	}
	frame := make([]byte, 8+len(output))
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(output)))
	copy(frame[8:], output)
	_, _ = connection.Write(frame)
	if holdOpen {
		if delay <= 0 {
			delay = 100 * time.Millisecond
		}
		time.Sleep(delay)
	}
}

func newFakeBackend(clientValue *Client) *Backend {
	return &Backend{
		client:              clientValue,
		image:               "image",
		contract:            testContract(),
		daemonIncarnation:   "daemon",
		allowTestImage:      true,
		statuses:            map[string]execution.EnvironmentStatus{},
		processes:           map[string]*dockerProcess{},
		processOrder:        map[string][]string{},
		environments:        map[string]*sharedEnvironment{},
		sharedGates:         map[string]chan struct{}{},
		environmentLocks:    map[string]*sync.Mutex{},
		networks:            map[string]struct{}{},
		reconciledProfiles:  map[string]struct{}{},
		maximumEnvironments: 10,
		maximumVolumes:      10,
	}
}

func TestClientLifecycleAndEndpointNormalization(t *testing.T) {
	tests := []struct {
		input  string
		output string
		err    string
	}{
		{
			input:  " npipe:////./pipe/docker_engine ",
			output: "npipe:////./pipe/docker_engine",
		},
		{
			input:  `\\.\pipe\docker_engine`,
			output: "npipe:////./pipe/docker_engine",
		},
		{
			input:  "unix:///var/run/../run/docker.sock",
			output: "unix:///var/run/docker.sock",
		},
		{
			input:  "/var/run/docker.sock",
			output: "unix:///var/run/docker.sock",
		},
		{
			input: "unix://relative.sock",
			err:   "docker endpoint must use an absolute local socket",
		},
		{
			input: "tcp://localhost:2375",
			err:   "docker endpoint must be a local Unix socket or named pipe",
		},
	}
	for _, test := range tests {
		normalized, err := normalizeEndpoint(test.input)
		if test.err != "" {
			require.EqualError(t, err, test.err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, test.output, normalized)
	}

	_, err := NewClient("invalid")
	require.Error(t, err)
	originalNewDockerClient := newDockerClient
	t.Cleanup(func() {
		newDockerClient = originalNewDockerClient
	})
	newDockerClient = func(...client.Opt) (*client.Client, error) {
		return nil, errors.New("client failed")
	}
	_, err = NewClient("/var/run/docker.sock")
	require.EqualError(t, err, "client failed")
	newDockerClient = originalNewDockerClient
	clientValue, err := NewClient("/var/run/docker.sock")
	require.NoError(t, err)
	require.NotNil(t, clientValue.Engine())
	_, err = clientValue.Ping(context.Background())
	require.NoError(t, err)
	require.NoError(t, clientValue.Close())

	var nilClient *Client
	_, err = nilClient.Ping(context.Background())
	require.EqualError(t, err, "docker client is required")
	require.Nil(t, nilClient.Engine())
	require.NoError(t, nilClient.Close())
	require.NoError(t, (&Client{}).Close())
}

func TestDockerAPIErrorHandling(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	spec := dockerTestCommandSpec(
		t,
		dockerTestExposure(t, nil),
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	owner := spec.Owner()

	server.setFailure("/_ping")
	_, err := backend.Acquire(context.Background(), spec)
	require.ErrorContains(t, err, "docker backend unavailable")
	server.clearFailure()

	backend.closing = true
	_, err = backend.Acquire(context.Background(), spec)
	require.EqualError(t, err, "docker execution backend is closing")
	backend.closing = false

	server.setFailure("/containers/json")
	_, err = backend.Acquire(context.Background(), spec)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	server.setFailure("/volumes/")
	err = backend.ensureWorkspace(context.Background(), spec, "workspace")
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	server.setFailure("/networks/")
	bridgeInput := testDockerExposure()
	bridgeInput.Network = execution.NetworkBridge
	bridge, err := execution.NewExposure(bridgeInput)
	require.NoError(t, err)
	bridgeSpec := dockerTestCommandSpec(
		t,
		bridge,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	_, err = backend.ensureNetwork(context.Background(), bridgeSpec)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	server.setFailure("/volumes")
	backend.maximumVolumes = 1
	err = backend.checkVolumeAdmission(context.Background(), owner.Profile)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	backend.maximumVolumes = 0
	backend.reservedFreeBytes = 1
	server.driverStatus = [][]string{
		{"Data Space Available", "invalid"},
	}
	err = backend.checkVolumeAdmission(context.Background(), owner.Profile)
	require.ErrorContains(t, err, "docker free-space reserve cannot be verified")
	server.driverStatus = [][]string{
		{"Data Space Available", "1B"},
	}
	backend.reservedFreeBytes = 2
	err = backend.checkVolumeAdmission(context.Background(), owner.Profile)
	require.EqualError(t, err, "docker execution reserved free-space threshold reached")
	backend.reservedFreeBytes = 1
	require.NoError(t, backend.checkVolumeAdmission(context.Background(), owner.Profile))

	server.driverStatus = nil
	server.setFailure("/containers/create")
	err = backend.checkVolumeAdmission(context.Background(), owner.Profile)
	require.ErrorContains(t, err, "docker free-space reserve cannot be verified")
	server.clearFailure()
	server.attachOutput = "1024\n"
	backend.reservedFreeBytes = 2 * 1024
	err = backend.checkVolumeAdmission(context.Background(), owner.Profile)
	require.EqualError(t, err, "docker execution reserved free-space threshold reached")
	backend.reservedFreeBytes = 1
	require.NoError(t, backend.checkVolumeAdmission(context.Background(), owner.Profile))

	server.setFailure("/info")
	err = backend.checkVolumeAdmission(context.Background(), owner.Profile)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	var nilBackend *Backend
	_, err = nilBackend.Acquire(context.Background(), spec)
	require.EqualError(t, err, "docker backend is required")

	backend.allowTestImage = false
	originalVerify := verifySandboxImageSignature
	t.Cleanup(func() {
		verifySandboxImageSignature = originalVerify
	})
	verifySandboxImageSignature = func(context.Context, string) error {
		return errors.New("image failed")
	}
	_, err = backend.Acquire(context.Background(), spec)
	require.EqualError(t, err, "image failed")
	backend.allowTestImage = true

	backend.maximumEnvironments = 1
	backend.statuses = map[string]execution.EnvironmentStatus{}
	backend.statuses["occupied"] = execution.EnvironmentStatus{
		State:             execution.EnvironmentRunning,
		WorkspaceIdentity: owner.Profile + ":session:occupied",
		UpdatedAt:         time.Now().UTC(),
	}
	_, err = backend.Acquire(context.Background(), spec)
	require.EqualError(t, err, "docker execution environment limit reached")
	backend.maximumEnvironments = 10
	delete(backend.statuses, "occupied")

	server.setFailure("/volumes/create")
	_, err = backend.Acquire(context.Background(), spec)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	bridgeBackend := newFakeBackend(server.client)
	server.setFailure("/networks/create")
	_, err = bridgeBackend.Acquire(context.Background(), bridgeSpec)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	cleanupBackend := newFakeBackend(server.client)
	cleanupKey := "stale"
	cleanupBackend.statuses[cleanupKey] = execution.EnvironmentStatus{
		WorkspaceIdentity: owner.Profile + ":session:stale",
		UpdatedAt:         time.Time{},
	}
	cleanupBackend.environments[cleanupKey] = &sharedEnvironment{
		containerID: "stale-container",
	}
	server.stopStatus = http.StatusInternalServerError
	server.removeStatus = http.StatusInternalServerError
	_, err = cleanupBackend.Acquire(context.Background(), spec)
	require.ErrorContains(t, err, "docker status failure")
}

func TestFetchEngineFreeBytes_UsesSandboxProbe(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	server.attachOutput = "8388608\n"

	available, err := backend.fetchEngineFreeBytes(context.Background(), "default")
	require.NoError(t, err)
	require.Equal(t, int64(8388608), available)

	server.attachOutput = "invalid"
	_, err = backend.fetchEngineFreeBytes(context.Background(), "default")
	require.EqualError(t, err, "sandbox free-space probe returned an invalid value")

	server.attachOutput = ""
	server.waitStatus = 1
	_, err = backend.fetchEngineFreeBytes(context.Background(), "default")
	require.EqualError(t, err, "sandbox free-space probe exited with status 1")
}

func TestIncarnationFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	originalIncarnation := newDockerIncarnation
	t.Cleanup(func() {
		newDockerIncarnation = originalIncarnation
	})

	sessionExposure := dockerTestExposure(t, nil)
	commandSpec := dockerTestCommandSpec(
		t,
		sessionExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	newDockerIncarnation = func() (string, error) {
		return "", errors.New("incarnation failed")
	}
	_, err := backend.runDisposable(
		context.Background(),
		commandSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(t, err, "incarnation failed")

	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	processSpec := dockerTestProcessSpec(
		t,
		sessionExposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	_, err = backend.StartProcess(context.Background(), processSpec)
	require.EqualError(t, err, "incarnation failed")

	calls := 0
	newDockerIncarnation = func() (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("token failed")
		}
		return "container", nil
	}
	_, err = backend.StartProcess(context.Background(), processSpec)
	require.EqualError(t, err, "token failed")

	sharedInput := testDockerExposure()
	sharedInput.Scope = execution.ScopeShared
	sharedInput.WorkspaceIdentity = "default:shared"
	sharedExposure, err := execution.NewExposure(sharedInput)
	require.NoError(t, err)
	sharedSpec := dockerTestCommandSpec(t, sharedExposure, plan)
	newDockerIncarnation = func() (string, error) {
		return "", errors.New("shared incarnation failed")
	}
	_, err = backend.ensureSharedLocked(context.Background(), sharedSpec, "workspace")
	require.EqualError(t, err, "shared incarnation failed")
}

func TestWorkspaceAndSharedEnvironmentVariants(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	input := testDockerExposure()
	input.WorkspaceMode = execution.WorkspaceReadWrite
	mountedExposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	mountedSpec := dockerTestCommandSpec(
		t,
		mountedExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	require.NoError(t, backend.ensureWorkspace(context.Background(), mountedSpec, "workspace"))

	privateSpec := dockerTestCommandSpec(
		t,
		dockerTestExposure(t, nil),
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	server.volumeExists = true
	require.NoError(t, backend.ensureWorkspace(context.Background(), privateSpec, "workspace"))
	server.volumeExists = false
	backend.maximumVolumes = 1
	server.volumes = []map[string]any{
		{
			"Name": "existing",
		},
	}
	err = backend.ensureWorkspace(context.Background(), privateSpec, "workspace")
	require.EqualError(t, err, "docker execution volume limit reached")
	backend.maximumVolumes = 10
	server.volumes = nil

	backend.reservedFreeBytes = 1 << 62
	server.driverStatus = [][]string{
		{"Data Space Available", "1GB"},
	}
	err = backend.checkVolumeAdmission(context.Background(), "default")
	require.EqualError(t, err, "docker execution reserved free-space threshold reached")
	backend.reservedFreeBytes = 0
	server.driverStatus = nil
	backend.reservedFreeBytes = 1 << 62
	server.attachOutput = "1\n"
	err = backend.checkVolumeAdmission(context.Background(), "default")
	require.EqualError(t, err, "docker execution reserved free-space threshold reached")
	backend.reservedFreeBytes = 0

	sharedInput := testDockerExposure()
	sharedInput.Scope = execution.ScopeShared
	sharedInput.WorkspaceIdentity = "default:shared"
	sharedExposure, err := execution.NewExposure(sharedInput)
	require.NoError(t, err)
	sharedSpec := dockerTestCommandSpec(
		t,
		sharedExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	key := getEnvironmentKey(sharedSpec, backend.daemonIncarnation)
	backend.environments[key] = &sharedEnvironment{
		containerID: "old-container",
		incarnation: "old",
	}
	server.containerRunning = false
	environment, err := backend.ensureSharedLocked(context.Background(), sharedSpec, "workspace")
	require.NoError(t, err)
	require.NotEqual(t, "old", environment.incarnation)
	server.containerRunning = true
	require.Same(
		t,
		environment,
		mustEnsureShared(t, backend, sharedSpec, "workspace"),
	)

	delete(backend.environments, key)
	server.setFailure("/containers/create")
	_, err = backend.ensureSharedLocked(context.Background(), sharedSpec, "workspace")
	require.ErrorContains(t, err, "docker API failed")
	server.setFailure("/start")
	_, err = backend.ensureSharedLocked(context.Background(), sharedSpec, "workspace")
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	bridgeInput := testDockerExposure()
	bridgeInput.Scope = execution.ScopeShared
	bridgeInput.WorkspaceIdentity = "default:shared-bridge"
	bridgeInput.Network = execution.NetworkBridge
	bridgeExposure, err := execution.NewExposure(bridgeInput)
	require.NoError(t, err)
	bridgeSpec := dockerTestCommandSpec(
		t,
		bridgeExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	server.setFailure("/networks/create")
	_, err = backend.ensureSharedLocked(context.Background(), bridgeSpec, "workspace")
	require.ErrorContains(t, err, "docker API failed")
}

func TestDisposableAndSharedExecutionAgainstDockerAPI(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	sessionSpec := dockerTestCommandSpec(
		t,
		dockerTestExposure(t, nil),
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	var nilBackend *Backend
	_, err := nilBackend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(t, err, "docker backend is required")
	result, err := backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "output", result.Stdout)

	server.oomKilled = true
	result, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.NoError(t, err)
	require.True(t, result.OOMKilled)
	server.oomKilled = false

	server.waitError = "wait failed"
	_, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.EqualError(t, err, "wait failed")
	server.waitError = ""

	server.setFailure("/containers/create")
	_, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
	server.setFailure("/attach")
	_, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorContains(t, err, "unable to upgrade")
	server.setFailure("/start")
	_, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	secretInput := testDockerExposure()
	secretInput.SecretReferences = []string{"token"}
	secretExposure, err := execution.NewExposure(secretInput)
	require.NoError(t, err)
	secretSpec := dockerTestCommandSpec(
		t,
		secretExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	_, err = backend.runDisposable(
		context.Background(),
		secretSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(t, err, "execution secret resolver is unavailable")
	resolver, err := NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "MISSING_DISPOSABLE_SECRET",
		},
	})
	require.NoError(t, err)
	backend.secretResolver = resolver
	_, err = backend.runDisposable(
		context.Background(),
		secretSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(t, err, "execution secret value is unavailable")
	t.Setenv("LARGE_DISPOSABLE_SECRET", strings.Repeat("x", 2048))
	resolver, err = NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "LARGE_DISPOSABLE_SECRET",
		},
	})
	require.NoError(t, err)
	backend.secretResolver = resolver
	_, err = backend.runDisposable(
		context.Background(),
		secretSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(
		t,
		err,
		"execution secret control payload exceeds the configured limit",
	)
	t.Setenv("SMALL_DISPOSABLE_SECRET", "secret")
	resolver, err = NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "SMALL_DISPOSABLE_SECRET",
		},
	})
	require.NoError(t, err)
	backend.secretResolver = resolver
	server.closeAttach = true
	_, err = backend.runDisposable(
		context.Background(),
		secretSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.Error(t, err)
	server.closeAttach = false

	outsideSpec := dockerTestCommandSpec(
		t,
		dockerTestExposure(t, nil),
		dockerTestPlan(t, "/bin/echo", "/outside", "hello"),
	)
	_, err = backend.runDisposable(
		context.Background(),
		outsideSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(
		t,
		err,
		"docker command working directory is outside configured mounts",
	)

	server.setFailure("/wait")
	_, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	server.waitDelay = 100 * time.Millisecond
	server.stopStatus = http.StatusInternalServerError
	server.removeStatus = http.StatusInternalServerError
	cancelContext, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err = backend.runDisposable(
		cancelContext,
		sessionSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorContains(t, err, "docker status failure")
	server.waitDelay = 0
	server.stopStatus = 0
	server.removeStatus = 0

	originalDrainTimeout := containerOutputDrainTimeout
	t.Cleanup(func() {
		containerOutputDrainTimeout = originalDrainTimeout
	})
	server.holdAttachOpen = true
	server.attachDelay = 50 * time.Millisecond
	containerOutputDrainTimeout = time.Millisecond
	result, err = backend.runDisposable(
		context.Background(),
		sessionSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.NoError(t, err)
	server.holdAttachOpen = false
	server.attachDelay = 0

	sharedInput := testDockerExposure()
	sharedInput.Scope = execution.ScopeShared
	sharedInput.WorkspaceIdentity = "default:shared"
	sharedExposure, err := execution.NewExposure(sharedInput)
	require.NoError(t, err)
	sharedSpec := dockerTestCommandSpec(
		t,
		sharedExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	result, err = backend.runShared(
		context.Background(),
		sharedSpec,
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "output", result.Stdout)

	server.setFailure("/containers/container/exec")
	_, err = backend.runShared(
		context.Background(),
		sharedSpec,
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
	server.setFailure("/exec/exec/start")
	_, err = backend.runShared(
		context.Background(),
		sharedSpec,
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorContains(t, err, "unable to upgrade")
	server.setFailure("/exec/exec/json")
	_, err = backend.runShared(
		context.Background(),
		sharedSpec,
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = backend.runShared(
		canceled,
		sharedSpec,
		[]string{"/bin/echo", "hello"},
		nil,
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDisposablePostAcquireResourceFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	spec := dockerTestCommandSpec(
		t,
		dockerTestExposure(t, nil),
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			server.setFailure("/volumes/")
		}
	}
	_, err := backend.runDisposable(
		context.Background(),
		spec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")

	server.clearFailure()
	bridgeInput := testDockerExposure()
	bridgeInput.Network = execution.NetworkBridge
	bridgeExposure, err := execution.NewExposure(bridgeInput)
	require.NoError(t, err)
	bridgeSpec := dockerTestCommandSpec(
		t,
		bridgeExposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			server.setFailure("/networks/")
		}
	}
	_, err = backend.runDisposable(
		context.Background(),
		bridgeSpec,
		"foreground",
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
}

func TestSharedExecutionControlBranches(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	input := testDockerExposure()
	input.Scope = execution.ScopeShared
	input.WorkspaceIdentity = "default:shared"
	exposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	spec := dockerTestCommandSpec(
		t,
		exposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)

	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			backend.mu.Lock()
			backend.closing = true
			backend.mu.Unlock()
		}
	}
	_, err = backend.runShared(
		context.Background(),
		spec,
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(t, err, "docker execution backend is closing")
	backend.closing = false
	backend.recordLifecycle = nil

	canceled, cancel := context.WithCancel(context.Background())
	key := getEnvironmentKey(spec, backend.daemonIncarnation)
	gate := backend.getSharedGate(key)
	<-gate
	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			cancel()
		}
	}
	_, err = backend.runShared(
		canceled,
		spec,
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorIs(t, err, context.Canceled)
	gate <- struct{}{}
	backend.recordLifecycle = nil

	filesystemSpec := dockerTestFilesystemSpec(
		t,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemRead,
			Path: dockerTestPath(
				t,
				execution.FilesystemRead,
				"/workspace/file.txt",
			),
		},
	)
	result, err := backend.runShared(
		context.Background(),
		filesystemSpec,
		[]string{"fs-read", "/workspace/file.txt", "10"},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "output", result.Stdout)

	result, err = backend.runShared(
		context.Background(),
		spec,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "output", result.Stdout)

	outsideSpec := dockerTestCommandSpec(
		t,
		exposure,
		dockerTestPlan(t, "/bin/echo", "/outside", "hello"),
	)
	_, err = backend.runShared(
		context.Background(),
		outsideSpec,
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(
		t,
		err,
		"docker command working directory is outside configured mounts",
	)

	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			backend.mu.Lock()
			delete(backend.environments, key)
			backend.mu.Unlock()
			server.containerRunning = false
			server.setFailure("/containers/create")
		}
	}
	_, err = backend.runShared(
		context.Background(),
		spec,
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
}

func TestSharedExecutionStreamFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	input := testDockerExposure()
	input.Scope = execution.ScopeShared
	input.WorkspaceIdentity = "default:stable-shared"
	input.EnvironmentIdleExpiry = time.Hour
	exposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	validSpec := dockerTestCommandSpec(
		t,
		exposure,
		dockerTestPlan(t, "/bin/echo", "/workspace", "hello"),
	)
	_, err = backend.runShared(
		context.Background(),
		validSpec,
		[]string{"/bin/echo"},
		nil,
	)
	require.NoError(t, err)

	key := getEnvironmentKey(validSpec, backend.daemonIncarnation)
	gate := backend.getSharedGate(key)
	<-gate
	canceled, cancelGate := context.WithCancel(context.Background())
	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			cancelGate()
		}
	}
	_, err = backend.runShared(
		canceled,
		validSpec,
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorIs(t, err, context.Canceled)
	gate <- struct{}{}
	backend.recordLifecycle = nil

	outsideSpec := dockerTestCommandSpec(
		t,
		exposure,
		dockerTestPlan(t, "/bin/echo", "/outside", "hello"),
	)
	_, err = backend.runShared(
		context.Background(),
		outsideSpec,
		[]string{"/bin/echo"},
		nil,
	)
	require.EqualError(
		t,
		err,
		"docker command working directory is outside configured mounts",
	)

	server.rawAttach = []byte("invalid multiplexed stream")
	_, err = backend.runShared(
		context.Background(),
		validSpec,
		[]string{"/bin/echo"},
		nil,
	)
	require.Error(t, err)
	server.rawAttach = nil

	server.closeAttach = true
	_, err = backend.runShared(
		context.Background(),
		validSpec,
		[]string{"/bin/echo"},
		[]byte("input"),
	)
	require.Error(t, err)
	server.closeAttach = false

	server.holdAttachOpen = true
	server.attachDelay = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		server.setFailure("/containers/create")
		cancel()
	}()
	_, err = backend.runShared(
		ctx,
		validSpec,
		[]string{"/bin/echo"},
		nil,
	)
	require.ErrorContains(t, err, "docker API failed")
}

func TestBuildContainerOptions_ValidatesInputsAndMappings(t *testing.T) {
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	spec := dockerTestCommandSpec(t, dockerTestExposure(t, nil), plan)

	_, err := BuildContainerOptions(ContainerInput{
		Spec: spec,
	})
	require.EqualError(t, err, "docker container input is incomplete")

	invalidContract := testContract()
	invalidContract.GOOS = "windows"
	_, err = BuildContainerOptions(ContainerInput{
		Spec:     spec,
		Image:    "image",
		Contract: invalidContract,
	})
	require.Error(t, err)

	unsupportedContract := testContract()
	unsupportedContract.Version = "unsupported"
	_, err = NewBackend(BackendOptions{
		Image:             "image",
		Contract:          unsupportedContract,
		AllowTestImageTag: true,
	})
	require.EqualError(t, err, "sandbox image runtime compatibility is unsupported")

	filesystemSpec := dockerTestFilesystemSpec(
		t,
		dockerTestExposure(t, nil),
		execution.FilesystemOperation{
			Action: execution.FilesystemRead,
			Path:   dockerTestPath(t, execution.FilesystemRead, "/workspace/file"),
		},
	)
	_, err = BuildContainerOptions(ContainerInput{
		Spec:     filesystemSpec,
		Image:    "image",
		Contract: testContract(),
	})
	require.EqualError(t, err, "docker operation has no command plan")

	invalidMountInput := testDockerExposure()
	invalidMountInput.WorkspaceMode = execution.WorkspaceReadWrite
	invalidMountInput.Mounts = []execution.Mount{
		{
			Name:           "missing",
			SourceIdentity: filepath.Join(t.TempDir(), "missing"),
			Target:         "/mnt/missing",
			Mode:           execution.MountReadOnly,
		},
	}
	invalidMountExposure, err := execution.NewExposure(invalidMountInput)
	require.NoError(t, err)
	invalidMountSpec := dockerTestCommandSpec(t, invalidMountExposure, plan)
	_, err = BuildContainerOptions(ContainerInput{
		Spec:                 invalidMountSpec,
		Image:                "image",
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		ContainerIncarnation: "container",
		ResourceKind:         "foreground",
	})
	require.Error(t, err)

	options, err := BuildContainerOptions(ContainerInput{
		Spec:                 filesystemSpec,
		Image:                "image",
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		ContainerIncarnation: "container",
		ResourceKind:         "filesystem",
		WorkspaceVolume:      "workspace",
		Command:              []string{"fs-read", "/workspace/file", "10"},
	})
	require.NoError(t, err)
	require.Equal(t, "/workspace", options.Config.WorkingDir)

	outsidePlan := dockerTestPlan(t, "/bin/echo", "/outside", "hello")
	outsideSpec := dockerTestCommandSpec(t, dockerTestExposure(t, nil), outsidePlan)
	_, err = BuildContainerOptions(ContainerInput{
		Spec:                 outsideSpec,
		Image:                "image",
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		ContainerIncarnation: "container",
		ResourceKind:         "foreground",
		WorkspaceVolume:      "workspace",
	})
	require.EqualError(t, err, "docker command working directory is outside configured mounts")

	require.Equal(t, "uid=65532,gid=65532,", getTmpfsOwner("65532:65532"))
	require.Empty(t, getTmpfsOwner("user"))
	require.Empty(t, getTmpfsOwner("user:65532"))
	require.Empty(t, getTmpfsOwner("65532:group"))

	require.EqualError(
		t,
		validateContainerOptions(client.ContainerCreateOptions{}),
		"docker container configuration is incomplete",
	)
	require.EqualError(
		t,
		validateContainerOptions(client.ContainerCreateOptions{
			Config: &container.Config{},
			HostConfig: &container.HostConfig{
				Privileged: true,
			},
		}),
		"docker container hardening is incomplete",
	)
	require.NoError(t, validateContainerOptions(options))

	require.Equal(t, "/workspace", mapWorkingDirectory("", spec.Exposure()))
	require.Equal(t, "/workspace", mapWorkingDirectory("/workspace/", spec.Exposure()))
	require.Equal(t, "/workspace/nested", mapWorkingDirectory("/workspace/nested", spec.Exposure()))
	require.Equal(t, "/mnt/data", mapWorkingDirectory("/mnt/data", spec.Exposure()))
	require.Equal(t, "relative", mapWorkingDirectory("relative", spec.Exposure()))
	require.Empty(t, mapWorkingDirectory("/outside", spec.Exposure()))
	mountRoot := t.TempDir()
	mountInput := testDockerExposure()
	mountInput.WorkspaceMode = execution.WorkspaceReadWrite
	mountInput.Mounts = []execution.Mount{
		{
			Name:           "data",
			SourceIdentity: mountRoot,
			Target:         "/mnt/data",
			Mode:           execution.MountReadOnly,
		},
	}
	mountExposure, err := execution.NewExposure(mountInput)
	require.NoError(t, err)
	require.Equal(t, "/mnt/data", mapWorkingDirectory(mountRoot, mountExposure))
	require.Equal(
		t,
		"/mnt/data/nested",
		mapWorkingDirectory(filepath.Join(mountRoot, "nested"), mountExposure),
	)

	bridgeInput := testDockerExposure()
	bridgeInput.Network = execution.NetworkBridge
	bridge, err := execution.NewExposure(bridgeInput)
	require.NoError(t, err)
	require.Equal(t, container.NetworkMode("network"), getNetworkMode(bridge.Network(), "network"))
	require.NotNil(t, getNetworkingConfig(bridge.Network(), "network"))
	require.Nil(t, getNetworkingConfig(bridge.Network(), ""))
	require.Equal(t, container.NetworkMode("none"), getNetworkMode(execution.NetworkNone, ""))
	require.Nil(t, getNetworkingConfig(execution.NetworkNone, "network"))
	require.NotEmpty(t, BuildNetworkOptions(map[string]string{"label": "value"}).Labels)
}

func TestImageContractAndSignatureValidation(t *testing.T) {
	_, err := LoadImageContract("")
	require.EqualError(t, err, "sandbox image contract path is required")
	_, err = LoadImageContract(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)

	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalidPath, []byte("{"), 0o600))
	_, err = LoadImageContract(invalidPath)
	require.Error(t, err)

	incompletePath := filepath.Join(t.TempDir(), "incomplete.json")
	require.NoError(t, os.WriteFile(incompletePath, []byte("{}"), 0o600))
	_, err = LoadImageContract(incompletePath)
	require.Error(t, err)

	validPath := filepath.Join(t.TempDir(), "contract.json")
	raw, err := os.ReadFile("../../../containers/sandbox/contract.json")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(validPath, raw, 0o600))
	contract, err := LoadImageContract(validPath)
	require.NoError(t, err)
	require.Equal(t, "linux", contract.GOOS)
	require.Equal(t, SandboxRuntimeCompatibility, contract.Version)

	manifestRaw, err := os.ReadFile("../../../containers/sandbox/manifest.json")
	require.NoError(t, err)
	var manifest struct {
		RuntimeCompatibility string `json:"runtime_compatibility"`
	}
	require.NoError(t, json.Unmarshal(manifestRaw, &manifest))
	require.Equal(t, SandboxRuntimeCompatibility, manifest.RuntimeCompatibility)

	digest := strings.Repeat("a", 64)
	validReference := SandboxRepository + "@sha256:" + digest
	require.NoError(t, ValidateImageReference(validReference))
	for _, reference := range []string{
		"image:tag",
		"@sha256:" + digest,
		"image@sha256:short",
		"image@sha256:" + strings.Repeat("z", 64),
	} {
		require.Error(t, ValidateImageReference(reference))
	}

	require.Error(t, VerifyImageSignature(context.Background(), "image:tag"))
	t.Setenv("PATH", t.TempDir())
	err = VerifyImageSignature(context.Background(), validReference)
	require.EqualError(t, err, "cosign is required to verify the sandbox image signature")

	bin := t.TempDir()
	cosign := filepath.Join(bin, "cosign")
	require.NoError(t, os.WriteFile(cosign, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	t.Setenv("PATH", bin)
	require.NoError(t, VerifyImageSignature(context.Background(), validReference))
	require.NoError(
		t,
		os.WriteFile(cosign, []byte("#!/bin/sh\nprintf failure\nexit 1\n"), 0o700),
	)
	err = VerifyImageSignature(context.Background(), validReference)
	require.EqualError(t, err, "sandbox image signature verification failed: failure")
	require.NoError(t, os.WriteFile(cosign, []byte("#!/bin/sh\nexit 1\n"), 0o700))
	require.Error(t, VerifyImageSignature(context.Background(), validReference))
}

func TestBackendImageVerification(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.allowTestImage = false
	originalVerify := verifySandboxImageSignature
	t.Cleanup(func() {
		verifySandboxImageSignature = originalVerify
	})
	verifyErr := error(nil)
	verifyCalls := 0
	verifySandboxImageSignature = func(context.Context, string) error {
		verifyCalls++
		return verifyErr
	}

	verifyErr = errors.New("signature failed")
	err := backend.verifyImage(context.Background())
	require.EqualError(t, err, "signature failed")
	verifyErr = nil

	server.setFailure("/images/")
	err = backend.verifyImage(context.Background())
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	server.imageOS = "windows"
	err = backend.verifyImage(context.Background())
	require.EqualError(t, err, "sandbox image platform does not match its contract")
	server.imageOS = "linux"
	server.imageArch = "unsupported"
	err = backend.verifyImage(context.Background())
	require.EqualError(t, err, "sandbox image platform does not match its contract")
	server.imageArch = "amd64"
	server.imageUser = "invalid"
	err = backend.verifyImage(context.Background())
	require.EqualError(t, err, "sandbox image runtime configuration does not match its contract")
	server.imageUser = backend.contract.User
	server.imageEntry = nil
	err = backend.verifyImage(context.Background())
	require.EqualError(t, err, "sandbox image runtime configuration does not match its contract")
	server.imageEntry = []string{
		backend.contract.Helper,
	}
	require.NoError(t, backend.verifyImage(context.Background()))
	require.True(t, backend.imageVerified)
	require.NoError(t, backend.verifyImage(context.Background()))
	require.Equal(t, 7, verifyCalls)

	backend.imageVerified = false
	backend.imageVerification = ImageVerificationDigest
	verifyErr = errors.New("signature should not be checked")
	require.NoError(t, backend.verifyImage(context.Background()))
	require.True(t, backend.imageVerified)
	require.Equal(t, 7, verifyCalls)

	backend.imageVerified = false
	server.imageOS = "windows"
	require.EqualError(
		t,
		backend.verifyImage(context.Background()),
		"sandbox image platform does not match its contract",
	)
	require.Equal(t, 7, verifyCalls)
	server.imageOS = "linux"

	backend.allowTestImage = true
	require.NoError(t, backend.verifyImage(context.Background()))
}

func TestMountPreparationAndProtection(t *testing.T) {
	exposure, err := execution.NewExposure(testDockerExposure())
	require.NoError(t, err)
	_, err = buildMounts(exposure, "")
	require.EqualError(t, err, "docker private workspace volume is required")

	root := createDockerTestMountSource(t)
	input := testDockerExposure()
	input.WorkspaceMode = execution.WorkspaceReadWrite
	input.Mounts = []execution.Mount{
		{
			Name:           "data",
			SourceIdentity: root,
			Target:         "/mnt/data",
			Mode:           execution.MountReadOnly,
		},
	}
	exposure, err = execution.NewExposure(input)
	require.NoError(t, err)
	mounts, err := buildMounts(exposure, "")
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	require.True(t, mounts[0].ReadOnly)

	createdParent := createDockerTestMountSource(t)
	created := filepath.Join(createdParent, "created")
	source, err := prepareMountSource(execution.Mount{
		SourceIdentity: created,
		Create:         true,
	})
	require.NoError(t, err)
	require.Equal(t, created, source)
	require.DirExists(t, created)

	symlinkRoot := createDockerTestMountSource(t)
	target := createDockerTestMountSource(t)
	symlink := filepath.Join(symlinkRoot, "link")
	require.NoError(t, os.Symlink(target, symlink))
	_, err = prepareMountSource(execution.Mount{
		SourceIdentity: symlink,
	})
	require.EqualError(t, err, "docker mount source changed after authorization")

	_, err = canonicalMountSource("relative")
	require.EqualError(t, err, "docker mount source must be absolute")
	_, err = canonicalMountSource(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
	_, err = canonicalMountSource("/")
	require.EqualError(t, err, "docker mount source is protected")

	socketPath := filepath.Join(t.TempDir(), "socket")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})
	_, err = canonicalMountSource(socketPath)
	require.EqualError(t, err, "docker mount source type is blocked")

	blockedParent := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockedParent, []byte("value"), 0o600))
	_, err = prepareMountSource(execution.Mount{
		SourceIdentity: filepath.Join(blockedParent, "child"),
		Create:         true,
	})
	require.Error(t, err)

	invalidInput := testDockerExposure()
	invalidInput.WorkspaceMode = execution.WorkspaceReadWrite
	invalidInput.Mounts = []execution.Mount{
		{
			Name:           "missing",
			SourceIdentity: filepath.Join(t.TempDir(), "missing"),
			Target:         "/mnt/missing",
			Mode:           execution.MountReadOnly,
		},
	}
	invalidExposure, err := execution.NewExposure(invalidInput)
	require.NoError(t, err)
	_, err = buildMounts(invalidExposure, "")
	require.Error(t, err)

	originalStat := statMountSource
	t.Cleanup(func() {
		statMountSource = originalStat
	})
	statMountSource = func(string) (os.FileInfo, error) {
		return nil, errors.New("stat failed")
	}
	_, err = canonicalMountSource(root)
	require.EqualError(t, err, "stat failed")
}

func createDockerTestMountSource(t *testing.T) string {
	t.Helper()

	temporary, err := os.MkdirTemp(".", "morph-mount-source-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(temporary))
	})

	source, err := filepath.Abs(temporary)
	require.NoError(t, err)
	canonical, err := filepath.EvalSymlinks(source)
	require.NoError(t, err)
	return canonical
}

func TestOutputBufferAccessorsAndBounds(t *testing.T) {
	writer := newBoundedWriter(0, nil)
	_, err := writer.Write([]byte("value"))
	require.NoError(t, err)
	require.Equal(t, 5, writer.Total())
	require.Equal(t, "value", writer.String())
	require.Equal(t, []byte("value"), writer.Bytes())
	require.False(t, writer.Truncated())

	writer = newBoundedWriter(3, nil)
	_, err = writer.Write([]byte("abc"))
	require.NoError(t, err)
	_, err = writer.Write([]byte("d"))
	require.NoError(t, err)
	require.Equal(t, "abc", writer.String())
	require.True(t, writer.Truncated())

	writer = newBoundedWriter(3, []string{"secret"})
	_, err = writer.Write([]byte("sec"))
	require.NoError(t, err)
	require.NotContains(t, string(writer.Bytes()), "secret")
	require.LessOrEqual(t, len(writer.Bytes()), 3)

	writer = newBoundedWriter(2, []string{"secret"})
	_, err = writer.Write([]byte("sec"))
	require.NoError(t, err)
	require.LessOrEqual(t, len(writer.String()), 2)
	require.True(t, writer.Truncated())
}

func TestSecretResolverValidationAndResolution(t *testing.T) {
	_, err := NewSecretResolver([]SecretReference{{}})
	require.EqualError(t, err, "execution secret reference is incomplete")
	_, err = NewSecretResolver([]SecretReference{
		{
			Name: "same",
			Env:  "ONE",
		},
		{
			Name: "same",
			Env:  "TWO",
		},
	})
	require.EqualError(t, err, "execution secret reference names must be unique")

	resolver, err := NewSecretResolver([]SecretReference{
		{
			Name: " Token ",
			Env:  " TEST_DOCKER_SECRET ",
		},
	})
	require.NoError(t, err)
	_, err = resolver.Resolve([]string{"missing"})
	require.EqualError(t, err, "execution secret reference is not configured")
	_, err = resolver.Resolve([]string{"token"})
	require.EqualError(t, err, "execution secret value is unavailable")
	t.Setenv("TEST_DOCKER_SECRET", "value")
	resolved, err := resolver.Resolve([]string{" TOKEN "})
	require.NoError(t, err)
	require.Equal(t, []string{"token"}, resolved.Names)
	require.Equal(t, "value", resolved.Values["token"])
}

func TestFilesystemOperationResponses(t *testing.T) {
	backend := newFakeBackend(nil)
	exposure := dockerTestExposure(t, nil)
	response := execution.CommandResult{}
	responseErr := error(nil)
	backend.executeOverride = func(
		context.Context,
		execution.Spec,
		string,
		[]string,
		[]byte,
	) (execution.CommandResult, error) {
		return response, responseErr
	}

	readSpec := dockerTestFilesystemSpec(
		t,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemRead,
			Path: dockerTestPath(
				t,
				execution.FilesystemRead,
				"/workspace/file.txt",
			),
		},
	)
	responseErr = errors.New("execute failed")
	_, err := backend.ReadFile(context.Background(), readSpec, 0)
	require.EqualError(t, err, "execute failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "read failed",
	}
	_, err = backend.ReadFile(context.Background(), readSpec, 10)
	require.EqualError(t, err, "read failed")
	response = execution.CommandResult{
		Stdout: "too long",
	}
	_, err = backend.ReadFile(context.Background(), readSpec, 2)
	require.EqualError(t, err, "file exceeds the read limit")
	response = execution.CommandResult{
		Stdout: string([]byte{0, 1}),
	}
	_, err = backend.ReadFile(context.Background(), readSpec, 10)
	require.EqualError(t, err, "file is not text")
	response = execution.CommandResult{
		Stdout: "text",
	}
	content, err := backend.ReadFile(context.Background(), readSpec, 10)
	require.NoError(t, err)
	require.Equal(t, "text", string(content))

	writeSpec := dockerTestFilesystemSpec(
		t,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Path: dockerTestPath(
				t,
				execution.FilesystemWrite,
				"/workspace/file.txt",
			),
			Data: []byte("value"),
		},
	)
	responseErr = errors.New("execute failed")
	_, err = backend.WriteFile(context.Background(), writeSpec, true)
	require.EqualError(t, err, "execute failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "write failed",
	}
	_, err = backend.WriteFile(context.Background(), writeSpec, true)
	require.EqualError(t, err, "write failed")
	response = execution.CommandResult{
		Stdout: "invalid",
	}
	_, err = backend.WriteFile(context.Background(), writeSpec, true)
	require.Error(t, err)
	response = execution.CommandResult{
		Stdout: `{"size":5,"mode":420,"created":true}`,
	}
	fileInfo, err := backend.WriteFile(context.Background(), writeSpec, true)
	require.NoError(t, err)
	require.Equal(t, int64(5), fileInfo.Size)
	require.True(t, fileInfo.Created)
	_, err = backend.ReadFile(context.Background(), writeSpec, 10)
	require.EqualError(t, err, "docker filesystem execution specification is invalid")

	patchSpec := dockerTestFilesystemSpec(
		t,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemPatch,
			Path: dockerTestPath(
				t,
				execution.FilesystemPatch,
				"/workspace/file.txt",
			),
			Data: []byte("patch"),
		},
	)
	responseErr = errors.New("execute failed")
	_, err = backend.PatchFile(context.Background(), patchSpec)
	require.EqualError(t, err, "execute failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "patch failed for another reason",
	}
	_, err = backend.PatchFile(context.Background(), patchSpec)
	require.ErrorIs(t, err, execution.ErrPatchConflict)
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "unavailable",
	}
	_, err = backend.PatchFile(context.Background(), patchSpec)
	require.EqualError(t, err, "unavailable")
	response = execution.CommandResult{}
	fileInfo, err = backend.PatchFile(context.Background(), patchSpec)
	require.NoError(t, err)
	require.Equal(t, "/workspace/file.txt", fileInfo.Path)

	listSpec := dockerTestFilesystemSpec(
		t,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemList,
			Path: dockerTestPath(
				t,
				execution.FilesystemList,
				"/workspace",
			),
		},
	)
	responseErr = errors.New("execute failed")
	_, err = backend.ListFiles(context.Background(), listSpec, 10)
	require.EqualError(t, err, "execute failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
	}
	_, err = backend.ListFiles(context.Background(), listSpec, 10)
	require.EqualError(t, err, "Docker filesystem helper failed")
	response = execution.CommandResult{
		Stdout: "invalid",
	}
	_, err = backend.ListFiles(context.Background(), listSpec, 10)
	require.Error(t, err)
	response = execution.CommandResult{
		Stdout: `[{"path":"file.txt"}]`,
	}
	entries, err := backend.ListFiles(context.Background(), listSpec, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	searchSpec := dockerTestFilesystemSpec(
		t,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemSearch,
			Path: dockerTestPath(
				t,
				execution.FilesystemSearch,
				"/workspace",
			),
			Query: "needle",
		},
	)
	responseErr = errors.New("execute failed")
	_, err = backend.SearchFiles(context.Background(), searchSpec, 10)
	require.EqualError(t, err, "execute failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "search failed",
	}
	_, err = backend.SearchFiles(context.Background(), searchSpec, 10)
	require.EqualError(t, err, "search failed")
	response = execution.CommandResult{
		Stdout: "invalid",
	}
	_, err = backend.SearchFiles(context.Background(), searchSpec, 10)
	require.Error(t, err)
	response = execution.CommandResult{
		Stdout: `[{"path":"file.txt","line":1,"text":"needle"}]`,
	}
	matches, err := backend.SearchFiles(context.Background(), searchSpec, 10)
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

func TestBackendStateHelpers(t *testing.T) {
	owner := dockerTestOwner()
	exposure := dockerTestExposure(t, nil)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	spec := dockerTestCommandSpec(t, exposure, plan)
	recorded := false
	backend := &Backend{
		daemonIncarnation:  "daemon",
		statuses:           map[string]execution.EnvironmentStatus{},
		processes:          map[string]*dockerProcess{},
		processOrder:       map[string][]string{},
		environments:       map[string]*sharedEnvironment{},
		sharedGates:        map[string]chan struct{}{},
		environmentLocks:   map[string]*sync.Mutex{},
		networks:           map[string]struct{}{},
		reconciledProfiles: map[string]struct{}{},
		recordLifecycle: func(string, string, any) {
			recorded = true
		},
	}
	backend.record(spec, "event", nil)
	require.True(t, recorded)
	backend.recordLifecycle = nil
	backend.record(spec, "event", nil)

	status := backend.getStatus(spec, execution.EnvironmentRunning, "container")
	backend.setStatus(status)
	backend.trackNetwork("network")
	require.Contains(t, backend.networks, "network")
	require.Same(t, backend.getEnvironmentLock("key"), backend.getEnvironmentLock("key"))
	require.NotEmpty(t, getEnvironmentKey(spec, "daemon"))
	require.NotEmpty(t, getWorkspaceVolumeName(spec))
	require.NotEmpty(t, getNetworkName(spec, "daemon"))
	require.Len(t, safeID("value"), 24)

	backend.maximumEnvironments = 1
	require.NoError(t, backend.checkEnvironmentAdmission(spec))
	otherExposure := dockerTestExposure(t, nil)
	otherPlan := dockerTestPlan(t, "/bin/echo", "/workspace", "other")
	otherSpec := dockerTestCommandSpec(t, otherExposure, otherPlan)
	backend.statuses = map[string]execution.EnvironmentStatus{
		"other": {
			WorkspaceIdentity: owner.Profile + ":other",
		},
	}
	require.EqualError(
		t,
		backend.checkEnvironmentAdmission(otherSpec),
		"docker execution environment limit reached",
	)
	backend.maximumEnvironments = 0
	require.NoError(t, backend.checkEnvironmentAdmission(spec))

	backend.statuses = map[string]execution.EnvironmentStatus{}
	statuses, err := backend.Status(context.Background(), owner)
	require.NoError(t, err)
	require.Empty(t, statuses)

	var nilBackend *Backend
	require.NoError(t, nilBackend.Reconcile(context.Background()))
	require.NoError(t, nilBackend.Close(context.Background()))
	require.NoError(t, nilBackend.removeContainer("", time.Second))

	require.False(t, isNotFound(nil))
	require.Equal(t, 0, getCursor(nil, 10))
	negative := -1
	require.Equal(t, 0, getCursor(&negative, 10))
	large := 20
	require.Equal(t, 10, getCursor(&large, 10))
	valid := 4
	require.Equal(t, 4, getCursor(&valid, 10))
}

func TestRemoveContainerErrorNormalization(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	server.stopStatus = http.StatusNotFound
	server.removeStatus = http.StatusNotFound
	require.NoError(t, backend.removeContainer("container", -time.Second))

	server.stopStatus = 0
	server.removeStatus = http.StatusConflict
	server.inspectStatus = http.StatusNotFound
	require.NoError(t, backend.removeContainer("container", time.Second))

	server.stopStatus = http.StatusInternalServerError
	server.removeStatus = http.StatusInternalServerError
	server.inspectStatus = 0
	err := backend.removeContainer("container", time.Second)
	require.ErrorContains(t, err, "docker status failure")

	server.stopStatus = 0
	server.removeStatus = http.StatusConflict
	server.inspectStatus = 0
	originalConflictTimeout := containerRemovalConflictTimeout
	originalPollInterval := containerRemovalPollInterval
	t.Cleanup(func() {
		containerRemovalConflictTimeout = originalConflictTimeout
		containerRemovalPollInterval = originalPollInterval
	})
	containerRemovalConflictTimeout = 2 * time.Millisecond
	containerRemovalPollInterval = time.Millisecond
	err = backend.removeContainer("container", time.Second)
	require.Error(t, err)
}

func TestProcessBookkeepingAndIdentityErrors(t *testing.T) {
	exposure := dockerTestExposure(t, nil)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	startSpec := dockerTestProcessSpec(t, exposure, execution.ProcessOperation{
		Action: execution.ProcessStart,
		Plan:   &plan,
	})
	backend := &Backend{
		daemonIncarnation: "daemon",
		processKey:        make([]byte, 32),
		processes:         map[string]*dockerProcess{},
		processOrder:      map[string][]string{},
		environments:      map[string]*sharedEnvironment{},
	}
	_, err := backend.StartProcess(context.Background(), execution.Spec{})
	require.EqualError(t, err, "docker process start specification is invalid")
	backend.processKey = nil
	_, err = backend.StartProcess(context.Background(), startSpec)
	require.EqualError(t, err, "docker process identity key is unavailable")

	process := &dockerProcess{
		owner:       dockerTestOwner(),
		generation:  "generation",
		incarnation: "container",
		info: processenv.Info{
			ID:     "handle",
			Label:  "worker",
			Status: processenv.StatusRunning,
		},
		stdout: newBoundedWriter(10, nil),
		stderr: newBoundedWriter(10, nil),
	}
	backend.processKey = make([]byte, 32)
	backend.processes["handle"] = process
	backend.processOrder[process.owner.Fingerprint()] = []string{"handle"}
	require.True(t, backend.hasProcessLabelLocked(process.owner.Fingerprint(), "worker"))
	require.False(t, backend.hasProcessLabelLocked(process.owner.Fingerprint(), "other"))

	snapshot := process.snapshot()
	require.Equal(t, "handle", snapshot.ID)
	backend.forgetProcess("handle")
	require.Empty(t, backend.processes)
	require.Empty(t, backend.processOrder[process.owner.Fingerprint()])

	_, err = backend.getProcess(startSpec, "missing")
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)
	_, err = backend.getProcess(startSpec, "")
	require.ErrorIs(t, err, execution.ErrProcessNotFound)
	_, err = backend.getProcess(startSpec, "invalid.handle")
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)
}

func TestSharedProcessControlResponses(t *testing.T) {
	backend := newFakeBackend(nil)
	backend.processKey = make([]byte, 32)
	input := testDockerExposure()
	input.Scope = execution.ScopeShared
	input.WorkspaceIdentity = "default:shared"
	exposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	startSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
			Label:  "worker",
		},
	)
	process, handle := trackSharedProcess(t, backend, startSpec, "worker")

	response := execution.CommandResult{}
	responseErr := error(nil)
	backend.executeOverride = func(
		context.Context,
		execution.Spec,
		string,
		[]string,
		[]byte,
	) (execution.CommandResult, error) {
		return response, responseErr
	}

	statusSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStatus,
			ProcessID: handle,
		},
	)
	responseErr = errors.New("status failed")
	_, err = backend.GetProcess(context.Background(), statusSpec)
	require.EqualError(t, err, "status failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "status unavailable",
	}
	_, err = backend.GetProcess(context.Background(), statusSpec)
	require.EqualError(t, err, "status unavailable")
	response = execution.CommandResult{
		Stdout: "invalid",
	}
	_, err = backend.GetProcess(context.Background(), statusSpec)
	require.Error(t, err)
	endedAt := time.Now().UTC()
	exitCode := 3
	state, err := json.Marshal(sharedProcessState{
		Token:     process.token,
		StartedAt: process.info.StartedAt,
		EndedAt:   &endedAt,
		ExitCode:  &exitCode,
	})
	require.NoError(t, err)
	response = execution.CommandResult{
		Stdout: string(state),
	}
	info, err := backend.GetProcess(context.Background(), statusSpec)
	require.NoError(t, err)
	require.Equal(t, processenv.StatusExited, info.Status)

	readSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessRead,
			ProcessID: handle,
		},
	)
	responseErr = errors.New("read failed")
	_, err = backend.ReadProcess(
		context.Background(),
		readSpec,
		processenv.ReadRequest{
			ProcessID: handle,
		},
	)
	require.EqualError(t, err, "read failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "read unavailable",
	}
	_, err = backend.ReadProcess(
		context.Background(),
		readSpec,
		processenv.ReadRequest{
			ProcessID: handle,
		},
	)
	require.EqualError(t, err, "read unavailable")
	response = execution.CommandResult{
		Stdout: "invalid",
	}
	_, err = backend.ReadProcess(
		context.Background(),
		readSpec,
		processenv.ReadRequest{
			ProcessID: handle,
		},
	)
	require.Error(t, err)
	cursor := 2
	response = execution.CommandResult{
		Stdout: `{"stdout":"hello","stderr":"error"}`,
	}
	output, err := backend.ReadProcess(
		context.Background(),
		readSpec,
		processenv.ReadRequest{
			ProcessID:    handle,
			StdoutCursor: &cursor,
			StderrCursor: &cursor,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "llo", output.Stdout)
	require.Equal(t, "ror", output.Stderr)
	_, err = backend.ReadProcess(
		context.Background(),
		readSpec,
		processenv.ReadRequest{
			ProcessID: "missing",
		},
	)
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)

	process.info.Status = processenv.StatusRunning
	listSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessList,
		},
	)
	responseErr = errors.New("refresh failed")
	processes, err := backend.ListProcesses(context.Background(), listSpec)
	require.NoError(t, err)
	require.Empty(t, processes)
	responseErr = nil
	response = execution.CommandResult{
		Stdout: string(state),
	}
	processes, err = backend.ListProcesses(context.Background(), listSpec)
	require.NoError(t, err)
	require.Len(t, processes, 1)

	stopSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStop,
			ProcessID: handle,
		},
	)
	responseErr = errors.New("stop failed")
	_, err = backend.StopProcess(context.Background(), stopSpec)
	require.EqualError(t, err, "stop failed")
	responseErr = nil
	backend.closing = true
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "stop unavailable",
	}
	info, err = backend.StopProcess(context.Background(), stopSpec)
	require.NoError(t, err)
	require.Equal(t, processenv.StatusStopped, info.Status)
	backend.closing = false
}

func TestProcessLookupAuthorizationStates(t *testing.T) {
	backend := newFakeBackend(nil)
	backend.processKey = make([]byte, 32)
	exposure := dockerTestExposure(t, nil)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	startSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	process, handle := trackDisposableProcess(t, backend, startSpec, "worker")

	process.generation = "old"
	_, err := backend.getProcess(startSpec, handle)
	require.ErrorIs(t, err, execution.ErrProcessStale)
	process.generation = exposure.SecurityGeneration()
	backend.processKey = nil
	_, err = backend.getProcess(startSpec, handle)
	require.Error(t, err)
	backend.processKey = make([]byte, 32)
	_, err = backend.getProcess(startSpec, handle+"broken")
	require.Error(t, err)

	codec, err := execution.NewProcessCodec(
		backend.processKey,
		exposure.SecurityGeneration(),
		backend.daemonIncarnation,
	)
	require.NoError(t, err)
	otherOwner := dockerTestOwner()
	otherOwner.ActorID = "other"
	otherHandle, err := codec.Encode(otherOwner, "container", "token")
	require.NoError(t, err)
	_, err = backend.getProcess(startSpec, otherHandle)
	require.ErrorIs(t, err, execution.ErrProcessDenied)

	staleCodec, err := execution.NewProcessCodec(
		backend.processKey,
		exposure.SecurityGeneration(),
		"old-daemon",
	)
	require.NoError(t, err)
	staleHandle, err := staleCodec.Encode(startSpec.Owner(), "container", "token")
	require.NoError(t, err)
	_, err = backend.getProcess(startSpec, staleHandle)
	require.ErrorIs(t, err, execution.ErrProcessStale)

	process.shared = true
	process.spec = startSpec
	_, err = backend.getProcess(startSpec, handle)
	require.ErrorIs(t, err, execution.ErrProcessStale)
	process.shared = false
	backend.processKey = nil
	_, err = backend.getProcess(startSpec, otherHandle)
	require.Error(t, err)
}

func TestStartSharedProcessResponses(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	input := testDockerExposure()
	input.Scope = execution.ScopeShared
	input.WorkspaceIdentity = "default:shared"
	exposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	spec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
			Label:  "worker",
		},
	)

	response := execution.CommandResult{}
	responseErr := error(nil)
	removeEnvironment := false
	backend.executeOverride = func(
		_ context.Context,
		current execution.Spec,
		_ string,
		_ []string,
		_ []byte,
	) (execution.CommandResult, error) {
		if removeEnvironment {
			delete(
				backend.environments,
				getEnvironmentKey(current, backend.daemonIncarnation),
			)
		}
		return response, responseErr
	}

	responseErr = errors.New("start failed")
	_, err = backend.StartProcess(context.Background(), spec)
	require.EqualError(t, err, "start failed")
	responseErr = nil
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "supervisor failed",
	}
	_, err = backend.StartProcess(context.Background(), spec)
	require.EqualError(t, err, "supervisor failed")
	response = execution.CommandResult{
		Stdout: "invalid",
	}
	_, err = backend.StartProcess(context.Background(), spec)
	require.Error(t, err)
	state, err := json.Marshal(sharedProcessState{
		Token:     "token",
		StartedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	response = execution.CommandResult{
		Stdout: string(state),
	}
	removeEnvironment = true
	_, err = backend.StartProcess(context.Background(), spec)
	require.EqualError(t, err, "shared Docker environment disappeared during process start")
	removeEnvironment = false
	info, err := backend.StartProcess(context.Background(), spec)
	require.NoError(t, err)
	require.Equal(t, "worker", info.Label)
	_, err = backend.StartProcess(context.Background(), spec)
	require.EqualError(t, err, "process label already exists")

	backend.processes = map[string]*dockerProcess{}
	backend.processOrder = map[string][]string{}
	backend.daemonIncarnation = ""
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "process identity codec configuration is incomplete")
	backend.daemonIncarnation = "daemon"
	response = execution.CommandResult{
		Stdout: `{"token":" "}`,
	}
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)
	response = execution.CommandResult{
		Stdout: string(state),
	}

	outsidePlan := dockerTestPlan(t, "/bin/echo", "/outside", "hello")
	outsideSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &outsidePlan,
		},
	)
	_, err = backend.StartProcess(context.Background(), outsideSpec)
	require.EqualError(
		t,
		err,
		"docker command working directory is outside configured mounts",
	)
}

func TestStartSharedProcessRejectsOutsideWorkingDirectory(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	input := testDockerExposure()
	input.Scope = execution.ScopeShared
	input.WorkspaceIdentity = "default:stable-process-shared"
	input.EnvironmentIdleExpiry = time.Hour
	exposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	validPlan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	validSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &validPlan,
		},
	)
	backend.executeOverride = func(
		context.Context,
		execution.Spec,
		string,
		[]string,
		[]byte,
	) (execution.CommandResult, error) {
		return execution.CommandResult{
			Stdout: `{"token":"token","started_at":"2026-01-01T00:00:00Z"}`,
		}, nil
	}
	_, err = backend.StartProcess(context.Background(), validSpec)
	require.NoError(t, err)

	outsidePlan := dockerTestPlan(t, "/bin/echo", "/outside", "hello")
	outsideSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &outsidePlan,
		},
	)
	_, err = backend.StartProcess(context.Background(), outsideSpec)
	require.EqualError(
		t,
		err,
		"docker process working directory is outside configured mounts",
	)
}

func TestStartDisposableProcessFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	exposure := dockerTestExposure(t, nil)
	spec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)

	server.setFailure("/containers/create")
	_, err := backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "docker API failed")
	server.setFailure("/attach")
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "unable to upgrade")
	server.clearFailure()

	secretInput := testDockerExposure()
	secretInput.SecretReferences = []string{"token"}
	secretExposure, err := execution.NewExposure(secretInput)
	require.NoError(t, err)
	secretSpec := dockerTestProcessSpec(
		t,
		secretExposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	_, err = backend.StartProcess(context.Background(), secretSpec)
	require.EqualError(t, err, "execution secret resolver is unavailable")
	resolver, err := NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "MISSING_DOCKER_TEST_SECRET",
		},
	})
	require.NoError(t, err)
	backend.secretResolver = resolver
	_, err = backend.StartProcess(context.Background(), secretSpec)
	require.EqualError(t, err, "execution secret value is unavailable")

	largeValue := strings.Repeat("x", 2048)
	t.Setenv("LARGE_DOCKER_TEST_SECRET", largeValue)
	resolver, err = NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "LARGE_DOCKER_TEST_SECRET",
		},
	})
	require.NoError(t, err)
	backend.secretResolver = resolver
	_, err = backend.StartProcess(context.Background(), secretSpec)
	require.EqualError(
		t,
		err,
		"execution secret control payload exceeds the configured limit",
	)

	server.setFailure("/start")
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "docker API failed")
	server.clearFailure()

	outsidePlan := dockerTestPlan(t, "/bin/echo", "/outside", "hello")
	outsideSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &outsidePlan,
		},
	)
	_, err = backend.StartProcess(context.Background(), outsideSpec)
	require.EqualError(
		t,
		err,
		"docker command working directory is outside configured mounts",
	)

	server.setFailure("/_ping")
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "docker backend unavailable")
	server.clearFailure()

	originalIncarnation := newDockerIncarnation
	t.Cleanup(func() {
		newDockerIncarnation = originalIncarnation
	})
	newDockerIncarnation = func() (string, error) {
		return " ", nil
	}
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)
	newDockerIncarnation = originalIncarnation

	backend.daemonIncarnation = ""
	_, err = backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "process identity codec configuration is incomplete")
	backend.daemonIncarnation = "daemon"

	t.Setenv("SMALL_DOCKER_TEST_SECRET", "secret")
	resolver, err = NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "SMALL_DOCKER_TEST_SECRET",
		},
	})
	require.NoError(t, err)
	backend.secretResolver = resolver
	server.holdAttachOpen = true
	info, err := backend.StartProcess(context.Background(), secretSpec)
	require.NoError(t, err)
	require.NotEmpty(t, info.ID)
	<-backend.processes[info.ID].done
	server.mu.Lock()
	server.holdAttachOpen = false
	server.mu.Unlock()
	server.closeAttach = true
	_, err = backend.StartProcess(context.Background(), secretSpec)
	require.Error(t, err)
	server.closeAttach = false

	originalDrainTimeout := processOutputDrainTimeout
	processOutputDrainTimeout = time.Millisecond
	t.Cleanup(func() {
		processOutputDrainTimeout = originalDrainTimeout
	})
	server.mu.Lock()
	server.holdAttachOpen = true
	server.attachDelay = 50 * time.Millisecond
	server.mu.Unlock()
	drainInfo, err := backend.StartProcess(context.Background(), spec)
	require.NoError(t, err)
	<-backend.processes[drainInfo.ID].done
	processOutputDrainTimeout = originalDrainTimeout
	server.mu.Lock()
	server.holdAttachOpen = false
	server.attachDelay = 0
	server.mu.Unlock()
}

func TestStartProcessPostAcquireResourceFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	spec := dockerTestProcessSpec(
		t,
		dockerTestExposure(t, nil),
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			server.setFailure("/volumes/")
		}
	}
	_, err := backend.StartProcess(context.Background(), spec)
	require.ErrorContains(t, err, "docker API failed")

	server.clearFailure()
	bridgeInput := testDockerExposure()
	bridgeInput.Network = execution.NetworkBridge
	bridgeExposure, err := execution.NewExposure(bridgeInput)
	require.NoError(t, err)
	bridgeSpec := dockerTestProcessSpec(
		t,
		bridgeExposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	backend.recordLifecycle = func(_ string, event string, _ any) {
		if event == "execution.environment.ready" {
			server.setFailure("/networks/")
		}
	}
	_, err = backend.StartProcess(context.Background(), bridgeSpec)
	require.ErrorContains(t, err, "docker API failed")
}

func TestStopProcessFailureAndCancellation(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	exposure := dockerTestExposure(t, nil)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	startSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	process, handle := trackDisposableProcess(t, backend, startSpec, "worker")
	stopSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStop,
			ProcessID: handle,
		},
	)

	server.stopStatus = http.StatusInternalServerError
	_, err := backend.StopProcess(context.Background(), stopSpec)
	require.ErrorContains(t, err, "docker status failure")
	server.stopStatus = 0
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = backend.StopProcess(canceled, stopSpec)
	require.ErrorIs(t, err, context.Canceled)
	close(process.done)
	info, err := backend.StopProcess(context.Background(), stopSpec)
	require.NoError(t, err)
	require.Equal(t, processenv.StatusStopped, info.Status)
	info, err = backend.StopProcess(context.Background(), stopSpec)
	require.NoError(t, err)
	require.Equal(t, processenv.StatusStopped, info.Status)

	missingSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStop,
			ProcessID: "missing",
		},
	)
	_, err = backend.StopProcess(context.Background(), missingSpec)
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)

	timeoutProcess, timeoutHandle := trackDisposableProcess(t, backend, startSpec, "timeout")
	_ = timeoutProcess
	timeoutSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStop,
			ProcessID: timeoutHandle,
		},
	)
	originalStopTimeout := processStopTimeout
	processStopTimeout = time.Millisecond
	t.Cleanup(func() {
		processStopTimeout = originalStopTimeout
	})
	_, err = backend.StopProcess(context.Background(), timeoutSpec)
	require.EqualError(t, err, "docker process stop did not become terminal")
	processStopTimeout = originalStopTimeout

	cancelProcess, cancelHandle := trackDisposableProcess(t, backend, startSpec, "cancel")
	_ = cancelProcess
	cancelSpec := dockerTestProcessSpec(
		t,
		exposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStop,
			ProcessID: cancelHandle,
		},
	)
	delayedContext, delayedCancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Millisecond)
		delayedCancel()
	}()
	_, err = backend.StopProcess(delayedContext, cancelSpec)
	require.ErrorIs(t, err, context.Canceled)

	sharedInput := testDockerExposure()
	sharedInput.Scope = execution.ScopeShared
	sharedInput.WorkspaceIdentity = "default:shared"
	sharedExposure, err := execution.NewExposure(sharedInput)
	require.NoError(t, err)
	sharedSpec := dockerTestProcessSpec(
		t,
		sharedExposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	_, sharedHandle := trackSharedProcess(t, backend, sharedSpec, "shared")
	sharedStopSpec := dockerTestProcessSpec(
		t,
		sharedExposure,
		execution.ProcessOperation{
			Action:    execution.ProcessStop,
			ProcessID: sharedHandle,
		},
	)
	backend.executeOverride = func(
		context.Context,
		execution.Spec,
		string,
		[]string,
		[]byte,
	) (execution.CommandResult, error) {
		return execution.CommandResult{
			ExitCode: 1,
			Stderr:   "stop failed",
		}, nil
	}
	server.setFailure("/containers/create")
	_, err = backend.StopProcess(context.Background(), sharedStopSpec)
	require.ErrorContains(t, err, "stop failed")
	require.ErrorContains(t, err, "docker API failed")
}

func TestProcessCleanupVariants(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processKey = make([]byte, 32)
	owner := dockerTestOwner()
	input := testDockerExposure()
	input.Scope = execution.ScopeShared
	input.WorkspaceIdentity = "default:shared"
	sharedExposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	sharedSpec := dockerTestProcessSpec(
		t,
		sharedExposure,
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	shared, sharedHandle := trackSharedProcess(t, backend, sharedSpec, "shared")
	disposableSpec := dockerTestProcessSpec(
		t,
		dockerTestExposure(t, nil),
		execution.ProcessOperation{
			Action: execution.ProcessStart,
			Plan:   &plan,
		},
	)
	disposable, _ := trackDisposableProcess(t, backend, disposableSpec, "disposable")

	response := execution.CommandResult{
		ExitCode: 1,
		Stderr:   "stop failed",
	}
	backend.executeOverride = func(
		context.Context,
		execution.Spec,
		string,
		[]string,
		[]byte,
	) (execution.CommandResult, error) {
		return response, nil
	}
	err = backend.CloseOwner(context.Background(), owner)
	require.ErrorContains(t, err, "stop failed")

	shared.info.Status = processenv.StatusExited
	disposable.info.Status = processenv.StatusExited
	require.NoError(t, backend.CloseOwner(context.Background(), owner))

	shared.info.Status = processenv.StatusRunning
	disposable.info.Status = processenv.StatusRunning
	delete(backend.environments, getEnvironmentKey(sharedSpec, backend.daemonIncarnation))
	err = backend.CloseSession(
		context.Background(),
		owner.Profile,
		owner.EffectiveSessionID,
		false,
	)
	require.NoError(t, err)
	require.NotContains(t, backend.processes, sharedHandle)

	_, activeSharedHandle := trackSharedProcess(t, backend, sharedSpec, "active-shared")
	_ = activeSharedHandle
	response = execution.CommandResult{
		ExitCode: 1,
		Stderr:   "session stop failed",
	}
	err = backend.CloseSession(
		context.Background(),
		owner.Profile,
		owner.EffectiveSessionID,
		false,
	)
	require.ErrorContains(t, err, "session stop failed")
}

func TestSharedDisabledMarker(t *testing.T) {
	value, err := loadSharedDisabledAt(execution.ScopeSession, "")
	require.NoError(t, err)
	require.True(t, value.IsZero())

	path := filepath.Join(t.TempDir(), "nested", "disabled-at")
	value, err = loadSharedDisabledAt(execution.ScopeSession, path)
	require.NoError(t, err)
	require.False(t, value.IsZero())
	loaded, err := loadSharedDisabledAt(execution.ScopeSession, path)
	require.NoError(t, err)
	require.Equal(t, value, loaded)

	require.NoError(t, os.WriteFile(path, []byte("invalid"), 0o600))
	_, err = loadSharedDisabledAt(execution.ScopeSession, path)
	require.Error(t, err)

	require.NoError(t, os.WriteFile(path, []byte("value"), 0o600))
	value, err = loadSharedDisabledAt(execution.ScopeShared, path)
	require.NoError(t, err)
	require.True(t, value.IsZero())
	require.NoFileExists(t, path)

	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, []byte("value"), 0o600))
	_, err = loadSharedDisabledAt(
		execution.ScopeSession,
		filepath.Join(blocked, "marker"),
	)
	require.Error(t, err)

	originalWrite := writeSharedDisabledMarker
	t.Cleanup(func() {
		writeSharedDisabledMarker = originalWrite
	})
	writeSharedDisabledMarker = func(string, []byte, os.FileMode) error {
		return errors.New("write failed")
	}
	_, err = loadSharedDisabledAt(
		execution.ScopeSession,
		filepath.Join(t.TempDir(), "marker"),
	)
	require.EqualError(t, err, "write failed")

	originalMkdir := mkdirSharedDisabledMarker
	t.Cleanup(func() {
		mkdirSharedDisabledMarker = originalMkdir
	})
	mkdirSharedDisabledMarker = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	_, err = loadSharedDisabledAt(
		execution.ScopeSession,
		filepath.Join(t.TempDir(), "mkdir-marker"),
	)
	require.EqualError(t, err, "mkdir failed")
}

func TestNewBackendValidationAndControlFrames(t *testing.T) {
	_, err := NewBackend(BackendOptions{
		Image: "image:tag",
	})
	require.Error(t, err)
	_, err = NewBackend(BackendOptions{
		AllowTestImageTag: true,
	})
	require.EqualError(t, err, "docker sandbox image is required")

	invalidContract := testContract()
	invalidContract.GOOS = "windows"
	_, err = NewBackend(BackendOptions{
		Image:             "image",
		Contract:          invalidContract,
		AllowTestImageTag: true,
	})
	require.Error(t, err)

	mkdirParent := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(mkdirParent, []byte("value"), 0o600))
	_, err = NewBackend(BackendOptions{
		Endpoint:             "/var/run/docker.sock",
		Image:                "image",
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		AllowTestImageTag:    true,
		ConfiguredScope:      execution.ScopeSession,
		SharedDisabledMarker: filepath.Join(mkdirParent, "marker"),
	})
	require.Error(t, err)

	server := newFakeDockerServer(t)
	digestReference := SandboxRepository + "@sha256:" + strings.Repeat("a", 64)
	originalVerify := verifySandboxImageSignature
	t.Cleanup(func() {
		verifySandboxImageSignature = originalVerify
	})
	verifySandboxImageSignature = func(context.Context, string) error {
		return errors.New("signature failed")
	}
	_, err = NewBackend(BackendOptions{
		Image:             "image",
		ImageVerification: "checksum",
		Contract:          testContract(),
		AllowTestImageTag: true,
	})
	require.EqualError(t, err, "sandbox image verification must be signature or digest")
	_, err = NewBackend(BackendOptions{
		Endpoint:          server.listener.Addr().String(),
		Image:             digestReference,
		Contract:          testContract(),
		DaemonIncarnation: "daemon",
	})
	require.EqualError(t, err, "signature failed")
	digestBackend, err := NewBackend(BackendOptions{
		Endpoint:          server.listener.Addr().String(),
		Image:             digestReference,
		ImageVerification: ImageVerificationDigest,
		Contract:          testContract(),
		DaemonIncarnation: "daemon",
	})
	require.NoError(t, err)
	require.Equal(t, ImageVerificationDigest, digestBackend.imageVerification)
	require.NoError(t, digestBackend.client.Close())
	verifySandboxImageSignature = func(context.Context, string) error {
		return nil
	}
	verifiedBackend, err := NewBackend(BackendOptions{
		Endpoint:          server.listener.Addr().String(),
		Image:             digestReference,
		Contract:          testContract(),
		DaemonIncarnation: "daemon",
	})
	require.NoError(t, err)
	require.NoError(t, verifiedBackend.client.Close())
	_, err = NewBackend(BackendOptions{
		Image:             "image",
		Contract:          testContract(),
		AllowTestImageTag: true,
	})
	require.EqualError(t, err, "docker daemon incarnation is required")
	_, err = NewBackend(BackendOptions{
		Endpoint:          "invalid",
		Image:             "image",
		Contract:          testContract(),
		DaemonIncarnation: "daemon",
		AllowTestImageTag: true,
	})
	require.Error(t, err)

	blocked := filepath.Join(t.TempDir(), "marker")
	require.NoError(t, os.WriteFile(blocked, []byte("invalid"), 0o600))
	_, err = NewBackend(BackendOptions{
		Endpoint:             "/var/run/docker.sock",
		Image:                "image",
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		AllowTestImageTag:    true,
		ConfiguredScope:      execution.ScopeSession,
		SharedDisabledMarker: blocked,
	})
	require.Error(t, err)

	frame, err := encodeControlFrame(map[string]string{"token": "value"}, 100)
	require.NoError(t, err)
	require.Greater(t, len(frame), 4)
	_, err = encodeControlFrame(map[string]string{"token": "value"}, 1)
	require.EqualError(t, err, "execution secret control payload exceeds the configured limit")

	require.EqualError(
		t,
		getFilesystemError(execution.CommandResult{
			Stderr: "failure",
		}),
		"failure",
	)
	require.EqualError(
		t,
		getFilesystemError(execution.CommandResult{}),
		"Docker filesystem helper failed",
	)
}

func TestBackendInMemoryCleanupAndProcessStates(t *testing.T) {
	exposure := dockerTestExposure(t, nil)
	plan := dockerTestPlan(t, "/bin/echo", "/workspace", "hello")
	spec := dockerTestCommandSpec(t, exposure, plan)
	owner := spec.Owner()
	backend := &Backend{
		daemonIncarnation: "daemon",
		statuses:          map[string]execution.EnvironmentStatus{},
		processes:         map[string]*dockerProcess{},
		processOrder:      map[string][]string{},
		environments:      map[string]*sharedEnvironment{},
		sharedGates:       map[string]chan struct{}{},
		environmentLocks:  map[string]*sync.Mutex{},
		networks:          map[string]struct{}{},
	}
	key := getEnvironmentKey(spec, backend.daemonIncarnation)
	backend.statuses[key] = execution.EnvironmentStatus{
		ID:                key,
		Scope:             execution.ScopeSession,
		WorkspaceIdentity: exposure.WorkspaceIdentity(),
		UpdatedAt:         time.Now().Add(-time.Hour),
	}
	backend.sharedGates[key] = make(chan struct{}, 1)
	backend.environmentLocks[key] = &sync.Mutex{}
	require.NoError(t, backend.cleanupIdle(context.Background(), spec))
	require.Empty(t, backend.statuses)
	require.Empty(t, backend.sharedGates)
	require.Empty(t, backend.environmentLocks)

	backend.statuses[key] = execution.EnvironmentStatus{
		ID:                key,
		Scope:             execution.ScopeSession,
		State:             execution.EnvironmentRunning,
		WorkspaceIdentity: exposure.WorkspaceIdentity(),
		UpdatedAt:         time.Now().Add(-time.Hour),
	}
	require.NoError(t, backend.cleanupIdle(context.Background(), spec))
	require.Contains(t, backend.statuses, key)

	backend.statuses[key] = execution.EnvironmentStatus{
		ID:                   key,
		Scope:                execution.ScopeSession,
		WorkspaceIdentity:    exposure.WorkspaceIdentity(),
		ContainerIncarnation: "shared-container",
		UpdatedAt:            time.Now().Add(-time.Hour),
	}
	backend.processes["shared"] = &dockerProcess{
		shared:      true,
		incarnation: "shared-container",
		info: processenv.Info{
			Status: processenv.StatusRunning,
		},
	}
	require.NoError(t, backend.cleanupIdle(context.Background(), spec))
	require.Contains(t, backend.statuses, key)
	delete(backend.processes, "shared")

	endedAt := time.Now()
	exitCode := 4
	process := &dockerProcess{
		owner: owner,
		info: processenv.Info{
			ID:       "process",
			Args:     []string{"one"},
			Status:   processenv.StatusExited,
			ExitCode: &exitCode,
			EndedAt:  &endedAt,
		},
	}
	snapshot := process.snapshot()
	*snapshot.ExitCode = 9
	snapshot.Args[0] = "changed"
	require.Equal(t, 4, *process.info.ExitCode)
	require.Equal(t, "one", process.info.Args[0])

	backend.processOrder[owner.Fingerprint()] = []string{"missing"}
	require.NoError(t, backend.CloseOwner(context.Background(), owner))

	backend.closing = true
	require.NoError(t, backend.recreateSharedLocked(spec, nil))

}

func TestStatusAggregation(t *testing.T) {
	backend := newFakeBackend(nil)
	owner := dockerTestOwner()
	shared := execution.EnvironmentStatus{
		Scope:                execution.ScopeShared,
		WorkspaceIdentity:    owner.Profile + ":shared",
		ContainerIncarnation: "shared-container",
	}
	session := execution.EnvironmentStatus{
		Scope:                execution.ScopeSession,
		WorkspaceIdentity:    owner.Profile + ":session:" + owner.EffectiveSessionID,
		ContainerIncarnation: "session-container",
	}
	backend.statuses["shared"] = shared
	backend.statuses["session"] = session
	backend.statuses["other-profile"] = execution.EnvironmentStatus{
		Scope:             execution.ScopeShared,
		WorkspaceIdentity: "other:shared",
	}
	backend.statuses["other-session"] = execution.EnvironmentStatus{
		Scope:             execution.ScopeSession,
		WorkspaceIdentity: owner.Profile + ":session:other",
	}
	backend.processes["one"] = &dockerProcess{
		owner:       owner,
		incarnation: "shared-container",
		info: processenv.Info{
			Status: processenv.StatusRunning,
		},
	}
	otherParticipant := owner
	otherParticipant.EffectiveSessionID = "other"
	backend.processes["two"] = &dockerProcess{
		owner:       otherParticipant,
		incarnation: "shared-container",
		info: processenv.Info{
			Status: processenv.StatusRunning,
		},
	}
	backend.processes["ended"] = &dockerProcess{
		owner:       owner,
		incarnation: "session-container",
		info: processenv.Info{
			Status: processenv.StatusExited,
		},
	}

	statuses, err := backend.Status(context.Background(), owner)
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	for _, status := range statuses {
		if status.Scope == execution.ScopeShared {
			require.Equal(t, 2, status.ProcessCount)
			require.Equal(t, 2, status.ParticipantCount)
		}
	}
}

func TestReconcileResourceVariants(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.configuredScope = execution.ScopeSession
	backend.sharedRetention = time.Minute
	backend.sharedDisabledAt = time.Now().Add(-2 * time.Minute)
	backend.sessionExists = func(_ context.Context, sessionID string) (bool, error) {
		return sessionID == "active", nil
	}
	server.containers = []map[string]any{
		{
			"Id": "current-container",
			"Labels": map[string]string{
				LabelDaemonIncarnation: backend.daemonIncarnation,
			},
		},
		{
			"Id": "old-container",
			"Labels": map[string]string{
				LabelDaemonIncarnation: "old",
			},
		},
	}
	server.networks = []map[string]any{
		{
			"Id": "current-network",
			"Labels": map[string]string{
				LabelDaemonIncarnation: backend.daemonIncarnation,
			},
		},
		{
			"Id": "old-network",
			"Labels": map[string]string{
				LabelDaemonIncarnation: "old",
			},
		},
	}
	server.volumes = []map[string]any{
		{
			"Name": "active-volume",
			"Labels": map[string]string{
				LabelScope:      string(execution.ScopeSession),
				LabelScopeOwner: "active",
			},
		},
		{
			"Name": "stale-volume",
			"Labels": map[string]string{
				LabelScopeOwner: "stale",
			},
		},
		{
			"Name": "shared-volume",
			"Labels": map[string]string{
				LabelScopeOwner: "default",
			},
		},
	}

	require.NoError(t, backend.reconcileResources(context.Background(), "default"))
	require.NoError(t, backend.reconcileProfile(context.Background(), "default"))
	require.NoError(t, backend.reconcileProfile(context.Background(), "default"))
	require.NoError(t, backend.Reconcile(context.Background()))

	server.setFailure("/containers/json")
	require.Error(t, backend.reconcileResources(context.Background(), "default"))
	server.setFailure("/networks")
	require.Error(t, backend.reconcileResources(context.Background(), "default"))
	server.setFailure("/volumes")
	require.Error(t, backend.reconcileResources(context.Background(), "default"))
	server.clearFailure()

	backend.sessionExists = func(context.Context, string) (bool, error) {
		return false, errors.New("session lookup failed")
	}
	require.EqualError(
		t,
		backend.reconcileResources(context.Background(), "default"),
		"session lookup failed",
	)
	backend.sessionExists = nil

	server.setMethodFailure(http.MethodDelete, "/networks/")
	require.ErrorContains(
		t,
		backend.reconcileResources(context.Background(), "default"),
		"docker API failed",
	)
	server.setMethodFailure(http.MethodDelete, "/volumes/")
	require.ErrorContains(
		t,
		backend.reconcileResources(context.Background(), "default"),
		"docker API failed",
	)

	server.clearFailure()
	server.stopStatus = http.StatusInternalServerError
	server.removeStatus = http.StatusInternalServerError
	err := backend.reconcileResources(context.Background(), "default")
	require.ErrorContains(t, err, "docker status failure")
	server.stopStatus = 0
	server.removeStatus = 0
}

func TestRemoveSessionResourcesAndCloseFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	server.containers = []map[string]any{
		{
			"Id": "container",
		},
	}
	server.networks = []map[string]any{
		{
			"Id": "network",
		},
	}
	require.NoError(
		t,
		backend.removeSessionResources(context.Background(), "default", "session", true),
	)

	server.setFailure("/containers/json")
	require.Error(
		t,
		backend.removeSessionResources(context.Background(), "default", "session", false),
	)
	server.setFailure("/networks")
	require.Error(
		t,
		backend.removeSessionResources(context.Background(), "default", "session", false),
	)
	server.setMethodFailure(http.MethodDelete, "/networks/")
	require.Error(
		t,
		backend.removeSessionResources(context.Background(), "default", "session", false),
	)
	server.setMethodFailure(http.MethodDelete, "/volumes/")
	require.Error(
		t,
		backend.removeSessionResources(context.Background(), "default", "session", true),
	)
	server.clearFailure()

	gate := make(chan struct{}, 1)
	backend.sharedGates["gate"] = gate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, backend.Close(ctx), context.Canceled)
	backend.closing = false
	gate <- struct{}{}
	backend.environments["environment"] = &sharedEnvironment{
		containerID: "container",
	}
	backend.networks["network"] = struct{}{}
	require.NoError(t, backend.Close(context.Background()))
	require.NoError(t, backend.Close(context.Background()))
}

func TestCloseCancellationRestoresAcquiredGates(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	first := make(chan struct{}, 1)
	first <- struct{}{}
	second := make(chan struct{}, 1)
	backend.sharedGates["first"] = first
	backend.sharedGates["second"] = second
	ctx, cancel := context.WithCancel(context.Background())
	backend.recordLifecycle = nil
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()
	err := backend.Close(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, first, 1)
}

func TestCloseCollectsResourceFailures(t *testing.T) {
	server := newFakeDockerServer(t)
	backend := newFakeBackend(server.client)
	backend.processes["running"] = &dockerProcess{
		containerID: "process-container",
		info: processenv.Info{
			Status: processenv.StatusRunning,
		},
	}
	backend.processes["shared"] = &dockerProcess{
		shared: true,
		info: processenv.Info{
			Status: processenv.StatusRunning,
		},
	}
	backend.processes["ended"] = &dockerProcess{
		info: processenv.Info{
			Status: processenv.StatusExited,
		},
	}
	backend.environments["environment"] = &sharedEnvironment{
		containerID: "environment-container",
	}
	backend.networks["network"] = struct{}{}
	server.stopStatus = http.StatusInternalServerError
	server.removeStatus = http.StatusInternalServerError
	server.setMethodFailure(http.MethodDelete, "/networks/")
	err := backend.Close(context.Background())
	require.ErrorContains(t, err, "docker status failure")
	require.ErrorContains(t, err, "docker API failed")
}

func dockerTestOwner() execution.Owner {
	return execution.Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		ActorID:            "actor",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}
}

func dockerTestExposure(
	t *testing.T,
	secrets []string,
) execution.Exposure {
	t.Helper()
	input := testDockerExposure()
	input.SecretReferences = secrets
	exposure, err := execution.NewExposure(input)
	require.NoError(t, err)
	return exposure
}

func dockerTestPlan(
	t *testing.T,
	command string,
	cwd string,
	arguments ...string,
) commandplan.Plan {
	t.Helper()
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          command,
		Args:             arguments,
		CWD:              cwd,
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		CleanEnvironment: true,
		LookPath: func(string) (string, error) {
			return command, nil
		},
	})
	require.NoError(t, err)
	return plan
}

func dockerTestCommandSpec(
	t *testing.T,
	exposure execution.Exposure,
	plan commandplan.Plan,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(dockerTestOwner(), exposure, execution.Operation{
		Kind:    execution.OperationCommand,
		Command: &plan,
	})
	require.NoError(t, err)
	return spec
}

func dockerTestProcessSpec(
	t *testing.T,
	exposure execution.Exposure,
	operation execution.ProcessOperation,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(dockerTestOwner(), exposure, execution.Operation{
		Kind:    execution.OperationProcess,
		Process: &operation,
	})
	require.NoError(t, err)
	return spec
}

func dockerTestFilesystemSpec(
	t *testing.T,
	exposure execution.Exposure,
	operation execution.FilesystemOperation,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(dockerTestOwner(), exposure, execution.Operation{
		Kind:       execution.OperationFilesystem,
		Filesystem: &operation,
	})
	require.NoError(t, err)
	return spec
}

func dockerTestPath(
	t *testing.T,
	action execution.FilesystemAction,
	path string,
) execution.PreparedPath {
	t.Helper()
	prepared, err := execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        path,
		ContainerPath:      path,
		Mode:               execution.MountReadWrite,
		Action:             action,
		SecurityGeneration: "generation",
	})
	require.NoError(t, err)
	return prepared
}

func trackSharedProcess(
	t *testing.T,
	backend *Backend,
	spec execution.Spec,
	label string,
) (*dockerProcess, string) {
	t.Helper()
	process, handle := newTrackedProcess(t, backend, spec, label)
	process.shared = true
	process.spec = spec
	key := getEnvironmentKey(spec, backend.daemonIncarnation)
	backend.environments[key] = &sharedEnvironment{
		containerID: process.containerID,
		incarnation: process.incarnation,
	}
	return process, handle
}

func trackDisposableProcess(
	t *testing.T,
	backend *Backend,
	spec execution.Spec,
	label string,
) (*dockerProcess, string) {
	t.Helper()
	return newTrackedProcess(t, backend, spec, label)
}

func newTrackedProcess(
	t *testing.T,
	backend *Backend,
	spec execution.Spec,
	label string,
) (*dockerProcess, string) {
	t.Helper()
	incarnation := "container"
	token := "token-" + label
	codec, err := execution.NewProcessCodec(
		backend.processKey,
		spec.Exposure().SecurityGeneration(),
		backend.daemonIncarnation,
	)
	require.NoError(t, err)
	handle, err := codec.Encode(spec.Owner(), incarnation, token)
	require.NoError(t, err)
	process := &dockerProcess{
		owner:       spec.Owner(),
		generation:  spec.Exposure().SecurityGeneration(),
		containerID: "container",
		incarnation: incarnation,
		token:       token,
		spec:        spec,
		stdout:      newBoundedWriter(1024, nil),
		stderr:      newBoundedWriter(1024, nil),
		done:        make(chan struct{}),
		info: processenv.Info{
			ID:        handle,
			Label:     label,
			Status:    processenv.StatusRunning,
			StartedAt: time.Now().UTC(),
		},
	}
	backend.processes[handle] = process
	ownerKey := spec.Owner().Fingerprint()
	backend.processOrder[ownerKey] = append(backend.processOrder[ownerKey], handle)
	return process, handle
}

func mustEnsureShared(
	t *testing.T,
	backend *Backend,
	spec execution.Spec,
	workspaceVolume string,
) *sharedEnvironment {
	t.Helper()
	environment, err := backend.ensureSharedLocked(
		context.Background(),
		spec,
		workspaceVolume,
	)
	require.NoError(t, err)
	return environment
}
