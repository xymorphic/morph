package storesqlite

import (
	"context"
	"errors"
	"time"

	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
	"gorm.io/gorm"
)

type automationJobModel struct {
	ID                string     `gorm:"column:id;primaryKey"`
	Name              string     `gorm:"column:name;not null;default:''"`
	Description       string     `gorm:"column:description;not null;default:''"`
	Enabled           bool       `gorm:"column:enabled;not null;index:idx_automation_jobs_enabled"`
	CreatedAt         time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;autoUpdateTime:false;index:idx_automation_jobs_updated_at"`
	ScheduleJSON      string     `gorm:"column:schedule_json;type:TEXT;not null;default:'{}'"`
	PayloadJSON       string     `gorm:"column:payload_json;type:TEXT;not null;default:'{}'"`
	DeliveryJSON      string     `gorm:"column:delivery_json;type:TEXT;not null;default:'{}'"`
	Profile           string     `gorm:"column:profile;not null;default:'';index:idx_automation_jobs_profile"`
	SessionTarget     string     `gorm:"column:session_target;not null;default:'';index:idx_automation_jobs_session_target"`
	DeleteAfterRun    bool       `gorm:"column:delete_after_run;not null;default:false"`
	AuthorizationJSON string     `gorm:"column:authorization_json;type:TEXT;not null;default:'{}'"`
	NextRunAt         *time.Time `gorm:"column:next_run_at;index:idx_automation_jobs_next_run_at"`
	StateJSON         string     `gorm:"column:state_json;type:TEXT;not null;default:'{}'"`
}

func (automationJobModel) TableName() string {
	return "automation_jobs"
}

type automationRunModel struct {
	ID             string     `gorm:"column:id;primaryKey"`
	JobID          string     `gorm:"column:job_id;not null;index:idx_automation_runs_job_id"`
	Status         string     `gorm:"column:status;not null;default:'';index:idx_automation_runs_status"`
	StartedAt      time.Time  `gorm:"column:started_at;autoCreateTime:false;index:idx_automation_runs_started_at"`
	EndedAt        *time.Time `gorm:"column:ended_at"`
	Duration       int64      `gorm:"column:duration;not null;default:0"`
	Output         string     `gorm:"column:output;type:TEXT;not null;default:''"`
	Error          string     `gorm:"column:error;type:TEXT;not null;default:''"`
	SessionID      string     `gorm:"column:session_id;not null;default:'';index:idx_automation_runs_session_id"`
	DeliveryStatus string     `gorm:"column:delivery_status;not null;default:''"`
	DeliveryError  string     `gorm:"column:delivery_error;type:TEXT;not null;default:''"`
	Model          string     `gorm:"column:model;not null;default:''"`
	Provider       string     `gorm:"column:provider;not null;default:''"`
	UsageJSON      string     `gorm:"column:usage_json;type:TEXT;not null;default:'{}'"`
}

func (automationRunModel) TableName() string {
	return "automation_runs"
}

func (s *Store) CreateJob(ctx context.Context, job state.AutomationJob) (state.AutomationJob, error) {
	if s == nil || s.db == nil {
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

	record := automationJobToModel(job)
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return state.AutomationJob{}, err
	}

	return job.Clone(), nil
}

func (s *Store) GetJob(ctx context.Context, id string) (state.AutomationJob, bool, error) {
	if s == nil || s.db == nil {
		return state.AutomationJob{}, false, errors.New("store is required")
	}

	jobID := str.String(id)
	id = jobID.Trim()
	if err := state.ValidateAutomationJobID(id); err != nil {
		return state.AutomationJob{}, false, err
	}

	var record automationJobModel
	err := s.db.WithContext(ctx).First(&record, "id = ?", id).Error
	switch {
	case err == nil:
		job, err := automationModelToJob(record)
		return job.Clone(), err == nil, err
	case errors.Is(err, gorm.ErrRecordNotFound):
		return state.AutomationJob{}, false, nil
	default:
		return state.AutomationJob{}, false, err
	}
}

