package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	morphagent "github.com/wandxy/morph/internal/agent"
	"github.com/wandxy/morph/internal/automation"
	"github.com/wandxy/morph/internal/browser"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/gateway"
	"github.com/wandxy/morph/internal/guardrails"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	"github.com/wandxy/morph/internal/trace"
	agent "github.com/wandxy/morph/pkg/agent"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"github.com/wandxy/morph/pkg/str"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service is the RPC service that wraps the agent-facing service interface.
type Service struct {
	morphpb.UnimplementedMorphServiceServer
	morphpb.UnimplementedSessionServiceServer
	morphpb.UnimplementedModelServiceServer
	api                  morphagent.ServiceAPI
	automation           AutomationAPI
	browser              BrowserAPI
	browserConfig        config.BrowserConfig
	browserCapability    bool
	profileName          string
	runtimeModel         ModelRuntime
	gatewayPairingSecret string
	gatewayConfig        config.GatewayConfig
	gatewayRuntime       GatewayRuntime
	permissions          permissions.Engine
	approvalService      *permissions.ApprovalService
}

var marshalRPCJSON = json.Marshal

type ServiceOptions struct {
	RuntimeModel         ModelRuntime
	GatewayPairingSecret string
	GatewayConfig        config.GatewayConfig
	GatewayRuntime       GatewayRuntime
	Automation           AutomationAPI
	Browser              BrowserAPI
	BrowserConfig        config.BrowserConfig
	BrowserCapability    bool
	ProfileName          string
	PermissionPolicy     permissions.Policy
}

type ModelRuntime struct {
	Provider      string
	API           string
	Model         string
	BaseURL       string
	ContextLength int
}

type GatewayRuntime interface {
	Start(context.Context, config.GatewayConfig, gateway.AgentService) error
	Stop(context.Context) error
	Status() gateway.Status
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

type BrowserAPI interface {
	Status() browser.Status
	Start(context.Context, browser.StartRequest) (browser.Session, error)
	Stop(context.Context, string) (browser.Session, error)
	ReadArtifact(context.Context, string) (browser.ArtifactContent, error)
}

// NewService creates a new RPC service that wraps the shared service interface.
func NewService(api morphagent.ServiceAPI) *Service {
	return NewServiceWithOptions(api, ServiceOptions{})
}

func NewServiceWithOptions(api morphagent.ServiceAPI, opts ServiceOptions) *Service {
	gatewayPairingSecretValue := str.String(opts.GatewayPairingSecret)
	var approvalService *permissions.ApprovalService
	if provider, ok := api.(interface {
		ApprovalService() *permissions.ApprovalService
	}); ok {
		approvalService = provider.ApprovalService()
	}
	return &Service{
		api:                  api,
		automation:           opts.Automation,
		browser:              opts.Browser,
		browserConfig:        opts.BrowserConfig,
		browserCapability:    opts.BrowserCapability,
		profileName:          strings.TrimSpace(opts.ProfileName),
		runtimeModel:         normalizeModelRuntime(opts.RuntimeModel),
		gatewayPairingSecret: gatewayPairingSecretValue.Trim(),
		gatewayConfig:        opts.GatewayConfig,
		gatewayRuntime:       opts.GatewayRuntime,
		permissions:          permissions.NewEngine(opts.PermissionPolicy),
		approvalService:      approvalService,
	}
}

func (s *Service) checkPermission(ctx context.Context, operation permissions.Operation) error {
	if s == nil {
		return status.Error(codes.Internal, "service is required")
	}

	ctx = s.getPermissionContext(ctx)

	_, err := s.permissions.Check(ctx, permissions.EvaluationInput{Operation: operation})
	decisionErr, ok := permissions.GetDecisionError(err)
	if !ok {
		return nil
	}
	if decisionErr.Code == permissions.ErrorCodeApprovalRequired {
		return status.Error(codes.FailedPrecondition, "approval required: "+decisionErr.Error())
	}

	return status.Error(codes.PermissionDenied, "permission denied: "+decisionErr.Error())
}

func (s *Service) getPermissionContext(ctx context.Context) context.Context {
	if _, ok := permissions.FromContext(ctx); ok {
		return withIncomingPermissionPreset(ctx)
	}

	ctx = permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:   rpcmeta.PermissionActorFromIncomingContext(ctx),
		Surface: rpcmeta.PermissionSurfaceFromIncomingContext(ctx),
	})
	return withIncomingPermissionPreset(ctx)
}

func ModelRuntimeFromConfig(cfg *config.Config) ModelRuntime {
	if cfg == nil {
		return ModelRuntime{}
	}

	snapshot := *cfg
	snapshot.Normalize()

	return normalizeModelRuntime(ModelRuntime{
		Provider:      snapshot.Models.Main.Provider,
		API:           snapshot.MainModelAPIEffective(),
		Model:         snapshot.Models.Main.Name,
		BaseURL:       snapshot.Models.Main.BaseURL,
		ContextLength: snapshot.Models.Main.ContextLength,
	})
}

func normalizeModelRuntime(runtime ModelRuntime) ModelRuntime {
	providerValue := str.String(runtime.Provider)
	runtime.Provider = providerValue.Normalized()
	aPIValue := str.String(runtime.API)
	runtime.API = aPIValue.Normalized()
	modelValue := str.String(runtime.Model)
	runtime.Model = modelValue.Trim()
	runtime.BaseURL = normalizeRuntimeModelBaseURL(runtime.BaseURL)
	if runtime.ContextLength < 0 {
		runtime.ContextLength = 0
	}

	return runtime
}

func normalizeRuntimeModelBaseURL(value string) string {
	valueText := str.String(value)
	return strings.TrimRight(valueText.Trim(), "/")
}

// Respond sends a chat request to the service and returns the completed response.
func (s *Service) Respond(req *morphpb.RespondRequest, stream morphpb.MorphService_RespondServer) error {
	if s == nil {
		return status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "respond request is required")
	}

	ctx := stream.Context()
	ctx = permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:     rpcmeta.PermissionActorFromIncomingContext(ctx),
		Surface:   rpcmeta.PermissionSurfaceFromIncomingContext(ctx),
		SessionID: req.GetId(),
	})
	ctx = withIncomingPermissionPreset(ctx)
	streamed := false
	var sendErr error
	opts := agent.RespondOptions{
		Instruct:    req.Instruct,
		SessionID:   req.GetId(),
		Stream:      req.Stream,
		TraceEvents: true,
		OnEvent: func(event agent.Event) {
			if sendErr != nil {
				return
			}

			protoEvent, ok := eventToProtoRespondEvent(event)
			if !ok {
				return
			}
			if protoEvent.GetType() == morphpb.RespondEvent_TEXT_DELTA {
				streamed = true
			}
			sendErr = stream.Send(protoEvent)
		},
	}

	reply, err := s.api.Respond(ctx, req.Message, opts)
	if sendErr != nil {
		return sendErr
	}
	if err != nil {
		grpcErr := getGRPCError(err)
		if sendErr := stream.Send(&morphpb.RespondEvent{
			Type:      morphpb.RespondEvent_ERROR,
			Error:     status.Convert(grpcErr).Message(),
			Timestamp: timestamppb.New(time.Now().UTC()),
		}); sendErr != nil {
			return sendErr
		}
		return nil
	}

	if !streamed {
		if err := stream.Send(&morphpb.RespondEvent{
			Type:    morphpb.RespondEvent_TEXT_DELTA,
			Text:    reply,
			Channel: morphpb.RespondEvent_ASSISTANT,
		}); err != nil {
			return err
		}
	}

	return stream.Send(&morphpb.RespondEvent{
		Type:      morphpb.RespondEvent_DONE,
		Timestamp: timestamppb.New(time.Now().UTC()),
	})
}

