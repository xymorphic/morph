package automation

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/profile"
	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *Service) prepareAddedJob(job Job, now time.Time) (Job, error) {
	job = job.Clone()
	if job.ID != "" {
		if err := ValidateJobID(job.ID); err != nil {
			return Job{}, err
		}
	}

	job.Name = strings.TrimSpace(job.Name)
	job.Description = strings.TrimSpace(job.Description)

	schedule, err := s.prepareAutomationSchedule(job.Schedule, now)
	if err != nil {
		return Job{}, err
	}
	job.Schedule = schedule

	payload, err := preparePayload(job.Payload)
	if err != nil {
		return Job{}, err
	}
	job.Payload = payload

	delivery, err := prepareAutomationDelivery(job, job.Delivery)
	if err != nil {
		return Job{}, err
	}
	job.Delivery = delivery

	profileName, err := prepareAutomationProfile(job.Profile)
	if err != nil {
		return Job{}, err
	}
	job.Profile = profileName

	sessionTarget, err := prepareAutomationSessionTarget(job.SessionTarget)
	if err != nil {
		return Job{}, err
	}
	job.SessionTarget = sessionTarget

	jobState, err := prepareAutomationJobState(job.State)
	if err != nil {
		return Job{}, err
	}
	job.State = jobState

	return s.prepareJobSchedule(job, now)
}

func (s *Service) prepareAutomationJobPatch(
	current Job,
	patch JobPatch,
	now time.Time,
) (JobPatch, error) {
	candidate := state.ApplyAutomationJobPatch(current, patch, current.UpdatedAt)

	if patch.Name != nil {
		value := strings.TrimSpace(candidate.Name)
		patch.Name = &value
		candidate.Name = value
	}
	if patch.Description != nil {
		value := strings.TrimSpace(candidate.Description)
		patch.Description = &value
		candidate.Description = value
	}
	schedule, err := s.prepareAutomationSchedule(candidate.Schedule, now)
	if err != nil {
		return JobPatch{}, err
	}
	candidate.Schedule = schedule
	if patch.Schedule != nil {
		patch.Schedule = &schedule
	}
	payload, err := preparePayload(candidate.Payload)
	if err != nil {
		return JobPatch{}, err
	}
	candidate.Payload = payload
	if patch.Payload != nil {
		patch.Payload = &payload
	}
	delivery, err := prepareAutomationDelivery(candidate, candidate.Delivery)
	if err != nil {
		return JobPatch{}, err
	}
	candidate.Delivery = delivery
	if patch.Delivery != nil {
		patch.Delivery = &delivery
	}
	profileName, err := prepareAutomationProfile(candidate.Profile)
	if err != nil {
		return JobPatch{}, err
	}
	candidate.Profile = profileName
	if patch.Profile != nil {
		patch.Profile = &profileName
	}
	sessionTarget, err := prepareAutomationSessionTarget(candidate.SessionTarget)
	if err != nil {
		return JobPatch{}, err
	}
	candidate.SessionTarget = sessionTarget
	if patch.SessionTarget != nil {
		patch.SessionTarget = &sessionTarget
	}
	jobState, err := prepareAutomationJobState(candidate.State)
	if err != nil {
		return JobPatch{}, err
	}
	candidate.State = jobState
	if patch.State != nil {
		patch.State = &jobState
	}

	if checkJobPatchNeedsScheduleRepair(patch) {
		prepared, err := s.prepareJobSchedule(candidate, now)
		if err != nil {
			return JobPatch{}, err
		}
		patch.State = &prepared.State
	}

	return patch, nil
}

func (s *Service) prepareAutomationSchedule(
	schedule Schedule,
	now time.Time,
) (Schedule, error) {
	kind := str.String(schedule.Kind)
	schedule.Kind = ScheduleKind(kind.Normalized())
	schedule.Cron = strings.Join(strings.Fields(schedule.Cron), " ")
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	if !schedule.At.IsZero() {
		schedule.At = schedule.At.UTC()
	}

	switch schedule.Kind {
	case ScheduleAt:
		if schedule.At.IsZero() {
			return Schedule{}, errors.New("automation one-shot schedule time is required")
		}
		if schedule.Every != 0 || schedule.Cron != "" || schedule.Timezone != "" {
			return Schedule{}, errors.New("automation one-shot schedule cannot include interval or cron fields")
		}
	case ScheduleEvery:
		if schedule.Every <= 0 {
			return Schedule{}, errors.New("automation interval schedule must be greater than zero")
		}
		if !schedule.At.IsZero() || schedule.Cron != "" || schedule.Timezone != "" {
			return Schedule{}, errors.New("automation interval schedule cannot include one-shot or cron fields")
		}
	case ScheduleCron:
		if schedule.Cron == "" {
			return Schedule{}, errors.New("automation cron schedule expression is required")
		}
		if !schedule.At.IsZero() || schedule.Every != 0 {
			return Schedule{}, errors.New("automation cron schedule cannot include one-shot or interval fields")
		}
	case "":
		return Schedule{}, errors.New("automation schedule kind is required")
	default:
		return Schedule{}, fmt.Errorf("unsupported automation schedule kind %q", schedule.Kind)
	}

	_, err := NextRun(schedule, NextRunOptions{
		Now:             now,
		Location:        s.location,
		DefaultTimezone: s.defaultTimezone,
	})
	if err != nil {
		return Schedule{}, err
	}

	return schedule, nil
}