func (s *Store) ListJobs(ctx context.Context, query state.AutomationJobQuery) (state.AutomationJobResult, error) {
	if s == nil || s.db == nil {
		return state.AutomationJobResult{}, errors.New("store is required")
	}

	ids, err := automationValidatedIDs(query.IDs, state.ValidateAutomationJobID)
	if err != nil {
		return state.AutomationJobResult{}, err
	}

	db := s.db.WithContext(ctx).Model(&automationJobModel{})
	if len(ids) > 0 {
		db = db.Where("id IN ?", ids)
	}
	if query.Enabled != nil {
		db = db.Where("enabled = ?", *query.Enabled)
	} else if !query.IncludeDisabled {
		db = db.Where("enabled = ?", true)
	}
	queryProfile := str.String(query.Profile)
	if profile := queryProfile.Trim(); profile != "" {
		db = db.Where("profile = ?", profile)
	}
	queryTarget := str.String(query.SessionTarget)
	if target := queryTarget.Trim(); target != "" {
		db = db.Where("session_target = ?", target)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}

	var records []automationJobModel
	if err := db.
		Order("CASE WHEN next_run_at IS NULL THEN 1 ELSE 0 END ASC").
		Order("next_run_at ASC").
		Order("updated_at DESC").
		Order("id ASC").
		Find(&records).
		Error; err != nil {
		return state.AutomationJobResult{}, err
	}

	jobs := make([]state.AutomationJob, 0, len(records))
	for _, record := range records {
		job, err := automationModelToJob(record)
		if err != nil {
			return state.AutomationJobResult{}, err
		}
		jobs = append(jobs, job.Clone())
	}

	return state.AutomationJobResult{Jobs: jobs}, nil
}

func (s *Store) PatchJob(ctx context.Context, patch state.AutomationJobPatch) (state.AutomationJob, error) {
	if s == nil || s.db == nil {
		return state.AutomationJob{}, errors.New("store is required")
	}

	patchID := str.String(patch.ID)
	patch.ID = patchID.Trim()
	if err := state.ValidateAutomationJobID(patch.ID); err != nil {
		return state.AutomationJob{}, err
	}

	var job state.AutomationJob
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record automationJobModel
		if err := tx.First(&record, "id = ?", patch.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("automation job not found")
			}

			return err
		}
		current, err := automationModelToJob(record)
		if err != nil {
			return err
		}
		job = state.ApplyAutomationJobPatch(current, patch, time.Now().UTC())
		return tx.Save(automationJobToModel(job)).Error
	}); err != nil {
		return state.AutomationJob{}, err
	}

	return job.Clone(), nil
}

func (s *Store) DeleteJob(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	jobID := str.String(id)
	id = jobID.Trim()
	if err := state.ValidateAutomationJobID(id); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&automationJobModel{}).Error
}

func (s *Store) CreateRun(ctx context.Context, run state.AutomationRun) (state.AutomationRun, error) {
	if s == nil || s.db == nil {
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

	var count int64
	if err := s.db.WithContext(ctx).Model(&automationJobModel{}).
		Where("id = ?", run.JobID).
		Count(&count).Error; err != nil {
		return state.AutomationRun{}, err
	}
	if count == 0 {
		return state.AutomationRun{}, errors.New("automation job not found")
	}

	record := automationRunToModel(run)
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return state.AutomationRun{}, err
	}

	return run.Clone(), nil
}

