package automation

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestParseSchedule_DurationForms(t *testing.T) {
	schedule, err := ParseSchedule("30m", ParseScheduleOptions{})
	require.NoError(t, err)
	require.Equal(t, ScheduleEvery, schedule.Kind)
	require.Equal(t, 30*time.Minute, schedule.Every)

	schedule, err = ParseSchedule("every 2h", ParseScheduleOptions{})
	require.NoError(t, err)
	require.Equal(t, ScheduleEvery, schedule.Kind)
	require.Equal(t, 2*time.Hour, schedule.Every)

	_, err = ParseSchedule("every 0s", ParseScheduleOptions{})
	require.EqualError(t, err, "automation interval schedule must be greater than zero")

	_, err = ParseSchedule("every nope", ParseScheduleOptions{})
	require.ErrorContains(t, err, "invalid automation interval schedule")

	_, err = ParseSchedule("-1s", ParseScheduleOptions{})
	require.EqualError(t, err, "automation interval schedule must be greater than zero")
}

func TestParseSchedule_TimestampsUseTimezoneFallback(t *testing.T) {
	location := time.FixedZone("Test/Zone", 2*60*60)

	schedule, err := ParseSchedule("2026-07-05T09:30:00Z", ParseScheduleOptions{Location: location})
	require.NoError(t, err)
	require.Equal(t, ScheduleAt, schedule.Kind)
	require.Equal(t, time.Date(2026, 7, 5, 9, 30, 0, 0, time.UTC), schedule.At)

	schedule, err = ParseSchedule("2026-07-05 09:30", ParseScheduleOptions{Location: location})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 5, 7, 30, 0, 0, time.UTC), schedule.At)

	schedule, err = ParseSchedule("2026-07-05T09:30", ParseScheduleOptions{DefaultTimezone: "Africa/Lagos"})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 5, 8, 30, 0, 0, time.UTC), schedule.At)
}

func TestParseSchedule_CronWithTimezone(t *testing.T) {
	schedule, err := ParseSchedule("CRON_TZ=Africa/Lagos 0 9 * * *", ParseScheduleOptions{})
	require.NoError(t, err)
	require.Equal(t, ScheduleCron, schedule.Kind)
	require.Equal(t, "0 9 * * *", schedule.Cron)
	require.Equal(t, "Africa/Lagos", schedule.Timezone)

	_, err = ParseSchedule("CRON_TZ=Missing/Zone 0 9 * * *", ParseScheduleOptions{})
	require.ErrorContains(t, err, "invalid automation cron timezone")

	schedule, err = ParseSchedule("TZ=Africa/Lagos 0 9 * * *", ParseScheduleOptions{})
	require.NoError(t, err)
	require.Equal(t, "Africa/Lagos", schedule.Timezone)

	schedule, err = ParseSchedule("0 9 * * *", ParseScheduleOptions{DefaultTimezone: "Africa/Lagos"})
	require.NoError(t, err)
	require.Equal(t, "Africa/Lagos", schedule.Timezone)

	schedule, err = ParseSchedule("0 9 * * *", ParseScheduleOptions{Location: time.FixedZone("Synthetic/Zone", 60*60)})
	require.NoError(t, err)
	require.Empty(t, schedule.Timezone)

	_, err = ParseSchedule("CRON_TZ= 0 9 * * *", ParseScheduleOptions{})
	require.ErrorContains(t, err, "automation cron timezone and expression are required")

	_, err = ParseSchedule("61 * * * *", ParseScheduleOptions{})
	require.ErrorContains(t, err, "invalid automation cron schedule")
}

func TestParseSchedule_InvalidInput(t *testing.T) {
	_, err := ParseSchedule("", ParseScheduleOptions{})
	require.EqualError(t, err, "automation schedule is required")

	_, err = ParseSchedule("not a schedule", ParseScheduleOptions{})
	require.EqualError(t, err, `unsupported automation schedule "not a schedule"`)

	_, err = ParseSchedule("0 9 * * *", ParseScheduleOptions{DefaultTimezone: "Missing/Zone"})
	require.ErrorContains(t, err, "invalid automation schedule timezone")
}

