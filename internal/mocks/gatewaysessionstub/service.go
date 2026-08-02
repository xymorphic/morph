package gatewaysessionstub

import (
	"context"
	"fmt"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/nanoid"
)

type Service struct {
	Binding        storage.GatewayBinding
	Session        storage.Session
	SavedBinding   storage.GatewayBinding
	CreatedSession storage.Session
	CreateOrigin   storage.SessionOrigin
	GetErr         error
	SaveErr        error
	SessionErr     error
	CreateErr      error
	BindingFound   bool
	SessionFound   bool
	Created        bool
}

func (s *Service) Respond(context.Context, string, agentcore.RespondOptions) (string, error) {
	return "", nil
}

func (s *Service) CreateSession(
	_ context.Context,
	_ string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	s.Created = true
	if len(opts) > 0 {
		s.CreateOrigin = opts[0].Origin
	}
	if s.CreateErr != nil {
		return storage.Session{}, s.CreateErr
	}
	if s.CreatedSession.ID == "" {
		s.CreatedSession = storage.Session{ID: newSessionID("created-binding")}
	}
	s.CreatedSession.Origin = s.CreateOrigin

	return s.CreatedSession, nil
}

func (s *Service) Get(
	context.Context,
	string,
	storage.SessionGetOptions,
) (storage.Session, bool, error) {
	return s.Session, s.SessionFound, s.SessionErr
}

func (s *Service) SaveGatewayBinding(
	_ context.Context,
	binding storage.GatewayBinding,
) error {
	s.SavedBinding = binding
	return s.SaveErr
}

func (s *Service) GetGatewayBinding(
	context.Context,
	string,
) (storage.GatewayBinding, bool, error) {
	return s.Binding, s.BindingFound, s.GetErr
}

type MapBackedService struct {
	Bindings map[string]storage.GatewayBinding
	Sessions map[string]storage.Session
	NextID   int
}

func NewMapBackedService() *MapBackedService {
	return &MapBackedService{
		Bindings: make(map[string]storage.GatewayBinding),
		Sessions: make(map[string]storage.Session),
	}
}

func (s *MapBackedService) Respond(context.Context, string, agentcore.RespondOptions) (string, error) {
	return "", nil
}

func (s *MapBackedService) CreateSession(
	_ context.Context,
	_ string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	var origin storage.SessionOrigin
	if len(opts) > 0 {
		origin = opts[0].Origin
	}
	s.NextID++
	session := storage.Session{
		ID:     newSessionID(fmt.Sprintf("generated-binding-%d", s.NextID)),
		Origin: origin,
	}
	s.Sessions[session.ID] = session
	return session, nil
}

func (s *MapBackedService) Get(
	_ context.Context,
	id string,
	_ storage.SessionGetOptions,
) (storage.Session, bool, error) {
	session, ok := s.Sessions[id]
	return session, ok, nil
}

func (s *MapBackedService) SaveGatewayBinding(
	_ context.Context,
	binding storage.GatewayBinding,
) error {
	s.Bindings[binding.Key] = binding
	return nil
}

func (s *MapBackedService) GetGatewayBinding(
	_ context.Context,
	key string,
) (storage.GatewayBinding, bool, error) {
	binding, ok := s.Bindings[key]
	return binding, ok, nil
}

type StateManagerService struct {
	Manager *statemanager.Manager
}

func (s *StateManagerService) Respond(context.Context, string, agentcore.RespondOptions) (string, error) {
	return "", nil
}

func (s *StateManagerService) CreateSession(
	ctx context.Context,
	id string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	if len(opts) == 0 {
		return s.Manager.CreateSession(ctx, id)
	}

	return s.Manager.CreateSessionWithOptions(ctx, id, opts[0])
}

func (s *StateManagerService) Get(
	ctx context.Context,
	id string,
	opts storage.SessionGetOptions,
) (storage.Session, bool, error) {
	return s.Manager.Get(ctx, id, opts)
}

func (s *StateManagerService) SaveGatewayBinding(
	ctx context.Context,
	binding storage.GatewayBinding,
) error {
	return s.Manager.SaveGatewayBinding(ctx, binding)
}

func (s *StateManagerService) GetGatewayBinding(
	ctx context.Context,
	key string,
) (storage.GatewayBinding, bool, error) {
	return s.Manager.GetGatewayBinding(ctx, key)
}

type BasicCreateService struct {
	SavedBinding   storage.GatewayBinding
	CreatedSession storage.Session
	Created        bool
}

func (s *BasicCreateService) Respond(context.Context, string, agentcore.RespondOptions) (string, error) {
	return "", nil
}

func (s *BasicCreateService) CreateSession(
	context.Context,
	string,
	...storage.SessionCreateOptions,
) (storage.Session, error) {
	s.Created = true
	return s.CreatedSession, nil
}

func (s *BasicCreateService) SaveGatewayBinding(
	_ context.Context,
	binding storage.GatewayBinding,
) error {
	s.SavedBinding = binding
	return nil
}

func (s *BasicCreateService) GetGatewayBinding(
	context.Context,
	string,
) (storage.GatewayBinding, bool, error) {
	return storage.GatewayBinding{}, false, nil
}

func newSessionID(seed string) string {
	return nanoid.MustFromSeed(storage.SessionIDPrefix, seed, "gateway-session-stub")
}
