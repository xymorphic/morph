package browser

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xymorphic/morph/internal/permissions"
)

const (
	defaultTransportPermitTTL      = 3 * time.Minute
	defaultResolutionFreshness     = 30 * time.Second
	defaultConnectDialBudget       = 4
	defaultConnectConcurrency      = 4
	defaultTransportPermitCapacity = 4096
)

type transportPermitInput struct {
	Target     permissions.NetworkTarget
	Addresses  []netip.Addr
	Uses       int
	ExpiresAt  time.Time
	FreshUntil time.Time
}

type transportPermitFailure string

const (
	transportPermitMissing    transportPermitFailure = "no_candidate"
	transportPermitMismatch   transportPermitFailure = "candidate_mismatch"
	transportPermitExpired    transportPermitFailure = "expired"
	transportPermitExhausted  transportPermitFailure = "exhausted"
	transportPermitConcurrent transportPermitFailure = "concurrency_exceeded"
	transportPermitRevoked    transportPermitFailure = "revoked"
)

type transportPermitError struct {
	Failure transportPermitFailure
}

func (e *transportPermitError) Error() string {
	return "transport permit " + string(e.Failure)
}

type transportPermit struct {
	id          uint64
	generation  uint64
	target      permissions.NetworkTarget
	addresses   []netip.Addr
	expiresAt   time.Time
	freshUntil  time.Time
	remaining   int
	active      int
	maxActive   int
	expired     bool
	timer       *time.Timer
	connections map[net.Conn]struct{}
}

type permitGeneration struct {
	id       uint64
	ctx      context.Context
	permits  map[uint64]struct{}
	reserved int
	stop     func() bool
}

type pendingTransportIntent struct {
	generation uint64
	done       chan struct{}
	closed     bool
}

type activePermitGeneration struct {
	ID      uint64
	Context context.Context
}

type transportPermitLedger struct {
	mu             sync.Mutex
	now            func() time.Time
	sessionID      string
	capacity       int
	nextGeneration uint64
	nextPermit     uint64
	generations    map[uint64]*permitGeneration
	permits        map[uint64]*transportPermit
	pending        map[permissions.NetworkTarget][]*pendingTransportIntent
	closed         bool
}

type transportPermitLease struct {
	ledger      *transportPermitLedger
	permitID    uint64
	generation  uint64
	addresses   []netip.Addr
	mu          sync.Mutex
	connections []net.Conn
	released    bool
}

type transportPermitReservation struct {
	ledger     *transportPermitLedger
	generation uint64
	count      int
	once       sync.Once
	result     error
}

func newTransportPermitLedger(now func() time.Time) *transportPermitLedger {
	if now == nil {
		now = time.Now
	}
	return &transportPermitLedger{
		now: now, capacity: defaultTransportPermitCapacity,
		generations: make(map[uint64]*permitGeneration), permits: make(map[uint64]*transportPermit),
		pending: make(map[permissions.NetworkTarget][]*pendingTransportIntent),
	}
}

func (l *transportPermitLedger) beginPending(target permissions.NetworkTarget) func() {
	if l == nil {
		return func() {}
	}
	target, err := getPendingTransportTarget(target)
	if err != nil {
		return func() {}
	}
	l.mu.Lock()
	var generationID uint64
	for id := l.nextGeneration; id > 0; id-- {
		if generation := l.generations[id]; generation != nil && generation.ctx.Err() == nil {
			generationID = id
			break
		}
	}
	if generationID == 0 || l.closed {
		l.mu.Unlock()
		return func() {}
	}
	intent := &pendingTransportIntent{generation: generationID, done: make(chan struct{})}
	l.pending[target] = append(l.pending[target], intent)
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if !intent.closed {
				intent.closed = true
				close(intent.done)
			}
			l.mu.Unlock()
		})
	}
}

