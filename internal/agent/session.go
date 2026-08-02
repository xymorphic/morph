package agent

import (
	"context"
	"errors"

	agentsession "github.com/xymorphic/morph/pkg/agent/session"

	storage "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

var errSessionManagerRequired = errors.New("session manager is required")

// SessionManager is the durable session surface the agent adapts into the core
// agent session interfaces.
type SessionManager interface {
	Resolve(context.Context, string) (storage.Session, error)
	GetMessages(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error)
	AppendMessages(context.Context, string, []morphmsg.Message) error
	UpdateLastPromptTokens(context.Context, string, int) error
	AppendTraceEvent(context.Context, storage.TraceEvent) (storage.TraceEvent, error)
}

// SessionStore adapts the agent state manager to pkg/agent session and trace
// interfaces.
type SessionStore struct {
	manager SessionManager
}

// NewSessionStore returns an adapter around manager for the core agent loop.
func NewSessionStore(manager SessionManager) *SessionStore {
	return &SessionStore{manager: manager}
}

// Resolve loads and converts a durable session into the core agent session shape.
func (s *SessionStore) Resolve(ctx context.Context, id string) (agentsession.Session, error) {
	if s == nil || s.manager == nil {
		return agentsession.Session{}, errSessionManagerRequired
	}

	resolved, err := s.manager.Resolve(ctx, id)
	if err != nil {
		return agentsession.Session{}, err
	}

	return agentSessionFromStorageSession(resolved), nil
}

// GetMessages loads stored messages using the core agent query shape.
func (s *SessionStore) GetMessages(
	ctx context.Context,
	id string,
	query agentsession.MessageQuery,
) ([]morphmsg.Message, error) {
	if s == nil || s.manager == nil {
		return nil, errSessionManagerRequired
	}

	return s.manager.GetMessages(ctx, id, messageQueryToStorageMessageQuery(query))
}

// AppendMessages persists messages emitted by the core agent loop.
func (s *SessionStore) AppendMessages(ctx context.Context, id string, messages []morphmsg.Message) error {
	if s == nil || s.manager == nil {
		return errSessionManagerRequired
	}

	return s.manager.AppendMessages(ctx, id, messages)
}

// UpdateLastPromptTokens records provider-reported prompt usage for compaction
// decisions.
func (s *SessionStore) UpdateLastPromptTokens(ctx context.Context, id string, tokens int) error {
	if s == nil || s.manager == nil {
		return errSessionManagerRequired
	}

	return s.manager.UpdateLastPromptTokens(ctx, id, tokens)
}

// AppendTraceEvent persists a trace event emitted by the core agent loop.
func (s *SessionStore) AppendTraceEvent(
	ctx context.Context,
	event agentsession.TraceEvent,
) (agentsession.TraceEvent, error) {
	if s == nil || s.manager == nil {
		return agentsession.TraceEvent{}, errSessionManagerRequired
	}

	stored, err := s.manager.AppendTraceEvent(ctx, storageTraceEventFromAgentTraceEvent(event))
	if err != nil {
		return agentsession.TraceEvent{}, err
	}

	return agentTraceEventFromStorageTraceEvent(stored), nil
}

func messageQueryToStorageMessageQuery(value agentsession.MessageQuery) storage.MessageQueryOptions {
	return storage.MessageQueryOptions{
		Limit:  value.Limit,
		Name:   value.Name,
		Order:  value.Order,
		Offset: value.Offset,
		Role:   morphmsg.Role(value.Role),
	}
}
