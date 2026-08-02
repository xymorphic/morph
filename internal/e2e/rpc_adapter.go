package e2e

import (
	"context"
	"errors"
	"fmt"

	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/trace"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/nanoid"
)

type rpcClientAPI interface {
	rpcclient.ChatAPI
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

	sessionID := req.SessionID
	if sessionID == "" {
		session, err := client.SessionAPI().Current(normalizeHarnessContext(ctx))
		if err != nil {
			return RootChatResult{}, err
		}
		sessionID = session.ID
	}
	var events []Event
	reply, err := runRPCSessionTurn(
		normalizeHarnessContext(ctx),
		client,
		sessionID,
		req.Message,
		req.Instruct,
		req.Stream,
		func(event rpcclient.Event) error {
			if event.TraceEvent != nil {
				return nil
			}
			events = append(events, Event{Channel: event.Channel, Text: event.Text})
			return nil
		},
	)
	if err != nil {
		return RootChatResult{}, err
	}

	return RootChatResult{
		Reply:     reply,
		SessionID: sessionID,
		Events:    events,
	}, nil
}

var errRPCSessionTurnComplete = errors.New("RPC session turn complete")

func runRPCSessionTurn(
	ctx context.Context,
	client rpcClientAPI,
	sessionID string,
	content string,
	instruct string,
	stream *bool,
	onProgress func(rpcclient.Event) error,
) (string, error) {
	submissionID, err := nanoid.Generate("sub_")
	if err != nil {
		return "", err
	}
	options := rpcclient.EnqueueMessageOptions{
		SessionID:          sessionID,
		Message:            content,
		ClientSubmissionID: submissionID,
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
		Stream:             stream,
	}
	options.Instruct = instruct
	entry, err := client.EnqueueMessage(ctx, options)
	if err != nil {
		return "", err
	}
	terminalEntry := entry
	terminal := isTerminalRPCQueueStatus(entry.Status)
	if terminal {
		if err := rpcQueueTerminalError(entry); err != nil {
			return "", err
		}
	}
	state, err := client.State(ctx, sessionID)
	if err != nil {
		return "", err
	}
	var progressSequence int64
	if onProgress != nil {
		for _, progress := range state.Progress {
			if progress.QueueEntryID != entry.ID {
				continue
			}
			progressSequence = max(progressSequence, progress.Sequence)
			if err := onProgress(rpcProgressToAgentEvent(progress)); err != nil {
				return "", err
			}
		}
	}
	if stateTerminalEntry, stateTerminal := getTerminalRPCQueueEntry(state.Queue, entry.ID); stateTerminal {
		terminalEntry = stateTerminalEntry
		terminal = true
		if err := rpcQueueTerminalError(stateTerminalEntry); err != nil {
			return "", err
		}
	}
	if !terminal {
		err = client.Observe(ctx, sessionID, state.Cursor, func(event rpcclient.SessionEvent) error {
			if event.Progress != nil &&
				event.Progress.QueueEntryID == entry.ID &&
				event.Progress.Sequence > progressSequence &&
				onProgress != nil {
				progressSequence = event.Progress.Sequence
				return onProgress(rpcProgressToAgentEvent(*event.Progress))
			}
			if event.Queue != nil && event.Queue.ID == entry.ID &&
				isTerminalRPCQueueStatus(event.Queue.Status) {
				if err := rpcQueueTerminalError(*event.Queue); err != nil {
					return err
				}
				return errRPCSessionTurnComplete
			}
			if event.Run != nil && event.Run.QueueEntryID == entry.ID &&
				event.Run.Status != agentsession.RunStatusRunning {
				if err := rpcRunTerminalError(*event.Run); err != nil {
					return err
				}
				return errRPCSessionTurnComplete
			}
			return nil
		})
		if err != nil && !errors.Is(err, errRPCSessionTurnComplete) {
			return "", err
		}
	}
	completedEntry := terminalEntry
	finalState, err := client.State(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, queued := range finalState.Queue {
		if queued.ID == entry.ID {
			completedEntry = queued
			break
		}
	}
	timeline, err := client.SessionAPI().Timeline(ctx, rpcclient.SessionTimelineOptions{
		SessionID: sessionID,
	})
	if err != nil {
		return "", err
	}
	for index := len(timeline.Messages) - 1; index >= 0; index-- {
		message := timeline.Messages[index].Message
		if message.Role != morphmsg.RoleAssistant || message.Content == "" {
			continue
		}
		if !completedEntry.StartedAt.IsZero() && message.CreatedAt.Before(completedEntry.StartedAt) {
			continue
		}
		if !completedEntry.CompletedAt.IsZero() && message.CreatedAt.After(completedEntry.CompletedAt) {
			continue
		}
		return message.Content, nil
	}
	return "", errors.New("session run completed without an assistant response")
}

func rpcProgressToAgentEvent(progress agentsession.ProgressEvent) agentcore.Event {
	event := agentcore.Event{
		Kind:    progress.Kind,
		Channel: progress.Channel,
		Text:    progress.Text,
	}
	if progress.TraceEvent != nil {
		event.TraceEvent = &trace.Event{
			SessionID: progress.TraceEvent.SessionID,
			Type:      progress.TraceEvent.Type,
			Timestamp: progress.TraceEvent.Timestamp,
			Payload:   progress.TraceEvent.Payload,
		}
	}
	return event
}

func getTerminalRPCQueueEntry(
	entries []rpcclient.SessionQueueEntry,
	entryID string,
) (rpcclient.SessionQueueEntry, bool) {
	for _, entry := range entries {
		if entry.ID == entryID {
			return entry, isTerminalRPCQueueStatus(entry.Status)
		}
	}
	return rpcclient.SessionQueueEntry{}, false
}

func isTerminalRPCQueueStatus(status agentsession.QueueStatus) bool {
	switch status {
	case agentsession.QueueStatusDelivered,
		agentsession.QueueStatusCompleted,
		agentsession.QueueStatusInterrupted,
		agentsession.QueueStatusFailed,
		agentsession.QueueStatusCancelled:
		return true
	default:
		return false
	}
}

func rpcQueueTerminalError(entry rpcclient.SessionQueueEntry) error {
	switch entry.Status {
	case agentsession.QueueStatusInterrupted:
		if entry.LastError != "" {
			return fmt.Errorf("session run interrupted: %s", entry.LastError)
		}
		return errors.New("session run interrupted")
	case agentsession.QueueStatusFailed:
		if entry.LastError != "" {
			return errors.New(entry.LastError)
		}
		return errors.New("session run failed")
	case agentsession.QueueStatusCancelled:
		return errors.New("session run cancelled")
	default:
		return nil
	}
}

func rpcRunTerminalError(run rpcclient.SessionActiveRun) error {
	switch run.Status {
	case agentsession.RunStatusFailed:
		if run.LastError != "" {
			return errors.New(run.LastError)
		}
		return errors.New("session run failed")
	case agentsession.RunStatusInterrupted:
		if run.Reason != "" {
			return fmt.Errorf("session run interrupted: %s", run.Reason)
		}
		return errors.New("session run interrupted")
	case agentsession.RunStatusCancelled:
		return errors.New("session run cancelled")
	default:
		return nil
	}
}
