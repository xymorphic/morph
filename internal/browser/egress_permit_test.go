package browser

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestTransportPermitLedger_EnforcesExactBoundedHTTPAuthority(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	ledger := newTestTransportPermitLedger(t, func() time.Time { return now })
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := testTransportTarget("/news", "GET")
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, Uses: 2,
		ExpiresAt: now.Add(time.Minute),
	}}))

	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, lease.Addresses())
	lease.Release()
	lease, err = ledger.acquire(target)
	require.NoError(t, err)
	lease.Release()

	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitExhausted)
	_, err = ledger.acquire(testTransportTarget("/other", "GET"))
	requirePermitFailure(t, err, transportPermitMismatch)
	_, err = ledger.acquire(testTransportTarget("/news", "POST"))
	requirePermitFailure(t, err, transportPermitMismatch)
	_, err = ledger.acquire(permissions.NetworkTarget{
		Scheme: "http", Host: "other.example", Port: 80, Path: "/news", Method: "GET",
		RequestClass: permissions.NetworkRequestSubresource,
	})
	requirePermitFailure(t, err, transportPermitMissing)
}

func TestTransportPermitLedger_InstallsBatchesAtomically(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	err = ledger.install(generation, []transportPermitInput{
		{Target: testTransportTarget("/valid", "GET"), Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}},
		{Target: testTransportTarget("/invalid", "GET")},
	})
	require.EqualError(t, err, "transport permit addresses are required")
	_, err = ledger.acquire(testTransportTarget("/valid", "GET"))
	requirePermitFailure(t, err, transportPermitMissing)
}

func TestTransportPermitLedger_CancelledReservationCannotCommit(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	reservation, err := ledger.reserve(generation, 1)
	require.NoError(t, err)
	reservation.Cancel()
	input := transportPermitInput{
		Target:    testTransportTarget("/news", "GET"),
		Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}

	err = reservation.Commit([]transportPermitInput{input})
	require.EqualError(t, err, "transport permit reservation is inactive")
	err = reservation.Commit([]transportPermitInput{input})
	require.EqualError(t, err, "transport permit reservation is inactive")
	_, err = ledger.acquire(input.Target)
	requirePermitFailure(t, err, transportPermitMissing)
}

func TestTransportPermitLedger_ExpiresAndRevokesAttachedConnections(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	ledger := newTestTransportPermitLedger(t, func() time.Time { return now })
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := testTransportTarget("/events", "GET")
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		ExpiresAt: now.Add(time.Minute),
	}}))
	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })
	require.NoError(t, lease.Attach(left))
	require.NoError(t, ledger.revokeGeneration(generation))
	require.Error(t, right.SetWriteDeadline(time.Now().Add(time.Second)))
	_, err = right.Write([]byte("closed"))
	require.Error(t, err)
	lease.Release()

	generation, err = ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		ExpiresAt: now.Add(time.Second),
	}}))
	now = now.Add(2 * time.Second)
	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitExpired)
}

func TestTransportPermitLedger_ExpiryClosesAttachedConnections(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := testTransportTarget("/events", "GET")
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		ExpiresAt: time.Now().Add(200 * time.Millisecond), FreshUntil: time.Now().Add(time.Minute),
	}}))
	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })
	require.NoError(t, lease.Attach(left))

	requireConnectionClosed(t, right)
	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitExpired)
	lease.Release()
}

func TestTransportPermitLedger_GenerationCancellationClosesAttachedConnections(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	generation, err := ledger.beginGeneration(ctx)
	require.NoError(t, err)
	target := testTransportTarget("/events", "GET")
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		ExpiresAt: time.Now().Add(time.Minute),
	}}))
	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })
	require.NoError(t, lease.Attach(left))

	cancel()

	requireConnectionClosed(t, right)
	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitMissing)
	lease.Release()
}

