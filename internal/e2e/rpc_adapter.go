package e2e

import (
	"context"
	"errors"

	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/pkg/str"
)

type rpcClientAPI interface {
	Respond(context.Context, string, rpcclient.RespondOptions) (string, error)
	SessionAPI() rpcclient.SessionAPI
	Close() error
}

var rpcclientNewClient = func(ctx context.Context, opts rpcclient.Options) (rpcClientAPI, error) {
	return rpcclient.NewClient(ctx, opts)
}

// RPCAdapter adapts agent operations to the rpc harness.
type RPCAdapter struct {
	harness *RPCHarness
}

// NewRPCAdapter returns an adapter that drives agent turns through RPC.
func NewRPCAdapter(harness *RPCHarness) *RPCAdapter {
	return &RPCAdapter{harness: harness}
}

func (a *RPCAdapter) Send(ctx context.Context, req RootChatRequest) (RootChatResult, error) {
	if a == nil || a.harness == nil {
		return RootChatResult{}, errors.New("e2e rpc adapter is required")
	}
	if err := req.Validate(); err != nil {
		return RootChatResult{}, err
	}

	client, err := rpcclientNewClient(normalizeHarnessContext(ctx), rpcclient.Options{
		Address:           a.harness.address,
		Port:              a.harness.port,
		PermissionSurface: permissions.SurfaceCLI,
		AuthAudience:      a.harness.authAudience,
		AuthKey:           append([]byte(nil), a.harness.authKey...),
		AuthOwnerID:       a.harness.authOwnerID,
	})
	if err != nil {
		return RootChatResult{}, err
	}
	defer func() {
		_ = client.Close()
	}()

	var events []Event
	reply, err := client.Respond(normalizeHarnessContext(ctx), req.Message, rpcclient.RespondOptions{
		Instruct:  req.Instruct,
		SessionID: req.SessionID,
		Stream:    req.Stream,
		OnEvent: func(event rpcclient.Event) {
			if event.TraceEvent != nil {
				return
			}

			channelValue := str.String(event.Channel)
			events = append(events, Event{
				Channel: channelValue.Trim(),
				Text:    event.Text,
			})
		},
	})
	if err != nil {
		return RootChatResult{}, err
	}
	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()
	if sessionID == "" {
		session, err := client.SessionAPI().Current(normalizeHarnessContext(ctx))
		if err != nil {
			return RootChatResult{}, err
		}
		sessionID = session.ID
	}

	return RootChatResult{
		Reply:     reply,
		SessionID: sessionID,
		Events:    events,
	}, nil
}
