package execution

import (
	"context"
	"errors"
	"sync"
)

type Manager struct {
	Service
	key      string
	registry *managerRegistry
	once     sync.Once
}

type managerEntry struct {
	service Service
	leases  int
}

type managerRegistry struct {
	mu      sync.Mutex
	entries map[string]*managerEntry
}

var executionManagers = managerRegistry{entries: map[string]*managerEntry{}}

func AcquireManager(key string, build func() (Service, error)) (*Manager, error) {
	if key == "" || build == nil {
		return nil, errors.New("execution manager configuration is incomplete")
	}
	executionManagers.mu.Lock()
	defer executionManagers.mu.Unlock()
	entry := executionManagers.entries[key]
	if entry == nil {
		service, err := build()
		if err != nil {
			return nil, err
		}
		entry = &managerEntry{service: service}
		executionManagers.entries[key] = entry
	}
	entry.leases++
	return &Manager{
		Service:  entry.service,
		key:      key,
		registry: &executionManagers,
	}, nil
}

func (m *Manager) Close(ctx context.Context) error {
	if m == nil || m.registry == nil {
		return nil
	}
	var closeErr error
	m.once.Do(func() {
		m.registry.mu.Lock()
		entry := m.registry.entries[m.key]
		if entry == nil {
			m.registry.mu.Unlock()
			return
		}
		entry.leases--
		if entry.leases > 0 {
			m.registry.mu.Unlock()
			return
		}
		delete(m.registry.entries, m.key)
		m.registry.mu.Unlock()
		closeErr = entry.service.Close(ctx)
	})
	return closeErr
}
