package e2e

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"

	morphagent "github.com/wandxy/morph/internal/agent"
	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storememory"
	"github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/pkg/str"
	"google.golang.org/grpc"

	"github.com/wandxy/morph/internal/rpc/server"
)

var rpcListen = net.Listen
var newBaseHarness = NewHarness
var grpcServe = func(srv *grpc.Server, lis net.Listener) error {
	return srv.Serve(lis)
}

// RPCHarness drives rpc e2e scenarios.
type RPCHarness struct {
	*Harness
	address      string
	port         int
	server       *grpc.Server
	authKey      []byte
	authAudience string
	authOwnerID  string
	errMu        sync.Mutex
	err          error
}

// NewRPCHarness returns an RPC-backed e2e harness.
func NewRPCHarness(ctx context.Context, opts HarnessOptions) (*RPCHarness, error) {
	base, err := newBaseHarness(ctx, opts)
	if err != nil {
		return nil, err
	}
	serviceAPI, ok := base.agent.(morphagent.ServiceAPI)
	if !ok {
		_ = base.Close()
		return nil, errors.New("e2e rpc harness requires a full agent service")
	}
	runner, ok := base.agent.(interface {
		StartSessionRunner(context.Context) error
	})
	if !ok {
		_ = base.Close()
		return nil, errors.New("e2e rpc harness requires a session runner")
	}
	if err := runner.StartSessionRunner(ctx); err != nil {
		_ = base.Close()
		return nil, err
	}
	activeProfile := profile.Active()
	identity, err := credential.NewFileStore("").LoadOrCreateIdentity()
	if err != nil {
		_ = base.Close()
		return nil, err
	}

	lis, err := rpcListen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = base.Close()
		return nil, err
	}

	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		_ = lis.Close()
		_ = base.Close()
		return nil, errors.New("e2e rpc listener must be tcp")
	}

	authService, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience:       base.cfg.Auth.Audience,
		Store:          storememory.New(),
		SessionIdleTTL: base.cfg.Auth.SessionIdleTTL,
		SessionMaxTTL:  base.cfg.Auth.SessionMaximumTTL,
	})
	if err != nil {
		_ = lis.Close()
		_ = base.Close()
		return nil, err
	}
	if _, err := authService.SeedRoot(ctx, identity, activeProfile.Name); err != nil {
		_ = lis.Close()
		_ = base.Close()
		return nil, err
	}
	authKey, err := morphauth.MarshalIdentity(identity)
	if err != nil {
		_ = lis.Close()
		_ = base.Close()
		return nil, err
	}
	grpcServer := server.New(serviceAPI, server.Options{
		Health:           true,
		PermissionPolicy: base.cfg.Permissions,
		ProfileName:      activeProfile.Name,
		Auth:             authService,
	})

	h := &RPCHarness{
		Harness:      base,
		address:      tcpAddr.IP.String(),
		port:         tcpAddr.Port,
		server:       grpcServer,
		authKey:      authKey,
		authAudience: base.cfg.Auth.Audience,
		authOwnerID:  activeProfile.Name,
	}

	go func() {
		if serveErr := grpcServe(grpcServer, lis); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			h.errMu.Lock()
			h.err = serveErr
			h.errMu.Unlock()
		}
	}()

	return h, nil
}

func (h *RPCHarness) Address() string {
	if h == nil {
		return ""
	}

	return h.address
}

func (h *RPCHarness) Port() int {
	if h == nil {
		return 0
	}

	return h.port
}

func (h *RPCHarness) Client(ctx context.Context) (*rpcclient.Client, error) {
	if h == nil {
		return nil, errors.New("e2e rpc harness is required")
	}

	return rpcclient.NewClient(normalizeHarnessContext(ctx), rpcclient.Options{
		Address: h.address, Port: h.port,
		PermissionSurface: permissions.SurfaceCLI,
		AuthAudience:      h.authAudience,
		AuthKey:           append([]byte(nil), h.authKey...),
		AuthOwnerID:       h.authOwnerID,
	})
}

func (h *RPCHarness) Close() error {
	if h == nil {
		return nil
	}

	if h.server != nil {
		h.server.Stop()
	}
	if h.Harness != nil {
		_ = h.Harness.Close()
	}

	h.errMu.Lock()
	defer h.errMu.Unlock()

	return h.err
}

func (h *RPCHarness) ConfigFileContents() string {
	if h == nil {
		return ""
	}

	addressValue := str.String(h.address)
	return "rpc:\n" +
		"  address: " + addressValue.Trim() + "\n" +
		"  port: " + strconv.Itoa(h.port) + "\n" +
		"auth:\n" +
		"  audience: " + h.authAudience + "\n" +
		"  key: |\n    " + strings.ReplaceAll(string(h.authKey), "\n", "\n    ")
}
