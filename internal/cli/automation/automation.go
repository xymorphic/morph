package automation

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	cli "github.com/urfave/cli/v3"

	coreautomation "github.com/xymorphic/morph/internal/automation"
	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	automationOutput io.Writer = os.Stdout
	newClient                  = func(ctx context.Context, cfg *config.Config) (automationClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address:           cfg.RPC.Address,
			Port:              cfg.RPC.Port,
			PermissionSurface: permissions.SurfaceCLI,
			PermissionPreset:  cfg.Permissions.EffectivePreset(),
			AuthServices:      []string{"/morph.v1.AutomationService"},
		}, cfg))
	}
)

type automationClient interface {
	Close() error
	AutomationAPI() rpcclient.AutomationAPI
}

func SetOutput(w io.Writer) io.Writer {
	previous := automationOutput
	if w == nil {
		automationOutput = io.Discard
		return previous
	}
	automationOutput = w
	return previous
}

func NewStatusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show automation scheduler status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			status, err := api.Status(ctx)
			if err != nil {
				return err
			}

			return writeAutomationStatus(status)
		},
	}
}

func NewListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List automation jobs",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "Include disabled jobs"},
			&cli.IntFlag{Name: "limit", Usage: "Maximum jobs to list"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			list, err := api.List(ctx, coreautomation.JobQuery{
				IncludeDisabled: cmd.Bool("all"),
				Limit:           cmd.Int("limit"),
			})
			if err != nil {
				return err
			}

			return writeJobList(list.Jobs)
		},
	}
}

func NewAddCommand() *cli.Command {
	return &cli.Command{
		Name:  "add",
		Usage: "Add an automation job",
		Flags: jobMutationFlags(true),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			job, err := jobFromCommand(cmd)
			if err != nil {
				return err
			}
			created, err := api.Add(ctx, job)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(automationOutput, created.ID)
			return err
		},
	}
}

func NewUpdateCommand() *cli.Command {
	return &cli.Command{
		Name:      "update",
		Usage:     "Update an automation job",
		ArgsUsage: "<job-id>",
		Flags:     jobMutationFlags(false),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			current, err := fetchAutomationJobForUpdate(ctx, api, cmd)
			if err != nil {
				return err
			}
			patch, err := patchFromCommand(cmd, current)
			if err != nil {
				return err
			}
			updated, err := api.Update(ctx, patch)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(automationOutput, updated.ID)
			return err
		},
	}
}

func newPauseCommand() *cli.Command {
	return toggleCommand("pause", "Pause an automation job", false)
}

func newResumeCommand() *cli.Command {
	return toggleCommand("resume", "Resume an automation job", true)
}

func NewPauseCommand() *cli.Command {
	return newPauseCommand()
}

func NewResumeCommand() *cli.Command {
	return newResumeCommand()
}

func NewRunCommand() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "Run an automation job now",
		ArgsUsage: "<job-id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			id, err := getRequiredArg(cmd, "automation job id is required")
			if err != nil {
				return err
			}
			run, err := api.Run(ctx, id)
			if err != nil {
				return err
			}

			return writeRunSummary(run)
		},
	}
}

func NewRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove an automation job",
		ArgsUsage: "<job-id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			id, err := getRequiredArg(cmd, "automation job id is required")
			if err != nil {
				return err
			}
			if err := api.Remove(ctx, id); err != nil {
				return err
			}

			_, err = fmt.Fprintln(automationOutput, id)
			return err
		},
	}
}

func NewRunsCommand() *cli.Command {
	return &cli.Command{
		Name:  "runs",
		Usage: "List automation runs",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "job", Usage: "Filter by automation job id"},
			&cli.StringFlag{Name: "status", Usage: "Comma-separated run statuses"},
			&cli.IntFlag{Name: "limit", Usage: "Maximum runs to list"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			list, err := api.Runs(ctx, coreautomation.RunQuery{
				JobID:  strings.TrimSpace(cmd.String("job")),
				Status: parseRunStatuses(cmd.String("status")),
				Limit:  cmd.Int("limit"),
			})
			if err != nil {
				return err
			}
			return writeRunList(list.Runs)
		},
	}
}

func toggleCommand(name string, usage string, enabled bool) *cli.Command {
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		ArgsUsage: "<job-id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			id, err := getRequiredArg(cmd, "automation job id is required")
			if err != nil {
				return err
			}
			job, err := api.Update(ctx, coreautomation.JobPatch{ID: id, Enabled: &enabled})
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(automationOutput, "%s enabled=%t\n", job.ID, job.Enabled)
			return err
		},
	}
}

