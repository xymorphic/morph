package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentapi "github.com/wandxy/morph/internal/agent"
	"github.com/wandxy/morph/internal/automation"
	"github.com/wandxy/morph/internal/browser"
	"github.com/wandxy/morph/internal/config"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"github.com/wandxy/morph/internal/rpc/tlsconfig"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	"github.com/wandxy/morph/internal/trace"
	agent "github.com/wandxy/morph/pkg/agent"
	"github.com/wandxy/morph/pkg/str"
)

// Client wraps a gRPC connection to the Morph RPC services.
type Client struct {
	conn        *grpc.ClientConn
	reconnector rpcReconnector
	client      morphpb.MorphServiceClient
	Session     *SessionService
	Model       *ModelService
	Gateway     *GatewayService
	Automation  *AutomationService
	Permission  *PermissionService
	Browser     *BrowserService
	Auth        *AuthService
	auth        *authResolver
}

type SessionService struct {
	client      morphpb.SessionServiceClient
	reconnector rpcReconnector
}

type ModelService struct {
	client      morphpb.ModelServiceClient
	reconnector rpcReconnector
}

type GatewayService struct {
	client      morphpb.GatewayServiceClient
	reconnector rpcReconnector
}

type AutomationService struct {
	client      morphpb.AutomationServiceClient
	reconnector rpcReconnector
}

type PermissionService struct {
	client      morphpb.PermissionServiceClient
	reconnector rpcReconnector
}

type BrowserService struct {
	client      morphpb.BrowserServiceClient
	reconnector rpcReconnector
}

type rpcReconnector interface {
	ResetConnectBackoff()
	Connect()
}

// RespondOptions mirrors agent response options at this package boundary.
type RespondOptions = agent.RespondOptions

// Event aliases agent.Event at this package boundary.
type Event = agent.Event

// CompactSessionResult aliases agent.CompactSessionResult at this package boundary.
type CompactSessionResult = agent.CompactSessionResult

// ContextStatus aliases agent.ContextStatus at this package boundary.
type ContextStatus = agent.ContextStatus

// SessionTimelineOptions mirrors agent timeline query options at this package boundary.
type SessionTimelineOptions = agentapi.SessionTimelineOptions

// SessionTimeline mirrors the agent timeline type at this package boundary.
type SessionTimeline = agentapi.SessionTimeline

// RepairSessionOptions aliases search.VectorRepairOptions at this package boundary.
type RepairSessionOptions = search.VectorRepairOptions

// RepairSessionResult aliases search.VectorRepairResult at this package boundary.
type RepairSessionResult = search.VectorRepairResult

type CreateSessionOptions struct {
	ID           string
	OriginSource string
	AutoSwitch   *bool
}

type SessionListOptions = storage.SessionListOptions

type ModelListOptions = agentapi.ModelListOptions

type ModelSelectOptions = agentapi.ModelSelectOptions

type ProviderOption = models.ProviderOption

type ProviderList = agentapi.ProviderList

type ModelOption = models.Option

type ModelList = agentapi.ModelList

type ModelRuntime struct {
	Provider      string
	API           string
	Model         string
	BaseURL       string
	ContextLength int
}

type GatewayPairingRequest struct {
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	Source      string
	SenderID    string
	DisplayName string
}

type GatewayPairedSender struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Source      string
	SenderID    string
	DisplayName string
}

type GatewayPairingList struct {
	Pending  []GatewayPairingRequest
	Approved []GatewayPairedSender
}

type GatewayStatus struct {
	State        string
	Address      string
	Port         int
	SlackMode    string
	TelegramMode string
	LastError    string
}

type AutomationStatus = automation.Status

// ChatAPI is the chat surface exposed by local and RPC clients.
type ChatAPI interface {
	Respond(context.Context, string, RespondOptions) (string, error)
}

// SessionAPI is the session-management surface exposed by local and RPC clients.
type SessionAPI interface {
	Create(context.Context, string) (storage.Session, error)
	CreateWithOptions(context.Context, CreateSessionOptions) (storage.Session, error)
	List(context.Context, ...SessionListOptions) ([]storage.Session, error)
	Use(context.Context, string) error
	Archive(context.Context, string) error
	Unarchive(context.Context, string) (storage.Session, error)
	Rename(context.Context, string, string) (storage.Session, error)
	Current(context.Context) (storage.Session, error)
	Compact(context.Context, string) (CompactSessionResult, error)
	Repair(context.Context, RepairSessionOptions) (RepairSessionResult, error)
	Status(context.Context, string) (ContextStatus, error)
	Timeline(context.Context, SessionTimelineOptions) (SessionTimeline, error)
}

