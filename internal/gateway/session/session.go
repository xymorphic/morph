package session

import (
	"context"
	"errors"
	"time"

	storage "github.com/xymorphic/morph/internal/state/core"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/gateway/bindings"
	"github.com/xymorphic/morph/pkg/str"
)

type Service interface {
	Respond(context.Context, string, agentcore.RespondOptions) (string, error)
	CreateSession(context.Context, string, ...storage.SessionCreateOptions) (storage.Session, error)
	SaveGatewayBinding(context.Context, storage.GatewayBinding) error
	GetGatewayBinding(context.Context, string) (storage.GatewayBinding, bool, error)
}

type sessionGetter interface {
	Get(context.Context, string, storage.SessionGetOptions) (storage.Session, bool, error)
}

type Resolver struct {
	service Service
	now     func() time.Time
}

func NewResolver(service Service) *Resolver {
	return &Resolver{
		service: service,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (r *Resolver) Resolve(ctx context.Context, key bindings.Key) (storage.Session, error) {
	if r == nil || r.service == nil {
		return storage.Session{}, errors.New("gateway session resolver service is required")
	}

	textValue := str.String(key.String())
	keyString := textValue.Trim()
	if keyString == "" {
		return storage.Session{}, errors.New("gateway binding key is required")
	}

	binding, ok, err := r.service.GetGatewayBinding(ctx, keyString)
	if err != nil {
		return storage.Session{}, err
	}
	if ok {
		if getter, ok := r.service.(sessionGetter); ok {
			session, found, err := getter.Get(ctx, binding.SessionID, storage.SessionGetOptions{})
			if err != nil {
				return storage.Session{}, err
			}
			if found {
				return session, nil
			}

			return r.createAndSaveBinding(ctx, key, keyString, binding.CreatedAt)
		}

		return storage.Session{ID: binding.SessionID}, nil
	}

	return r.createAndSaveBinding(ctx, key, keyString, time.Time{})
}

func (r *Resolver) createAndSaveBinding(
	ctx context.Context,
	key bindings.Key,
	keyString string,
	createdAt time.Time,
) (storage.Session, error) {
	origin := sessionOriginFromBindingKey(key)
	session, err := r.createSession(ctx, origin)
	if err != nil {
		return storage.Session{}, err
	}

	now := r.now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	if err := r.service.SaveGatewayBinding(ctx, storage.GatewayBinding{
		Key:       keyString,
		SessionID: session.ID,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}); err != nil {
		return storage.Session{}, err
	}

	return session, nil
}

func (r *Resolver) createSession(ctx context.Context, origin storage.SessionOrigin) (storage.Session, error) {
	return r.service.CreateSession(ctx, "", storage.SessionCreateOptions{Origin: origin})
}

func sessionOriginFromBindingKey(key bindings.Key) storage.SessionOrigin {
	parts, err := bindings.ParseKey(key)
	if err != nil {
		return storage.SessionOrigin{}
	}

	return storage.SessionOrigin{
		Source:         parts.Source,
		AccountID:      parts.AccountID,
		ConversationID: parts.ConversationID,
		ThreadID:       parts.ThreadID,
	}
}