func (s *Store) FinishRun(ctx context.Context, patch state.AutomationRunPatch) (state.AutomationRun, error) {
	if s == nil || s.db == nil {
		return state.AutomationRun{}, errors.New("store is required")
	}

	runID := str.String(patch.ID)
	patch.ID = runID.Trim()
	if err := state.ValidateAutomationRunID(patch.ID); err != nil {
		return state.AutomationRun{}, err
	}

	var run state.AutomationRun
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record automationRunModel
		if err := tx.First(&record, "id = ?", patch.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("automation run not found")
			}

			return err
		}
		current, err := automationModelToRun(record)
		if err != nil {
			return err
		}
		run = state.ApplyAutomationRunPatch(current, patch, time.Now().UTC())
		return tx.Save(automationRunToModel(run)).Error
	}); err != nil {
		return state.AutomationRun{}, err
	}

	return run.Clone(), nil
}

func (s *Store) ListRuns(ctx context.Context, query state.AutomationRunQuery) (state.AutomationRunResult, error) {
	if s == nil || s.db == nil {
		return state.AutomationRunResult{}, errors.New("store is required")
	}

	ids, err := automationValidatedIDs(query.IDs, state.ValidateAutomationRunID)
	if err != nil {
		return state.AutomationRunResult{}, err
	}

	db := s.db.WithContext(ctx).Model(&automationRunModel{})
	queryJobID := str.String(query.JobID)
	if jobID := queryJobID.Trim(); jobID != "" {
		if err := state.ValidateAutomationJobID(jobID); err != nil {
			return state.AutomationRunResult{}, err
		}

		db = db.Where("job_id = ?", jobID)
	}
	if len(ids) > 0 {
		db = db.Where("id IN ?", ids)
	}
	if len(query.Status) > 0 {
		statuses := state.AutomationRunStatusesToStrings(query.Status)
		if len(statuses) > 0 {
			db = db.Where("status IN ?", statuses)
		}
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}

	var records []automationRunModel
	if err := db.Order("started_at DESC").Order("id ASC").Find(&records).Error; err != nil {
		return state.AutomationRunResult{}, err
	}

	runs := make([]state.AutomationRun, 0, len(records))
	for _, record := range records {
		run, err := automationModelToRun(record)
		if err != nil {
			return state.AutomationRunResult{}, err
		}
		runs = append(runs, run.Clone())
	}

	return state.AutomationRunResult{Runs: runs}, nil
}

func (s *Store) DeleteRuns(ctx context.Context, query state.AutomationRunDeleteQuery) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store is required")
	}

	if !state.HasAutomationRunDeleteFilter(query) {
		return 0, errors.New("automation run delete query requires a filter")
	}

	ids, err := automationValidatedIDs(query.IDs, state.ValidateAutomationRunID)
	if err != nil {
		return 0, err
	}

	db := s.db.WithContext(ctx).Model(&automationRunModel{})

	queryJobID := str.String(query.JobID).Trim()
	if queryJobID != "" {
		if err := state.ValidateAutomationJobID(queryJobID); err != nil {
			return 0, err
		}

		db = db.Where("job_id = ?", queryJobID)
	}

	if len(ids) > 0 {
		db = db.Where("id IN ?", ids)
	}

	if !query.StartedBefore.IsZero() {
		db = db.Where("started_at < ?", query.StartedBefore.UTC())
	}

	if len(query.Status) > 0 {
		statuses := state.AutomationRunStatusesToStrings(query.Status)
		if len(statuses) > 0 {
			db = db.Where("status IN ?", statuses)
		}
	}

	if query.Limit > 0 {
		subquery := db.
			Session(&gorm.Session{}).
			Select("id").
			Order("started_at ASC").
			Order("id ASC").
			Limit(query.Limit)
		db = s.db.WithContext(ctx).Where("id IN (?)", subquery)
	}

	result := db.Delete(&automationRunModel{})
	if result.Error != nil {
		return 0, result.Error
	}

	return int(result.RowsAffected), nil
}

