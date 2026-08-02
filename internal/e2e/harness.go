package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"

	morphagent "github.com/xymorphic/morph/internal/agent"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/datadir"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

var setHarnessEnv = os.Setenv

// HarnessOptions wires an e2e scenario to config and scripted model clients.
type HarnessOptions struct {
	Spec          HarnessSpec
	Config        *config.Config
	ModelClient   models.Client
	SummaryClient models.Client
}

// Harness drives harness e2e scenarios.
type Harness struct {
	cfg          *config.Config
	agent        harnessAgent
	inspectStore storage.Store
	cancel       context.CancelFunc
	restoreEnv   func()
	stdout       *bytes.Buffer
	stderr       *bytes.Buffer
}

type harnessAgent interface {
	Respond(context.Context, string, agentcore.RespondOptions) (string, error)
	CurrentSession(context.Context) (storage.Session, error)
}

type harnessSessionAgent interface {
	CreateSession(context.Context, string, ...storage.SessionCreateOptions) (storage.Session, error)
	UseSession(context.Context, string) error
}

type harnessTurnMessagesAgent interface {
	TurnMessages() []morphmsg.Message
}

type harnessCompactionAgent interface {
	CompactSession(context.Context, string) (agentcore.CompactSessionResult, error)
}

var openHarnessInspectStore = openInspectStore

// NewHarness returns an e2e harness using the supplied provider and options.
func NewHarness(ctx context.Context, opts HarnessOptions) (*Harness, error) {
	if err := opts.Spec.Validate(); err != nil {
		return nil, err
	}
	if opts.ModelClient == nil {
		return nil, errors.New("e2e harness model client is required")
	}

	restoreEnv, err := applyHarnessEnv(opts.Spec)
	if err != nil {
		return nil, err
	}

	cfg, err := getHarnessConfig(opts.Spec, opts.Config)
	if err != nil {
		restoreEnv()
		return nil, err
	}
	traceDirValue := str.String(opts.Spec.Isolation.TraceDir)
	if traceDirValue.Trim() != "" {
		cfg.Trace.Disk.Dir = opts.Spec.Isolation.TraceDir
	}

	runCtx, cancel := context.WithCancel(normalizeHarnessContext(ctx))
	ag := morphagent.NewAgent(runCtx, cfg, opts.ModelClient, opts.SummaryClient)
	if err := ag.Start(runCtx); err != nil {
		cancel()
		restoreEnv()
		return nil, err
	}
	inspectStore, err := openHarnessInspectStore(cfg)
	if err != nil {
		cancel()
		restoreEnv()
		return nil, err
	}

	return &Harness{
		cfg:          cfg,
		agent:        ag,
		inspectStore: inspectStore,
		cancel:       cancel,
		restoreEnv:   restoreEnv,
		stdout:       &bytes.Buffer{},
		stderr:       &bytes.Buffer{},
	}, nil
}

func (h *Harness) Close() error {
	if h == nil {
		return nil
	}

	var closeErr error
	if closer, ok := h.agent.(interface{ Close() error }); ok {
		closeErr = closer.Close()
	}
	if h.cancel != nil {
		h.cancel()
	}
	if closer, ok := h.inspectStore.(interface{ Close() error }); ok {
		if err := closer.Close(); closeErr == nil {
			closeErr = err
		}
	}
	if h.restoreEnv != nil {
		h.restoreEnv()
	}
	return closeErr
}

func (h *Harness) CreateSession(ctx context.Context, id string) (storage.Session, error) {
	if h == nil || h.agent == nil {
		return storage.Session{}, errors.New("e2e harness is required")
	}

	agent, ok := h.agent.(harnessSessionAgent)
	if !ok {
		return storage.Session{}, errors.New("e2e harness session management is unavailable")
	}

	return agent.CreateSession(normalizeHarnessContext(ctx), id)
}

func (h *Harness) UseSession(ctx context.Context, id string) error {
	if h == nil || h.agent == nil {
		return errors.New("e2e harness is required")
	}

	agent, ok := h.agent.(harnessSessionAgent)
	if !ok {
		return errors.New("e2e harness session management is unavailable")
	}

	return agent.UseSession(normalizeHarnessContext(ctx), id)
}

func (h *Harness) CompactSession(ctx context.Context, id string) (agentcore.CompactSessionResult, error) {
	if h == nil || h.agent == nil {
		return agentcore.CompactSessionResult{}, errors.New("e2e harness is required")
	}

	compactor, ok := h.agent.(harnessCompactionAgent)
	if !ok {
		return agentcore.CompactSessionResult{}, errors.New("e2e harness compaction is unavailable")
	}

	return compactor.CompactSession(normalizeHarnessContext(ctx), id)
}

func (h *Harness) Config() *config.Config {
	if h == nil || h.cfg == nil {
		return nil
	}

	cloned := *h.cfg
	return &cloned
}

func (h *Harness) Stdout() string {
	if h == nil || h.stdout == nil {
		return ""
	}

	return h.stdout.String()
}

func (h *Harness) Stderr() string {
	if h == nil || h.stderr == nil {
		return ""
	}

	return h.stderr.String()
}

