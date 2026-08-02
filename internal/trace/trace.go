package trace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	"github.com/xymorphic/morph/internal/guardrails"
	storage "github.com/xymorphic/morph/internal/state/core"
)

var log = logutils.Module("trace")

var (
	mkdirAll       = os.MkdirAll
	globTraceFiles = filepath.Glob
	openTraceFile  = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.OpenFile(name, flag, perm)
	}
	statOpenedFile = func(f *os.File) (os.FileInfo, error) {
		return f.Stat()
	}
	closeTraceFile = func(f *os.File) error {
		return f.Close()
	}
)

// ErrAmbiguousTraceFiles is returned when more than one file matches *<session_id>.jsonl in the trace directory.
var ErrAmbiguousTraceFiles = errors.New("multiple trace files match session id")

// traceTimeLayout is the UTC timestamp prefix for new trace filenames: "<layout>-<session_id>.jsonl".
const traceTimeLayout = "20060102T150405.000000000Z"

/*
Trace sessions record durable, inspectable events for one agent session.

Factories hide where events are written: JSONL files for local inspection,
state stores for database-backed timelines, or multiple sinks when callers need
both. Recording code should depend on the Session interface and avoid caring
about storage details.
*/

// Session records trace events for one logical agent run.
type Session interface {
	ID() string
	Record(string, any)
	Close()
}

// Factory opens trace sessions for a storage backend.
type Factory interface {
	OpenSession(context.Context, string, Metadata) Session
}

// Metadata describes the run associated with a trace session.
type Metadata struct {
	AgentName          string     `json:"agent_name"`
	Model              string     `json:"model"`
	API                string     `json:"api"`
	Source             string     `json:"source"`
	PublicSessionID    string     `json:"public_session_id,omitempty"`
	EffectiveSessionID string     `json:"effective_session_id,omitempty"`
	ChildSessionID     string     `json:"child_session_id,omitempty"`
	ParentSessionID    string     `json:"parent_session_id,omitempty"`
	RunID              string     `json:"run_id,omitempty"`
	PersonalityName    string     `json:"personality_name,omitempty"`
	StateMode          string     `json:"state_mode,omitempty"`
	SourceProfile      string     `json:"source_profile,omitempty"`
	SpawnedAt          *time.Time `json:"spawned_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	TraceDir           string     `json:"trace_dir,omitempty"`
}

// Event is one persisted trace record.
type Event struct {
	SessionID string    `json:"session_id"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
}

// JSONLFactory opens trace sessions for a specific backend.
type JSONLFactory struct {
	directory string
	redactor  guardrails.Redactor
	now       func() time.Time
	pathLocks sync.Map // path -> *sync.Mutex
}

// StateStore persists trace events for state-backed traces.
type StateStore interface {
	AppendTraceEvent(context.Context, storage.TraceEvent) (storage.TraceEvent, error)
	ListTraceEvents(context.Context, storage.TraceQuery) (storage.TraceResult, error)
	PruneTraceEvents(context.Context, string, int) error
}

// StateFactory opens trace sessions for a specific backend.
type StateFactory struct {
	store               StateStore
	redactor            guardrails.Redactor
	maxEventsPerSession int
	now                 func() time.Time
}

type stateSession struct {
	ctx                 context.Context
	id                  string
	store               StateStore
	redactor            guardrails.Redactor
	maxEventsPerSession int
	now                 func() time.Time
	closed              bool
	mu                  sync.Mutex
	noop                bool
}

type multiFactory struct {
	factories []Factory
}

type multiSession struct {
	sessions []Session
}

type jsonlSession struct {
	id       string
	encoder  *json.Encoder
	file     *os.File
	redactor guardrails.Redactor
	closed   bool
	pathLock *sync.Mutex
	path     string
	noop     bool
}

type noopSession struct{}

type noopFactory struct{}