func (l *transportPermitLedger) waitForPending(
	ctx context.Context,
	target permissions.NetworkTarget,
) (bool, error) {
	if l == nil {
		return false, nil
	}
	target, err := getPendingTransportTarget(target)
	if err != nil {
		return false, err
	}
	l.mu.Lock()
	intents := l.pending[target]
	if len(intents) == 0 {
		l.mu.Unlock()
		return false, nil
	}
	intent := intents[0]
	l.mu.Unlock()

	select {
	case <-intent.done:
	case <-ctx.Done():
		return true, ctx.Err()
	}
	l.mu.Lock()
	l.removePendingIntentLocked(target, intent)
	l.mu.Unlock()
	return true, nil
}

func (l *transportPermitLedger) removePendingIntentLocked(
	target permissions.NetworkTarget,
	intent *pendingTransportIntent,
) {
	intents := l.pending[target]
	for index, current := range intents {
		if current != intent {
			continue
		}
		intents = append(intents[:index], intents[index+1:]...)
		if len(intents) == 0 {
			delete(l.pending, target)
		} else {
			l.pending[target] = intents
		}
		return
	}
}

func (l *transportPermitLedger) beginGeneration(ctx context.Context) (uint64, error) {
	if ctx == nil {
		return 0, errors.New("transport permit generation context is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, errors.New("transport permit generation context is inactive")
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return 0, errors.New("transport permit ledger is closed")
	}
	l.nextGeneration++
	generation := &permitGeneration{id: l.nextGeneration, ctx: ctx, permits: make(map[uint64]struct{})}
	l.generations[generation.id] = generation
	l.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		_ = l.revokeGeneration(generation.id)
	})
	l.mu.Lock()
	if current := l.generations[generation.id]; current != nil {
		current.stop = stop
	} else {
		stop()
	}
	l.mu.Unlock()
	return generation.id, nil
}

func (l *transportPermitLedger) install(generationID uint64, inputs []transportPermitInput) error {
	prepared, err := l.prepareInputs(inputs)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("transport permit ledger is closed")
	}
	generation, ok := l.generations[generationID]
	if !ok || generation.ctx.Err() != nil {
		return errors.New("transport permit generation is inactive")
	}
	reserved := 0
	for _, value := range l.generations {
		reserved += value.reserved
	}
	if len(l.permits)+reserved+len(prepared) > l.capacity {
		return errors.New("transport permit capacity exceeded")
	}
	for _, input := range prepared {
		l.addPermitLocked(generation, input)
	}
	return nil
}

func (l *transportPermitLedger) addPermitLocked(generation *permitGeneration, input transportPermitInput) {
	l.nextPermit++
	permit := &transportPermit{
		id: l.nextPermit, generation: generation.id, target: input.Target,
		addresses: slices.Clone(input.Addresses), expiresAt: input.ExpiresAt,
		freshUntil: input.FreshUntil,
		remaining:  input.Uses, maxActive: input.Uses,
		connections: make(map[net.Conn]struct{}),
	}
	if input.Target.Method == "CONNECT" {
		permit.remaining = max(input.Uses, defaultConnectDialBudget)
		permit.maxActive = defaultConnectConcurrency
	}
	l.permits[permit.id] = permit
	generation.permits[permit.id] = struct{}{}
	permit.timer = time.AfterFunc(input.ExpiresAt.Sub(l.now()), func() {
		l.expirePermit(permit.id)
	})
}

func (l *transportPermitLedger) reserve(generationID uint64, count int) (*transportPermitReservation, error) {
	if count <= 0 {
		return nil, errors.New("transport permit reservation size must be greater than zero")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errors.New("transport permit ledger is closed")
	}
	generation := l.generations[generationID]
	if generation == nil || generation.ctx.Err() != nil {
		return nil, errors.New("transport permit generation is inactive")
	}
	reserved := 0
	for _, value := range l.generations {
		reserved += value.reserved
	}
	if len(l.permits)+reserved+count > l.capacity {
		return nil, errors.New("transport permit capacity exceeded")
	}
	generation.reserved += count
	return &transportPermitReservation{ledger: l, generation: generationID, count: count}, nil
}

