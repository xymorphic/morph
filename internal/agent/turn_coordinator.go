package agent

import (
	"context"
	"sync"

	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/str"
)

type TurnCoordinator interface {
	Acquire(context.Context, string, string) (func(), error)
}

type turnCoordinator struct {
	mu    sync.Mutex
	gates map[string]*turnGate
}

type turnGate struct {
	token chan struct{}
	refs  int
}

var defaultTurnCoordinator TurnCoordinator = NewTurnCoordinator()

func NewTurnCoordinator() TurnCoordinator {
	return &turnCoordinator{gates: make(map[string]*turnGate)}
}

func (c *turnCoordinator) Acquire(ctx context.Context, scope string, sessionID string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	key := getTurnCoordinationKey(scope, sessionID)
	c.mu.Lock()
	gate := c.gates[key]
	if gate == nil {
		gate = &turnGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		c.gates[key] = gate
	}
	gate.refs++
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		c.releaseReference(key, gate)
		return nil, ctx.Err()
	case <-gate.token:
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			gate.token <- struct{}{}
			c.releaseReference(key, gate)
		})
	}, nil
}

func (c *turnCoordinator) releaseReference(key string, gate *turnGate) {
	c.mu.Lock()
	defer c.mu.Unlock()

	gate.refs--
	if gate.refs == 0 && c.gates[key] == gate {
		delete(c.gates, key)
	}
}

func getTurnCoordinationKey(scope string, sessionID string) string {
	scopeValue := str.String(scope)
	sessionIDValue := str.String(sessionID)
	return scopeValue.Trim() + "\x00" + sessionIDValue.Trim()
}

func getTurnCoordinationScope() string {
	active := profile.Active()
	homeDir := str.String(active.HomeDir)
	if scope := homeDir.Trim(); scope != "" {
		return scope
	}
	name := str.String(active.Name)
	return name.Trim()
}