func (h *Harness) Send(ctx context.Context, req RootChatRequest) (RootChatResult, error) {
	if h == nil || h.agent == nil {
		return RootChatResult{}, errors.New("e2e harness is required")
	}
	if err := req.Validate(); err != nil {
		h.writeStderr(err.Error())
		return RootChatResult{}, err
	}

	events := make([]Event, 0, 4)
	reply, err := h.agent.Respond(normalizeHarnessContext(ctx), req.Message, agentcore.RespondOptions{
		Instruct:  req.Instruct,
		SessionID: req.SessionID,
		Stream:    req.Stream,
		OnEvent: func(event agentcore.Event) {
			events = append(events, Event{Channel: event.Channel, Text: event.Text})
			h.writeStdout(event.Text)
		},
	})
	if err != nil {
		h.writeStderr(err.Error())
		return RootChatResult{}, err
	}
	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()
	if sessionID == "" {
		session, err := h.agent.CurrentSession(normalizeHarnessContext(ctx))
		if err != nil {
			h.writeStderr(err.Error())
			return RootChatResult{}, err
		}
		sessionID = session.ID
	}

	return RootChatResult{
		Reply:     reply,
		SessionID: sessionID,
		Events:    events,
	}, nil
}

func (h *Harness) Messages(ctx context.Context, sessionID string) ([]morphmsg.Message, error) {
	if h == nil {
		return nil, errors.New("e2e harness is required")
	}
	if h.inspectStore == nil {
		return nil, errors.New("e2e harness message inspection is unavailable for non-persistent storage")
	}

	sessionIDValue2 := str.String(sessionID)
	sessionID = sessionIDValue2.Trim()
	if sessionID == "" {
		var err error
		session, err := h.agent.CurrentSession(normalizeHarnessContext(ctx))
		if err != nil {
			return nil, err
		}
		sessionID = session.ID
	}

	return h.inspectStore.Session().GetMessages(normalizeHarnessContext(ctx), sessionID, storage.MessageQueryOptions{})
}

// TurnMessages returns the messages emitted by the most recent harness turn.
func (h *Harness) TurnMessages() ([]morphmsg.Message, error) {
	if h == nil || h.agent == nil {
		return nil, errors.New("e2e harness is required")
	}

	agent, ok := h.agent.(harnessTurnMessagesAgent)
	if !ok {
		return nil, errors.New("e2e harness turn message inspection is unavailable")
	}

	return agent.TurnMessages(), nil
}

func getHarnessConfig(spec HarnessSpec, cfg *config.Config) (*config.Config, error) {
	if spec.Config.Mode() == ConfigModeRealInput {
		if cfg != nil {
			return nil, errors.New("e2e harness must not use in-memory config when real config inputs are present")
		}

		return config.Load(spec.Config.EnvFilePath, spec.Config.ConfigFilePath)
	}

	if cfg == nil {
		return nil, errors.New("e2e harness requires config for in-memory mode")
	}

	cloned := *cfg
	cloned.Normalize()
	return &cloned, nil
}

func openInspectStore(cfg *config.Config) (storage.Store, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	backendValue := str.String(cfg.Storage.Backend)
	if backendValue.Normalized() == "memory" {
		return nil, nil
	}

	inspectCfg := *cfg
	rerankDisabled := false
	inspectCfg.Search.Vector.Enabled = false
	inspectCfg.Reranker.Enabled = &rerankDisabled
	inspectCfg.Search.EnableRerank = &rerankDisabled
	return statemanager.OpenStore(&inspectCfg)
}

func applyHarnessEnv(spec HarnessSpec) (func(), error) {
	homeDir := filepath.Dir(spec.Isolation.DataDir)
	updates := make(map[string]string, len(spec.Config.Env))
	maps.Copy(updates, spec.Config.Env)

	profileName, err := profile.ResolveName("", updates)
	if err != nil {
		return nil, err
	}

	originalProfile := profile.Active()
	restoreEnv := captureEnv(updates)
	restore := func() {
		restoreEnv()
		profile.SetActive(originalProfile)
	}
	profile.SetActive(profile.Profile{Name: profileName, HomeDir: homeDir})

	for key, value := range updates {
		if err := setHarnessEnv(key, value); err != nil {
			restore()
			return nil, err
		}
	}

	if datadir.DataDir() != spec.Isolation.DataDir {
		restore()
		return nil, errors.New("e2e isolation data dir must match profile home data dir")
	}
	if datadir.StateDBPath() != spec.Isolation.StoragePath {
		restore()
		return nil, errors.New("e2e isolation storage path must match profile home state db")
	}

	return restore, nil
}

func captureEnv(updates map[string]string) func() {
	originals := make(map[string]*string, len(updates))
	for key := range updates {
		value, ok := os.LookupEnv(key)
		if ok {
			copied := value
			originals[key] = &copied
			continue
		}
		originals[key] = nil
	}

	return func() {
		for key, value := range originals {
			if value == nil {
				_ = os.Unsetenv(key)
				continue
			}

			_ = os.Setenv(key, *value)
		}
	}
}

func normalizeHarnessContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

func (h *Harness) writeStdout(text string) {
	textValue := str.String(text)
	if h == nil || h.stdout == nil || textValue.Trim() == "" {
		return
	}
	_, _ = io.WriteString(h.stdout, text)
}

func (h *Harness) writeStderr(text string) {
	textValue2 := str.String(text)
	if h == nil || h.stderr == nil || textValue2.Trim() == "" {
		return
	}
	_, _ = io.WriteString(h.stderr, text)
}