func (r *transportPermitReservation) Commit(inputs []transportPermitInput) error {
	if r == nil || r.ledger == nil {
		return errors.New("transport permit reservation is required")
	}
	prepared, err := r.ledger.prepareInputs(inputs)
	if err != nil {
		r.Cancel()
		return err
	}
	if len(prepared) > r.count {
		r.Cancel()
		return errors.New("transport permit reservation is too small")
	}
	r.once.Do(func() {
		l := r.ledger
		l.mu.Lock()
		defer l.mu.Unlock()
		generation := l.generations[r.generation]
		if l.closed || generation == nil || generation.ctx.Err() != nil || generation.reserved < r.count {
			r.result = errors.New("transport permit reservation is inactive")
			return
		}
		generation.reserved -= r.count
		for _, input := range prepared {
			l.addPermitLocked(generation, input)
		}
	})
	return r.result
}

func (r *transportPermitReservation) Cancel() {
	if r == nil || r.ledger == nil {
		return
	}
	r.once.Do(func() {
		r.ledger.mu.Lock()
		defer r.ledger.mu.Unlock()
		r.result = errors.New("transport permit reservation is inactive")
		if generation := r.ledger.generations[r.generation]; generation != nil && generation.reserved >= r.count {
			generation.reserved -= r.count
		}
	})
}

func (l *transportPermitLedger) getActiveGeneration() (activePermitGeneration, bool) {
	if l == nil {
		return activePermitGeneration{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for generationID := l.nextGeneration; generationID > 0; generationID-- {
		generation := l.generations[generationID]
		if generation != nil && generation.ctx.Err() == nil {
			return activePermitGeneration{ID: generation.id, Context: generation.ctx}, true
		}
	}
	return activePermitGeneration{}, false
}

func (l *transportPermitLedger) prepareInputs(inputs []transportPermitInput) ([]transportPermitInput, error) {
	if len(inputs) == 0 {
		return nil, errors.New("transport permit inputs are required")
	}
	now := l.now()
	prepared := make([]transportPermitInput, 0, len(inputs))
	for _, input := range inputs {
		target, err := getProxyPermitTarget(input.Target)
		if err != nil {
			return nil, err
		}
		if len(input.Addresses) == 0 {
			return nil, errors.New("transport permit addresses are required")
		}
		addresses := make([]netip.Addr, 0, len(input.Addresses))
		for _, address := range input.Addresses {
			if !address.IsValid() {
				return nil, errors.New("transport permit address is invalid")
			}
			address = address.Unmap()
			if !slices.Contains(addresses, address) {
				addresses = append(addresses, address)
			}
		}
		if input.Uses <= 0 {
			input.Uses = 1
		}
		if input.ExpiresAt.IsZero() {
			input.ExpiresAt = now.Add(defaultTransportPermitTTL)
		}
		if !input.ExpiresAt.After(now) {
			return nil, errors.New("transport permit is already expired")
		}
		if input.FreshUntil.IsZero() {
			input.FreshUntil = now.Add(defaultResolutionFreshness)
		}
		if !input.FreshUntil.After(now) {
			return nil, errors.New("transport permit resolution is already stale")
		}
		if input.FreshUntil.Before(input.ExpiresAt) {
			input.ExpiresAt = input.FreshUntil
		}
		input.Target = target
		input.Addresses = addresses
		merged := false
		for index := range prepared {
			if isSameProxyPermitTarget(prepared[index].Target, input.Target) &&
				slices.Equal(prepared[index].Addresses, input.Addresses) &&
				prepared[index].ExpiresAt.Equal(input.ExpiresAt) && prepared[index].FreshUntil.Equal(input.FreshUntil) {
				prepared[index].Uses += input.Uses
				merged = true
				break
			}
		}
		if !merged {
			prepared = append(prepared, input)
		}
	}
	return prepared, nil
}

func (l *transportPermitLedger) acquire(target permissions.NetworkTarget) (*transportPermitLease, error) {
	if l == nil {
		return nil, &transportPermitError{Failure: transportPermitMissing}
	}
	normalized, err := getProxyPermitTarget(target)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errors.New("transport permit ledger is closed")
	}
	now := l.now()
	failure := transportPermitMissing
	for permitID := uint64(1); permitID <= l.nextPermit; permitID++ {
		permit := l.permits[permitID]
		if permit == nil {
			continue
		}
		if !isSameProxyPermitOrigin(permit.target, normalized) {
			continue
		}
		if !isSameProxyPermitTarget(permit.target, normalized) {
			failure = getHigherPriorityPermitFailure(failure, transportPermitMismatch)
			continue
		}
		generation := l.generations[permit.generation]
		if generation == nil || generation.ctx.Err() != nil {
			failure = getHigherPriorityPermitFailure(failure, transportPermitRevoked)
			continue
		}
		if permit.expired || !permit.expiresAt.After(now) || !permit.freshUntil.After(now) {
			failure = getHigherPriorityPermitFailure(failure, transportPermitExpired)
			continue
		}
		if permit.remaining <= 0 {
			failure = getHigherPriorityPermitFailure(failure, transportPermitExhausted)
			continue
		}
		if permit.active >= permit.maxActive {
			failure = getHigherPriorityPermitFailure(failure, transportPermitConcurrent)
			continue
		}
		permit.remaining--
		permit.active++
		return &transportPermitLease{
			ledger: l, permitID: permit.id, generation: permit.generation,
			addresses: slices.Clone(permit.addresses),
		}, nil
	}
	return nil, &transportPermitError{Failure: failure}
}

func getHigherPriorityPermitFailure(
	current transportPermitFailure,
	candidate transportPermitFailure,
) transportPermitFailure {
	if getPermitFailurePriority(candidate) > getPermitFailurePriority(current) {
		return candidate
	}

	return current
}

func getPermitFailurePriority(failure transportPermitFailure) int {
	switch failure {
	case transportPermitMismatch:
		return 1
	case transportPermitConcurrent:
		return 2
	case transportPermitExhausted:
		return 3
	case transportPermitExpired:
		return 4
	case transportPermitRevoked:
		return 5
	default:
		return 0
	}
}

func (l *transportPermitLedger) revokeGeneration(generationID uint64) error {
	if l == nil || generationID == 0 {
		return nil
	}
	l.mu.Lock()
	connections := l.removeGenerationLocked(generationID)
	l.mu.Unlock()
	return closePermitConnections(connections)
}

func (l *transportPermitLedger) close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	connections := make([]net.Conn, 0)
	for generationID := range l.generations {
		connections = append(connections, l.removeGenerationLocked(generationID)...)
	}
	l.mu.Unlock()
	return closePermitConnections(connections)
}

