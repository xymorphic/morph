package automation

import (
	"context"
	"errors"

	coreautomation "github.com/xymorphic/morph/internal/automation"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
)

type automationCommandClientStub struct {
	api *automationCommandAPIStub
}

func (s *automationCommandClientStub) Close() error {
	return nil
}

func (s *automationCommandClientStub) AutomationAPI() rpcclient.AutomationAPI {
	return s.api
}

type automationCommandAPIStub struct {
	status    coreautomation.Status
	added     coreautomation.Job
	jobs      []coreautomation.Job
	patch     coreautomation.JobPatch
	removedID string
	run       coreautomation.Run
	jobQuery  coreautomation.JobQuery
	runQuery  coreautomation.RunQuery
	runs      []coreautomation.Run
	statusErr error
	listErr   error
	addErr    error
	updateErr error
	removeErr error
	runErr    error
	runsErr   error
}

func (s *automationCommandAPIStub) Status(context.Context) (coreautomation.Status, error) {
	if s.statusErr != nil {
		return coreautomation.Status{}, s.statusErr
	}

	return s.status, nil
}

func (s *automationCommandAPIStub) List(_ context.Context, query coreautomation.JobQuery) (coreautomation.JobList, error) {
	if s.listErr != nil {
		return coreautomation.JobList{}, s.listErr
	}

	s.jobQuery = query
	if s.jobs != nil {
		return coreautomation.JobList{Jobs: s.jobs}, nil
	}

	return coreautomation.JobList{Jobs: []coreautomation.Job{s.added}}, nil
}

func (s *automationCommandAPIStub) Add(
	_ context.Context,
	job coreautomation.Job,
) (coreautomation.Job, error) {
	if s.addErr != nil {
		return coreautomation.Job{}, s.addErr
	}

	s.added = job
	if s.added.ID == "" {
		s.added.ID = testAutomationCommandJobID
	}

	return s.added, nil
}

func (s *automationCommandAPIStub) Update(
	_ context.Context,
	patch coreautomation.JobPatch,
) (coreautomation.Job, error) {
	if s.updateErr != nil {
		return coreautomation.Job{}, s.updateErr
	}

	s.patch = patch
	enabled := false
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}

	return coreautomation.Job{ID: patch.ID, Enabled: enabled}, nil
}

func (s *automationCommandAPIStub) Remove(_ context.Context, id string) error {
	if s.removeErr != nil {
		return s.removeErr
	}

	s.removedID = id

	return nil
}

func (s *automationCommandAPIStub) Run(_ context.Context, id string) (coreautomation.Run, error) {
	if s.runErr != nil {
		return coreautomation.Run{}, s.runErr
	}

	if s.run.ID == "" {
		s.run = coreautomation.Run{ID: testAutomationCommandRunID, JobID: id, Status: coreautomation.RunStatusOK}
	}

	return s.run, nil
}

func (s *automationCommandAPIStub) Runs(
	_ context.Context,
	query coreautomation.RunQuery,
) (coreautomation.RunList, error) {
	if s.runsErr != nil {
		return coreautomation.RunList{}, s.runsErr
	}

	s.runQuery = query

	return coreautomation.RunList{Runs: s.runs}, nil
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