func jobMutationFlags(add bool) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "name", Usage: "Job name"},
		&cli.StringFlag{Name: "description", Usage: "Job description"},
		&cli.StringFlag{Name: "schedule", Usage: "Schedule expression: RFC3339 time, duration, every <duration>, or cron"},
		&cli.StringFlag{Name: "prompt", Usage: "Prompt payload"},
		&cli.StringFlag{Name: "system-event", Usage: "System event payload"},
		&cli.StringFlag{Name: "profile", Usage: "Profile to run with"},
		&cli.StringFlag{Name: "session-target", Usage: "Session target: isolated, main, current, origin, or session:<id>"},
		&cli.StringFlag{Name: "model", Usage: "Model override"},
		&cli.StringFlag{Name: "provider", Usage: "Provider override"},
		&cli.StringFlag{Name: "base-url", Usage: "Provider base URL override"},
		&cli.StringSliceFlag{Name: "tool-group", Usage: "Allowed tool group"},
		&cli.DurationFlag{Name: "max-runtime", Usage: "Per-run timeout"},
		&cli.BoolFlag{Name: "no-timeout", Usage: "Disable run timeout"},
		&cli.IntFlag{Name: "max-iterations", Usage: "Maximum agent iterations"},
		&cli.IntFlag{Name: "retry-attempts", Usage: "Retry attempts"},
		&cli.DurationFlag{Name: "retry-backoff", Usage: "Initial retry backoff"},
		&cli.DurationFlag{Name: "retry-max-delay", Usage: "Maximum retry delay"},
		&cli.StringFlag{Name: "delivery", Usage: "Delivery mode: none, local, origin, gateway, or webhook"},
		&cli.StringFlag{Name: "channel", Usage: "Delivery channel"},
		&cli.StringFlag{Name: "target", Usage: "Delivery target"},
		&cli.StringFlag{Name: "thread", Usage: "Delivery thread id"},
		&cli.StringFlag{Name: "webhook-url", Usage: "Webhook URL"},
		&cli.BoolFlag{Name: "best-effort", Usage: "Do not fail run when delivery fails"},
		&cli.BoolFlag{Name: "delete-after-run", Usage: "Delete job after a successful run"},
	}
	if add {
		flags = append(flags, &cli.BoolFlag{Name: "disabled", Usage: "Create the job disabled"})
	}

	return flags
}

func jobFromCommand(cmd *cli.Command) (coreautomation.Job, error) {
	schedule, err := parseCommandSchedule(cmd)
	if err != nil {
		return coreautomation.Job{}, err
	}
	delivery, err := deliveryFromCommand(cmd)
	if err != nil {
		return coreautomation.Job{}, err
	}

	return coreautomation.Job{
		Name:           strings.TrimSpace(cmd.String("name")),
		Description:    strings.TrimSpace(cmd.String("description")),
		Enabled:        !cmd.Bool("disabled"),
		Schedule:       schedule,
		Payload:        payloadFromCommand(cmd),
		Delivery:       delivery,
		Profile:        strings.TrimSpace(cmd.String("profile")),
		SessionTarget:  strings.TrimSpace(cmd.String("session-target")),
		DeleteAfterRun: cmd.Bool("delete-after-run"),
	}, nil
}

func patchFromCommand(
	cmd *cli.Command,
	current coreautomation.Job,
) (coreautomation.JobPatch, error) {
	id, err := getRequiredArg(cmd, "automation job id is required")
	if err != nil {
		return coreautomation.JobPatch{}, err
	}

	patch := coreautomation.JobPatch{ID: id}
	if cmd.IsSet("name") {
		value := strings.TrimSpace(cmd.String("name"))
		patch.Name = &value
	}
	if cmd.IsSet("description") {
		value := strings.TrimSpace(cmd.String("description"))
		patch.Description = &value
	}
	if cmd.IsSet("schedule") {
		schedule, err := parseCommandSchedule(cmd)
		if err != nil {
			return coreautomation.JobPatch{}, err
		}
		patch.Schedule = &schedule
	}
	if hasPayloadFlag(cmd) {
		payload := payloadUpdateFromCommand(cmd, current.Payload)
		patch.Payload = &payload
	}
	if hasDeliveryFlag(cmd) {
		delivery, err := deliveryUpdateFromCommand(cmd, current.Delivery)
		if err != nil {
			return coreautomation.JobPatch{}, err
		}
		patch.Delivery = &delivery
	}
	if cmd.IsSet("profile") {
		value := strings.TrimSpace(cmd.String("profile"))
		patch.Profile = &value
	}
	if cmd.IsSet("session-target") {
		value := strings.TrimSpace(cmd.String("session-target"))
		patch.SessionTarget = &value
	}
	if cmd.IsSet("delete-after-run") {
		value := cmd.Bool("delete-after-run")
		patch.DeleteAfterRun = &value
	}

	return patch, nil
}