func (l *transportPermitLedger) invalidate() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	connections := make([]net.Conn, 0)
	for generationID := range l.generations {
		connections = append(connections, l.removeGenerationLocked(generationID)...)
	}
	l.mu.Unlock()
	return closePermitConnections(connections)
}

func (l *transportPermitLedger) removeGenerationLocked(generationID uint64) []net.Conn {
	generation := l.generations[generationID]
	if generation == nil {
		return nil
	}
	if generation.stop != nil {
		generation.stop()
	}
	connections := make([]net.Conn, 0)
	for permitID := range generation.permits {
		permit := l.permits[permitID]
		if permit == nil {
			continue
		}
		if permit.timer != nil {
			permit.timer.Stop()
		}
		for connection := range permit.connections {
			connections = append(connections, connection)
		}
		delete(l.permits, permitID)
	}
	for target, intents := range l.pending {
		retained := intents[:0]
		for _, intent := range intents {
			if intent.generation != generationID {
				retained = append(retained, intent)
				continue
			}
			if !intent.closed {
				intent.closed = true
				close(intent.done)
			}
		}
		if len(retained) == 0 {
			delete(l.pending, target)
		} else {
			l.pending[target] = retained
		}
	}
	delete(l.generations, generationID)
	return connections
}

func (l *transportPermitLedger) expirePermit(permitID uint64) {
	l.mu.Lock()
	permit := l.permits[permitID]
	if permit == nil || permit.expired {
		l.mu.Unlock()
		return
	}
	permit.expired = true
	generationID := permit.generation
	connections := make([]net.Conn, 0, len(permit.connections))
	for connection := range permit.connections {
		connections = append(connections, connection)
	}
	l.mu.Unlock()
	_ = closePermitConnections(connections)
	log.Debug().
		Str("browser_session_id", l.sessionID).
		Uint64("transport_permit_generation", generationID).
		Uint64("transport_permit_id", permitID).
		Int("transport_connection_count", len(connections)).
		Msg("Browser transport authority expired")
}