func TestTransportPermitLedger_BoundsConcurrentConnectLeases(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := permissions.NetworkTarget{
		Scheme: "https", Host: "example.com", Port: 443, Path: "/private", Method: "GET",
		RequestClass: permissions.NetworkRequestNavigation,
	}
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, Uses: 10,
		ExpiresAt: time.Now().Add(time.Minute),
	}}))
	connect := permissions.NetworkTarget{
		Scheme: "https", Host: "example.com", Port: 443, Path: "/", Method: "CONNECT",
		RequestClass: permissions.NetworkRequestSubresource,
	}
	leases := make([]*transportPermitLease, 0, defaultConnectConcurrency)
	for range defaultConnectConcurrency {
		lease, acquireErr := ledger.acquire(connect)
		require.NoError(t, acquireErr)
		leases = append(leases, lease)
	}
	_, err = ledger.acquire(connect)
	requirePermitFailure(t, err, transportPermitConcurrent)
	leases[0].Release()
	lease, err := ledger.acquire(connect)
	require.NoError(t, err)
	lease.Release()
	for _, value := range leases[1:] {
		value.Release()
	}
}

func TestTransportPermitLedger_RejectsInvalidInputsAndInactiveAuthority(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	ledger := newTestTransportPermitLedger(t, func() time.Time { return now })
	var nilContext context.Context
	_, err := ledger.beginGeneration(nilContext)
	require.EqualError(t, err, "transport permit generation context is required")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ledger.beginGeneration(cancelled)
	require.EqualError(t, err, "transport permit generation context is inactive")
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := testTransportTarget("/news", "GET")
	address := netip.MustParseAddr("192.0.2.1")

	tests := []struct {
		name  string
		input transportPermitInput
		err   string
	}{
		{
			name: "invalid target",
			input: transportPermitInput{
				Target: permissions.NetworkTarget{
					Scheme: "ftp",
					Host:   "example.com",
					Method: "GET",
				},
				Addresses: []netip.Addr{address},
			},
			err: "permission network scheme must be one of: http, https, ws, wss",
		},
		{name: "missing addresses", input: transportPermitInput{Target: target}, err: "transport permit addresses are required"},
		{name: "invalid address", input: transportPermitInput{
			Target: target, Addresses: []netip.Addr{{}},
		}, err: "transport permit address is invalid"},
		{name: "expired", input: transportPermitInput{
			Target: target, Addresses: []netip.Addr{address}, ExpiresAt: now,
		}, err: "transport permit is already expired"},
		{name: "stale resolution", input: transportPermitInput{
			Target: target, Addresses: []netip.Addr{address}, ExpiresAt: now.Add(time.Minute), FreshUntil: now,
		}, err: "transport permit resolution is already stale"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.EqualError(t, ledger.install(generation, []transportPermitInput{test.input}), test.err)
		})
	}

	require.NoError(t, ledger.revokeGeneration(generation))
	require.EqualError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{address},
	}}), "transport permit generation is inactive")
	_, err = ledger.reserve(generation, 1)
	require.EqualError(t, err, "transport permit generation is inactive")
	_, err = ledger.reserve(generation, 0)
	require.EqualError(t, err, "transport permit reservation size must be greater than zero")

	require.NoError(t, ledger.close())
	require.NoError(t, ledger.close())
	_, err = ledger.beginGeneration(context.Background())
	require.EqualError(t, err, "transport permit ledger is closed")
	require.EqualError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{address},
	}}), "transport permit ledger is closed")
	_, err = ledger.reserve(generation, 1)
	require.EqualError(t, err, "transport permit ledger is closed")
	_, err = ledger.acquire(target)
	require.EqualError(t, err, "transport permit ledger is closed")
}

func TestTransportPermitLedger_ReportsTheMostSpecificTerminalFailure(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	ledger := newTestTransportPermitLedger(t, func() time.Time { return now })
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	exact := testTransportTarget("/news", "GET")
	require.NoError(t, ledger.install(generation, []transportPermitInput{
		{
			Target: exact, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			ExpiresAt: now.Add(time.Minute), Uses: 1,
		},
		{
			Target:    testTransportTarget("/other", "GET"),
			Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, ExpiresAt: now.Add(time.Minute),
		},
	}))
	lease, err := ledger.acquire(exact)
	require.NoError(t, err)
	lease.Release()

	_, err = ledger.acquire(exact)
	requirePermitFailure(t, err, transportPermitExhausted)
}

