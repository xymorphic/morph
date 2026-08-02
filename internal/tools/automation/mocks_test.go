package automation

import (
	"context"

	storage "github.com/xymorphic/morph/internal/state/core"
)

type automationToolServiceStub struct {
	store     storage.AutomationStore
	listErr   error
	addErr    error
	updateErr error
	removeErr error
	runErr    error
	runsErr   error
	runID     string
	run       storage.AutomationRun
}

func (s *automationToolServiceStub) List(
	ctx context.Context,
	query storage.AutomationJobQuery,
) (storage.AutomationJobResult, error) {
	if s.listErr != nil {
		return storage.AutomationJobResult{}, s.listErr
	}

	return s.store.ListJobs(ctx, query)
}

func (s *automationToolServiceStub) Add(
	ctx context.Context,
	job storage.AutomationJob,
) (storage.AutomationJob, error) {
	if s.addErr != nil {
		return storage.AutomationJob{}, s.addErr
	}

	return s.store.CreateJob(ctx, job)
}

func (s *automationToolServiceStub) Update(
	ctx context.Context,
	patch storage.AutomationJobPatch,
) (storage.AutomationJob, error) {
	if s.updateErr != nil {
		return storage.AutomationJob{}, s.updateErr
	}

	return s.store.PatchJob(ctx, patch)
}

func (s *automationToolServiceStub) Remove(ctx context.Context, id string) error {
	if s.removeErr != nil {
		return s.removeErr
	}

	return s.store.DeleteJob(ctx, id)
}

func (s *automationToolServiceStub) Run(
	_ context.Context,
	id string,
) (storage.AutomationRun, error) {
	s.runID = id
	return s.run, s.runErr
}

func (s *automationToolServiceStub) Runs(
	ctx context.Context,
	query storage.AutomationRunQuery,
) (storage.AutomationRunResult, error) {
	if s.runsErr != nil {
		return storage.AutomationRunResult{}, s.runsErr
	}

	return s.store.ListRuns(ctx, query)
}
