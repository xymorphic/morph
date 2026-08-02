package docker

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/wandxy/morph/internal/execution"
)

func isNotFound(err error) bool {
	return err != nil && errdefs.IsNotFound(err)
}

func (b *Backend) Reconcile(ctx context.Context) error {
	if b == nil || b.client == nil {
		return nil
	}
	b.mu.Lock()
	profiles := make([]string, 0, len(b.reconciledProfiles))
	for profile := range b.reconciledProfiles {
		profiles = append(profiles, profile)
	}
	b.mu.Unlock()
	var joined error
	for _, profile := range profiles {
		joined = errors.Join(joined, b.reconcileResources(ctx, profile))
	}
	return joined
}

func (b *Backend) reconcileProfile(ctx context.Context, profile string) error {
	b.mu.Lock()
	if _, ok := b.reconciledProfiles[profile]; ok {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()
	if err := b.reconcileResources(ctx, profile); err != nil {
		return err
	}
	b.mu.Lock()
	b.reconciledProfiles[profile] = struct{}{}
	b.mu.Unlock()
	return nil
}

func (b *Backend) reconcileResources(ctx context.Context, profile string) error {
	filters := make(client.Filters).Add("label", LabelProfile+"="+profile)
	containers, err := b.client.Engine().
		ContainerList(ctx, client.ContainerListOptions{
			All:     true,
			Filters: filters,
		})
	if err != nil {
		return err
	}
	for _, unit := range containers.Items {
		if unit.Labels[LabelDaemonIncarnation] == b.daemonIncarnation {
			continue
		}
		if err := b.removeContainer(unit.ID, 3*time.Second); err != nil {
			return err
		}
	}
	networks, err := b.client.Engine().NetworkList(ctx, client.NetworkListOptions{Filters: filters})
	if err != nil {
		return err
	}
	for _, network := range networks.Items {
		if network.Labels[LabelDaemonIncarnation] == b.daemonIncarnation {
			continue
		}
		if _, err := b.client.Engine().NetworkRemove(
			ctx,
			network.ID,
			client.NetworkRemoveOptions{},
		); err != nil &&
			!errdefs.IsNotFound(err) {
			return err
		}
	}
	volumes, err := b.client.Engine().VolumeList(ctx, client.VolumeListOptions{Filters: filters})
	if err != nil {
		return err
	}
	for _, volume := range volumes.Items {
		remove := false
		scope := volume.Labels[LabelScope]
		if scope == "" {
			if volume.Labels[LabelScopeOwner] == profile {
				scope = string(execution.ScopeShared)
			} else {
				scope = string(execution.ScopeSession)
			}
		}
		switch scope {
		case string(execution.ScopeSession):
			if b.sessionExists != nil {
				exists, existsErr := b.sessionExists(ctx, volume.Labels[LabelScopeOwner])
				if existsErr != nil {
					return existsErr
				}
				remove = !exists
			}
		case string(execution.ScopeShared):
			if b.configuredScope != execution.ScopeShared && b.sharedRetention > 0 &&
				!b.sharedDisabledAt.IsZero() {
				remove = b.sharedDisabledAt.Before(time.Now().UTC().Add(-b.sharedRetention))
			}
		}
		if remove {
			_, removeErr := b.client.Engine().
				VolumeRemove(ctx, volume.Name, client.VolumeRemoveOptions{Force: false})
			if removeErr != nil && !errdefs.IsNotFound(removeErr) {
				return removeErr
			}
		}
	}
	return nil
}

func (b *Backend) Close(ctx context.Context) error {
	if b == nil || b.client == nil {
		return nil
	}
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		return nil
	}
	b.closing = true
	gates := make([]chan struct{}, 0, len(b.sharedGates))
	for _, gate := range b.sharedGates {
		gates = append(gates, gate)
	}
	b.mu.Unlock()
	b.admissionMu.Lock()
	defer b.admissionMu.Unlock()
	acquired := make([]chan struct{}, 0, len(gates))
	for _, gate := range gates {
		select {
		case <-ctx.Done():
			for _, held := range acquired {
				held <- struct{}{}
			}
			return ctx.Err()
		case <-gate:
			acquired = append(acquired, gate)
		}
	}
	defer func() {
		for _, gate := range acquired {
			gate <- struct{}{}
		}
	}()
	b.mu.Lock()
	processes := make([]*dockerProcess, 0, len(b.processes))
	for _, process := range b.processes {
		processes = append(processes, process)
	}
	environments := make([]*sharedEnvironment, 0, len(b.environments))
	for _, environment := range b.environments {
		environments = append(environments, environment)
	}
	networks := make([]string, 0, len(b.networks))
	for network := range b.networks {
		networks = append(networks, network)
	}
	b.mu.Unlock()
	var joined error
	for _, process := range processes {
		if process.shared || process.snapshot().Status != "running" {
			continue
		}
		joined = errors.Join(joined, b.removeContainer(process.containerID, 3*time.Second))
	}
	for _, environment := range environments {
		joined = errors.Join(joined, b.removeContainer(environment.containerID, 3*time.Second))
	}
	slices.Sort(networks)
	for _, network := range networks {
		_, err := b.client.Engine().NetworkRemove(ctx, network, client.NetworkRemoveOptions{})
		if err != nil && !errdefs.IsNotFound(err) {
			joined = errors.Join(joined, err)
		}
	}
	joined = errors.Join(joined, b.client.Close())
	return joined
}

func (b *Backend) removeSessionResources(
	ctx context.Context,
	profile string,
	sessionID string,
	removeWorkspace bool,
) error {
	filters := make(client.Filters).
		Add("label", LabelProfile+"="+profile).
		Add("label", LabelScopeOwner+"="+sessionID)
	containers, err := b.client.Engine().
		ContainerList(ctx, client.ContainerListOptions{
			All:     true,
			Filters: filters,
		})
	if err != nil {
		return err
	}
	var joined error
	for _, unit := range containers.Items {
		joined = errors.Join(joined, b.removeContainer(unit.ID, 3*time.Second))
	}
	networks, err := b.client.Engine().NetworkList(ctx, client.NetworkListOptions{Filters: filters})
	if err != nil {
		joined = errors.Join(joined, err)
	} else {
		for _, network := range networks.Items {
			_, removeErr := b.client.Engine().NetworkRemove(ctx, network.ID, client.NetworkRemoveOptions{})
			if removeErr != nil && !errdefs.IsNotFound(removeErr) {
				joined = errors.Join(joined, removeErr)
			}
		}
	}
	if removeWorkspace {
		name := "morph-ws-" + safeID(profile+":session:"+sessionID)
		_, removeErr := b.client.Engine().
			VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: false})
		if removeErr != nil && !errdefs.IsNotFound(removeErr) {
			joined = errors.Join(joined, removeErr)
		}
	}
	return joined
}
