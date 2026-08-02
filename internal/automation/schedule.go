package automation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultScheduleErrorDisableAfter = 3
)

var standardCronParser = cron.NewParser(
	cron.Minute |
		cron.Hour |
		cron.Dom |
		cron.Month |
		cron.Dow |
		cron.Descriptor,
)

type ParseScheduleOptions struct {
	Now             time.Time
	Location        *time.Location
	DefaultTimezone string
}

type NextRunOptions struct {
	Now             time.Time
	Anchor          time.Time
	LastRunAt       time.Time
	Location        *time.Location
	DefaultTimezone string
}

type NextRunResult struct {
	NextRunAt time.Time
	Due       bool
	Done      bool
}

type JobScheduleEvaluation struct {
	Job       Job
	NextRunAt time.Time
	Due       bool
	Done      bool
}

type ScheduleErrorOptions struct {
	Now          time.Time
	DisableAfter int
}

func ParseSchedule(value string, opts ParseScheduleOptions) (Schedule, error) {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return Schedule{}, errors.New("automation schedule is required")
	}

	location, err := getScheduleLocation("", opts.Location, opts.DefaultTimezone)
	if err != nil {
		return Schedule{}, err
	}

	lower := strings.ToLower(valueText)
	if after, ok := strings.CutPrefix(lower, "every "); ok {
		return parseIntervalSchedule(after)
	}

	if duration, err := time.ParseDuration(valueText); err == nil {
		if duration <= 0 {
			return Schedule{}, errors.New("automation interval schedule must be greater than zero")
		}

		return Schedule{Kind: ScheduleEvery, Every: duration}, nil
	}

	if schedule, ok, err := parseCronSchedule(valueText, location, opts.DefaultTimezone); ok || err != nil {
		return schedule, err
	}

	if at, err := parseScheduleTime(valueText, location); err == nil {
		return Schedule{Kind: ScheduleAt, At: at}, nil
	}

	return Schedule{}, fmt.Errorf("unsupported automation schedule %q", valueText)
}

func NextRun(schedule Schedule, opts NextRunOptions) (NextRunResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	switch schedule.Kind {
	case ScheduleAt:
		at := schedule.At
		if at.IsZero() {
			return NextRunResult{}, errors.New("automation one-shot schedule time is required")
		}
		if !opts.LastRunAt.IsZero() && !opts.LastRunAt.Before(at) {
			return NextRunResult{Done: true}, nil
		}
		return NextRunResult{
			NextRunAt: at.UTC(),
			Due:       !at.After(now),
		}, nil
	case ScheduleEvery:
		return nextIntervalRun(schedule, opts, now)
	case ScheduleCron:
		return nextCronRun(schedule, opts, now)
	default:
		return NextRunResult{}, errors.New("automation schedule kind is required")
	}
}

func EvaluateJob(job Job, opts NextRunOptions) (JobScheduleEvaluation, error) {
	result, err := NextRun(job.Schedule, opts)
	if err != nil {
		return JobScheduleEvaluation{Job: job.Clone()}, err
	}

	job = ApplyScheduleSuccess(job, result, opts.Now)

	return JobScheduleEvaluation{
		Job:       job,
		NextRunAt: result.NextRunAt,
		Due:       result.Due,
		Done:      result.Done,
	}, nil
}

func ApplyScheduleError(job Job, scheduleErr error, opts ScheduleErrorOptions) Job {
	job = job.Clone()
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	disableAfter := opts.DisableAfter
	if disableAfter <= 0 {
		disableAfter = defaultScheduleErrorDisableAfter
	}

	job.State.LastError = ""
	if scheduleErr != nil {
		job.State.LastError = scheduleErr.Error()
	}
	job.State.ConsecutiveErrors++
	job.UpdatedAt = now.UTC()
	if job.State.ConsecutiveErrors >= disableAfter {
		job.Enabled = false
	}

	return job
}

func ApplyScheduleSuccess(job Job, result NextRunResult, now time.Time) Job {
	job = job.Clone()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	job.State.LastError = ""
	job.State.ConsecutiveErrors = 0
	job.State.NextRunAt = result.NextRunAt.UTC()
	if result.Done {
		job.State.NextRunAt = time.Time{}
	}
	job.UpdatedAt = now.UTC()

	return job
}