func withIncomingPermissionPreset(ctx context.Context) context.Context {
	authorization, ok := permissions.FromContext(ctx)
	if !ok || authorization.Actor.Kind != permissions.ActorLocalOwner ||
		(authorization.Surface != permissions.SurfaceCLI &&
			authorization.Surface != permissions.SurfaceTUI) {
		return ctx
	}

	if preset, ok := rpcmeta.PermissionPresetFromIncomingContext(ctx); ok {
		return permissions.WithPreset(ctx, preset)
	}

	return ctx
}

func eventToProtoRespondEvent(event agent.Event) (*morphpb.RespondEvent, bool) {
	kindValue := str.String(event.Kind)
	kind := kindValue.Trim()
	if kind == agent.EventKindTrace {
		traceEvent, ok := traceEventFromAgentEvent(event)
		if !ok {
			return nil, false
		}
		return traceEventToProtoRespondEvent(traceEvent)
	}
	if kind != "" && kind != agent.EventKindTextDelta {
		return nil, false
	}

	return &morphpb.RespondEvent{
		Type:    morphpb.RespondEvent_TEXT_DELTA,
		Text:    event.Text,
		Channel: agentChannelToProtoStreamChannel(event.Channel),
	}, true
}

func traceEventFromAgentEvent(event agent.Event) (trace.Event, bool) {
	switch value := event.TraceEvent.(type) {
	case trace.Event:
		return value, true
	case *trace.Event:
		if value == nil {
			return trace.Event{}, false
		}
		return *value, true
	default:
		return trace.Event{}, false
	}
}

func agentChannelToProtoStreamChannel(channel string) morphpb.RespondEvent_Channel {
	channelValue := str.String(channel)
	switch channelValue.Normalized() {
	case "reasoning":
		return morphpb.RespondEvent_REASONING
	default:
		return morphpb.RespondEvent_ASSISTANT
	}
}

func traceEventToProtoRespondEvent(event trace.Event) (*morphpb.RespondEvent, bool) {
	trimmedValueValue := str.String(event.Type)
	event.Type = trimmedValueValue.Trim()
	if event.Type == "" {
		return nil, false
	}

	payload, ok := getRPCTracePayload(event.Type, event.Payload)
	if !ok {
		return nil, false
	}

	payloadJSON, err := marshalRPCJSON(payload)
	if err != nil {
		return nil, false
	}
	sessionIDValue := str.String(event.SessionID)
	protoEvent := &morphpb.RespondEvent{
		Type:             morphpb.RespondEvent_TRACE_EVENT,
		TraceSessionId:   sessionIDValue.Trim(),
		TraceType:        event.Type,
		TracePayloadJson: string(payloadJSON),
	}
	if !event.Timestamp.IsZero() {
		protoEvent.Timestamp = timestamppb.New(event.Timestamp.UTC())
	} else {
		protoEvent.Timestamp = timestamppb.New(time.Now().UTC())
	}

	return protoEvent, true
}

