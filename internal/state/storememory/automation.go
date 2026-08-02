package storememory

import (
	"context"
	"errors"
	"sort"
	"time"

	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *Store) CreateJob(_ context.Context, job state.AutomationJob) (state.AutomationJob, error) {
	if s == nil {
		return state.AutomationJob{}, errors.New("store is required")
	}

	now := time.Now().UTC()
	job = job.Clone()
	jobID := str.String(job.ID)
	job.ID = jobID.Trim()
	if job.ID == "" {
		job.ID = nanoid.MustGenerate(state.AutomationJobIDPrefix)
	}
	if err := state.ValidateAutomationJobID(job.ID); err != nil {
		return state.AutomationJob{}, err
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	} else {
		job.CreatedAt = job.CreatedAt.UTC()
	}
	job.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.automationJobs == nil {
		s.automationJobs = make(map[string]state.AutomationJob)
	}
	if _, ok := s.automationJobs[job.ID]; ok {
		return state.AutomationJob{}, errors.New("automation job already exists")
	}
	s.automationJobs[job.ID] = job.Clone()

	return job.Clone(), nil
}

func (s *Store) GetJob(_ context.Context, id string) (state.AutomationJob, bool, error) {
	if s == nil {
		return state.AutomationJob{}, false, errors.New("store is required")
	}

	jobID := str.String(id)
	id = jobID.Trim()
	if err := state.ValidateAutomationJobID(id); err != nil {
		return state.AutomationJob{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.automationJobs[id]
	return job.Clone(), ok, nil
}

func (s *Store) ListJobs(_ context.Context, query state.AutomationJobQuery) (state.AutomationJobResult, error) {
	if s == nil {
		return state.AutomationJobResult{}, errors.New("store is required")
	}

	idSet, err := automationIDSet(query.IDs, state.ValidateAutomationJobID)
	if err != nil {
		return state.AutomationJobResult{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]state.AutomationJob, 0, len(s.automationJobs))
	for _, job := range s.automationJobs {
		if !automationJobMatchesQuery(job, query, idSet) {
			continue
		}

		jobs = append(jobs, job.Clone())
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		left := jobs[i].State.NextRunAt
		right := jobs[j].State.NextRunAt
		if !left.IsZero() && !right.IsZero() && !left.Equal(right) {
			return left.Before(right)
		}
		if left.IsZero() != right.IsZero() {
			return !left.IsZero()
		}
		if !jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	if query.Limit > 0 && len(jobs) > query.Limit {
		jobs = jobs[:query.Limit]
	}

	return state.AutomationJobResult{Jobs: jobs}, nil
}

func (s *Store) PatchJob(_ context.Context, patch state.AutomationJobPatch) (state.AutomationJob, error) {
	if s == nil {
		return state.AutomationJob{}, errors.New("store is required")
	}

	patchID := str.String(patch.ID)
	patch.ID = patchID.Trim()
	if err := state.ValidateAutomationJobID(patch.ID); err != nil {
		return state.AutomationJob{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.automationJobs[patch.ID]
	if !ok {
		return state.AutomationJob{}, errors.New("automation job not found")
	}
	job = state.ApplyAutomationJobPatch(job, patch, time.Now().UTC())
	s.automationJobs[patch.ID] = job.Clone()

	return job.Clone(), nil
}

func (s *Store) DeleteJob(_ context.Context, id string) error {
	if s == nil {
		return errors.New("store is required")
	}

	jobID := str.String(id)
	id = jobID.Trim()
	if err := state.ValidateAutomationJobID(id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.automationJobs, id)

	return nil
}

func (s *Store) CreateRun(_ context.Context, run state.AutomationRun) (state.AutomationRun, error) {
	if s == nil {
		return state.AutomationRun{}, errors.New("store is required")
	}

	now := time.Now().UTC()
	run = run.Clone()
	runID := str.String(run.ID)
	run.ID = runID.Trim()
	if run.ID == "" {
		run.ID = nanoid.MustGenerate(state.AutomationRunIDPrefix)
	}
	if err := state.ValidateAutomationRunID(run.ID); err != nil {
		return state.AutomationRun{}, err
	}
	jobID := str.String(run.JobID)
	run.JobID = jobID.Trim()
	if err := state.ValidateAutomationJobID(run.JobID); err != nil {
		return state.AutomationRun{}, err
	}
	if run.Status == "" {
		run.Status = state.AutomationRunStatusRunning
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	} else {
		run.StartedAt = run.StartedAt.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.automationRuns == nil {
		s.automationRuns = make(map[string]state.AutomationRun)
	}
	if _, ok := s.automationJobs[run.JobID]; !ok {
		return state.AutomationRun{}, errors.New("automation job not found")
	}
	if _, ok := s.automationRuns[run.ID]; ok {
		return state.AutomationRun{}, errors.New("automation run already exists")
	}
	s.automationRuns[run.ID] = run.Clone()

	return run.Clone(), nil
}

func (s *Store) FinishRun(_ context.Context, patch state.AutomationRunPatch) (state.AutomationRun, error) {
	if s == nil {
		return state.AutomationRun{}, errors.New("store is required")
	}

	runID := str.String(patch.ID)
	patch.ID = runID.Trim()
	if err := state.ValidateAutomationRunID(patch.ID); err != nil {
		return state.AutomationRun{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.automationRuns[patch.ID]
	if !ok {
		return state.AutomationRun{}, errors.New("automation run not found")
	}
	run = state.ApplyAutomationRunPatch(run, patch, time.Now().UTC())
	s.automationRuns[run.ID] = run.Clone()

	return run.Clone(), nil
}

func (s *Store) ListRuns(_ context.Context, query state.AutomationRunQuery) (state.AutomationRunResult, error) {
	if s == nil {
		return state.AutomationRunResult{}, errors.New("store is required")
	}

	queryJobID := str.String(query.JobID)
	if id := queryJobID.Trim(); id != "" {
		if err := state.ValidateAutomationJobID(id); err != nil {
			return state.AutomationRunResult{}, err
		}

		query.JobID = id
	}
	idSet, err := automationIDSet(query.IDs, state.ValidateAutomationRunID)
	if err != nil {
		return state.AutomationRunResult{}, err
	}
	statusSet := state.AutomationRunStatusSet(query.Status)

	s.mu.RLock()
	defer s.mu.RUnlock()

	runs := make([]state.AutomationRun, 0, len(s.automationRuns))
	for _, run := range s.automationRuns {
		if !automationRunMatchesQuery(run, query, idSet, statusSet) {
			continue
		}

		runs = append(runs, run.Clone())
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		}

		return runs[i].ID < runs[j].ID
	})
	if query.Limit > 0 && len(runs) > query.Limit {
		runs = runs[:query.Limit]
	}

	return state.AutomationRunResult{Runs: runs}, nil
}

func (s *Store) DeleteRuns(_ context.Context, query state.AutomationRunDeleteQuery) (int, error) {
	if s == nil {
		return 0, errors.New("store is required")
	}

	if !state.HasAutomationRunDeleteFilter(query) {
		return 0, errors.New("automation run delete query requires a filter")
	}

	queryJobID := str.String(query.JobID)
	if id := queryJobID.Trim(); id != "" {
		if err := state.ValidateAutomationJobID(id); err != nil {
			return 0, err
		}

		query.JobID = id
	}

	idSet, err := automationIDSet(query.IDs, state.ValidateAutomationRunID)
	if err != nil {
		return 0, err
	}

	statusSet := state.AutomationRunStatusSet(query.Status)

	s.mu.Lock()
	defer s.mu.Unlock()

	runs := make([]state.AutomationRun, 0, len(s.automationRuns))
	for _, run := range s.automationRuns {
		if !automationRunMatchesDeleteQuery(run, query, idSet, statusSet) {
			continue
		}

		runs = append(runs, run.Clone())
	}

	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.Before(runs[j].StartedAt)
		}

		return runs[i].ID < runs[j].ID
	})

	if query.Limit > 0 && len(runs) > query.Limit {
		runs = runs[:query.Limit]
	}

	for _, run := range runs {
		delete(s.automationRuns, run.ID)
	}

	return len(runs), nil
}

func automationIDSet(ids []string, validate func(string) error) (map[string]struct{}, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	values := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := str.String(rawID)
		trimmedID := id.Trim()
		if trimmedID == "" {
			continue
		}
		if err := validate(trimmedID); err != nil {
			return nil, err
		}
		values[trimmedID] = struct{}{}
	}

	return values, nil
}

func automationJobMatchesQuery(job state.AutomationJob, query state.AutomationJobQuery, ids map[string]struct{}) bool {
	if len(ids) > 0 {
		if _, ok := ids[job.ID]; !ok {
			return false
		}
	}
	if query.Enabled != nil && job.Enabled != *query.Enabled {
		return false
	}
	if !query.IncludeDisabled && query.Enabled == nil && !job.Enabled {
		return false
	}
	queryProfile := str.String(query.Profile)
	if profile := queryProfile.Trim(); profile != "" && job.Profile != profile {
		return false
	}
	queryTarget := str.String(query.SessionTarget)
	if target := queryTarget.Trim(); target != "" && job.SessionTarget != target {
		return false
	}
	return true
}

func automationRunMatchesQuery(
	run state.AutomationRun,
	query state.AutomationRunQuery,
	ids map[string]struct{},
	statuses map[state.AutomationRunStatus]struct{},
) bool {
	if query.JobID != "" && run.JobID != query.JobID {
		return false
	}

	if len(ids) > 0 {
		if _, ok := ids[run.ID]; !ok {
			return false
		}
	}
	if len(statuses) > 0 {
		if _, ok := statuses[run.Status]; !ok {
			return false
		}
	}
	return true
}

func automationRunMatchesDeleteQuery(
	run state.AutomationRun,
	query state.AutomationRunDeleteQuery,
	ids map[string]struct{},
	statuses map[state.AutomationRunStatus]struct{},
) bool {
	if query.JobID != "" && run.JobID != query.JobID {
		return false
	}

	if len(ids) > 0 {
		if _, ok := ids[run.ID]; !ok {
			return false
		}
	}
	if len(statuses) > 0 {
		if _, ok := statuses[run.Status]; !ok {
			return false
		}
	}
	if !query.StartedBefore.IsZero() && !run.StartedAt.Before(query.StartedBefore.UTC()) {
		return false
	}
	return true
}
