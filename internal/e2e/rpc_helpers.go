package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/xymorphic/morph/internal/config"
	models "github.com/xymorphic/morph/internal/model"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

var rpcHelperListen = net.Listen
var rpcHelperNewClient = rpcclient.NewClient

// RPCConfigOptions controls rpc config.
type RPCConfigOptions struct {
	Name     string
	Stream   bool
	Instruct string
	NoColor  bool
}

// NewDefaultRPCHarness returns an RPC harness with default test dependencies.
func NewDefaultRPCHarness(
	ctx context.Context,
	home string,
	client models.Client,
	cfg *config.Config,
) (*RPCHarness, error) {
	if cfg == nil {
		cfg = DefaultConfig(ConfigOptions{StorageBackend: "sqlite"})
	}

	return NewRPCHarness(ctx, HarnessOptions{
		Spec:        DefaultSpec(home),
		Config:      cfg,
		ModelClient: client,
	})
}

// ReserveRPCPort reserves an available localhost port for an RPC test server.
func ReserveRPCPort() (int, error) {
	lis, err := rpcHelperListen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = lis.Close() }()

	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("rpc helper listener must be tcp")
	}

	return tcpAddr.Port, nil
}

// WaitForRPC waits until an RPC endpoint accepts connections.
func WaitForRPC(
	address string,
	port int,
	timeout time.Duration,
	authOptions ...rpcclient.Options,
) (*rpcclient.Client, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		addressValue := str.String(address)
		options := rpcclient.Options{Address: addressValue.Trim(), Port: port}
		if len(authOptions) > 0 {
			options = authOptions[0]
			options.Address = addressValue.Trim()
			options.Port = port
		}
		client, err := rpcHelperNewClient(context.Background(), options)
		if err == nil {
			_, currentErr := client.Session.Current(context.Background())
			if currentErr == nil {
				return client, nil
			}
			_ = client.Close()
		}

		time.Sleep(100 * time.Millisecond)
	}
	addressValue2 := str.String(address)
	return nil, fmt.Errorf("rpc server did not become ready on %s:%d", addressValue2.Trim(), port)
}

// WriteRPCConfigFile writes a temporary RPC config file for tests.
func WriteRPCConfigFile(dir, address string, port int, opts RPCConfigOptions) (string, error) {
	nameValue := str.String(opts.Name)
	name := nameValue.Trim()
	if name == "" {
		name = "yaml-agent"
	}
	addressValue3 := str.String(address)
	content := fmt.Sprintf(
		`name: %s
models:
  main:
    stream: %t
rpc:
  address: %s
  port: %d
permissions:
  preset: approve
log:
  noColor: %t
`,
		name,
		opts.Stream, addressValue3.
			Trim(), port,
		opts.NoColor,
	)
	instructValue := str.String(opts.Instruct)
	if instructValue.Trim() != "" {
		instructValue2 := str.String(opts.Instruct)
		content += "session:\n  instruct: " + instructValue2.Trim() + "\n"
	}

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}

	return path, nil
}

// MissingTools returns the expected missing-tool names from err.
func MissingTools(names ...string) RequestAssert {
	missing := make([]string, len(names))
	copy(missing, names)
	slices.Sort(missing)

	return func(req models.Request) error {
		available := make([]string, 0, len(req.Tools))
		for _, tool := range req.Tools {
			available = append(available, tool.Name)
		}

		for _, name := range missing {
			if slices.Contains(available, name) {
				return fmt.Errorf("expected tool %q to be unavailable, got tools %v", name, available)
			}
		}

		return nil
	}
}

// CombineChecks joins multiple e2e assertions into one check.
func CombineChecks(checks ...RequestAssert) RequestAssert {
	return func(req models.Request) error {
		for _, check := range checks {
			if check == nil {
				continue
			}
			if err := check(req); err != nil {
				return err
			}
		}

		return nil
	}
}

// ToolMessagePresent checks that a tool message for name appears in the session.
func ToolMessagePresent(expectedID, expectedName string) RequestAssert {
	return func(req models.Request) error {
		for _, message := range req.Messages {
			if message.Role != morphmsg.RoleTool {
				continue
			}

			toolCallIDValue := str.String(message.ToolCallID)
			if toolCallIDValue.Trim() != expectedID {
				continue
			}
			nameValue2 := str.String(message.Name)
			if nameValue2.Trim() != expectedName {
				return fmt.Errorf("expected tool message name %q", expectedName)
			}
			return nil
		}

		return fmt.Errorf("expected tool message for tool call %q", expectedID)
	}
}

// ToolOutputString returns a string field from a recorded tool output.
func ToolOutputString(expectedID, expectedName string, check func(string) error) RequestAssert {
	return func(req models.Request) error {
		output, err := getToolEnvelopeOutput(req, expectedID, expectedName)
		if err != nil {
			return err
		}
		return check(output)
	}
}

// ToolOutputJSON decodes recorded tool output into target.
func ToolOutputJSON(expectedID, expectedName string, check func(map[string]any) error) RequestAssert {
	return func(req models.Request) error {
		output, err := getToolEnvelopeOutput(req, expectedID, expectedName)
		if err != nil {
			return err
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(output), &payload); err != nil {
			return err
		}

		return check(payload)
	}
}

// ToolError returns the error text from a recorded tool output.
func ToolError(expectedID, expectedName, expectedCode, expectedMessage string) RequestAssert {
	return func(req models.Request) error {
		for _, message := range req.Messages {
			toolCallIDValue2 := str.String(message.ToolCallID)
			if message.Role != morphmsg.RoleTool || toolCallIDValue2.Trim() != expectedID {
				continue
			}
			nameValue3 := str.String(message.Name)
			if nameValue3.Trim() != expectedName {
				return fmt.Errorf("expected tool message name %q", expectedName)
			}

			var payload struct {
				Name  string `json:"name"`
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(message.Content), &payload); err != nil {
				return err
			}
			nameValue4 := str.String(payload.Name)
			if nameValue4.Trim() != expectedName {
				return fmt.Errorf("expected tool payload name %q", expectedName)
			}
			codeValue := str.String(payload.Error.Code)
			if codeValue.Trim() != expectedCode {
				return fmt.Errorf("expected tool error code %q, got %q", expectedCode, payload.Error.Code)
			}
			messageValue := str.String(payload.Error.Message)
			if messageValue.Trim() != expectedMessage {
				return fmt.Errorf("expected tool error message %q, got %q", expectedMessage, payload.Error.Message)
			}

			return nil
		}

		return fmt.Errorf("expected tool error for tool call %q", expectedID)
	}
}

func getToolEnvelopeOutput(req models.Request, expectedID, expectedName string) (string, error) {
	for _, message := range req.Messages {
		toolCallIDValue3 := str.String(message.ToolCallID)
		if message.Role != morphmsg.RoleTool || toolCallIDValue3.Trim() != expectedID {
			continue
		}
		nameValue5 := str.String(message.Name)
		if nameValue5.Trim() != expectedName {
			return "", fmt.Errorf("expected tool message name %q", expectedName)
		}

		var envelope struct {
			Name   string `json:"name"`
			Output string `json:"output"`
		}
		if err := json.Unmarshal([]byte(message.Content), &envelope); err != nil {
			return "", err
		}
		nameValue6 := str.String(envelope.Name)
		if nameValue6.Trim() != expectedName {
			return "", fmt.Errorf("expected tool payload name %q", expectedName)
		}
		outputValue := str.String(envelope.Output)
		return outputValue.Trim(), nil
	}

	return "", fmt.Errorf("expected tool output for tool call %q", expectedID)
}