func getRPCTracePayload(eventType string, payload any) (any, bool) {
	typedPayload, payloadOK := trace.DecodePayload(eventType, payload)

	switch eventType {
	case trace.EvtToolInvocationStarted:
		toolPayload, ok := typedPayload.(trace.ToolInvocationStartedPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		iDValue := str.String(toolPayload.ID)
		nameValue := str.String(toolPayload.Name)
		result := rpcToolInvocationStartedPayload{
			ID:     iDValue.Trim(),
			Name:   nameValue.Trim(),
			Detail: getRPCTraceToolDetail(toolPayload.Name, toolPayload.Input),
		}
		result.PlanState = toolPayload.PlanState
		result.ProcessState = toolPayload.ProcessState
		return result, result.hasData()
	case trace.EvtToolInvocationCompleted:
		toolPayload, ok := typedPayload.(trace.ToolInvocationCompletedPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		toolCallIDValue := str.String(toolPayload.ToolCallID)
		nameValue2 := str.String(toolPayload.Name)
		result := rpcToolInvocationCompletedPayload{
			ToolCallID: toolCallIDValue.Trim(),
			Name:       nameValue2.Trim(),
			Content:    getRPCSafeToolResult(toolPayload.Name, toolPayload.Content),
			Failed:     toolPayload.Failed,
		}
		result.PlanState = toolPayload.PlanState
		result.ProcessState = toolPayload.ProcessState
		return result, result.hasData()
	case trace.EvtPermissionApprovalChanged:
		approvalPayload, ok := typedPayload.(trace.PermissionApprovalPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		result := rpcPermissionApprovalPayload{
			RequestID:  str.String(approvalPayload.RequestID).Trim(),
			Status:     str.String(approvalPayload.Status).Trim(),
			Scope:      str.String(approvalPayload.Scope).Trim(),
			Tool:       str.String(approvalPayload.Tool).Trim(),
			Resource:   str.String(approvalPayload.Resource).Trim(),
			Action:     str.String(approvalPayload.Action).Trim(),
			Effects:    append([]string(nil), approvalPayload.Effects...),
			Summary:    str.String(approvalPayload.Summary).Trim(),
			Reason:     str.String(approvalPayload.Reason).Trim(),
			Operations: append([]string(nil), approvalPayload.Operations...),
			ExpiresAt:  approvalPayload.ExpiresAt,
		}
		return result, result.RequestID != "" && result.Status != ""
	case trace.EvtInputSafetyBlocked,
		trace.EvtOutputSafetyApplied,
		trace.EvtToolOutputSafetyApplied,
		trace.EvtLoadedContentSafetyBlocked,
		trace.EvtMemorySafetyBlocked:
		safetyPayload, ok := typedPayload.(trace.SafetyEventPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		actionValue := str.String(safetyPayload.Action)
		refusalValue := str.String(safetyPayload.Refusal)
		result := rpcSafetyEventPayload{
			Action:   actionValue.Trim(),
			Blocked:  safetyPayload.Blocked,
			Redacted: safetyPayload.Redacted,
			Refusal:  refusalValue.Trim(),
			Findings: getRPCSafetyFindingSummaries(safetyPayload.Findings),
		}
		return result, true
	case trace.EvtSessionFailed:
		sessionPayload, ok := typedPayload.(trace.SessionFailedPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		errorValue := str.String(sessionPayload.Error)
		messageValue := str.String(sessionPayload.Message)
		result := trace.SessionFailedPayload{
			Error:   errorValue.Trim(),
			Message: messageValue.Trim(),
		}
		return result, result.Error != "" || result.Message != ""
	case trace.EvtPlanUpdated,
		trace.EvtPlanCleared,
		trace.EvtPlanHydrated:
		planPayload, ok := typedPayload.(trace.PlanEventPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		sessionIDValue2 := str.String(planPayload.SessionID)
		sourceValue := str.String(planPayload.Source)
		activeStepIDValue := str.String(planPayload.ActiveStepID)
		explanationValue := str.String(planPayload.Explanation)
		result := rpcPlanPayload{
			SessionID:    sessionIDValue2.Trim(),
			Source:       sourceValue.Trim(),
			ActiveStepID: activeStepIDValue.Trim(),
			Explanation:  explanationValue.Trim(),
			Steps:        getRPCPlanSteps(planPayload.Steps),
			Summary:      getRPCPlanSummary(planPayload.Summary),
			Changes:      append([]trace.PlanToolChange(nil), planPayload.Changes...),
		}
		return result, true
	case trace.EvtContextCompactionPending,
		trace.EvtContextCompactionRunning,
		trace.EvtContextCompactionSucceeded,
		trace.EvtContextCompactionFailed:
		compactionPayload, ok := typedPayload.(trace.CompactionEventPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		sessionIDValue3 := str.String(compactionPayload.SessionID)
		statusValue := str.String(compactionPayload.Status)
		errorValue2 := str.String(compactionPayload.Error)
		return trace.CompactionEventPayload{
			SessionID:          sessionIDValue3.Trim(),
			Status:             statusValue.Trim(),
			Auto:               compactionPayload.Auto,
			TargetMessageCount: compactionPayload.TargetMessageCount,
			TargetOffset:       compactionPayload.TargetOffset,
			RequestedAt:        compactionPayload.RequestedAt,
			StartedAt:          compactionPayload.StartedAt,
			CompletedAt:        compactionPayload.CompletedAt,
			FailedAt:           compactionPayload.FailedAt,
			Error:              errorValue2.Trim(),
		}, true
	case trace.EvtModelReasoningCompleted:
		reasoningPayload, ok := typedPayload.(trace.ModelReasoningCompletedPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		result := trace.ModelReasoningCompletedPayload{DurationMS: reasoningPayload.DurationMS}
		return result, result.DurationMS != 0
	case trace.EvtFinalAssistantResponse:
		finalPayload, ok := typedPayload.(trace.FinalAssistantResponsePayload)
		if !payloadOK || !ok {
			return nil, false
		}
		messageValue2 := str.String(finalPayload.Message)
		textValue := str.String(finalPayload.Text)
		result := trace.FinalAssistantResponsePayload{
			Message: messageValue2.Trim(),
			Text:    textValue.Trim(),
		}
		return result, result.Message != "" || result.Text != ""
	default:
		return nil, false
	}
}

type rpcToolInvocationStartedPayload struct {
	ID           string                  `json:"id,omitempty"`
	Name         string                  `json:"name,omitempty"`
	Detail       string                  `json:"detail,omitempty"`
	PlanState    *trace.PlanToolState    `json:"plan_state,omitempty"`
	ProcessState *trace.ProcessToolState `json:"process_state,omitempty"`
}

func (p rpcToolInvocationStartedPayload) hasData() bool {
	return p.ID != "" ||
		p.Name != "" ||
		p.Detail != "" ||
		p.PlanState != nil ||
		p.ProcessState != nil
}

type rpcToolInvocationCompletedPayload struct {
	ToolCallID   string                  `json:"tool_call_id,omitempty"`
	Name         string                  `json:"name,omitempty"`
	Content      string                  `json:"content,omitempty"`
	Failed       bool                    `json:"failed,omitempty"`
	PlanState    *trace.PlanToolState    `json:"plan_state,omitempty"`
	ProcessState *trace.ProcessToolState `json:"process_state,omitempty"`
}

func getRPCSafeToolResult(name string, content string) string {
	if str.String(name).Normalized() != "browser" {
		return ""
	}
	var artifact struct {
		Handle    string               `json:"handle"`
		Kind      browser.ArtifactKind `json:"kind"`
		Name      string               `json:"name"`
		MIMEType  string               `json:"mime_type"`
		Size      int64                `json:"size"`
		CreatedAt time.Time            `json:"created_at"`
		ExpiresAt time.Time            `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &artifact); err != nil ||
		strings.TrimSpace(artifact.Handle) == "" || artifact.Kind == "" || artifact.Size < 0 {
		return ""
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (p rpcToolInvocationCompletedPayload) hasData() bool {
	return p.ToolCallID != "" ||
		p.Name != "" ||
		p.PlanState != nil ||
		p.ProcessState != nil
}

type rpcPermissionApprovalPayload struct {
	RequestID  string    `json:"request_id,omitempty"`
	Status     string    `json:"status,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	Resource   string    `json:"resource,omitempty"`
	Action     string    `json:"action,omitempty"`
	Effects    []string  `json:"effects,omitempty"`
	Summary    string    `json:"operation_summary,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Operations []string  `json:"operations,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

type rpcSafetyEventPayload struct {
	Action   string                    `json:"action,omitempty"`
	Blocked  bool                      `json:"blocked,omitempty"`
	Redacted bool                      `json:"redacted,omitempty"`
	Refusal  string                    `json:"refusal,omitempty"`
	Findings []rpcSafetyFindingSummary `json:"findings,omitempty"`
}

type rpcSafetyFindingSummary struct {
	ID       string `json:"id,omitempty"`
	Category string `json:"category,omitempty"`
	Severity string `json:"severity,omitempty"`
}

type rpcPlanPayload struct {
	SessionID    string                    `json:"session_id,omitempty"`
	Source       string                    `json:"source,omitempty"`
	ActiveStepID string                    `json:"active_step_id,omitempty"`
	Explanation  string                    `json:"explanation,omitempty"`
	Steps        []trace.PlanStepPayload   `json:"steps,omitempty"`
	Summary      *trace.PlanSummaryPayload `json:"summary,omitempty"`
	Changes      []trace.PlanToolChange    `json:"changes,omitempty"`
}

func getRPCTraceToolDetail(name string, input string) string {
	nameValue3 := str.String(name)
	toolName := nameValue3.Trim()
	action := getRPCToolActionName(toolName)
	toolNameValue := str.String(toolName)
	if toolNameValue.Trim() == "" || action == "" {
		return ""
	}

	inputFields := getRPCTraceToolInputFields(input)
	if inputFields == nil {
		return ""
	}

	switch action {
	case "Automation":
		return getRPCAutomationToolDetail(inputFields)
	case "Run":
		return getRPCRunToolDetail(inputFields)
	case "Web Search", "Memory Search":
		return getRPCSearchToolDetail(inputFields)
	case "Search Files":
		return getRPCSearchFilesToolDetail(inputFields)
	case "Read", "Write":
		return getRPCPathToolDetail(toolName, inputFields)
	case "Patch":
		return getRPCPatchToolDetail(toolName, inputFields)
	default:
		if isRPCGenericToolDetailEnabled(toolName) {
			return getRPCGenericToolDetail(toolName, inputFields)
		}
		return ""
	}
}

func getRPCAutomationToolDetail(inputFields map[string]any) string {
	actionValue := str.String(getRPCMapString(inputFields, "action"))
	action := actionValue.Normalized()
	switch action {
	case "status", "list":
		return action
	case "add", "update", "pause", "resume", "run", "remove", "runs":
	default:
		return ""
	}

	target := getRPCMapString(inputFields, "id")
	switch action {
	case "add":
		job, _ := inputFields["job"].(map[string]any)
		target = getRPCMapString(job, "name", "id")
	case "runs":
		query, _ := inputFields["run_query"].(map[string]any)
		target = getRPCMapString(query, "job_id")
	}

	if target == "" {
		return action
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(target).(string)
	sanitized = truncateRPCTraceToolDetail(sanitized, 80)
	if sanitized == "" {
		return action
	}

	return action + ":" + sanitized
}

func getRPCTraceToolInputFields(input string) map[string]any {
	inputValue := str.String(input)
	inputText := inputValue.Trim()
	if inputText == "" {
		return nil
	}

	var inputFields map[string]any
	if err := json.Unmarshal([]byte(inputText), &inputFields); err != nil {
		return nil
	}

	return inputFields
}

func getRPCRunToolDetail(inputFields map[string]any) string {
	command := getRPCMapString(inputFields, "command")
	if command == "" {
		return ""
	}

	args := getRPCStringSlice(inputFields["args"])
	if len(args) > 0 {
		parts := append([]string{command}, args...)
		for index, part := range parts {
			parts[index] = shellQuoteRPCCommandPart(part)
		}
		command = strings.Join(parts, " ")
	}
	command = appendRPCToolTimeout(command, inputFields["timeout_seconds"])

	sanitized, _ := guardrails.NewRedactor().Sanitize(command).(string)
	sanitizedValue := str.String(sanitized)
	return sanitizedValue.Trim()
}

func getRPCSearchToolDetail(inputFields map[string]any) string {
	query := getRPCMapString(inputFields, "query", "q", "search_query")
	if query == "" {
		return ""
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(query).(string)
	sanitized = truncateRPCTraceToolDetail(sanitized, 80)

	return `Search "` + strings.ReplaceAll(sanitized, `"`, `'`) + `"`
}

func getRPCSearchFilesToolDetail(inputFields map[string]any) string {
	pattern := getRPCMapString(inputFields, "pattern", "query", "q")
	if pattern == "" {
		return ""
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(pattern).(string)
	sanitized = truncateRPCTraceToolDetail(sanitized, 80)

	detail := `Search "` + strings.ReplaceAll(sanitized, `"`, `'`) + `"`
	if path := getRPCDisplayPath(inputFields); path != "" {
		detail += " in " + path
	}
	if maxResults := formatOptionalRPCToolNumber(inputFields["max_results"]); maxResults != "" {
		detail += " max_results=" + maxResults
	}

	return detail
}

func getRPCPathToolDetail(name string, inputFields map[string]any) string {
	path := getRPCDisplayPath(inputFields)
	if path == "" {
		return ""
	}
	nameValue4 := str.String(name)
	return nameValue4.Trim() + " " + path
}

func getRPCPatchToolDetail(name string, inputFields map[string]any) string {
	patch := getRPCMapString(inputFields, "patch", "diff", "unified_diff")
	path, added, removed := getRPCPatchToolSummary(patch)
	if path == "" {
		path = getRPCDisplayPath(inputFields)
	}
	nameValue5 := str.String(name)
	parts := []string{nameValue5.Trim()}
	if path != "" {
		parts = append(parts, path)
	}
	if added > 0 || removed > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d", added, removed))
	}

	return strings.Join(parts, " ")
}

func getRPCDisplayPath(inputFields map[string]any) string {
	path := getRPCMapString(inputFields, "path", "file", "filepath", "filename")
	if path == "" {
		return ""
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(path).(string)
	return shortenRPCTraceToolPath(sanitized, 42)
}

func getRPCPatchToolSummary(patch string) (string, int, int) {
	var path string
	added := 0
	removed := 0

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			trimPrefixValue := str.String(strings.TrimPrefix(line, "+++ "))
			candidate := normalizeRPCPatchToolPath(trimPrefixValue.Trim())
			if candidate != "" && candidate != "/dev/null" {
				path = candidate
			}
		case strings.HasPrefix(line, "--- "):
			if path == "" {
				trimPrefixValue2 := str.String(strings.TrimPrefix(line, "--- "))
				candidate := normalizeRPCPatchToolPath(trimPrefixValue2.Trim())
				if candidate != "" && candidate != "/dev/null" {
					path = candidate
				}
			}
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}

	if path != "" {
		sanitized, _ := guardrails.NewRedactor().Sanitize(path).(string)
		path = shortenRPCTraceToolPath(sanitized, 42)
	}

	return path, added, removed
}

func normalizeRPCPatchToolPath(path string) string {
	pathValue := str.String(path)
	path = pathValue.Trim()
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return strings.Trim(path, `"`)
}

func isRPCGenericToolDetailEnabled(name string) bool {
	nameValue6 := str.String(name)
	switch nameValue6.Normalized() {
	case "list_files":
		return true
	default:
		return false
	}
}

func getRPCGenericToolDetail(name string, inputFields map[string]any) string {
	nameValue7 := str.String(name)
	name = nameValue7.Trim()
	if name == "" || len(inputFields) == 0 {
		return ""
	}

	keys := make([]string, 0, len(inputFields))
	for key, value := range inputFields {
		keyValue := str.String(key)
		if keyValue.Trim() == "" || isRPCEmptyToolInputValue(value) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		keyValue2 := str.String(key)
		parts = append(parts, keyValue2.Trim()+"="+formatRPCGenericToolInputValue(key, inputFields[key]))
	}

	return name + "(" + strings.Join(parts, " ") + ")"
}

func isRPCEmptyToolInputValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		typedValue := str.String(typed)
		return typedValue.Trim() == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func formatOptionalRPCToolNumber(value any) string {
	switch typed := value.(type) {
	case float64:
		if typed <= 0 {
			return ""
		}
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", typed), "0"), ".")
	case int:
		if typed <= 0 {
			return ""
		}
		return fmt.Sprintf("%d", typed)
	default:
		return ""
	}
}

func formatRPCGenericToolInputValue(key string, value any) string {
	switch typed := value.(type) {
	case string:
		sanitized, _ := guardrails.NewRedactor().Sanitize(typed).(string)
		keyValue3 := str.String(key)
		if strings.EqualFold(keyValue3.Trim(), "path") {
			return shortenRPCTraceToolPath(sanitized, 42)
		}
		return truncateRPCTraceToolDetail(sanitized, 60)
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", typed), "0"), ".")
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return truncateRPCTraceToolDetail(fmt.Sprintf("%v", typed), 60)
		}
		return truncateRPCTraceToolDetail(string(data), 60)
	}
}

func getRPCMapString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := fields[key].(string)
		value2 := str.String(value)
		if value = value2.Trim(); value != "" {
			return value
		}
	}

	return ""
}

func getRPCStringSlice(raw any) []string {
	values, ok := raw.([]any)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		textValue2 := str.String(text)
		if text = textValue2.Trim(); text != "" {
			result = append(result, text)
		}
	}

	return result
}

func getRPCToolActionName(name string) string {
	nameValue8 := str.String(name)
	normalized := nameValue8.Normalized()
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "read", "read_file", "view_file", "open_file", "cat":
		return "Read"
	case "write", "write_file", "edit_file", "create_file":
		return "Write"
	case "patch", "apply_patch":
		return "Patch"
	case "process":
		return "Process"
	case "exec", "exec_command", "run", "run_command", "shell", "bash":
		return "Run"
	case "web_search", "search_web", "search", "web":
		return "Web Search"
	case "search_files":
		return "Search Files"
	case "memory_search", "search_memory", "memory":
		return "Memory Search"
	case "memory_extract", "extract_memory":
		return "Memory Extract"
	case "memory_add", "add_memory":
		return "Memory Add"
	case "memory_update", "update_memory":
		return "Memory Update"
	case "memory_delete", "delete_memory":
		return "Memory Delete"
	case "plan", "plan_tool", "update_plan":
		return "Plan"
	default:
		return humanizeRPCToolActionName(name)
	}
}

func shellQuoteRPCCommandPart(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\"'\\$&|;()<>*?![]{}") {
		return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	}

	return value
}

func appendRPCToolTimeout(command string, raw any) string {
	timeout, ok := raw.(float64)
	if !ok || timeout <= 0 {
		return command
	}

	return command + " [timeout " + strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", timeout), "0"), ".") + "s]"
}

func shortenRPCTraceToolPath(path string, limit int) string {
	pathValue2 := str.String(path)
	path = strings.Join(strings.Fields(pathValue2.Trim()), " ")
	if limit <= 0 {
		return path
	}

	runes := []rune(path)
	if len(runes) <= limit {
		return path
	}
	if limit <= 5 {
		return string(runes[:limit])
	}

	separator := "/"
	if strings.Contains(path, "\\") && !strings.Contains(path, "/") {
		separator = "\\"
	}
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	tail := ""
	if len(parts) > 0 {
		tail = parts[len(parts)-1]
	}
	if tail == "" {
		return truncateRPCTraceToolDetail(path, limit)
	}

	tailRunes := []rune(tail)
	if len(tailRunes)+5 >= limit {
		return "..." + separator + string(tailRunes[max(len(tailRunes)-(limit-4), 0):])
	}

	prefixLimit := limit - len(tailRunes) - 4
	prefix := string(runes[:max(prefixLimit, 1)])

	return strings.TrimRight(prefix, `/\`) + separator + "..." + separator + tail
}

func humanizeRPCToolActionName(name string) string {
	nameValue9 := str.String(name)
	parts := strings.FieldsFunc(nameValue9.Trim(), func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	for index, part := range parts {
		runes := []rune(strings.ToLower(part))
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		parts[index] = string(runes)
	}

	return strings.Join(parts, " ")
}

func truncateRPCTraceToolDetail(value string, limit int) string {
	value3 := str.String(value)
	value = strings.Join(strings.Fields(value3.Trim()), " ")
	if limit <= 0 {
		return value
	}

	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}

	return string(runes[:limit-3]) + "..."
}

func getRPCSafetyFindingSummaries(findings []map[string]string) []rpcSafetyFindingSummary {
	summaries := make([]rpcSafetyFindingSummary, 0, len(findings))
	for _, finding := range findings {
		findingValue := str.String(finding["id"])
		findingValue2 := str.String(finding["category"])
		findingValue3 := str.String(finding["severity"])
		summary := rpcSafetyFindingSummary{
			ID:       findingValue.Trim(),
			Category: findingValue2.Trim(),
			Severity: findingValue3.Trim(),
		}
		if summary.ID != "" || summary.Category != "" || summary.Severity != "" {
			summaries = append(summaries, summary)
		}
	}

	return summaries
}

func getRPCPlanSummary(summary trace.PlanSummaryPayload) *trace.PlanSummaryPayload {
	if summary.Total == 0 &&
		summary.Pending == 0 &&
		summary.InProgress == 0 &&
		summary.Completed == 0 &&
		summary.Cancelled == 0 {
		return nil
	}

	return &summary
}

func getRPCPlanSteps(steps []trace.PlanStepPayload) []trace.PlanStepPayload {
	if len(steps) == 0 {
		return nil
	}

	result := make([]trace.PlanStepPayload, 0, len(steps))
	for _, step := range steps {
		iDValue2 := str.String(step.ID)
		contentValue := str.String(step.Content)
		statusValue2 := str.String(step.Status)
		item := trace.PlanStepPayload{
			ID:      iDValue2.Trim(),
			Content: contentValue.Trim(),
			Status:  statusValue2.Trim(),
		}
		if item.ID != "" || item.Content != "" || item.Status != "" {
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func (s *Service) Create(ctx context.Context, req *morphpb.CreateSessionRequest) (*morphpb.CreateSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "create session request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionCreate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}

	session, err := s.createSession(ctx, req)
	if err != nil {
		return nil, getGRPCError(err)
	}
	if isCreateSessionAutoSwitchEnabled(req) {
		if err := s.api.UseSession(ctx, session.ID); err != nil {
			return nil, getGRPCError(err)
		}
	}

	return &morphpb.CreateSessionResponse{Session: sessionToProtoSummary(session)}, nil
}

func (s *Service) createSession(
	ctx context.Context,
	req *morphpb.CreateSessionRequest,
) (storage.Session, error) {
	originSource := str.String(req.GetOriginSource())
	source := originSource.Trim()
	if source == "" {
		return s.api.CreateSession(ctx, req.GetId())
	}
	return s.api.CreateSession(ctx, req.GetId(), storage.SessionCreateOptions{
		Origin: storage.SessionOrigin{Source: source},
	})
}

func isCreateSessionAutoSwitchEnabled(req *morphpb.CreateSessionRequest) bool {
	if req == nil {
		return false
	}
	if req.AutoSwitch == nil {
		return true
	}

	return req.GetAutoSwitch()
}

func (s *Service) List(ctx context.Context, req *morphpb.ListSessionsRequest) (*morphpb.ListSessionsResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "list sessions request is required")
	}

	sessions, err := s.api.ListSessions(ctx, storage.SessionListOptions{
		Archived:     req.Archived,
		OriginSource: req.GetOriginSource(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}

	items := make([]*morphpb.SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, sessionToProtoSummary(session))
	}

	return &morphpb.ListSessionsResponse{Sessions: items}, nil
}

func (s *Service) ListProviders(ctx context.Context, req *morphpb.ListProvidersRequest) (*morphpb.ListProvidersResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "list providers request is required")
	}

	list, err := s.api.ListProviders(ctx)
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.ListProvidersResponse{Providers: providerOptionsToProto(list.Providers)}, nil
}

func (s *Service) RuntimeModel(
	context.Context,
	*morphpb.RuntimeModelRequest,
) (*morphpb.RuntimeModelResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}

	return modelRuntimeToProto(s.runtimeModel), nil
}

func (s *Service) ListModels(ctx context.Context, req *morphpb.ListModelsRequest) (*morphpb.ListModelsResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "list models request is required")
	}

	list, err := s.api.ListModels(ctx, morphagent.ModelListOptions{Provider: req.GetProvider()})
	if err != nil {
		return nil, getGRPCError(err)
	}
	providerValue2 := str.String(list.Provider)
	authTypeValue := str.String(list.AuthType)
	return &morphpb.ListModelsResponse{
		Provider: providerValue2.Trim(),
		AuthType: authTypeValue.Trim(),
		Models:   modelOptionsToProto(list.Models),
	}, nil
}

func (s *Service) SelectModel(ctx context.Context, req *morphpb.SelectModelRequest) (*morphpb.SelectModelResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "select model request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource:      permissions.ResourceModel,
		Action:        permissions.ActionUpdate,
		Effects:       []permissions.Effect{permissions.EffectWrite},
		Target:        req.GetProvider() + "/" + req.GetId(),
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}

	model, err := s.api.SelectModel(ctx, req.GetId(), morphagent.ModelSelectOptions{Provider: req.GetProvider()})
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.SelectModelResponse{Model: modelOptionToProto(model)}, nil
}

func (s *Service) SetProviderAPIKey(
	ctx context.Context,
	req *morphpb.SetProviderAPIKeyRequest,
) (*morphpb.SetProviderAPIKeyResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "set provider API key request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceConfiguration,
		Action:   permissions.ActionUpdate,
		Effects: []permissions.Effect{
			permissions.EffectCredentialBearing,
			permissions.EffectWrite,
		},
		Target:        req.GetProvider(),
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}

	if err := s.api.SetProviderAPIKey(ctx, req.GetProvider(), req.GetApiKey()); err != nil {
		return nil, getGRPCError(err)
	}

	providerValue3 := str.String(req.GetProvider())
	return &morphpb.SetProviderAPIKeyResponse{Provider: providerValue3.Trim()}, nil
}

func (s *Service) Use(ctx context.Context, req *morphpb.UseSessionRequest) (*morphpb.UseSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "use session request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionUpdate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}

	if err := s.api.UseSession(ctx, req.GetId()); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.UseSessionResponse{Id: req.GetId()}, nil
}

func (s *Service) Archive(ctx context.Context, req *morphpb.ArchiveSessionRequest) (*morphpb.ArchiveSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "archive session request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionDelete,
		Effects:  []permissions.Effect{permissions.EffectDestructive, permissions.EffectWrite},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}

	if err := s.api.ArchiveSession(ctx, req.GetId()); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.ArchiveSessionResponse{Id: req.GetId()}, nil
}

func (s *Service) Unarchive(
	ctx context.Context,
	req *morphpb.UnarchiveSessionRequest,
) (*morphpb.UnarchiveSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "unarchive session request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionUpdate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}

	session, err := s.api.UnarchiveSession(ctx, req.GetId())
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.UnarchiveSessionResponse{Session: sessionToProtoSummary(session)}, nil
}

func (s *Service) Rename(ctx context.Context, req *morphpb.RenameSessionRequest) (*morphpb.RenameSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "rename session request is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionUpdate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}

	session, err := s.api.RenameSession(ctx, req.GetId(), req.GetTitle())
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.RenameSessionResponse{Session: sessionToProtoSummary(session)}, nil
}

func (s *Service) Current(ctx context.Context, req *morphpb.CurrentSessionRequest) (*morphpb.CurrentSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "current session request is required")
	}

	session, err := s.api.CurrentSession(ctx)
	if err != nil {
		return nil, getGRPCError(err)
	}

	response := &morphpb.CurrentSessionResponse{
		Id:          session.ID,
		Title:       session.Title,
		TitleSource: session.TitleSource,
	}

	return response, nil
}

func (s *Service) Compact(
	ctx context.Context,
	req *morphpb.CompactSessionRequest,
) (*morphpb.CompactSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "compact session request is required")
	}

	result, err := s.api.CompactSession(ctx, req.GetId())
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.CompactSessionResponse{
		Id:                   result.SessionID,
		SourceEndOffset:      int32(result.SourceEndOffset),
		SourceMessageCount:   int32(result.SourceMessageCount),
		UpdatedAt:            timestamppb.New(result.UpdatedAt),
		CurrentContextLength: int32(result.CurrentContextLength),
		TotalContextLength:   int32(result.TotalContextLength),
	}, nil
}

func (s *Service) Repair(
	ctx context.Context,
	req *morphpb.RepairSessionRequest,
) (*morphpb.RepairSessionResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "repair session request is required")
	}
	if req.GetType() != morphpb.RepairSessionRequest_VECTOR {
		return nil, status.Error(codes.InvalidArgument, "repair session type must be vector")
	}
	if req.GetVector() == nil {
		return nil, status.Error(codes.InvalidArgument, "repair session vector options are required")
	}

	result, err := s.api.RepairSession(ctx, search.VectorRepairOptions{
		SessionID: req.GetVector().GetId(),
		Full:      req.GetVector().GetFull(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.RepairSessionResponse{
		Type: morphpb.RepairSessionRequest_VECTOR,
		Vector: &morphpb.VectorRepairResponse{
			SessionsScanned:    int32(result.SessionsScanned),
			MessagesScanned:    int32(result.MessagesScanned),
			RowsScanned:        int32(result.RowsScanned),
			MissingRows:        int32(result.MissingRows),
			StaleRows:          int32(result.StaleRows),
			UnchangedRows:      int32(result.UnchangedRows),
			RebuiltRows:        int32(result.RebuiltRows),
			DeletedSources:     int32(result.DeletedSources),
			Batches:            int32(result.Batches),
			AttemptedSources:   int32(result.AttemptedSources),
			RecoveredSources:   int32(result.RecoveredSources),
			StillFailedSources: int32(result.StillFailedSources),
		},
	}, nil
}

func (s *Service) Status(ctx context.Context, req *morphpb.GetSessionStatusRequest) (*morphpb.GetSessionStatusResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "get session status request is required")
	}
	if req.GetContext() == nil {
		return nil, status.Error(codes.InvalidArgument, "get session status request context is required")
	}

	result, err := s.api.ContextStatus(ctx, req.GetContext().GetId())
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.GetSessionStatusResponse{
		Id:               result.SessionID,
		Size:             int32(result.Size),
		CreatedAt:        timestamppb.New(result.CreatedAt),
		UpdatedAt:        timestamppb.New(result.UpdatedAt),
		CompactionStatus: result.CompactionStatus,
		Context: &morphpb.GetSessionStatusResponse_Context{
			Offset:       int32(result.Offset),
			Length:       int32(result.Length),
			Used:         int32(result.Used),
			Remaining:    int32(result.Remaining),
			UsedPct:      result.UsedPct,
			RemainingPct: result.RemainingPct,
		},
	}, nil
}

func (s *Service) ListPairings(
	ctx context.Context,
	req *morphpb.ListGatewayPairingsRequest,
) (*morphpb.ListGatewayPairingsResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}

	store, ok := s.api.(pairing.Store)
	if !ok {
		return nil, status.Error(codes.Internal, "gateway pairing store is required")
	}

	source := ""
	if req != nil {
		sourceValue2 := str.String(req.GetSource())
		source = sourceValue2.Trim()
	}
	pending, err := store.ListGatewayPairingRequests(ctx, source)
	if err != nil {
		return nil, getGRPCError(err)
	}
	approved, err := store.ListGatewayPairedSenders(ctx, source)
	if err != nil {
		return nil, getGRPCError(err)
	}

	resp := &morphpb.ListGatewayPairingsResponse{}
	for _, request := range pending {
		resp.Pending = append(resp.Pending, gatewayPairingRequestToProto(request))
	}
	for _, sender := range approved {
		resp.Approved = append(resp.Approved, gatewayPairedSenderToProto(sender))
	}

	return resp, nil
}

func (s *Service) GatewayStatus(
	context.Context,
	*morphpb.GetGatewayStatusRequest,
) (*morphpb.GetGatewayStatusResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}

	status := gateway.Status{
		State:        gateway.StateDisabled,
		Address:      s.gatewayConfig.Address,
		Port:         s.gatewayConfig.Port,
		SlackMode:    s.gatewayConfig.Slack.Mode,
		TelegramMode: s.gatewayConfig.Telegram.Mode,
	}
	if s.gatewayConfig.Enabled {
		status.State = gateway.StateStopped
	}
	if s.gatewayRuntime != nil {
		status = s.gatewayRuntime.Status()
	}

	return &morphpb.GetGatewayStatusResponse{Status: gatewayStatusToProto(status)}, nil
}

func (s *Service) Start(
	ctx context.Context,
	_ *morphpb.StartGatewayRequest,
) (*morphpb.StartGatewayResponse, error) {
	if err := s.checkGatewayRuntimeReady(); err != nil {
		return nil, err
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource:      permissions.ResourceGateway,
		Action:        permissions.ActionStart,
		Effects:       []permissions.Effect{permissions.EffectNetwork, permissions.EffectWrite},
		Target:        "gateway",
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}

	cfg, err := normalizeGatewayRuntimeConfig(s.gatewayConfig)
	if err != nil {
		return nil, err
	}
	if err := s.gatewayRuntime.Start(context.Background(), cfg, s.api); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.StartGatewayResponse{Status: gatewayStatusToProto(s.gatewayRuntime.Status())}, nil
}

func (s *Service) Stop(
	ctx context.Context,
	_ *morphpb.StopGatewayRequest,
) (*morphpb.StopGatewayResponse, error) {
	if err := s.checkGatewayRuntimeReady(); err != nil {
		return nil, err
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource:      permissions.ResourceGateway,
		Action:        permissions.ActionStop,
		Effects:       []permissions.Effect{permissions.EffectNetwork, permissions.EffectWrite},
		Target:        "gateway",
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}
	if err := s.gatewayRuntime.Stop(ctx); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.StopGatewayResponse{Status: gatewayStatusToProto(s.gatewayRuntime.Status())}, nil
}

func (s *Service) Restart(
	ctx context.Context,
	_ *morphpb.RestartGatewayRequest,
) (*morphpb.RestartGatewayResponse, error) {
	if err := s.checkGatewayRuntimeReady(); err != nil {
		return nil, err
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource:      permissions.ResourceGateway,
		Action:        permissions.ActionManage,
		Effects:       []permissions.Effect{permissions.EffectNetwork, permissions.EffectWrite},
		Target:        "gateway",
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}

	cfg, err := normalizeGatewayRuntimeConfig(s.gatewayConfig)
	if err != nil {
		return nil, err
	}
	if err := s.gatewayRuntime.Stop(ctx); err != nil {
		return nil, getGRPCError(err)
	}
	if err := s.gatewayRuntime.Start(context.Background(), cfg, s.api); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.RestartGatewayResponse{Status: gatewayStatusToProto(s.gatewayRuntime.Status())}, nil
}

func (s *Service) checkGatewayRuntimeReady() error {
	if s == nil {
		return status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return status.Error(codes.Internal, "agent handler is required")
	}
	if s.gatewayRuntime == nil {
		return status.Error(codes.Internal, "gateway runtime is required")
	}

	return nil
}

func normalizeGatewayRuntimeConfig(cfg config.GatewayConfig) (config.GatewayConfig, error) {
	if !cfg.Enabled {
		return config.GatewayConfig{}, status.Error(codes.FailedPrecondition, "gateway is disabled")
	}

	full := config.NewDefaultConfig()
	full.Gateway = cfg
	if err := full.ValidateGateway(); err != nil {
		return config.GatewayConfig{}, getGRPCError(err)
	}

	return full.Gateway, nil
}

func (s *Service) ApprovePairing(
	ctx context.Context,
	req *morphpb.ApproveGatewayPairingRequest,
) (*morphpb.ApproveGatewayPairingResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "approve pairing request is required")
	}

	store, ok := s.api.(pairing.Store)
	if !ok {
		return nil, status.Error(codes.Internal, "gateway pairing store is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceGateway,
		Action:   permissions.ActionUpdate,
		Effects: []permissions.Effect{
			permissions.EffectPrivilegeChanging,
			permissions.EffectWrite,
		},
		Target:        req.GetSource(),
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}

	sender, ok, err := pairing.NewManager(pairing.Options{
		Store:  store,
		Secret: s.gatewayPairingSecret,
	}).Approve(ctx, req.GetSource(), req.GetCode())
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.ApproveGatewayPairingResponse{
		Approved: ok,
		Sender:   gatewayPairedSenderToProto(sender),
	}, nil
}

func (s *Service) RevokePairing(
	ctx context.Context,
	req *morphpb.RevokeGatewayPairingRequest,
) (*morphpb.RevokeGatewayPairingResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "revoke pairing request is required")
	}

	store, ok := s.api.(pairing.Store)
	if !ok {
		return nil, status.Error(codes.Internal, "gateway pairing store is required")
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceGateway,
		Action:   permissions.ActionDelete,
		Effects: []permissions.Effect{
			permissions.EffectDestructive,
			permissions.EffectPrivilegeChanging,
			permissions.EffectWrite,
		},
		Target:        req.GetSource() + "/" + req.GetSenderId(),
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}
	if err := store.DeleteGatewayPairedSender(ctx, req.GetSource(), req.GetSenderId()); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.RevokeGatewayPairingResponse{}, nil
}

func (s *Service) ClearPendingPairings(
	ctx context.Context,
	req *morphpb.ClearPendingGatewayPairingsRequest,
) (*morphpb.ClearPendingGatewayPairingsResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}

	store, ok := s.api.(pairing.Store)
	if !ok {
		return nil, status.Error(codes.Internal, "gateway pairing store is required")
	}

	source := ""
	if req != nil {
		source = req.GetSource()
	}
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceGateway,
		Action:   permissions.ActionDelete,
		Effects: []permissions.Effect{
			permissions.EffectDestructive,
			permissions.EffectPrivilegeChanging,
			permissions.EffectWrite,
		},
		Target:        source,
		OwnerRequired: true,
	}); err != nil {
		return nil, err
	}
	if err := store.ClearGatewayPairingRequests(ctx, source); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.ClearPendingGatewayPairingsResponse{}, nil
}

func gatewayPairingRequestToProto(request pairing.PendingRequest) *morphpb.GatewayPairingRequest {
	return &morphpb.GatewayPairingRequest{
		Source:      request.Source,
		SenderId:    request.SenderID,
		DisplayName: request.DisplayName,
		CreatedAt:   timestampOrNil(request.CreatedAt),
		LastSeenAt:  timestampOrNil(request.LastSeenAt),
		ExpiresAt:   timestampOrNil(request.ExpiresAt),
	}
}

func gatewayPairedSenderToProto(sender pairing.ApprovedSender) *morphpb.GatewayPairedSender {
	return &morphpb.GatewayPairedSender{
		Source:      sender.Source,
		SenderId:    sender.SenderID,
		DisplayName: sender.DisplayName,
		CreatedAt:   timestampOrNil(sender.CreatedAt),
		UpdatedAt:   timestampOrNil(sender.UpdatedAt),
	}
}

func gatewayStatusToProto(status gateway.Status) *morphpb.GatewayStatus {
	addressValue := str.String(status.Address)
	slackModeValue := str.String(status.SlackMode)
	telegramModeValue := str.String(status.TelegramMode)
	lastErrorValue := str.String(status.LastError)
	return &morphpb.GatewayStatus{
		State:        string(status.State),
		Address:      addressValue.Trim(),
		Port:         int32(status.Port),
		SlackMode:    slackModeValue.Trim(),
		TelegramMode: telegramModeValue.Trim(),
		LastError:    lastErrorValue.Trim(),
	}
}

func timestampOrNil(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}

	return timestamppb.New(value.UTC())
}

func (s *Service) Timeline(
	ctx context.Context,
	req *morphpb.GetSessionTimelineRequest,
) (*morphpb.GetSessionTimelineResponse, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "get session timeline request is required")
	}

	result, err := s.api.GetSessionTimeline(ctx, morphagent.SessionTimelineOptions{
		SessionID:     req.GetId(),
		MessageOffset: int(req.GetMessageOffset()),
		MessageLimit:  int(req.GetMessageLimit()),
		TraceOffset:   int(req.GetTraceOffset()),
		TraceLimit:    int(req.GetTraceLimit()),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}

	return sessionTimelineToProtoResponse(result), nil
}

func getGRPCError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := status.FromError(err); ok {
		return err
	}

	message := err.Error()
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, message)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, message)
	case strings.HasSuffix(message, "is required"),
		strings.Contains(message, "is required when"),
		strings.Contains(message, "API key is required"),
		strings.Contains(message, "must be a valid"),
		strings.Contains(message, "must be greater than or equal to"),
		strings.Contains(message, "cannot be deleted"),
		strings.Contains(message, "cannot be archived"):
		return status.Error(codes.InvalidArgument, message)
	case strings.Contains(message, "is archived"),
		strings.Contains(message, "is not archived"):
		return status.Error(codes.FailedPrecondition, message)
	case strings.HasSuffix(message, "not found"):
		return status.Error(codes.NotFound, message)
	case strings.HasSuffix(message, "already exists"):
		return status.Error(codes.AlreadyExists, message)
	default:
		return status.Error(codes.Internal, message)
	}
}

func sessionToProtoSummary(session storage.Session) *morphpb.SessionSummary {
	return &morphpb.SessionSummary{
		Id:            session.ID,
		OriginSource:  session.Origin.Source,
		Title:         session.Title,
		TitleSource:   session.TitleSource,
		UpdatedAtUnix: session.UpdatedAt.Unix(),
	}
}

func providerOptionsToProto(options []models.ProviderOption) []*morphpb.ProviderOption {
	items := make([]*morphpb.ProviderOption, 0, len(options))
	for _, option := range options {
		items = append(items, providerOptionToProto(option))
	}

	return items
}

func providerOptionToProto(option models.ProviderOption) *morphpb.ProviderOption {
	return &morphpb.ProviderOption{
		Id:             option.ID,
		Name:           option.Name,
		Type:           option.Type,
		ModelCount:     int32(option.ModelCount),
		SupportsApiKey: option.SupportsAPIKey,
		SupportsOauth:  option.SupportsOAuth,
		AuthType:       option.AuthType,
		Current:        option.Current,
	}
}

func modelOptionsToProto(options []models.Option) []*morphpb.ModelOption {
	items := make([]*morphpb.ModelOption, 0, len(options))
	for _, option := range options {
		items = append(items, modelOptionToProto(option))
	}

	return items
}

func modelOptionToProto(option models.Option) *morphpb.ModelOption {
	return &morphpb.ModelOption{
		Id:            option.ID,
		Name:          option.Name,
		Provider:      option.Provider,
		Api:           option.API,
		ContextWindow: int32(option.ContextWindow),
		MaxTokens:     int32(option.MaxTokens),
		Input:         append([]string(nil), option.Input...),
		Reasoning:     option.Reasoning,
		SupportsOauth: option.SupportsOAuth,
		Current:       option.Current,
	}
}

func modelRuntimeToProto(runtime ModelRuntime) *morphpb.RuntimeModelResponse {
	runtime = normalizeModelRuntime(runtime)

	return &morphpb.RuntimeModelResponse{
		Provider:      runtime.Provider,
		Api:           runtime.API,
		Model:         runtime.Model,
		BaseUrl:       runtime.BaseURL,
		ContextLength: int32(runtime.ContextLength),
	}
}

func sessionTimelineToProtoResponse(timeline morphagent.SessionTimeline) *morphpb.GetSessionTimelineResponse {
	response := &morphpb.GetSessionTimelineResponse{
		Id:                    timeline.SessionID,
		Title:                 timeline.Title,
		TitleSource:           timeline.TitleSource,
		MessagesHasMore:       timeline.MessagesHasMore,
		TracesHasMore:         timeline.TracesHasMore,
		TracesTruncatedBefore: timeline.TracesTruncatedBefore,
		Messages:              make([]*morphpb.SessionTimelineMessage, 0, len(timeline.Messages)),
		TraceEvents:           make([]*morphpb.SessionTimelineTraceEvent, 0, len(timeline.TraceEvents)),
	}
	for _, message := range timeline.Messages {
		response.Messages = append(response.Messages, timelineMessageToProto(message))
	}
	for _, event := range timeline.TraceEvents {
		if protoEvent, ok := timelineTraceEventToProto(event.Event); ok {
			response.TraceEvents = append(response.TraceEvents, protoEvent)
		}
	}
	if len(response.TraceEvents) > 0 {
		response.FirstTraceSequence = response.TraceEvents[0].GetSequence()
		response.LastTraceSequence = response.TraceEvents[len(response.TraceEvents)-1].GetSequence()
	}

	return response
}

func timelineMessageToProto(record morphagent.SessionTimelineMessage) *morphpb.SessionTimelineMessage {
	message := record.Message
	protoMessage := &morphpb.SessionTimelineMessage{
		Offset:     int32(record.Offset),
		Id:         uint64(message.ID),
		Role:       string(message.Role),
		Name:       message.Name,
		ToolCallId: message.ToolCallID,
		Content:    message.Content,
		CreatedAt:  timestamppb.New(message.CreatedAt),
		ToolCalls:  make([]*morphpb.SessionTimelineToolCall, 0, len(message.ToolCalls)),
	}
	for _, toolCall := range message.ToolCalls {
		iDValue3 := str.String(toolCall.ID)
		nameValue10 := str.String(toolCall.Name)
		inputValue2 := str.String(toolCall.Input)
		protoMessage.ToolCalls = append(protoMessage.ToolCalls, &morphpb.SessionTimelineToolCall{
			Id:    iDValue3.Trim(),
			Name:  nameValue10.Trim(),
			Input: inputValue2.Trim(),
		})
	}

	return protoMessage
}

func timelineTraceEventToProto(event agentsession.TraceEvent) (*morphpb.SessionTimelineTraceEvent, bool) {
	payload, ok := getRPCTracePayload(event.Type, event.Payload)
	if !ok {
		return nil, false
	}

	payloadJSON, err := marshalRPCJSON(payload)
	if err != nil {
		return nil, false
	}
	trimmedValueValue2 := str.String(event.Type)
	return &morphpb.SessionTimelineTraceEvent{
		Id:          uint64(event.ID),
		Sequence:    int32(event.Sequence),
		Type:        trimmedValueValue2.Trim(),
		Timestamp:   timestamppb.New(event.Timestamp),
		PayloadJson: string(payloadJSON),
	}, true
}