func normalizeAutomationPayload(payload Payload) Payload {
	payload = payload.Clone()
	kind := str.String(payload.Kind)
	provider := str.String(payload.Provider)
	payload.Kind = PayloadKind(kind.Normalized())
	payload.Prompt = strings.TrimSpace(payload.Prompt)
	payload.SystemEvent = strings.TrimSpace(payload.SystemEvent)
	payload.Model = strings.TrimSpace(payload.Model)
	payload.Provider = provider.Normalized()
	payload.BaseURL = strings.TrimSpace(payload.BaseURL)
	payload.ToolGroups = normalizeToolGroups(payload.ToolGroups)

	return payload
}

func checkAutomationPayloadContent(payload Payload) error {
	switch payload.Kind {
	case PayloadPrompt:
		if payload.Prompt == "" {
			return errors.New("automation prompt payload is required")
		}
		if payload.SystemEvent != "" {
			return errors.New("automation prompt payload cannot include a system event")
		}
	case PayloadSystemEvent:
		if payload.SystemEvent == "" {
			return errors.New("automation system event payload is required")
		}
		if payload.Prompt != "" {
			return errors.New("automation system event payload cannot include a prompt")
		}
	default:
		return fmt.Errorf("unsupported automation payload kind %q", payload.Kind)
	}

	return nil
}

func checkAutomationPayloadLimits(payload Payload) error {
	if payload.MaxIterations < 0 {
		return errors.New("automation max iterations must be non-negative")
	}
	if payload.MaxRuntime < 0 {
		return errors.New("automation max runtime must be non-negative")
	}
	if payload.RetryAttempts < 0 {
		return errors.New("automation retry attempts must be non-negative")
	}
	if payload.RetryBackoff < 0 {
		return errors.New("automation retry backoff must be non-negative")
	}
	if payload.RetryMaxDelay < 0 {
		return errors.New("automation retry max delay must be non-negative")
	}

	return nil
}

func prepareAutomationDelivery(job Job, delivery Delivery) (Delivery, error) {
	if delivery.FailureAfter < 0 {
		return Delivery{}, errors.New("automation delivery failure threshold must be non-negative")
	}
	if delivery.FailureCooldown < 0 {
		return Delivery{}, errors.New("automation delivery failure cooldown must be non-negative")
	}

	delivery = normalizeDelivery(delivery)
	switch delivery.Mode {
	case DeliveryNone, DeliveryLocal:
		if hasAutomationDeliveryRoute(delivery) {
			return Delivery{}, fmt.Errorf("automation %s delivery cannot include routing fields", delivery.Mode)
		}
	case DeliveryGateway:
		if delivery.Channel == "" {
			return Delivery{}, errors.New("automation gateway delivery channel is required")
		}
		if delivery.Target == "" {
			return Delivery{}, errors.New("automation gateway delivery target is required")
		}
		if delivery.WebhookURL != "" {
			return Delivery{}, errors.New("automation gateway delivery cannot include a webhook URL")
		}
	case DeliveryOrigin:
		target := getDeliveryTarget(job, delivery)
		if target.Channel == "" {
			return Delivery{}, errors.New("automation origin delivery channel is required")
		}
		if target.Target == "" {
			return Delivery{}, errors.New("automation origin delivery target is required")
		}
		if delivery.WebhookURL != "" {
			return Delivery{}, errors.New("automation origin delivery cannot include a webhook URL")
		}
	case DeliveryWebhook:
		if delivery.Channel != "" || delivery.Target != "" || delivery.ThreadID != "" {
			return Delivery{}, errors.New("automation webhook delivery cannot include gateway routing fields")
		}
		if err := checkAutomationWebhookURL(delivery.WebhookURL); err != nil {
			return Delivery{}, err
		}
	default:
		return Delivery{}, fmt.Errorf("unsupported automation delivery mode %q", delivery.Mode)
	}

	return delivery, nil
}

func hasAutomationDeliveryRoute(delivery Delivery) bool {
	return delivery.Channel != "" ||
		delivery.Target != "" ||
		delivery.ThreadID != "" ||
		delivery.WebhookURL != ""
}

func checkAutomationWebhookURL(value string) error {
	if value == "" {
		return errors.New("automation webhook URL is required")
	}

	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return errors.New("automation webhook URL must be an absolute HTTP or HTTPS URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.Host == "" || (scheme != "http" && scheme != "https") {
		return errors.New("automation webhook URL must be an absolute HTTP or HTTPS URL")
	}

	return nil
}

func prepareAutomationProfile(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	return profile.NormalizeName(value)
}

func prepareAutomationSessionTarget(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	target, err := parseSessionTarget(str.String(value))
	if err != nil {
		return "", err
	}
	if target.Kind == SessionTargetPrefix {
		return target.Kind + target.SessionID, nil
	}

	return target.Kind, nil
}

func prepareAutomationJobState(jobState JobState) (JobState, error) {
	switch jobState.LastStatus {
	case "", RunStatusRunning, RunStatusOK, RunStatusError, RunStatusSkipped:
	default:
		return JobState{}, fmt.Errorf("unsupported automation run status %q", jobState.LastStatus)
	}
	if jobState.LastDuration < 0 {
		return JobState{}, errors.New("automation last duration must be non-negative")
	}
	if jobState.ConsecutiveErrors < 0 {
		return JobState{}, errors.New("automation consecutive errors must be non-negative")
	}

	jobState.NextRunAt = jobState.NextRunAt.UTC()
	jobState.RunningAt = jobState.RunningAt.UTC()
	jobState.LastRunAt = jobState.LastRunAt.UTC()
	jobState.LastFailureNoticeAt = jobState.LastFailureNoticeAt.UTC()

	return jobState, nil
}