func TestNextRun_OneShotFuturePastAndDone(t *testing.T) {
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	at := now.Add(time.Hour)

	result, err := NextRun(Schedule{Kind: ScheduleAt, At: at}, NextRunOptions{Now: now})
	require.NoError(t, err)
	require.Equal(t, at, result.NextRunAt)
	require.False(t, result.Due)
	require.False(t, result.Done)

	result, err = NextRun(Schedule{Kind: ScheduleAt, At: now.Add(-time.Minute)}, NextRunOptions{Now: now})
	require.NoError(t, err)
	require.True(t, result.Due)
	require.False(t, result.Done)

	result, err = NextRun(Schedule{Kind: ScheduleAt, At: at}, NextRunOptions{
		Now:       now,
		LastRunAt: at,
	})
	require.NoError(t, err)
	require.True(t, result.Done)
	require.Zero(t, result.NextRunAt)

	_, err = NextRun(Schedule{Kind: ScheduleAt}, NextRunOptions{Now: now})
	require.EqualError(t, err, "automation one-shot schedule time is required")

	result, err = NextRun(Schedule{Kind: ScheduleAt, At: at}, NextRunOptions{})
	require.NoError(t, err)
	require.True(t, result.Due)
}

func TestNextRun_IntervalUsesStableAnchor(t *testing.T) {
	anchor := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	now := anchor.Add(95 * time.Minute)

	result, err := NextRun(Schedule{Kind: ScheduleEvery, Every: 30 * time.Minute}, NextRunOptions{
		Now:    now,
		Anchor: anchor,
	})
	require.NoError(t, err)
	require.Equal(t, anchor.Add(2*time.Hour), result.NextRunAt)
	require.False(t, result.Due)

	result, err = NextRun(Schedule{Kind: ScheduleEvery, Every: 30 * time.Minute}, NextRunOptions{
		Now:       now,
		LastRunAt: anchor,
	})
	require.NoError(t, err)
	require.Equal(t, anchor.Add(2*time.Hour), result.NextRunAt)

	result, err = NextRun(Schedule{Kind: ScheduleEvery, Every: time.Hour}, NextRunOptions{Now: now})
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Hour), result.NextRunAt)
}

func TestNextRun_CronTimezone(t *testing.T) {
	now := time.Date(2026, 7, 5, 8, 10, 0, 0, time.UTC)

	result, err := NextRun(Schedule{
		Kind:     ScheduleCron,
		Cron:     "0 9 * * *",
		Timezone: "Africa/Lagos",
	}, NextRunOptions{Now: now})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC), result.NextRunAt)

	result, err = NextRun(Schedule{
		Kind: ScheduleCron,
		Cron: "0 9 * * *",
	}, NextRunOptions{Now: now, DefaultTimezone: "Africa/Lagos"})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC), result.NextRunAt)
}

func TestNextRun_CronSupportsRangesStepsAndWeekdays(t *testing.T) {
	now := time.Date(2026, 7, 6, 8, 1, 0, 0, time.UTC)

	result, err := NextRun(Schedule{
		Kind: ScheduleCron,
		Cron: "*/15 8-9 * * 1-5",
	}, NextRunOptions{Now: now, Location: time.UTC})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 6, 8, 15, 0, 0, time.UTC), result.NextRunAt)
}

func TestEvaluateJob_RecomputesRestartState(t *testing.T) {
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	job := Job{
		ID:      nanoid.MustFromSeed(JobIDPrefix, "job", "AutomationScheduleJobSeed"),
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleEvery,
			Every: time.Hour,
		},
		State: JobState{
			NextRunAt:         now.Add(-time.Hour),
			ConsecutiveErrors: 2,
			LastError:         "old",
		},
	}

	evaluation, err := EvaluateJob(job, NextRunOptions{Now: now, Anchor: now.Add(-2 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Hour), evaluation.NextRunAt)
	require.Equal(t, evaluation.NextRunAt, evaluation.Job.State.NextRunAt)
	require.Zero(t, evaluation.Job.State.ConsecutiveErrors)
	require.Empty(t, evaluation.Job.State.LastError)
	require.True(t, evaluation.Job.Enabled)

	_, err = EvaluateJob(Job{Schedule: Schedule{}}, NextRunOptions{Now: now})
	require.EqualError(t, err, "automation schedule kind is required")
}