func fetchAutomationJobForUpdate(
	ctx context.Context,
	api rpcclient.AutomationAPI,
	cmd *cli.Command,
) (coreautomation.Job, error) {
	if !hasPayloadFlag(cmd) && !hasDeliveryFlag(cmd) {
		return coreautomation.Job{}, nil
	}

	id, err := getRequiredArg(cmd, "automation job id is required")
	if err != nil {
		return coreautomation.Job{}, err
	}
	list, err := api.List(ctx, coreautomation.JobQuery{
		IDs:             []string{id},
		Limit:           1,
		IncludeDisabled: true,
	})
	if err != nil {
		return coreautomation.Job{}, err
	}
	for _, job := range list.Jobs {
		if job.ID == id {
			return job.Clone(), nil
		}
	}

	return coreautomation.Job{}, fmt.Errorf("automation job not found")
}

func parseCommandSchedule(cmd *cli.Command) (coreautomation.Schedule, error) {
	value := strings.TrimSpace(cmd.String("schedule"))
	if value == "" {
		return coreautomation.Schedule{}, fmt.Errorf("automation schedule is required")
	}

	return coreautomation.ParseSchedule(value, coreautomation.ParseScheduleOptions{})
}

func payloadFromCommand(cmd *cli.Command) coreautomation.Payload {
	payload := coreautomation.Payload{
		Prompt:        strings.TrimSpace(cmd.String("prompt")),
		SystemEvent:   strings.TrimSpace(cmd.String("system-event")),
		Model:         strings.TrimSpace(cmd.String("model")),
		Provider:      strings.TrimSpace(cmd.String("provider")),
		BaseURL:       strings.TrimSpace(cmd.String("base-url")),
		NoTimeout:     cmd.Bool("no-timeout"),
		MaxRuntime:    cmd.Duration("max-runtime"),
		MaxIterations: cmd.Int("max-iterations"),
		RetryAttempts: cmd.Int("retry-attempts"),
		RetryBackoff:  cmd.Duration("retry-backoff"),
		RetryMaxDelay: cmd.Duration("retry-max-delay"),
		ToolGroups:    cmd.StringSlice("tool-group"),
	}
	if payload.SystemEvent != "" {
		payload.Kind = coreautomation.PayloadSystemEvent
	} else {
		payload.Kind = coreautomation.PayloadPrompt
	}

	return payload
}

func payloadUpdateFromCommand(
	cmd *cli.Command,
	payload coreautomation.Payload,
) coreautomation.Payload {
	payload = payload.Clone()
	if cmd.IsSet("prompt") {
		payload.Kind = coreautomation.PayloadPrompt
		payload.Prompt = strings.TrimSpace(cmd.String("prompt"))
		payload.SystemEvent = ""
	}
	if cmd.IsSet("system-event") {
		payload.Kind = coreautomation.PayloadSystemEvent
		payload.Prompt = ""
		payload.SystemEvent = strings.TrimSpace(cmd.String("system-event"))
	}
	if cmd.IsSet("model") {
		payload.Model = strings.TrimSpace(cmd.String("model"))
	}
	if cmd.IsSet("provider") {
		payload.Provider = strings.TrimSpace(cmd.String("provider"))
	}
	if cmd.IsSet("base-url") {
		payload.BaseURL = strings.TrimSpace(cmd.String("base-url"))
	}
	if cmd.IsSet("no-timeout") {
		payload.NoTimeout = cmd.Bool("no-timeout")
	}
	if cmd.IsSet("max-runtime") {
		payload.MaxRuntime = cmd.Duration("max-runtime")
	}
	if cmd.IsSet("max-iterations") {
		payload.MaxIterations = cmd.Int("max-iterations")
	}
	if cmd.IsSet("retry-attempts") {
		payload.RetryAttempts = cmd.Int("retry-attempts")
	}
	if cmd.IsSet("retry-backoff") {
		payload.RetryBackoff = cmd.Duration("retry-backoff")
	}
	if cmd.IsSet("retry-max-delay") {
		payload.RetryMaxDelay = cmd.Duration("retry-max-delay")
	}
	if cmd.IsSet("tool-group") {
		payload.ToolGroups = append([]string(nil), cmd.StringSlice("tool-group")...)
	}

	return payload
}

func deliveryFromCommand(cmd *cli.Command) (coreautomation.Delivery, error) {
	mode, err := getDeliveryMode(cmd)
	if err != nil {
		return coreautomation.Delivery{}, err
	}
	if mode == "" && hasDeliveryOptionFlag(cmd) {
		return coreautomation.Delivery{}, fmt.Errorf("--delivery is required when setting delivery options")
	}

	return coreautomation.Delivery{
		Mode:       mode,
		Channel:    strings.TrimSpace(cmd.String("channel")),
		Target:     strings.TrimSpace(cmd.String("target")),
		ThreadID:   strings.TrimSpace(cmd.String("thread")),
		WebhookURL: strings.TrimSpace(cmd.String("webhook-url")),
		BestEffort: cmd.Bool("best-effort"),
	}, nil
}

