package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	urfavecli "github.com/urfave/cli/v3"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	provider_ollama "github.com/wandxy/morph/internal/model/provider_ollama"
	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"github.com/wandxy/morph/internal/runtime"
	storage "github.com/wandxy/morph/internal/state/core"
	agent "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/nanoid"
	"github.com/wandxy/morph/pkg/str"
)

const (
	rootColorGray         = "\x1b[90m"
	rootColorReset        = "\x1b[0m"
	pullProgressLineLimit = 5
)

// NewChatClientFunc creates a chat client for CLI commands.
type NewChatClientFunc func(context.Context, *config.Config) (rpcclient.ChatClient, error)

// EnsureDaemonFunc ensures the daemon is reachable and returns cleanup for daemon instances it starts.
type EnsureDaemonFunc func(context.Context, *config.Config) (func() error, error)

// MainActionOptions controls main action.
type MainActionOptions struct {
	Input               io.Reader
	Output              io.Writer
	NewChatClient       NewChatClientFunc
	EnsureDaemonRunning EnsureDaemonFunc
	PullOllamaModel     func(context.Context, string, string, map[string]string, func(provider_ollama.PullProgress)) error
	IsInteractive       func(io.Reader, io.Writer) bool
	Now                 func() time.Time
}

// NewMainAction returns the root CLI action wired to the supplied chat client factory.
func NewMainAction(opts MainActionOptions) func(context.Context, *urfavecli.Command) error {
	input := opts.Input
	output := opts.Output
	if output == nil {
		output = io.Discard
	}

	newChatClient := opts.NewChatClient
	if newChatClient == nil {
		newChatClient = newDefaultChatClient
	}
	ensureDaemonRunning := opts.EnsureDaemonRunning
	if ensureDaemonRunning == nil {
		ensureDaemonRunning = EnsureDaemonRunning
	}
	pullOllamaModel := opts.PullOllamaModel
	if pullOllamaModel == nil {
		pullOllamaModel = provider_ollama.EnsureModel
	}
	isInteractive := opts.IsInteractive
	if isInteractive == nil {
		isInteractive = isInteractiveTerminal
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context, cmd *urfavecli.Command) error {
		joinValue := str.String(strings.Join(cmd.Args().Slice(), " "))
		message := joinValue.Trim()
		if message == "" {
			return urfavecli.ShowAppHelp(cmd)
		}

		cfg, inputs, err := LoadConfig(cmd)
		if err != nil {
			return err
		}

		ApplyConfigOverrides(cmd, cfg)
		AddStartupFilesystemRoots(cfg, inputs)
		if err := validateRootChatModelConfig(cfg); err != nil {
			return err
		}

		endpoint, err := runtime.ResolveRPC(ctx, cmd, cfg)
		if err != nil {
			return err
		}
		cfg.RPC = endpoint

		config.Set(cfg)
		_ = logutils.ConfigureLogger("morph", cfg.Log.NoColor)
		logutils.SetLogLevel(cfg.Log.Level)

		if cmd.Bool("pull") {
			progressPrinter := NewPullProgressPrinter(output, !cmd.Bool("pull-quiet"))
			var onProgress func(provider_ollama.PullProgress)
			if progressPrinter != nil {
				onProgress = progressPrinter.Progress
			}
			if err := pullSelectedOllamaModel(ctx, cfg, pullOllamaModel, onProgress); err != nil {
				if progressPrinter != nil {
					progressPrinter.Finish()
				}
				return err
			}
			if progressPrinter != nil {
				progressPrinter.Finish()
			}
		}

		daemonCleanup, err := ensureDaemonRunning(ctx, cfg)
		if err != nil {
			return err
		}
		if daemonCleanup != nil {
			defer func() {
				_ = daemonCleanup()
			}()
		}

		client, err := newChatClient(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		if err := validateRootChatDaemonModel(ctx, cmd, cfg, client); err != nil {
			return err
		}

		literalValue := str.String(cmd.String("session"))
		ctx = rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceCLI)
		ctx = rpcmeta.WithOutgoingPermissionPreset(ctx, cfg.Permissions.EffectivePreset())
		respondCtx, cancelRespond := context.WithCancel(ctx)
		defer cancelRespond()

		requestInstruct := ""
		if cmd.IsSet("instruct") {
			requestInstruct = cfg.Session.Instruct
		}
		approvalHandler := newRootChatApprovalHandler(
			input,
			output,
			getRootChatPermissionAPI(client),
			isInteractive(input, output),
		)
		var formatter *chatStreamFormatter
		assistantProgress := false
		var onProgress func(rpcclient.Event) error
		if cfg.StreamEnabled() {
			formatter = newChatStreamFormatter(cfg, now, cmd.Bool("no-color"))
			formatter.terminalLinefeeds = isTerminalWriter(output)
			onProgress = func(event rpcclient.Event) error {
				if event.Channel == "assistant" && event.Text != "" {
					assistantProgress = true
				}
				_, writeErr := fmt.Fprint(output, formatter.Format(event))
				return writeErr
			}
		}
		reply, err := runQueuedRootChat(
			respondCtx,
			client,
			literalValue.Trim(),
			message,
			requestInstruct,
			cfg.Models.Main.Stream,
			approvalHandler,
			onProgress,
		)
		if err != nil {
			return err
		}

		if cfg.StreamEnabled() {
			if !assistantProgress {
				if _, err = fmt.Fprint(output, formatter.Format(rpcclient.Event{
					Kind:    agent.EventKindTextDelta,
					Channel: "assistant",
					Text:    reply,
				})); err != nil {
					return err
				}
			}
			_, err = fmt.Fprint(output, formatter.Finish())
			return err
		}
		_, err = fmt.Fprintln(output, reply)
		return err
	}
}

