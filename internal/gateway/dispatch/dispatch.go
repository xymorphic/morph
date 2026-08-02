package dispatch

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

var (
	ErrDispatcherClosed = errors.New("gateway dispatcher is closed")
	ErrJobIDRequired    = errors.New("gateway dispatch job id is required")
	ErrJobRunRequired   = errors.New("gateway dispatch job run function is required")
	ErrQueueFull        = errors.New("gateway dispatch queue is full")
)

type Job struct {
	ID          string
	MaxAttempts int
	Timeout     time.Duration
	Run         func(context.Context) error
}

type Options struct {
	Capacity         int
	Workers          int
	MaxAttempts      int
	Timeout          time.Duration
	IdempotencyLimit int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
}

type Status struct {
	QueueDepth int
	Capacity   int
	Workers    int
	InFlight   int
	Completed  uint64
	Failed     uint64
	Dropped    uint64
	Duplicates uint64
	Degraded   bool
	LastError  string
}

type Dispatcher struct {
	queue    chan queuedJob
	opts     Options
	once     sync.Once
	wg       sync.WaitGroup
	closeMu  sync.Mutex
	closed   bool
	statusMu sync.Mutex
	status   Status
	seen     map[string]struct{}
	seenIDs  []string
}

type queuedJob struct {
	id          string
	maxAttempts int
	timeout     time.Duration
	run         func(context.Context) error
}

func New(opts Options) *Dispatcher {
	opts = setDefaultOptions(opts)
	return &Dispatcher{
		queue: make(chan queuedJob, opts.Capacity),
		opts:  opts,
		status: Status{
			Capacity: opts.Capacity,
			Workers:  opts.Workers,
		},
		seen: make(map[string]struct{}),
	}
}

func (d *Dispatcher) Start(ctx context.Context) {
	if d == nil {
		return
	}

	d.once.Do(func() {
		for range d.opts.Workers {
			d.wg.Go(func() {
				d.work(ctx)
			})
		}

		go func() {
			<-ctx.Done()
			d.close()
		}()
	})
}

func (d *Dispatcher) Enqueue(job Job) (bool, error) {
	if d == nil {
		return false, ErrDispatcherClosed
	}

	iDValue := str.String(job.ID)
	id := iDValue.Trim()
	if id == "" {
		return false, ErrJobIDRequired
	}
	if job.Run == nil {
		return false, ErrJobRunRequired
	}

	d.closeMu.Lock()
	if d.closed {
		d.closeMu.Unlock()
		return false, ErrDispatcherClosed
	}
	d.statusMu.Lock()
	if _, ok := d.seen[id]; ok {
		d.status.Duplicates++
		d.statusMu.Unlock()
		d.closeMu.Unlock()
		return false, nil
	}
	d.statusMu.Unlock()

	select {
	case d.queue <- queuedJob{id: id, maxAttempts: job.MaxAttempts, timeout: job.Timeout, run: job.Run}:
		d.rememberJobID(id)
		d.closeMu.Unlock()
		return true, nil
	default:
		d.closeMu.Unlock()
		d.recordDrop(ErrQueueFull)
		return false, ErrQueueFull
	}
}

func (d *Dispatcher) Shutdown(ctx context.Context) error {
	if d == nil {
		return nil
	}

	d.close()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) Close() {
	if d == nil {
		return
	}

	d.close()
}

func (d *Dispatcher) Status() Status {
	if d == nil {
		return Status{}
	}

	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	status := d.status
	status.QueueDepth = len(d.queue)
	status.Degraded = status.LastError != ""
	return status
}

func (d *Dispatcher) close() {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()

	if d.closed {
		return
	}

	d.closed = true
	close(d.queue)
}

func (d *Dispatcher) work(ctx context.Context) {
	for job := range d.queue {
		d.setInFlight(1)
		err := d.runWithRetry(ctx, job)
		d.setInFlight(-1)
		if err != nil {
			d.recordFailure(err)
			continue
		}
		d.recordCompletion()
	}
}

func (d *Dispatcher) runWithRetry(ctx context.Context, job queuedJob) error {
	maxAttempts := job.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = d.opts.MaxAttempts
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := d.runJobAttempt(ctx, job)
		if err == nil {
			return nil
		}
		if attempt >= maxAttempts {
			return err
		}
		if !sleepRetry(ctx, retryDelay(d.opts, attempt)) {
			return ctx.Err()
		}
	}
}

func (d *Dispatcher) runJobAttempt(ctx context.Context, job queuedJob) error {
	timeout := job.timeout
	if timeout <= 0 {
		timeout = d.opts.Timeout
	}
	if timeout <= 0 {
		return job.run(ctx)
	}

	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return job.run(attemptCtx)
}

func (d *Dispatcher) setInFlight(delta int) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	d.status.InFlight += delta
}

func (d *Dispatcher) recordCompletion() {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	d.status.Completed++
}

func (d *Dispatcher) recordFailure(err error) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	d.status.Failed++
	d.status.LastError = err.Error()
}

func (d *Dispatcher) recordDrop(err error) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	d.status.Dropped++
	d.status.LastError = err.Error()
}

func (d *Dispatcher) rememberJobID(id string) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	if len(d.seenIDs) >= d.opts.IdempotencyLimit {
		oldest := d.seenIDs[0]
		delete(d.seen, oldest)
		copy(d.seenIDs, d.seenIDs[1:])
		d.seenIDs = d.seenIDs[:len(d.seenIDs)-1]
	}
	d.seen[id] = struct{}{}
	d.seenIDs = append(d.seenIDs, id)
}

func setDefaultOptions(opts Options) Options {
	if opts.Capacity <= 0 {
		opts.Capacity = 128
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.IdempotencyLimit <= 0 {
		opts.IdempotencyLimit = 4096
	}
	if opts.RetryBaseDelay <= 0 {
		opts.RetryBaseDelay = 200 * time.Millisecond
	}
	if opts.RetryMaxDelay <= 0 {
		opts.RetryMaxDelay = 2 * time.Second
	}

	return opts
}

func retryDelay(opts Options, attempt int) time.Duration {
	delay := float64(opts.RetryBaseDelay) * math.Pow(2, float64(attempt-1))
	if delay > float64(opts.RetryMaxDelay) {
		return opts.RetryMaxDelay
	}

	return time.Duration(delay)
}

func sleepRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