// NewFileFactory returns a trace factory that writes JSONL files under dir.
func NewFileFactory(directory string, redactor guardrails.Redactor) *JSONLFactory {
	if redactor == nil {
		redactor = guardrails.NewRedactor()
	}
	directoryValue := str.String(directory)
	return &JSONLFactory{
		directory: directoryValue.Trim(),
		redactor:  redactor,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// NewStateFactory returns a trace factory that persists events through store.
func NewStateFactory(store StateStore, redactor guardrails.Redactor, maxEventsPerSession int) *StateFactory {
	if redactor == nil {
		redactor = guardrails.NewRedactor()
	}

	return &StateFactory{
		store:               store,
		redactor:            redactor,
		maxEventsPerSession: maxEventsPerSession,
		now:                 func() time.Time { return time.Now().UTC() },
	}
}

// NewMultiFactory returns a trace factory that records each event through every factory.
func NewMultiFactory(factories ...Factory) Factory {
	filtered := make([]Factory, 0, len(factories))
	for _, factory := range factories {
		if factory != nil {
			filtered = append(filtered, factory)
		}
	}
	if len(filtered) == 0 {
		return NoopFactory()
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return multiFactory{factories: filtered}
}

// NoopFactory returns a trace factory that discards events.
func NoopFactory() Factory {
	return noopFactory{}
}

// NoopSession returns a trace session that discards events.
func NoopSession() Session {
	return noopSession{}
}

// validateSessionID mirrors internal/trace/inspect getSessionPath rules for the id segment.
func validateSessionID(id string) bool {
	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		return false
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\`) {
		return false
	}
	if filepath.Base(id) != id || id == "." || id == ".." {
		return false
	}

	return true
}

// SessionIDFromTraceFilename returns the storage session id from a time-prefixed trace file basename without ".jsonl".
// Filenames are "<UTC>Z-<session_id>"; the segment after "Z-" is the id. If "Z-" is absent, the whole stem is returned.
func SessionIDFromTraceFilename(stem string) string {
	stemValue := str.String(stem)
	stem = stemValue.Trim()
	if _, after, ok := strings.Cut(stem, "Z-"); ok {
		return after
	}

	return stem
}

// ResolveTraceFilePath returns the path to the JSONL trace file for a storage session id.
// It looks for exactly one file matching "*<session_id>.jsonl" (time-prefixed names included).
// If none exist, it returns [os.ErrNotExist]. If more than one match, it returns [ErrAmbiguousTraceFiles].
func ResolveTraceFilePath(directory, sessionID string) (string, error) {
	directoryValue2 := str.String(directory)
	directory = directoryValue2.Trim()
	if !validateSessionID(sessionID) || directory == "" {
		return "", os.ErrNotExist
	}

	pattern := filepath.Join(directory, "*"+sessionID+".jsonl")
	matches, err := globTraceFiles(pattern)
	if err != nil {
		return "", err
	}

	switch len(matches) {
	case 0:
		return "", os.ErrNotExist
	case 1:
		return matches[0], nil
	default:
		return "", ErrAmbiguousTraceFiles
	}
}

func newTraceFilename(sessionID string, utc time.Time) string {
	return utc.Format(traceTimeLayout) + "-" + sessionID + ".jsonl"
}

func (f *JSONLFactory) tracePathForSession(sessionID string) (string, error) {
	dir := f.directory
	pattern := filepath.Join(dir, "*"+sessionID+".jsonl")
	matches, err := globTraceFiles(pattern)
	if err != nil {
		return "", err
	}

	switch len(matches) {
	case 0:
		return filepath.Join(dir, newTraceFilename(sessionID, f.now())), nil
	case 1:
		return matches[0], nil
	default:
		return "", ErrAmbiguousTraceFiles
	}
}

func (f *JSONLFactory) lockForPath(absPath string) *sync.Mutex {
	v, _ := f.pathLocks.LoadOrStore(absPath, new(sync.Mutex))
	return v.(*sync.Mutex)
}

func (f *JSONLFactory) OpenSession(_ context.Context, sessionID string, metadata Metadata) Session {
	if f == nil {
		return NoopSession()
	}

	directory := str.String(f.directory)
	if directory.Trim() == "" {
		return NoopSession()
	}
	if !validateSessionID(sessionID) {
		log.Warn().Str("sessionID", sessionID).Msg("Invalid trace session id; skipping trace file")
		return NoopSession()
	}

	if err := mkdirAll(f.directory, 0o755); err != nil {
		log.Warn().Err(err).Str("traceDir", f.directory).Msg("Failed to initialize trace directory")
		return NoopSession()
	}

	path, err := f.tracePathForSession(sessionID)
	if err != nil {
		log.Warn().Err(err).Str("sessionID", sessionID).Msg("Failed to resolve trace file path")
		return NoopSession()
	}

	pathLock := f.lockForPath(path)
	pathLock.Lock()
	defer pathLock.Unlock()

	file, err := openTraceFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn().Err(err).Str("tracePath", path).Msg("Failed to open trace session file")
		return NoopSession()
	}

	info, err := statOpenedFile(file)
	if err != nil {
		_ = closeTraceFile(file)
		log.Warn().Err(err).Str("tracePath", path).Msg("Failed to stat trace session file")
		return NoopSession()
	}

	session := &jsonlSession{
		id:       sessionID,
		encoder:  json.NewEncoder(file),
		file:     file,
		redactor: f.redactor,
		pathLock: pathLock,
		path:     path,
	}

	metadata.TraceDir = f.directory
	if info.Size() == 0 {
		session.recordUnlocked(EvtChatStarted, metadata)
	}

	return session
}

func (noopFactory) OpenSession(context.Context, string, Metadata) Session {
	return NoopSession()
}

func (f *StateFactory) OpenSession(ctx context.Context, sessionID string, metadata Metadata) Session {
	if f == nil || f.store == nil {
		return NoopSession()
	}
	if !validateSessionID(sessionID) {
		log.Warn().Str("sessionID", sessionID).Msg("Invalid trace session id; skipping state trace")
		return NoopSession()
	}

	if ctx == nil {
		ctx = context.Background()
	}

	session := &stateSession{
		ctx:                 ctx,
		id:                  sessionID,
		store:               f.store,
		redactor:            f.redactor,
		maxEventsPerSession: f.maxEventsPerSession,
		now:                 f.now,
	}

	result, err := f.store.ListTraceEvents(ctx, storage.TraceQuery{SessionID: sessionID, Limit: 1})
	if err != nil {
		log.Warn().Err(err).Str("sessionID", sessionID).Msg("Failed to inspect state trace session")
	} else if len(result.Events) == 0 {
		session.recordUnlocked(EvtChatStarted, metadata)
	}

	return session
}

func (f multiFactory) OpenSession(ctx context.Context, sessionID string, metadata Metadata) Session {
	sessions := make([]Session, 0, len(f.factories))
	for _, factory := range f.factories {
		session := factory.OpenSession(ctx, sessionID, metadata)
		if session != nil {
			sessions = append(sessions, session)
		}
	}
	if len(sessions) == 0 {
		return NoopSession()
	}
	return multiSession{sessions: sessions}
}

func (s *jsonlSession) ID() string {
	if s == nil {
		return ""
	}

	return s.id
}

func (s *jsonlSession) Record(eventType string, payload any) {
	if s == nil || s.noop {
		return
	}

	s.pathLock.Lock()
	defer s.pathLock.Unlock()

	if s.closed {
		return
	}

	s.recordUnlocked(eventType, payload)
}

func (s *jsonlSession) recordUnlocked(eventType string, payload any) {
	eventTypeValue := str.String(eventType)
	event := Event{
		SessionID: s.id,
		Type:      eventTypeValue.Trim(),
		Timestamp: time.Now().UTC(),
	}
	if payload != nil {
		event.Payload = sanitizeTracePayload(event.Type, payload, s.redactor)
	}
	if err := s.encoder.Encode(event); err != nil {
		log.Warn().Err(err).Str("tracePath", s.path).Str("eventType", event.Type).Msg("Failed to write trace event")
	}
}

func (s *jsonlSession) Close() {
	if s == nil || s.noop {
		return
	}

	s.pathLock.Lock()
	defer s.pathLock.Unlock()

	if s.closed {
		return
	}

	s.closed = true
	if err := closeTraceFile(s.file); err != nil {
		log.Warn().Err(err).Str("tracePath", s.path).Msg("Failed to close trace session file")
	}
}

func (s noopSession) ID() string {
	return ""
}

func (s noopSession) Record(string, any) {
	_ = s
}

func (s noopSession) Close() {
	_ = s
}

func (s *stateSession) ID() string {
	if s == nil {
		return ""
	}

	return s.id
}

func (s *stateSession) Record(eventType string, payload any) {
	if s == nil || s.noop {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.recordUnlocked(eventType, payload)
}

func (s *stateSession) recordUnlocked(eventType string, payload any) {
	eventTypeValue2 := str.String(eventType)
	event := storage.TraceEvent{
		SessionID: s.id,
		Type:      eventTypeValue2.Trim(),
		Timestamp: s.now().UTC(),
	}
	if payload != nil {
		event.Payload = sanitizeTracePayload(event.Type, payload, s.redactor)
	}
	if _, err := s.store.AppendTraceEvent(s.ctx, event); err != nil {
		log.Warn().Err(err).Str("sessionID", s.id).Str("eventType", event.Type).Msg("Failed to write state trace event")
		return
	}
	if s.maxEventsPerSession >= 0 {
		if err := s.store.PruneTraceEvents(s.ctx, s.id, s.maxEventsPerSession); err != nil {
			log.Warn().Err(err).Str("sessionID", s.id).Msg("Failed to prune state trace events")
		}
	}
}

func sanitizeTracePayload(eventType string, payload any, redactor guardrails.Redactor) any {
	if redactor == nil {
		redactor = guardrails.NewRedactor()
	}

	sanitized := redactor.Sanitize(payload)
	if _, ok := payload.(map[string]any); ok {
		return sanitized
	}
	if typed, ok := DecodePayload(eventType, sanitized); ok {
		return typed
	}

	return sanitized
}

func (s *stateSession) Close() {
	if s == nil || s.noop {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
}

func (s multiSession) ID() string {
	for _, session := range s.sessions {
		id := session.ID()
		idValue := str.String(id)
		if idValue.Trim() != "" {
			return id
		}
	}

	return ""
}

func (s multiSession) Record(eventType string, payload any) {
	for _, session := range s.sessions {
		session.Record(eventType, payload)
	}
}

func (s multiSession) Close() {
	for _, session := range s.sessions {
		session.Close()
	}
}