func TestTransportPermitLedger_ReservationsBoundCapacityAndCommitAtomically(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	ledger.capacity = 2
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	first := transportPermitInput{
		Target:    testTransportTarget("/first", "GET"),
		Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}
	second := transportPermitInput{
		Target:    testTransportTarget("/second", "GET"),
		Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.2")},
	}

	reservation, err := ledger.reserve(generation, 1)
	require.NoError(t, err)
	_, err = ledger.reserve(generation, 2)
	require.EqualError(t, err, "transport permit capacity exceeded")
	require.EqualError(t, reservation.Commit([]transportPermitInput{first, second}), "transport permit reservation is too small")
	_, err = ledger.acquire(first.Target)
	requirePermitFailure(t, err, transportPermitMissing)

	reservation, err = ledger.reserve(generation, 2)
	require.NoError(t, err)
	require.NoError(t, reservation.Commit([]transportPermitInput{first, second}))
	for _, target := range []permissions.NetworkTarget{first.Target, second.Target} {
		lease, acquireErr := ledger.acquire(target)
		require.NoError(t, acquireErr)
		lease.Release()
	}
	_, err = ledger.reserve(generation, 1)
	require.EqualError(t, err, "transport permit capacity exceeded")
}

func TestTransportPermitLedger_CommitFailsWhenGenerationEndsDuringReservation(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	generation, err := ledger.beginGeneration(ctx)
	require.NoError(t, err)
	reservation, err := ledger.reserve(generation, 1)
	require.NoError(t, err)
	cancel()

	err = reservation.Commit([]transportPermitInput{{
		Target:    testTransportTarget("/news", "GET"),
		Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}})
	require.EqualError(t, err, "transport permit reservation is inactive")
}

func TestTransportPermitLedger_NormalizesAndMergesEquivalentAuthority(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	ledger := newTestTransportPermitLedger(t, func() time.Time { return now })
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := permissions.NetworkTarget{
		Scheme: "WSS", Host: "EXAMPLE.COM.", Port: 443, Path: "/socket", Method: "GET",
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	input := transportPermitInput{
		Target: target,
		Addresses: []netip.Addr{
			netip.MustParseAddr("::ffff:192.0.2.1"),
			netip.MustParseAddr("192.0.2.1"),
		},
		Uses: 3, ExpiresAt: now.Add(time.Minute), FreshUntil: now.Add(30 * time.Second),
	}
	require.NoError(t, ledger.install(generation, []transportPermitInput{input, input}))
	require.Len(t, ledger.permits, 1)
	permit := ledger.permits[1]
	require.Equal(t, "https", permit.target.Scheme)
	require.Equal(t, "/", permit.target.Path)
	require.Equal(t, "CONNECT", permit.target.Method)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, permit.addresses)
	require.Equal(t, 6, permit.remaining)
	require.Equal(t, now.Add(30*time.Second), permit.expiresAt)
}