type ModelAPI interface {
	RuntimeModel(context.Context) (ModelRuntime, error)
	ListProviders(context.Context) (ProviderList, error)
	ListModels(context.Context, ...ModelListOptions) (ModelList, error)
	SelectModel(context.Context, string, ...agentapi.ModelSelectOptions) (ModelOption, error)
	SetProviderAPIKey(context.Context, string, string) error
}

type GatewayAPI interface {
	GatewayStatus(context.Context) (GatewayStatus, error)
	Start(context.Context) (GatewayStatus, error)
	Stop(context.Context) (GatewayStatus, error)
	Restart(context.Context) (GatewayStatus, error)
	ListPairings(context.Context, string) (GatewayPairingList, error)
	ApprovePairing(context.Context, string, string) (GatewayPairedSender, bool, error)
	RevokePairing(context.Context, string, string) error
	ClearPendingPairings(context.Context, string) error
}

type AutomationAPI interface {
	Status(context.Context) (automation.Status, error)
	List(context.Context, automation.JobQuery) (automation.JobList, error)
	Add(context.Context, automation.Job) (automation.Job, error)
	Update(context.Context, automation.JobPatch) (automation.Job, error)
	Remove(context.Context, string) error
	Run(context.Context, string) (automation.Run, error)
	Runs(context.Context, automation.RunQuery) (automation.RunList, error)
}

type PermissionAPI interface {
	ListApprovalRequests(context.Context, permissions.ApprovalQuery) ([]permissions.ApprovalRequest, error)
	GetApprovalRequest(context.Context, string) (permissions.ApprovalRequest, bool, error)
	ResolveApprovalRequest(context.Context, string, bool, permissions.GrantScope) (permissions.ApprovalRequest, error)
	ListApprovalGrants(context.Context, permissions.GrantQuery) ([]permissions.ApprovalGrant, error)
	GetApprovalGrant(context.Context, string) (permissions.ApprovalGrant, bool, error)
	RevokeApprovalGrant(context.Context, string) (permissions.ApprovalGrant, error)
	DeleteApprovalRecord(context.Context, string) (permissions.ApprovalDeleteResult, error)
	PruneApprovals(context.Context, bool) (permissions.ApprovalPruneResult, error)
}

type BrowserEffectiveConfig struct {
	Enabled              bool
	CapabilityEnabled    bool
	DefaultProfile       string
	NetworkStrict        bool
	PermissionPreset     permissions.Preset
	ExecutableConfigured bool
}

type BrowserAPI interface {
	Status(context.Context) (browser.Status, error)
	Profiles(context.Context) ([]browser.Profile, error)
	Sessions(context.Context) ([]browser.Session, error)
	Start(context.Context, string, string) (browser.Session, error)
	Stop(context.Context, string, string) (browser.Session, error)
	ReadArtifact(context.Context, string, string, string) (browser.ArtifactContent, error)
	EffectiveConfig(context.Context) (BrowserEffectiveConfig, error)
}

// ServiceAPI combines chat and session operations.
type ServiceAPI interface {
	ChatAPI
	SessionAPI() SessionAPI
	ModelAPI() ModelAPI
	GatewayAPI() GatewayAPI
	AutomationAPI() AutomationAPI
	BrowserAPI() BrowserAPI
}

// ChatClient is a closable client that can run chat turns.
type ChatClient interface {
	ChatAPI
	Close() error
}

// ClientAPI is the complete closable RPC client surface.
type ClientAPI interface {
	ServiceAPI
	Close() error
}

// Options configures this package operation.
type Options struct {
	Address                   string
	Port                      int
	PermissionSurface         permissions.Surface
	PermissionPreset          permissions.Preset
	AuthToken                 string
	AuthKey                   []byte
	AuthIdentityGeneration    uint64
	AuthAudience              string
	AuthOwnerID               string
	AuthTokenTTL              time.Duration
	AuthNonceBytes            int
	AuthSessionIdleTTL        time.Duration
	AuthServices              []string
	AuthMethods               []string
	AuthTLS                   config.AuthTLSConfig
	authCertificateThumbprint string
}