func parseIntervalSchedule(value string) (Schedule, error) {
	value2 := str.String(value)
	duration, err := time.ParseDuration(value2.Trim())
	if err != nil {
		return Schedule{}, fmt.Errorf("invalid automation interval schedule: %w", err)
	}
	if duration <= 0 {
		return Schedule{}, errors.New("automation interval schedule must be greater than zero")
	}

	return Schedule{Kind: ScheduleEvery, Every: duration}, nil
}

func parseScheduleTime(value string, location *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}

	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, errors.New("invalid automation timestamp")
}

func parseCronSchedule(value string, location *time.Location, defaultTimezone string) (Schedule, bool, error) {
	defaultTimezoneValue := str.String(defaultTimezone)
	timezone := defaultTimezoneValue.Trim()
	if timezone == "" && location != nil {
		timezone = location.String()
		if _, err := time.LoadLocation(timezone); err != nil {
			timezone = ""
		}
	}
	for _, prefix := range []string{"TZ=", "CRON_TZ="} {
		if after, ok := strings.CutPrefix(value, prefix); ok {
			timezone, value, _ = strings.Cut(after, " ")
			timezoneValue := str.String(timezone)
			timezone = timezoneValue.Trim()
			value3 := str.String(value)
			value = value3.Trim()
			if timezone == "" || value == "" {
				return Schedule{}, true, errors.New("automation cron timezone and expression are required")
			}
			if _, err := time.LoadLocation(timezone); err != nil {
				return Schedule{}, true, fmt.Errorf("invalid automation cron timezone: %w", err)
			}
			break
		}
	}

	fields := strings.Fields(value)
	if len(fields) != 5 {
		return Schedule{}, false, nil
	}
	expr := strings.Join(fields, " ")
	if _, err := parseCronExpression(expr); err != nil {
		return Schedule{}, true, err
	}

	return Schedule{Kind: ScheduleCron, Cron: expr, Timezone: timezone}, true, nil
}

func nextIntervalRun(schedule Schedule, opts NextRunOptions, now time.Time) (NextRunResult, error) {
	if schedule.Every <= 0 {
		return NextRunResult{}, errors.New("automation interval schedule must be greater than zero")
	}

	anchor := opts.Anchor
	if anchor.IsZero() {
		anchor = opts.LastRunAt
	}
	if anchor.IsZero() {
		anchor = now
	}

	next := anchor
	for !next.After(now) {
		next = next.Add(schedule.Every)
	}

	return NextRunResult{NextRunAt: next.UTC()}, nil
}

func nextCronRun(schedule Schedule, opts NextRunOptions, now time.Time) (NextRunResult, error) {
	cronValue := str.String(schedule.Cron)
	expr := cronValue.Trim()
	if expr == "" {
		return NextRunResult{}, errors.New("automation cron schedule expression is required")
	}
	location, err := getScheduleLocation(schedule.Timezone, opts.Location, opts.DefaultTimezone)
	if err != nil {
		return NextRunResult{}, err
	}
	spec, err := parseCronExpression(expr)
	if err != nil {
		return NextRunResult{}, err
	}

	next := spec.Next(now.In(location))
	if next.IsZero() {
		return NextRunResult{}, errors.New("automation cron schedule has no future occurrence")
	}

	return NextRunResult{NextRunAt: next.UTC()}, nil
}

func getScheduleLocation(value string, fallback *time.Location, fallbackName string) (*time.Location, error) {
	value4 := str.String(value)
	value = value4.Trim()
	if value != "" {
		location, err := time.LoadLocation(value)
		if err != nil {
			return nil, fmt.Errorf("invalid automation schedule timezone: %w", err)
		}
		return location, nil
	}
	if fallback != nil {
		return fallback, nil
	}
	fallbackNameValue := str.String(fallbackName)
	fallbackName = fallbackNameValue.Trim()
	if fallbackName != "" {
		location, err := time.LoadLocation(fallbackName)
		if err != nil {
			return nil, fmt.Errorf("invalid automation schedule timezone: %w", err)
		}
		return location, nil
	}

	return time.Local, nil
}

func parseCronExpression(value string) (cron.Schedule, error) {
	schedule, err := standardCronParser.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid automation cron schedule: %w", err)
	}

	return schedule, nil
}
