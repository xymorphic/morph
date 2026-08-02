package execution

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type managedServiceStub struct {
	Service
	closed atomic.Int32
}

func (s *managedServiceStub) Close(context.Context) error {
	s.closed.Add(1)
	return nil
}

func TestAcquireManager_ClosesAfterFinalLease(t *testing.T) {
	service := &managedServiceStub{}
	builds := 0
	build := func() (Service, error) {
		builds++
		return service, nil
	}
	first, err := AcquireManager(t.Name(), build)
	require.NoError(t, err)
	second, err := AcquireManager(t.Name(), build)
	require.NoError(t, err)
	require.Equal(t, 1, builds)
	require.NoError(t, first.Close(context.Background()))
	require.Zero(t, service.closed.Load())
	require.NoError(t, second.Close(context.Background()))
	require.EqualValues(t, 1, service.closed.Load())
	replacement := &managedServiceStub{}
	third, err := AcquireManager(t.Name(), func() (Service, error) { return replacement, nil })
	require.NoError(t, err)
	require.Same(t, replacement, third.Service)
	require.NoError(t, third.Close(context.Background()))
}