func (l *transportPermitLedger) attach(permitID, generationID uint64, connections []net.Conn) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	permit := l.permits[permitID]
	generation := l.generations[generationID]
	if l.closed || permit == nil || permit.generation != generationID || generation == nil || generation.ctx.Err() != nil ||
		permit.expired || !permit.expiresAt.After(l.now()) {
		return errors.New("transport permit is inactive")
	}
	for _, connection := range connections {
		if connection == nil {
			return errors.New("transport connection is required")
		}
	}
	for _, connection := range connections {
		permit.connections[connection] = struct{}{}
	}
	return nil
}

func (l *transportPermitLedger) release(permitID, generationID uint64, connections []net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	permit := l.permits[permitID]
	if permit == nil || permit.generation != generationID {
		return
	}
	for _, connection := range connections {
		delete(permit.connections, connection)
	}
	if permit.active > 0 {
		permit.active--
	}
}

func (l *transportPermitLease) Addresses() []netip.Addr {
	if l == nil {
		return nil
	}
	return slices.Clone(l.addresses)
}

func (l *transportPermitLease) Attach(connections ...net.Conn) error {
	if l == nil || l.ledger == nil {
		return errors.New("transport permit lease is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return errors.New("transport permit lease is released")
	}
	if err := l.ledger.attach(l.permitID, l.generation, connections); err != nil {
		return err
	}
	l.connections = append(l.connections, connections...)
	return nil
}

func (l *transportPermitLease) Release() {
	if l == nil || l.ledger == nil {
		return
	}
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	connections := slices.Clone(l.connections)
	l.connections = nil
	l.mu.Unlock()
	l.ledger.release(l.permitID, l.generation, connections)
}

func getProxyPermitTarget(target permissions.NetworkTarget) (permissions.NetworkTarget, error) {
	target, err := target.Normalize()
	if err != nil {
		return permissions.NetworkTarget{}, err
	}
	if target.Scheme == "https" || target.Scheme == "ws" || target.Scheme == "wss" {
		target.Scheme = "https"
		target.Path = "/"
		target.QueryHash = ""
		target.Method = "CONNECT"
	}
	return target, nil
}

func getPendingTransportTarget(target permissions.NetworkTarget) (permissions.NetworkTarget, error) {
	target, err := getProxyPermitTarget(target)
	if err != nil {
		return permissions.NetworkTarget{}, err
	}
	target.RequestClass = ""
	return target, nil
}

func isSameProxyPermitTarget(left, right permissions.NetworkTarget) bool {
	left, leftErr := left.Normalize()
	right, rightErr := right.Normalize()
	if leftErr != nil || rightErr != nil {
		return false
	}
	if left.Method == "CONNECT" && right.Method == "CONNECT" {
		return left.Scheme == right.Scheme && left.Host == right.Host && left.Port == right.Port
	}
	return left.Scheme == right.Scheme && left.Host == right.Host && left.Port == right.Port &&
		left.Path == right.Path && left.QueryHash == right.QueryHash && left.Method == right.Method
}

func isSameProxyPermitOrigin(left, right permissions.NetworkTarget) bool {
	left, leftErr := left.Normalize()
	right, rightErr := right.Normalize()
	return leftErr == nil && rightErr == nil && left.Scheme == right.Scheme && left.Host == right.Host &&
		left.Port == right.Port
}

func closePermitConnections(connections []net.Conn) error {
	var closeErrors []error
	for _, connection := range connections {
		if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}