func TestApplyScheduleError_DisablesAfterRepeatedErrors(t *testing.T) {
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	job := Job{
		ID:      nanoid.MustFromSeed(JobIDPrefix, "job", "AutomationScheduleErrSeed"),
		Enabled: true,
		State: JobState{
			ConsecutiveErrors: 2,
		},
	}

	job = ApplyScheduleError(job, errors.New("bad schedule"), ScheduleErrorOptions{Now: now, DisableAfter: 3})

	require.False(t, job.Enabled)
	require.Equal(t, 3, job.State.ConsecutiveErrors)
	require.Equal(t, "bad schedule", job.State.LastError)
	require.Equal(t, now, job.UpdatedAt)

	job = ApplyScheduleError(Job{Enabled: true}, nil, ScheduleErrorOptions{Now: now, DisableAfter: 2})
	require.True(t, job.Enabled)
	require.Equal(t, 1, job.State.ConsecutiveErrors)
	require.Empty(t, job.State.LastError)

	job = ApplyScheduleError(Job{Enabled: true}, errors.New("default threshold"), ScheduleErrorOptions{Now: now})
	require.True(t, job.Enabled)
	require.Equal(t, 1, job.State.ConsecutiveErrors)
	require.Equal(t, "default threshold", job.State.LastError)

	job = ApplyScheduleError(Job{Enabled: true}, errors.New("uses current time"), ScheduleErrorOptions{})
	require.False(t, job.UpdatedAt.IsZero())
}

func TestApplyScheduleSuccess_ClearsNextRunWhenDone(t *testing.T) {
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	next := now.Add(time.Hour)

	job := ApplyScheduleSuccess(Job{
		State: JobState{
			LastError:         "bad",
			ConsecutiveErrors: 2,
		},
	}, NextRunResult{NextRunAt: next}, now)
	require.Equal(t, next, job.State.NextRunAt)
	require.Empty(t, job.State.LastError)
	require.Zero(t, job.State.ConsecutiveErrors)

	job = ApplyScheduleSuccess(job, NextRunResult{NextRunAt: next, Done: true}, now)
	require.Zero(t, job.State.NextRunAt)

	job = ApplyScheduleSuccess(job, NextRunResult{NextRunAt: next}, time.Time{})
	require.False(t, job.UpdatedAt.IsZero())
}

func TestNextRun_InvalidSchedulesReturnErrors(t *testing.T) {
	_, err := NextRun(Schedule{Kind: ScheduleEvery}, NextRunOptions{})
	require.EqualError(t, err, "automation interval schedule must be greater than zero")

	_, err = NextRun(Schedule{Kind: ScheduleCron, Cron: "61 * * * *"}, NextRunOptions{})
	require.ErrorContains(t, err, "invalid automation cron schedule")

	_, err = NextRun(Schedule{Kind: ScheduleCron}, NextRunOptions{})
	require.EqualError(t, err, "automation cron schedule expression is required")

	_, err = NextRun(Schedule{Kind: ScheduleCron, Cron: "0 9 * * *"}, NextRunOptions{
		DefaultTimezone: "Missing/Zone",
	})
	require.ErrorContains(t, err, "invalid automation schedule timezone")

	_, err = NextRun(Schedule{Kind: ScheduleCron, Cron: "0 0 31 2 *"}, NextRunOptions{
		Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	require.EqualError(t, err, "automation cron schedule has no future occurrence")

	_, err = getScheduleLocation("", nil, "Missing/Zone")
	require.ErrorContains(t, err, "invalid automation schedule timezone")

	_, err = getScheduleLocation("Missing/Zone", nil, "")
	require.ErrorContains(t, err, "invalid automation schedule timezone")

	_, err = NextRun(Schedule{}, NextRunOptions{})
	require.EqualError(t, err, "automation schedule kind is required")
}