// NewClient returns a client configured with the supplied dependencies.
func NewClient(ctx context.Context, opts Options) (*Client, error) {
	addressValue := str.String(opts.Address)
	address := addressValue.Trim()
	if address == "" {
		return nil, fmt.Errorf("rpc address is required")
	}

	if opts.Port <= 0 {
		return nil, fmt.Errorf("rpc port must be greater than zero")
	}

	target := fmt.Sprintf("%s:%d", address, opts.Port)
	if _, err := url.Parse("dns:///" + target); err != nil {
		return nil, err
	}
	transportCredentials, certificateThumbprint, err := tlsconfig.ClientCredentials(opts.AuthTLS, target)
	if err != nil {
		return nil, err
	}
	opts.authCertificateThumbprint = certificateThumbprint
	resolver := newAuthResolver(opts)
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithChainUnaryInterceptor(
			permissionUnaryClientInterceptor(opts), authUnaryClientInterceptor(resolver),
		),
		grpc.WithChainStreamInterceptor(
			permissionStreamClientInterceptor(opts), authStreamClientInterceptor(resolver),
		),
	)
	if err != nil {
		return nil, err
	}

	resolver.setConnection(conn)
	return &Client{
		conn:        conn,
		reconnector: conn,
		client:      morphpb.NewMorphServiceClient(conn),
		Session:     newSessionService(morphpb.NewSessionServiceClient(conn), conn),
		Model:       newModelService(morphpb.NewModelServiceClient(conn), conn),
		Gateway:     newGatewayService(morphpb.NewGatewayServiceClient(conn), conn),
		Automation:  newAutomationService(morphpb.NewAutomationServiceClient(conn), conn),
		Permission:  newPermissionService(morphpb.NewPermissionServiceClient(conn), conn),
		Browser:     newBrowserService(morphpb.NewBrowserServiceClient(conn), conn),
		Auth:        newAuthService(morphpb.NewAuthServiceClient(conn)),
		auth:        resolver,
	}, nil
}

func permissionUnaryClientInterceptor(opts Options) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req any,
		reply any,
		conn *grpc.ClientConn,
		invoke grpc.UnaryInvoker,
		callOptions ...grpc.CallOption,
	) error {
		return invoke(withPermissionMetadata(ctx, opts), method, req, reply, conn, callOptions...)
	}
}

func permissionStreamClientInterceptor(opts Options) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		conn *grpc.ClientConn,
		method string,
		stream grpc.Streamer,
		callOptions ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return stream(withPermissionMetadata(ctx, opts), desc, conn, method, callOptions...)
	}
}

func withPermissionMetadata(ctx context.Context, opts Options) context.Context {
	ctx = rpcmeta.WithOutgoingPermissionSurface(ctx, opts.PermissionSurface)
	return rpcmeta.WithOutgoingPermissionPreset(ctx, opts.PermissionPreset)
}

func NewSessionService(client morphpb.SessionServiceClient) *SessionService {
	return newSessionService(client, nil)
}

func NewModelService(client morphpb.ModelServiceClient) *ModelService {
	return newModelService(client, nil)
}

func NewGatewayService(client morphpb.GatewayServiceClient) *GatewayService {
	return newGatewayService(client, nil)
}

func NewAutomationService(client morphpb.AutomationServiceClient) *AutomationService {
	return newAutomationService(client, nil)
}

func NewPermissionService(client morphpb.PermissionServiceClient) *PermissionService {
	return newPermissionService(client, nil)
}

func NewBrowserService(client morphpb.BrowserServiceClient) *BrowserService {
	return newBrowserService(client, nil)
}

func newSessionService(client morphpb.SessionServiceClient, reconnector rpcReconnector) *SessionService {
	return &SessionService{client: client, reconnector: reconnector}
}

func newModelService(client morphpb.ModelServiceClient, reconnector rpcReconnector) *ModelService {
	return &ModelService{client: client, reconnector: reconnector}
}

func newGatewayService(client morphpb.GatewayServiceClient, reconnector rpcReconnector) *GatewayService {
	return &GatewayService{client: client, reconnector: reconnector}
}

func newPermissionService(client morphpb.PermissionServiceClient, reconnector rpcReconnector) *PermissionService {
	return &PermissionService{client: client, reconnector: reconnector}
}

func newBrowserService(client morphpb.BrowserServiceClient, reconnector rpcReconnector) *BrowserService {
	return &BrowserService{client: client, reconnector: reconnector}
}

func newAutomationService(client morphpb.AutomationServiceClient, reconnector rpcReconnector) *AutomationService {
	return &AutomationService{client: client, reconnector: reconnector}
}

func prepareRPCConnection(reconnector rpcReconnector) {
	if reconnector == nil {
		return
	}

	reconnector.ResetConnectBackoff()
	reconnector.Connect()
}