func automationJobToModel(job state.AutomationJob) automationJobModel {
	var nextRunAt *time.Time
	if !job.State.NextRunAt.IsZero() {
		next := job.State.NextRunAt.UTC()
		nextRunAt = &next
	}
	return automationJobModel{
		ID:                job.ID,
		Name:              job.Name,
		Description:       job.Description,
		Enabled:           job.Enabled,
		CreatedAt:         job.CreatedAt,
		UpdatedAt:         job.UpdatedAt,
		ScheduleJSON:      toJSONString(job.Schedule),
		PayloadJSON:       toJSONString(job.Payload),
		DeliveryJSON:      toJSONString(job.Delivery),
		Profile:           job.Profile,
		SessionTarget:     job.SessionTarget,
		DeleteAfterRun:    job.DeleteAfterRun,
		AuthorizationJSON: toJSONString(job.Authorization),
		NextRunAt:         nextRunAt,
		StateJSON:         toJSONString(job.State),
	}
}

func automationModelToJob(record automationJobModel) (state.AutomationJob, error) {
	job := state.AutomationJob{
		ID:             record.ID,
		Name:           record.Name,
		Description:    record.Description,
		Enabled:        record.Enabled,
		CreatedAt:      record.CreatedAt.UTC(),
		UpdatedAt:      record.UpdatedAt.UTC(),
		Profile:        record.Profile,
		SessionTarget:  record.SessionTarget,
		DeleteAfterRun: record.DeleteAfterRun,
	}
	if err := fromJSONString(record.AuthorizationJSON, &job.Authorization); err != nil {
		return state.AutomationJob{}, err
	}
	if err := fromJSONString(record.ScheduleJSON, &job.Schedule); err != nil {
		return state.AutomationJob{}, err
	}
	if err := fromJSONString(record.PayloadJSON, &job.Payload); err != nil {
		return state.AutomationJob{}, err
	}
	if err := fromJSONString(record.DeliveryJSON, &job.Delivery); err != nil {
		return state.AutomationJob{}, err
	}
	if err := fromJSONString(record.StateJSON, &job.State); err != nil {
		return state.AutomationJob{}, err
	}
	return job.Clone(), nil
}

func automationRunToModel(run state.AutomationRun) automationRunModel {
	var endedAt *time.Time
	if !run.EndedAt.IsZero() {
		ended := run.EndedAt.UTC()
		endedAt = &ended
	}
	return automationRunModel{
		ID:             run.ID,
		JobID:          run.JobID,
		Status:         string(run.Status),
		StartedAt:      run.StartedAt.UTC(),
		EndedAt:        endedAt,
		Duration:       int64(run.Duration),
		Output:         run.Output,
		Error:          run.Error,
		SessionID:      run.SessionID,
		DeliveryStatus: string(run.DeliveryStatus),
		DeliveryError:  run.DeliveryError,
		Model:          run.Model,
		Provider:       run.Provider,
		UsageJSON:      toJSONString(run.Usage),
	}
}

func automationModelToRun(record automationRunModel) (state.AutomationRun, error) {
	run := state.AutomationRun{
		ID:             record.ID,
		JobID:          record.JobID,
		Status:         state.AutomationRunStatus(record.Status),
		StartedAt:      record.StartedAt.UTC(),
		Duration:       time.Duration(record.Duration),
		Output:         record.Output,
		Error:          record.Error,
		SessionID:      record.SessionID,
		DeliveryStatus: state.AutomationDeliveryStatus(record.DeliveryStatus),
		DeliveryError:  record.DeliveryError,
		Model:          record.Model,
		Provider:       record.Provider,
	}
	if record.EndedAt != nil {
		run.EndedAt = record.EndedAt.UTC()
	}
	if err := fromJSONString(record.UsageJSON, &run.Usage); err != nil {
		return state.AutomationRun{}, err
	}
	return run.Clone(), nil
}

func automationValidatedIDs(ids []string, validate func(string) error) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	values := make([]string, 0, len(ids))
	for _, rawID := range ids {
		id := str.String(rawID)
		trimmedID := id.Trim()
		if trimmedID == "" {
			continue
		}
		if err := validate(trimmedID); err != nil {
			return nil, err
		}
		values = append(values, trimmedID)
	}

	return values, nil
}