func TestTransportPermitLedger_RevokingGenerationClosesEveryPendingIntent(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := permissions.NetworkTarget{
		Scheme: "wss", Host: "socket.example", Port: 443, Path: "/events", Method: "CONNECT",
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	ledger.beginPending(target)
	ledger.beginPending(target)
	key, err := getPendingTransportTarget(target)
	require.NoError(t, err)
	require.Len(t, ledger.pending[key], 2)

	require.NoError(t, ledger.revokeGeneration(generation))
	require.Empty(t, ledger.pending)
}

func TestTransportPermitLedger_PendingIntentWaitsForDecisionAndHonorsCancellation(t *testing.T) {
	target := permissions.NetworkTarget{
		Scheme: "wss", Host: "socket.example", Port: 443, Path: "/events", Method: "GET",
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	ledger := newTestTransportPermitLedger(t, time.Now)
	ledger.beginPending(target)()
	waited, err := ledger.waitForPending(context.Background(), target)
	require.NoError(t, err)
	require.False(t, waited)

	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	finish := ledger.beginPending(target)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waited, err = ledger.waitForPending(ctx, target)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, waited)
	finish()
	waited, err = ledger.waitForPending(context.Background(), target)
	require.NoError(t, err)
	require.True(t, waited)
	require.Empty(t, ledger.pending)
	require.NoError(t, ledger.revokeGeneration(generation))

	invalid := permissions.NetworkTarget{
		Scheme: "ftp",
		Host:   "socket.example",
		Method: "GET",
	}
	ledger.beginPending(invalid)()
	_, err = ledger.waitForPending(context.Background(), invalid)
	require.EqualError(t, err, "permission network scheme must be one of: http, https, ws, wss")

	var nilLedger *transportPermitLedger
	nilLedger.beginPending(target)()
	waited, err = nilLedger.waitForPending(context.Background(), target)
	require.NoError(t, err)
	require.False(t, waited)
}

func TestTransportPermitLease_RejectsInvalidAttachmentAndIsIdempotent(t *testing.T) {
	var nilLease *transportPermitLease
	require.Nil(t, nilLease.Addresses())
	require.EqualError(t, nilLease.Attach(nil), "transport permit lease is required")
	nilLease.Release()

	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := testTransportTarget("/events", "GET")
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}}))
	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	require.EqualError(t, lease.Attach(nil), "transport connection is required")
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	require.NoError(t, lease.Attach(left))
	lease.Release()
	lease.Release()
	require.EqualError(t, lease.Attach(right), "transport permit lease is released")

	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitExhausted)
}

func TestTransportPermitLedger_ConnectDialBudgetIsNonReturnable(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	target := permissions.NetworkTarget{
		Scheme: "https", Host: "example.com", Port: 443, Path: "/", Method: "CONNECT",
		RequestClass: permissions.NetworkRequestSubresource,
	}
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, Uses: 1,
	}}))
	for range defaultConnectDialBudget {
		lease, acquireErr := ledger.acquire(target)
		require.NoError(t, acquireErr)
		lease.Release()
	}
	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitExhausted)
}

func TestTransportPermitLedger_NilAndMissingLifecycleOperationsAreSafe(t *testing.T) {
	var ledger *transportPermitLedger
	_, ok := ledger.getActiveGeneration()
	require.False(t, ok)
	_, err := ledger.acquire(testTransportTarget("/news", "GET"))
	requirePermitFailure(t, err, transportPermitMissing)
	require.NoError(t, ledger.revokeGeneration(1))
	require.NoError(t, ledger.close())
	require.NoError(t, ledger.invalidate())

	ledger = newTestTransportPermitLedger(t, time.Now)
	require.NoError(t, ledger.revokeGeneration(0))
	require.NoError(t, ledger.revokeGeneration(99))
	_, ok = ledger.getActiveGeneration()
	require.False(t, ok)
	require.Equal(t, "transport permit expired", (&transportPermitError{Failure: transportPermitExpired}).Error())
}

func testTransportTarget(path, method string) permissions.NetworkTarget {
	return permissions.NetworkTarget{
		Scheme: "http", Host: "example.com", Port: 80, Path: path, Method: method,
		RequestClass: permissions.NetworkRequestNavigation,
	}
}

func requirePermitFailure(t *testing.T, err error, failure transportPermitFailure) {
	t.Helper()
	var permitErr *transportPermitError
	require.ErrorAs(t, err, &permitErr)
	require.Equal(t, failure, permitErr.Failure)
}

func requireConnectionClosed(t *testing.T, connection net.Conn) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		_, err := connection.Read(make([]byte, 1))
		result <- err
	}()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("transport connection was not closed")
	}
}

func newTestTransportPermitLedger(t *testing.T, now func() time.Time) *transportPermitLedger {
	t.Helper()
	ledger := newTransportPermitLedger(now)
	t.Cleanup(func() {
		require.NoError(t, ledger.close())
	})

	return ledger
}