func (c *Client) Respond(ctx context.Context, message string, opts RespondOptions) (string, error) {
	instructValue := str.String(opts.Instruct)
	sessionIDValue := str.String(opts.SessionID)
	req := &morphpb.RespondRequest{
		Message:  message,
		Instruct: instructValue.Trim(),
		Id:       sessionIDValue.Trim(),
	}
	if opts.Stream != nil {
		req.Stream = opts.Stream
	}

	prepareRPCConnection(c.reconnector)
	stream, err := c.client.Respond(ctx, req)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				return builder.String(), errors.New("respond stream ended before done event")
			}

			return builder.String(), recvErr
		}
		switch event.GetType() {
		case morphpb.RespondEvent_TEXT_DELTA:
			if event.GetChannel() != morphpb.RespondEvent_REASONING {
				builder.WriteString(event.GetText())
			}
			if opts.OnEvent != nil {
				opts.OnEvent(Event{
					Kind:    agent.EventKindTextDelta,
					Channel: protoStreamChannelToAgentChannel(event.GetChannel()),
					Text:    event.GetText(),
				})
			}
		case morphpb.RespondEvent_TRACE_EVENT:
			if opts.OnEvent != nil {
				traceEvent, ok := protoRespondTraceEventToTraceEvent(event)
				if ok {
					opts.OnEvent(Event{
						Kind:       agent.EventKindTrace,
						TraceEvent: &traceEvent,
					})
				}
			}
		case morphpb.RespondEvent_ERROR:
			eventError := str.String(event.GetError())
			message := eventError.Trim()
			if message == "" {
				message = "respond stream failed"
			}
			return builder.String(), errors.New(message)
		case morphpb.RespondEvent_DONE:
			return builder.String(), nil
		}
	}
}

func protoRespondTraceEventToTraceEvent(event *morphpb.RespondEvent) (trace.Event, bool) {
	if event == nil {
		return trace.Event{}, false
	}

	traceTypeValue := str.String(event.GetTraceType())
	eventType := traceTypeValue.Trim()
	if eventType == "" {
		return trace.Event{}, false
	}
	traceSessionIdValue := str.String(event.GetTraceSessionId())
	traceEvent := trace.Event{
		SessionID: traceSessionIdValue.Trim(),
		Type:      eventType,
		Timestamp: protoTimestampToTime(event.GetTimestamp()),
	}
	tracePayloadJsonValue := str.String(event.GetTracePayloadJson())
	if payloadJSON := tracePayloadJsonValue.Trim(); payloadJSON != "" {
		var payload any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return trace.Event{}, false
		}
		traceEvent.Payload = payload
	}

	return traceEvent, true
}

func protoStreamChannelToAgentChannel(channel morphpb.RespondEvent_Channel) string {
	switch channel {
	case morphpb.RespondEvent_REASONING:
		return "reasoning"
	default:
		return "assistant"
	}
}

func (c *Client) SessionAPI() SessionAPI {
	if c == nil {
		return nil
	}

	return c.Session
}

func (c *Client) ModelAPI() ModelAPI {
	if c == nil {
		return nil
	}

	return c.Model
}

func (c *Client) GatewayAPI() GatewayAPI {
	if c == nil {
		return nil
	}

	return c.Gateway
}

func (c *Client) AutomationAPI() AutomationAPI {
	if c == nil {
		return nil
	}

	return c.Automation
}

func (c *Client) PermissionAPI() PermissionAPI {
	if c == nil {
		return nil
	}

	return c.Permission
}

func (c *Client) BrowserAPI() BrowserAPI {
	if c == nil {
		return nil
	}

	return c.Browser
}

func (c *Client) CheckHealth(ctx context.Context) (string, error) {
	if c == nil || c.conn == nil {
		return "", errors.New("RPC client is unavailable")
	}
	response, err := healthpb.NewHealthClient(c.conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return "", err
	}
	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return "", fmt.Errorf("daemon health status is %s", response.GetStatus())
	}

	return response.GetStatus().String(), nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	if c.auth != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.auth.close(ctx)
		cancel()
	}

	return c.conn.Close()
}

func protoTimestampToTime(value interface{ AsTime() time.Time }) time.Time {
	if timestamp, ok := any(value).(*timestamppb.Timestamp); ok && timestamp == nil {
		return time.Time{}
	}
	if value == nil {
		return time.Time{}
	}

	return value.AsTime().UTC()
}