var errRootChatRunComplete = errors.New("root chat run complete")

func runQueuedRootChat(
	ctx context.Context,
	client rpcclient.ChatClient,
	sessionID string,
	message string,
	instruct string,
	stream *bool,
	approvalHandler *rootChatApprovalHandler,
	onProgress func(rpcclient.Event) error,
) (string, error) {
	sessions, ok := client.(interface{ SessionAPI() rpcclient.SessionAPI })
	if !ok {
		return "", errors.New("session API is unavailable")
	}
	sessionAPI := sessions.SessionAPI()
	if sessionID == "" {
		current, err := sessionAPI.Current(ctx)
		if err != nil {
			return "", err
		}
		sessionID = current.ID
		if sessionID == "" {
			sessionID = storage.DefaultSessionID
		}
	}
	submissionID, err := nanoid.Generate("sub_")
	if err != nil {
		return "", err
	}
	entry, err := client.EnqueueMessage(ctx, rpcclient.EnqueueMessageOptions{
		SessionID:          sessionID,
		Message:            message,
		Instruct:           instruct,
		Stream:             stream,
		ClientSubmissionID: submissionID,
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	if err != nil {
		return "", err
	}
	terminal := isTerminalRootChatQueueStatus(entry.Status)
	if terminal {
		if err := rootChatQueueTerminalError(entry); err != nil {
			return "", err
		}
	}
	state, err := client.State(ctx, sessionID)
	if err != nil {
		return "", err
	}
	var progressSequence int64
	if onProgress != nil {
		for _, progress := range state.Progress {
			if progress.QueueEntryID != entry.ID {
				continue
			}
			progressSequence = max(progressSequence, progress.Sequence)
			if err := onProgress(rpcclient.Event{
				Kind: progress.Kind, Channel: progress.Channel, Text: progress.Text,
			}); err != nil {
				return "", err
			}
		}
	}
	for _, queued := range state.Queue {
		if queued.ID == entry.ID {
			terminal = isTerminalRootChatQueueStatus(queued.Status)
			if terminal {
				if err := rootChatQueueTerminalError(queued); err != nil {
					return "", err
				}
			}
			break
		}
	}
	if !terminal {
		observeContext, cancelObserve := context.WithCancel(ctx)
		approvalDone := make(chan error, 1)
		if approvalHandler != nil && approvalHandler.permissions != nil {
			go func() {
				approvalErr := monitorRootChatApprovals(
					observeContext,
					approvalHandler,
					sessionID,
				)
				if approvalErr != nil {
					cancelObserve()
				}
				approvalDone <- approvalErr
			}()
		} else {
			approvalDone <- nil
		}
		err = client.Observe(observeContext, sessionID, state.Cursor, func(event rpcclient.SessionEvent) error {
			if event.Progress != nil &&
				event.Progress.QueueEntryID == entry.ID &&
				event.Progress.Sequence > progressSequence &&
				onProgress != nil {
				progressSequence = event.Progress.Sequence
				return onProgress(rpcclient.Event{
					Kind: event.Progress.Kind, Channel: event.Progress.Channel,
					Text: event.Progress.Text,
				})
			}
			if event.Queue != nil && event.Queue.ID == entry.ID &&
				isTerminalRootChatQueueStatus(event.Queue.Status) {
				if err := rootChatQueueTerminalError(*event.Queue); err != nil {
					return err
				}
				return errRootChatRunComplete
			}
			if event.Run != nil && event.Run.QueueEntryID == entry.ID &&
				event.Run.Status != agentsession.RunStatusRunning {
				if err := rootChatRunTerminalError(*event.Run); err != nil {
					return err
				}
				return errRootChatRunComplete
			}
			return nil
		})
		cancelObserve()
		approvalErr := <-approvalDone
		if approvalErr != nil {
			return "", approvalErr
		}
		if err != nil && !errors.Is(err, errRootChatRunComplete) {
			return "", err
		}
	}
	completedEntry := entry
	finalState, err := client.State(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, queued := range finalState.Queue {
		if queued.ID == entry.ID {
			completedEntry = queued
			break
		}
	}
	timeline, err := sessionAPI.Timeline(ctx, rpcclient.SessionTimelineOptions{
		SessionID: sessionID,
	})
	if err != nil {
		return "", err
	}
	for index := len(timeline.Messages) - 1; index >= 0; index-- {
		item := timeline.Messages[index].Message
		if item.Role != morphmsg.RoleAssistant || item.Content == "" {
			continue
		}
		if !completedEntry.StartedAt.IsZero() && item.CreatedAt.Before(completedEntry.StartedAt) {
			continue
		}
		if !completedEntry.CompletedAt.IsZero() && item.CreatedAt.After(completedEntry.CompletedAt) {
			continue
		}
		return item.Content, nil
	}
	return "", errors.New("session run completed without an assistant response")
}

func monitorRootChatApprovals(
	ctx context.Context,
	handler *rootChatApprovalHandler,
	sessionID string,
) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	handled := make(map[string]struct{})
	for {
		requests, err := handler.permissions.ListApprovalRequests(ctx, permissions.ApprovalQuery{
			Status: permissions.ApprovalPending,
			Limit:  100,
		})
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		for _, request := range requests {
			if request.SessionID != sessionID {
				continue
			}
			if _, ok := handled[request.ID]; ok {
				continue
			}
			handled[request.ID] = struct{}{}
			if err := handler.HandleRequest(ctx, request); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func isTerminalRootChatQueueStatus(status agentsession.QueueStatus) bool {
	switch status {
	case agentsession.QueueStatusDelivered,
		agentsession.QueueStatusCompleted,
		agentsession.QueueStatusInterrupted,
		agentsession.QueueStatusFailed,
		agentsession.QueueStatusCancelled:
		return true
	default:
		return false
	}
}

func rootChatQueueTerminalError(entry rpcclient.SessionQueueEntry) error {
	switch entry.Status {
	case agentsession.QueueStatusInterrupted:
		if entry.LastError != "" {
			return fmt.Errorf("session run interrupted: %s", entry.LastError)
		}
		return errors.New("session run interrupted")
	case agentsession.QueueStatusFailed:
		if entry.LastError != "" {
			return errors.New(entry.LastError)
		}
		return errors.New("session run failed")
	case agentsession.QueueStatusCancelled:
		return errors.New("session run cancelled")
	default:
		return nil
	}
}

func rootChatRunTerminalError(run rpcclient.SessionActiveRun) error {
	switch run.Status {
	case agentsession.RunStatusFailed:
		if run.LastError != "" {
			return errors.New(run.LastError)
		}
		return errors.New("session run failed")
	case agentsession.RunStatusInterrupted:
		if run.Reason != "" {
			return fmt.Errorf("session run interrupted: %s", run.Reason)
		}
		return errors.New("session run interrupted")
	case agentsession.RunStatusCancelled:
		return errors.New("session run cancelled")
	default:
		return nil
	}
}

func validateRootChatModelConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}

	aPIValue := str.String(cfg.Models.Main.API)
	if aPIValue.Trim() == "" {
		return nil
	}

	cfg.Normalize()
	return config.ValidateModelGenerationAPIForProvider(
		"model API",
		cfg.Models.Main.Provider,
		cfg.Models.Main.API,
	)
}

func validateRootChatDaemonModel(
	ctx context.Context,
	cmd *urfavecli.Command,
	cfg *config.Config,
	client rpcclient.ChatClient,
) error {
	if !hasRootChatModelOverride(cmd) {
		return nil
	}

	modelAPIProvider, ok := client.(interface{ ModelAPI() rpcclient.ModelAPI })
	if !ok || modelAPIProvider.ModelAPI() == nil {
		return fmt.Errorf("running daemon model identity is not available")
	}

	runtimeModel, err := modelAPIProvider.ModelAPI().RuntimeModel(ctx)
	if err != nil {
		return fmt.Errorf("check running daemon model: %w", err)
	}

	requestedModel := rootChatModelRuntimeFromConfig(cfg)
	if rootChatModelRuntimeEqual(runtimeModel, requestedModel) {
		return nil
	}

	return fmt.Errorf(
		"running daemon uses %s; requested %s. Stop or restart the daemon before running root chat with model overrides",
		formatRootChatModelRuntime(runtimeModel),
		formatRootChatModelRuntime(requestedModel),
	)
}

func hasRootChatModelOverride(cmd *urfavecli.Command) bool {
	if cmd == nil {
		return false
	}

	return slices.ContainsFunc([]string{
		"model",
		"model.provider",
		"provider",
		"model.api",
		"model.base-url",
		"base-url",
	}, cmd.IsSet)
}

func rootChatModelRuntimeFromConfig(cfg *config.Config) rpcclient.ModelRuntime {
	if cfg == nil {
		return rpcclient.ModelRuntime{}
	}

	snapshot := *cfg
	snapshot.Normalize()

	return normalizeRootChatModelRuntime(rpcclient.ModelRuntime{
		Provider:      snapshot.Models.Main.Provider,
		API:           snapshot.MainModelAPIEffective(),
		Model:         snapshot.Models.Main.Name,
		BaseURL:       snapshot.Models.Main.BaseURL,
		ContextLength: snapshot.Models.Main.ContextLength,
	})
}

func rootChatModelRuntimeEqual(a rpcclient.ModelRuntime, b rpcclient.ModelRuntime) bool {
	a = normalizeRootChatModelRuntime(a)
	b = normalizeRootChatModelRuntime(b)

	return a.Provider == b.Provider &&
		a.API == b.API &&
		a.Model == b.Model &&
		a.BaseURL == b.BaseURL
}

func normalizeRootChatModelRuntime(runtime rpcclient.ModelRuntime) rpcclient.ModelRuntime {
	providerValue := str.String(runtime.Provider)
	runtime.Provider = providerValue.Normalized()
	aPIValue2 := str.String(runtime.API)
	runtime.API = aPIValue2.Normalized()
	modelValue := str.String(runtime.Model)
	runtime.Model = modelValue.Trim()
	if runtime.Provider == constants.ModelProviderOllama {
		runtime.Model = provider_ollama.NormalizeModelIDForComparison(runtime.Model)
	}
	baseURLValue := str.String(runtime.BaseURL)
	runtime.BaseURL = strings.TrimRight(baseURLValue.Trim(), "/")
	if runtime.ContextLength < 0 {
		runtime.ContextLength = 0
	}

	return runtime
}

func formatRootChatModelRuntime(runtime rpcclient.ModelRuntime) string {
	runtime = normalizeRootChatModelRuntime(runtime)
	parts := []string{
		fmt.Sprintf("provider=%q", runtime.Provider),
		fmt.Sprintf("model=%q", runtime.Model),
	}
	if runtime.API != "" {
		parts = append(parts, fmt.Sprintf("api=%q", runtime.API))
	}
	if runtime.BaseURL != "" {
		parts = append(parts, fmt.Sprintf("base_url=%q", runtime.BaseURL))
	}

	return strings.Join(parts, " ")
}

func pullSelectedOllamaModel(
	ctx context.Context,
	cfg *config.Config,
	pullOllamaModel func(context.Context, string, string, map[string]string, func(provider_ollama.PullProgress)) error,
	onProgress func(provider_ollama.PullProgress),
) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if pullOllamaModel == nil {
		return fmt.Errorf("ollama puller is required")
	}

	cfg.Normalize()
	if cfg.Models.Main.Provider != constants.ModelProviderOllama {
		return fmt.Errorf("--pull is only supported with provider %q", constants.ModelProviderOllama)
	}
	api := cfg.MainModelAPIEffective()
	if api != modelprovider.APIOllamaNative && api != modelprovider.APIOpenAICompletions {
		return fmt.Errorf("--pull is only supported with Ollama chat APIs")
	}

	auth, err := cfg.ResolveModelAuth()
	if err != nil {
		return err
	}

	return pullOllamaModel(ctx, auth.BaseURL, cfg.Models.Main.Name, auth.Headers, onProgress)
}

type pullProgressPrinter struct {
	output   io.Writer
	live     bool
	lines    []string
	rendered int
}

type PullProgressPrinter = pullProgressPrinter

func NewPullProgressPrinter(output io.Writer, enabled bool) *PullProgressPrinter {
	return newPullProgressPrinter(output, enabled)
}

func newPullProgressPrinter(output io.Writer, enabled bool) *pullProgressPrinter {
	if !enabled || output == nil {
		return nil
	}

	return &pullProgressPrinter{
		output: output,
		live:   isTerminalWriter(output),
	}
}

func (p *pullProgressPrinter) Progress(progress provider_ollama.PullProgress) {
	if p == nil {
		return
	}

	line := formatPullProgress(progress)
	if line == "" {
		return
	}
	if len(p.lines) > 0 && p.lines[len(p.lines)-1] == line {
		return
	}

	p.lines = append(p.lines, line)
	if len(p.lines) > pullProgressLineLimit {
		p.lines = p.lines[len(p.lines)-pullProgressLineLimit:]
	}
	if p.live {
		p.renderLive()
	}
}

func (p *pullProgressPrinter) Finish() {
	if p == nil {
		return
	}
	if p.live {
		if p.rendered > 0 {
			_, _ = fmt.Fprint(p.output, "\r\x1b[2K")
		}
		return
	}

	for _, line := range p.lines {
		_, _ = fmt.Fprintln(p.output, line)
	}
}

func (p *pullProgressPrinter) Lines() []string {
	if p == nil {
		return nil
	}

	return slices.Clone(p.lines)
}

func (p *pullProgressPrinter) renderLive() {
	if p.rendered > 0 {
		_, _ = fmt.Fprintf(p.output, "\x1b[%dF", p.rendered)
	}

	rows := max(p.rendered, len(p.lines))
	for i := 0; i < rows; i++ {
		_, _ = fmt.Fprint(p.output, "\r\x1b[2K")
		if i < len(p.lines) {
			_, _ = fmt.Fprint(p.output, p.lines[i])
		}
		_, _ = fmt.Fprintln(p.output)
	}
	p.rendered = len(p.lines)
}

func formatPullProgress(progress provider_ollama.PullProgress) string {
	return FormatPullProgress(progress)
}

func FormatPullProgress(progress provider_ollama.PullProgress) string {
	statusValue := str.String(progress.Status)
	text := statusValue.Trim()
	if text == "" {
		return ""
	}
	if progress.Total > 0 && progress.Completed >= 0 {
		percent := int((progress.Completed * 100) / progress.Total)
		return fmt.Sprintf("Ollama pull: %s %d%%", text, percent)
	}

	return fmt.Sprintf("Ollama pull: %s", text)
}

type fdWriter interface {
	Fd() uintptr
}

func isInteractiveTerminal(input io.Reader, output io.Writer) bool {
	reader, readerOK := input.(interface{ Fd() uintptr })
	writer, writerOK := output.(fdWriter)
	if !readerOK || !writerOK {
		return false
	}

	return term.IsTerminal(reader.Fd()) && term.IsTerminal(writer.Fd())
}

func isTerminalWriter(output io.Writer) bool {
	if writer, ok := output.(fdWriter); ok {
		return term.IsTerminal(writer.Fd())
	}

	return false
}

func newDefaultChatClient(ctx context.Context, cfg *config.Config) (rpcclient.ChatClient, error) {
	return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
		Address:           cfg.RPC.Address,
		Port:              cfg.RPC.Port,
		PermissionSurface: permissions.SurfaceCLI,
		PermissionPreset:  cfg.Permissions.EffectivePreset(),
		AuthServices: []string{
			"/morph.v1.SessionService",
			"/morph.v1.ModelService",
			"/morph.v1.PermissionService",
		},
	}, cfg))
}

