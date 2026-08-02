package browser

import (
	"context"
	"sync"
	"time"

	"github.com/xymorphic/morph/internal/permissions"
)

const networkAuthorizationBatchWindow = 25 * time.Millisecond

type networkAuthorizationRequest struct {
	ctx    context.Context
	target permissions.NetworkTarget
	result chan error
}

type networkAuthorizationTarget struct {
	Target permissions.NetworkTarget
	Count  int
}

type networkAuthorizationCoordinator struct {
	ctx       context.Context
	cancel    context.CancelFunc
	authorize func(context.Context, []networkAuthorizationTarget) error
	pause     func() func()
	requests  chan networkAuthorizationRequest
	done      chan struct{}
	closeOnce sync.Once
}

func newNetworkAuthorizationCoordinator(
	ctx context.Context,
	authorize func(context.Context, []networkAuthorizationTarget) error,
	pause func() func(),
) *networkAuthorizationCoordinator {
	coordinatorCtx, cancel := context.WithCancel(ctx)
	coordinator := &networkAuthorizationCoordinator{
		ctx: coordinatorCtx, cancel: cancel, authorize: authorize, pause: pause,
		requests: make(chan networkAuthorizationRequest, 128), done: make(chan struct{}),
	}
	go coordinator.run()
	return coordinator
}

func (c *networkAuthorizationCoordinator) Authorize(
	ctx context.Context,
	target permissions.NetworkTarget,
) error {
	target, err := target.Normalize()
	if err != nil {
		return err
	}
	request := networkAuthorizationRequest{ctx: ctx, target: target, result: make(chan error, 1)}
	select {
	case c.requests <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return context.Cause(c.ctx)
	}
	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return context.Cause(c.ctx)
	}
}

func (c *networkAuthorizationCoordinator) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		<-c.done
	})
}

func (c *networkAuthorizationCoordinator) run() {
	defer close(c.done)
	pending := make([]networkAuthorizationRequest, 0)
	for {
		var first networkAuthorizationRequest
		if len(pending) > 0 {
			first = pending[0]
			pending = pending[1:]
		} else {
			select {
			case <-c.ctx.Done():
				return
			case first = <-c.requests:
			}
		}

		batch := []networkAuthorizationRequest{first}
		if isBatchableNetworkTarget(first.target) {
			timer := time.NewTimer(networkAuthorizationBatchWindow)
		collect:
			for {
				select {
				case <-c.ctx.Done():
					timer.Stop()
					return
				case request := <-c.requests:
					if isCompatibleNetworkTarget(first.target, request.target) {
						batch = append(batch, request)
					} else {
						pending = append(pending, request)
					}
				case <-timer.C:
					break collect
				}
			}
		}

		batch = getActiveNetworkAuthorizationRequests(batch)
		if len(batch) == 0 {
			continue
		}
		targets := getNetworkAuthorizationTargets(batch)
		resume := func() {}
		if c.pause != nil {
			resume = c.pause()
		}
		err := c.authorize(c.ctx, targets)
		resume()
		for _, request := range batch {
			request.result <- err
		}
	}
}

func getActiveNetworkAuthorizationRequests(
	batch []networkAuthorizationRequest,
) []networkAuthorizationRequest {
	active := batch[:0]
	for _, request := range batch {
		if err := request.ctx.Err(); err != nil {
			request.result <- err
			continue
		}
		active = append(active, request)
	}
	return active
}

func getNetworkAuthorizationTargets(batch []networkAuthorizationRequest) []networkAuthorizationTarget {
	indices := make(map[permissions.NetworkTarget]int, len(batch))
	targets := make([]networkAuthorizationTarget, 0, len(batch))
	for _, request := range batch {
		if index, ok := indices[request.target]; ok {
			targets[index].Count++
			continue
		}
		indices[request.target] = len(targets)
		targets = append(targets, networkAuthorizationTarget{Target: request.target, Count: 1})
	}
	return targets
}

func isBatchableNetworkTarget(target permissions.NetworkTarget) bool {
	return target.RequestClass == permissions.NetworkRequestSubresource &&
		(target.Method == "GET" || target.Method == "HEAD")
}

func isCompatibleNetworkTarget(left, right permissions.NetworkTarget) bool {
	return isBatchableNetworkTarget(right) && left.Scheme == right.Scheme && left.Host == right.Host &&
		left.Port == right.Port
}