func deliveryUpdateFromCommand(
	cmd *cli.Command,
	delivery coreautomation.Delivery,
) (coreautomation.Delivery, error) {
	if cmd.IsSet("delivery") {
		mode, err := getDeliveryMode(cmd)
		if err != nil {
			return coreautomation.Delivery{}, err
		}
		delivery.Mode = mode
		switch mode {
		case coreautomation.DeliveryNone, coreautomation.DeliveryLocal:
			delivery.Channel = ""
			delivery.Target = ""
			delivery.ThreadID = ""
			delivery.WebhookURL = ""
		case coreautomation.DeliveryOrigin, coreautomation.DeliveryGateway:
			delivery.WebhookURL = ""
		case coreautomation.DeliveryWebhook:
			delivery.Channel = ""
			delivery.Target = ""
			delivery.ThreadID = ""
		}
	}
	if cmd.IsSet("channel") {
		delivery.Channel = strings.TrimSpace(cmd.String("channel"))
	}
	if cmd.IsSet("target") {
		delivery.Target = strings.TrimSpace(cmd.String("target"))
	}
	if cmd.IsSet("thread") {
		delivery.ThreadID = strings.TrimSpace(cmd.String("thread"))
	}
	if cmd.IsSet("webhook-url") {
		delivery.WebhookURL = strings.TrimSpace(cmd.String("webhook-url"))
	}
	if cmd.IsSet("best-effort") {
		delivery.BestEffort = cmd.Bool("best-effort")
	}

	return delivery, nil
}

func getDeliveryMode(cmd *cli.Command) (coreautomation.DeliveryMode, error) {
	mode := coreautomation.DeliveryMode(strings.ToLower(strings.TrimSpace(cmd.String("delivery"))))
	switch mode {
	case "", coreautomation.DeliveryNone, coreautomation.DeliveryLocal,
		coreautomation.DeliveryOrigin, coreautomation.DeliveryGateway, coreautomation.DeliveryWebhook:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported automation delivery mode %q", mode)
	}
}

func hasPayloadFlag(cmd *cli.Command) bool {
	return slices.ContainsFunc([]string{
		"prompt", "system-event", "model", "provider", "base-url", "no-timeout",
		"max-runtime", "max-iterations", "retry-attempts", "retry-backoff",
		"retry-max-delay", "tool-group",
	}, cmd.IsSet)
}

func hasDeliveryFlag(cmd *cli.Command) bool {
	return slices.ContainsFunc([]string{
		"delivery", "channel", "target", "thread", "webhook-url", "best-effort",
	}, cmd.IsSet)
}

func hasDeliveryOptionFlag(cmd *cli.Command) bool {
	return slices.ContainsFunc([]string{
		"channel", "target", "thread", "webhook-url", "best-effort",
	}, cmd.IsSet)
}

func getAutomationAPI(ctx context.Context, cmd *cli.Command) (rpcclient.AutomationAPI, func(), error) {
	cfg, err := loadAutomationConfig(cmd)
	if err != nil {
		return nil, func() {}, err
	}
	client, err := newClient(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}

	return client.AutomationAPI(), func() { _ = client.Close() }, nil
}

func loadAutomationConfig(cmd *cli.Command) (*config.Config, error) {
	cfg, _, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, err
	}
	cfg.Normalize()

	return cfg, nil
}

func getRequiredArg(cmd *cli.Command, message string) (string, error) {
	firstValue := str.String(cmd.Args().First())
	value := firstValue.Trim()
	if value == "" {
		return "", fmt.Errorf("%s", message)
	}

	return value, nil
}

func parseRunStatuses(value string) []coreautomation.RunStatus {
	var statuses []coreautomation.RunStatus
	for item := range strings.SplitSeq(value, ",") {
		status := strings.TrimSpace(item)
		if status == "" {
			continue
		}
		statuses = append(statuses, coreautomation.RunStatus(status))
	}

	return statuses
}

func formatSchedule(schedule coreautomation.Schedule) string {
	switch schedule.Kind {
	case coreautomation.ScheduleAt:
		return "at " + formatTime(schedule.At)
	case coreautomation.ScheduleEvery:
		return "every " + schedule.Every.String()
	case coreautomation.ScheduleCron:
		return "cron " + schedule.Cron
	default:
		return string(schedule.Kind)
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}

	return value.UTC().Format(time.RFC3339)
}

func formatTimeInScheduleTimezone(value time.Time, schedule coreautomation.Schedule) string {
	if value.IsZero() {
		return "-"
	}

	timezone := strings.TrimSpace(schedule.Timezone)
	if timezone == "" {
		return formatTime(value)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return formatTime(value)
	}

	return value.In(location).Format(time.RFC3339)
}