type chatStreamFormatter struct {
	cfg                      *config.Config
	now                      func() time.Time
	noColor                  bool
	turnStarted              time.Time
	reasoningStarted         time.Time
	reasoningActive          bool
	terminalLinefeeds        bool
	lastChannel              string
	wroteText                bool
	lastTextTrailingNewlines int
}

func newChatStreamFormatter(cfg *config.Config, now func() time.Time, noColor bool) *chatStreamFormatter {
	clock := now
	if clock == nil {
		clock = time.Now
	}
	return &chatStreamFormatter{
		cfg:         cfg,
		now:         clock,
		noColor:     noColor,
		turnStarted: clock(),
	}
}

func (f *chatStreamFormatter) Format(event rpcclient.Event) string {
	if event.TraceEvent != nil {
		return ""
	}

	channelValue := str.String(event.Channel)
	channel := channelValue.Trim()
	if channel == "reasoning" && !f.reasoningActive {
		f.reasoningStarted = f.now()
		f.reasoningActive = true
	}

	output := formatChatEvent(f.cfg, event, f.noColor)
	if output == "" {
		return ""
	}

	prefix := ""
	if f.wroteText && f.lastChannel == "reasoning" && channel != "reasoning" {
		prefix = f.finishReasoning()
	}

	f.wroteText = true
	f.lastChannel = channel
	f.lastTextTrailingNewlines = countTrailingNewlines(event.Text, 2)

	return f.formatOutput(prefix + output)
}

func (f *chatStreamFormatter) Finish() string {
	if f == nil {
		return ""
	}

	output := ""
	if f.reasoningActive {
		output += f.finishReasoning()
	}
	if !strings.HasSuffix(output, "\n\n") {
		output += strings.Repeat("\n", max(0, 2-f.lastTextTrailingNewlines))
	}
	output += f.formatMutedLabel("Worked for " + formatElapsed(f.now().Sub(f.turnStarted)))
	output += "\n"

	return f.formatOutput(output)
}

func (f *chatStreamFormatter) finishReasoning() string {
	f.reasoningActive = false

	output := strings.Repeat("\n", max(0, 2-f.lastTextTrailingNewlines))
	output += f.formatMutedLabel("Thought for " + formatElapsed(f.now().Sub(f.reasoningStarted)))
	output += "\n\n"

	return output
}

func (f *chatStreamFormatter) formatMutedLabel(label string) string {
	if f == nil || f.noColor {
		return label
	}

	return rootColorGray + label + rootColorReset
}

func (f *chatStreamFormatter) formatOutput(output string) string {
	if f == nil || !f.terminalLinefeeds {
		return output
	}

	return normalizeTerminalLinefeeds(output)
}

func normalizeTerminalLinefeeds(output string) string {
	if !strings.Contains(output, "\n") {
		return output
	}

	var builder strings.Builder
	builder.Grow(len(output) + strings.Count(output, "\n"))
	for index := 0; index < len(output); index++ {
		if output[index] == '\n' && (index == 0 || output[index-1] != '\r') {
			builder.WriteByte('\r')
		}
		builder.WriteByte(output[index])
	}

	return builder.String()
}

func formatElapsed(duration time.Duration) string {
	duration = duration.Round(time.Second)
	if duration < time.Second {
		return "0s"
	}

	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration/time.Second))
	}

	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}

	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

func countTrailingNewlines(text string, limit int) int {
	count := 0
	for i := len(text) - 1; i >= 0 && count < limit; i-- {
		if text[i] != '\n' {
			break
		}

		count++
	}

	return count
}

// FormatChatEvent formats one streamed chat event for terminal output.
func FormatChatEvent(cfg *config.Config, event rpcclient.Event) string {
	return formatChatEvent(cfg, event, false)
}

func formatChatEvent(cfg *config.Config, event rpcclient.Event, noColor bool) string {
	if event.TraceEvent != nil {
		return ""
	}

	channelValue2 := str.String(event.Channel)
	if channelValue2.Trim() != "reasoning" || cfg == nil || noColor {
		return event.Text
	}

	return rootColorGray + event.Text + rootColorReset
}
