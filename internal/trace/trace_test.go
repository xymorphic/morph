package trace

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/guardrails"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

func init() {
	zerolog.SetGlobalLevel(zerolog.Disabled)
}

const testTraceSessionID = "ses_testtraceid"

func TestJSONLFactory_OpenSessionCreatesSessionAndWritesEvents(t *testing.T) {
	dir := t.TempDir()
	factory := NewFileFactory(dir, guardrails.NewRedactor())

	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{AgentName: "morph", Model: "gpt-5.1", API: "openai-responses", Source: "agent"})
	require.Equal(t, testTraceSessionID, session.ID())
	session.Record(EvtModelRequest, map[string]any{"authorization": "Bearer secret", "message": "hello"})
	session.Close()

	matches, err := filepath.Glob(filepath.Join(dir, "*"+testTraceSessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	file, err := os.Open(matches[0])
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	var events []Event
	for scanner.Scan() {
		var event Event
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, events, 2)
	require.Equal(t, EvtChatStarted, events[0].Type)
	require.Equal(t, EvtModelRequest, events[1].Type)
	payload := events[1].Payload.(map[string]any)
	require.Equal(t, "[REDACTED]", payload["authorization"])
	require.Equal(t, "hello", payload["message"])
}

func TestMetadataOmitsUnsetLineageTimes(t *testing.T) {
	data, err := json.Marshal(Metadata{Source: "agent"})

	require.NoError(t, err)
	require.NotContains(t, string(data), "spawned_at")
	require.NotContains(t, string(data), "completed_at")
}

func TestJSONLFactory_OpenSessionSecondOpenAppendsWithoutDuplicateChatStarted(t *testing.T) {
	dir := t.TempDir()
	factory := NewFileFactory(dir, guardrails.NewRedactor())
	meta := Metadata{AgentName: "morph", Model: "m", API: "openai-responses", Source: "agent"}

	s1 := factory.OpenSession(context.Background(), testTraceSessionID, meta)
	s1.Record(EvtModelRequest, map[string]any{"n": 1})
	s1.Close()

	s2 := factory.OpenSession(context.Background(), testTraceSessionID, meta)
	s2.Record(EvtModelResponse, map[string]any{"ok": true})
	s2.Close()

	matches, err := filepath.Glob(filepath.Join(dir, "*"+testTraceSessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	dataValue := str.String(string(data))
	lines := strings.Split(dataValue.Trim(), "\n")
	require.Len(t, lines, 3)

	var types []string
	for _, line := range lines {
		var event Event
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		types = append(types, event.Type)
	}
	require.Equal(t, []string{EvtChatStarted, EvtModelRequest, EvtModelResponse}, types)
}

func TestJSONLFactory_OpenSessionReturnsNoopWhenDirectoryIsEmpty(t *testing.T) {
	factory := NewFileFactory("", guardrails.NewRedactor())
	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", session.ID())
	session.Record("ignored", nil)
	session.Close()
}

func TestJSONLFactory_OpenSessionReturnsNoopWhenSessionIDInvalid(t *testing.T) {
	dir := t.TempDir()
	factory := NewFileFactory(dir, guardrails.NewRedactor())
	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		session := factory.OpenSession(context.Background(), id, Metadata{})
		require.Equal(t, "", session.ID(), id)
		session.Close()
	}
}

func TestNoopFactory_OpenSessionReturnsSession(t *testing.T) {
	session := NoopFactory().OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", session.ID())
	session.Record("ignored", map[string]any{"x": 1})
	session.Close()
}

func TestJSONLSession_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	session := NewFileFactory(dir, guardrails.NewRedactor()).OpenSession(context.Background(), testTraceSessionID, Metadata{})
	session.Close()
	session.Close()
}

func TestJSONLFactory_OpenSessionReturnsNoopWhenDirectoryInitializationFails(t *testing.T) {
	dir := t.TempDir()
	blockedPath := filepath.Join(dir, "blocked")
	require.NoError(t, os.WriteFile(blockedPath, []byte("x"), 0o600))

	session := NewFileFactory(filepath.Join(blockedPath, "child"), guardrails.NewRedactor()).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", session.ID())
}

func TestJSONLSession_RecordAfterCloseIsIgnored(t *testing.T) {
	dir := t.TempDir()
	factory := NewFileFactory(dir, guardrails.NewRedactor())
	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	session.Close()
	session.Record("ignored", map[string]any{"x": 1})

	matches, err := filepath.Glob(filepath.Join(dir, "*"+testTraceSessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	contentValue := str.String(string(content))
	require.Equal(t, 1, len(strings.Split(contentValue.Trim(), "\n")))
}

func TestJSONLSession_IDHandlesNilReceiver(t *testing.T) {
	var session *jsonlSession
	require.Equal(t, "", session.ID())
}

func TestNewFactory_UsesDefaultRedactor(t *testing.T) {
	factory := NewFileFactory(t.TempDir(), nil)
	require.NotNil(t, factory.redactor)
}

func TestJSONLFactory_OpenSessionHandlesNilReceiver(t *testing.T) {
	var factory *JSONLFactory
	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", session.ID())
}

func TestJSONLFactory_OpenSessionReturnsNoopWhenOpenFails(t *testing.T) {
	originalOpen := openTraceFile
	openTraceFile = func(string, int, os.FileMode) (*os.File, error) {
		return nil, os.ErrPermission
	}
	defer func() {
		openTraceFile = originalOpen
	}()

	session := NewFileFactory(t.TempDir(), guardrails.NewRedactor()).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", session.ID())
}

func TestJSONLFactory_OpenSessionReturnsNoopWhenMkdirFails(t *testing.T) {
	originalMkdirAll := mkdirAll
	mkdirAll = func(string, os.FileMode) error {
		return os.ErrPermission
	}
	defer func() {
		mkdirAll = originalMkdirAll
	}()

	session := NewFileFactory(t.TempDir(), guardrails.NewRedactor()).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", session.ID())
}

func TestNoopSession_NoOps(t *testing.T) {
	session := noopSession{}
	require.Equal(t, "", session.ID())
	session.Record("ignored", map[string]any{"x": 1})
	session.Close()
}

func TestJSONLSession_RecordHandlesNilReceiverAndNoop(t *testing.T) {
	var session *jsonlSession
	session.Record("ignored", nil)

	noop := &jsonlSession{noop: true}
	noop.Record("ignored", nil)
}

func TestJSONLSession_RecordHandlesEncoderError(t *testing.T) {
	dir := t.TempDir()
	session := NewFileFactory(dir, guardrails.NewRedactor()).
		OpenSession(context.Background(), testTraceSessionID, Metadata{}).(*jsonlSession)
	require.NoError(t, session.file.Close())
	session.Record("broken", map[string]any{"x": 1})
}

func TestJSONLSession_CloseHandlesNilReceiverAndNoop(t *testing.T) {
	var session *jsonlSession
	session.Close()

	noop := &jsonlSession{noop: true}
	noop.Close()
}

func TestJSONLSession_CloseHandlesFileCloseError(t *testing.T) {
	dir := t.TempDir()
	session := NewFileFactory(dir, guardrails.NewRedactor()).
		OpenSession(context.Background(), testTraceSessionID, Metadata{}).(*jsonlSession)
	require.NoError(t, session.file.Close())
	session.Close()
}

func TestSessionIDFromTraceFilename(t *testing.T) {
	require.Equal(t, "ses_abc", SessionIDFromTraceFilename("20060102T150405.000000000Z-ses_abc"))
	require.Equal(t, "plainstem", SessionIDFromTraceFilename("plainstem"))
}

func TestResolveTraceFilePath_TimePrefixedFile(t *testing.T) {
	dir := t.TempDir()
	name := "20260102T000000.000000000Z-ses_x.jsonl"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	p, err := ResolveTraceFilePath(dir, "ses_x")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, name), p)
}

func TestResolveTraceFilePath_NotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveTraceFilePath(dir, "ses_missing")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestResolveTraceFilePath_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a-ses_x.jsonl"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b-ses_x.jsonl"), []byte("y"), 0o644))
	_, err := ResolveTraceFilePath(dir, "ses_x")
	require.ErrorIs(t, err, ErrAmbiguousTraceFiles)
}

func TestResolveTraceFilePath_GlobError(t *testing.T) {
	orig := globTraceFiles
	globTraceFiles = func(string) ([]string, error) {
		return nil, os.ErrPermission
	}
	defer func() { globTraceFiles = orig }()

	_, err := ResolveTraceFilePath(t.TempDir(), testTraceSessionID)
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestResolveTraceFilePath_EmptyDirectory(t *testing.T) {
	_, err := ResolveTraceFilePath("", testTraceSessionID)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestResolveTraceFilePath_WhitespaceDirectory(t *testing.T) {
	_, err := ResolveTraceFilePath("   ", testTraceSessionID)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestJSONLFactory_OpenSessionGlobErrorReturnsNoop(t *testing.T) {
	dir := t.TempDir()
	orig := globTraceFiles
	globTraceFiles = func(string) ([]string, error) {
		return nil, os.ErrPermission
	}
	defer func() { globTraceFiles = orig }()

	factory := NewFileFactory(dir, guardrails.NewRedactor())
	s := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", s.ID())
	s.Close()
}

func TestJSONLFactory_OpenSessionStatErrorReturnsNoop(t *testing.T) {
	dir := t.TempDir()
	factory := NewFileFactory(dir, guardrails.NewRedactor())

	origStat := statOpenedFile
	statOpenedFile = func(*os.File) (os.FileInfo, error) {
		return nil, os.ErrPermission
	}
	defer func() { statOpenedFile = origStat }()

	s := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, "", s.ID())
	s.Close()
}

func TestJSONLSession_CloseTraceFileError(t *testing.T) {
	dir := t.TempDir()
	session := NewFileFactory(dir, guardrails.NewRedactor()).OpenSession(context.Background(), testTraceSessionID, Metadata{}).(*jsonlSession)

	origClose := closeTraceFile
	closeTraceFile = func(*os.File) error {
		return os.ErrPermission
	}
	defer func() { closeTraceFile = origClose }()

	session.Close()
}

func TestJSONLFactory_OpenSessionAmbiguousReturnsNoop(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a-ses_amb.jsonl"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b-ses_amb.jsonl"), []byte("y"), 0o644))
	factory := NewFileFactory(dir, guardrails.NewRedactor())
	s := factory.OpenSession(context.Background(), "ses_amb", Metadata{})
	require.Equal(t, "", s.ID())
	s.Close()
}

func TestStateFactory_OpenSessionRecordsSanitizedEvents(t *testing.T) {
	store := &traceStateStoreStub{}
	factory := NewStateFactory(store, guardrails.NewRedactor(), 10)

	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{AgentName: "morph"})
	require.Equal(t, testTraceSessionID, session.ID())
	session.Record(EvtModelRequest, map[string]any{"authorization": "Bearer secret", "message": "hello"})
	session.Close()

	require.Equal(t, []string{EvtChatStarted, EvtModelRequest}, traceStoreEventTypes(store.events))
	payload, ok := store.events[1].Payload.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED]", payload["authorization"])
	require.Equal(t, "hello", payload["message"])
	require.Equal(t, []int{10, 10}, store.pruneCaps)
}

func TestStateFactory_UsesDefaultRedactor(t *testing.T) {
	factory := NewStateFactory(&traceStateStoreStub{}, nil, 10)

	require.NotNil(t, factory.redactor)
}

func TestStateFactory_OpenSessionReturnsNoopWhenUnavailable(t *testing.T) {
	require.Equal(t, "", NewStateFactory(nil, guardrails.NewRedactor(), 10).
		OpenSession(context.Background(), testTraceSessionID, Metadata{}).ID())

	var factory *StateFactory
	require.Equal(t, "", factory.OpenSession(context.Background(), testTraceSessionID, Metadata{}).ID())
}

func TestStateFactory_OpenSessionReturnsNoopWhenSessionIDInvalid(t *testing.T) {
	session := NewStateFactory(&traceStateStoreStub{}, guardrails.NewRedactor(), 10).
		OpenSession(context.Background(), "bad/id", Metadata{})

	require.Equal(t, "", session.ID())
}

func TestStateFactory_OpenSessionDoesNotDuplicateChatStarted(t *testing.T) {
	store := &traceStateStoreStub{
		events: []storage.TraceEvent{{SessionID: testTraceSessionID, Type: EvtChatStarted}},
	}
	factory := NewStateFactory(store, guardrails.NewRedactor(), 10)

	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	session.Record(EvtModelResponse, map[string]any{"ok": true})

	require.Equal(t, []string{EvtChatStarted, EvtModelResponse}, traceStoreEventTypes(store.events))
}

func TestStateFactory_OpenSessionContinuesWhenInspectFails(t *testing.T) {
	store := &traceStateStoreStub{listErr: os.ErrPermission}
	session := NewStateFactory(store, guardrails.NewRedactor(), 10).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})

	require.Equal(t, testTraceSessionID, session.ID())
	session.Record(EvtModelRequest, map[string]any{"message": "hello"})
	require.Equal(t, []string{EvtModelRequest}, traceStoreEventTypes(store.events))
}

func TestStateFactory_OpenSessionUsesBackgroundContextWhenNil(t *testing.T) {
	store := &traceStateStoreStub{}
	var nilContext context.Context
	session := NewStateFactory(store, guardrails.NewRedactor(), 10).
		OpenSession(nilContext, testTraceSessionID, Metadata{})

	session.Record(EvtModelRequest, map[string]any{"message": "hello"})
	require.Equal(t, []string{EvtChatStarted, EvtModelRequest}, traceStoreEventTypes(store.events))
}

func TestStateSession_RecordIgnoresStoreFailures(t *testing.T) {
	store := &traceStateStoreStub{appendErr: os.ErrPermission, pruneErr: os.ErrPermission}
	session := NewStateFactory(store, guardrails.NewRedactor(), 1).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})

	session.Record(EvtModelRequest, map[string]any{"x": 1})
	session.Close()
	session.Record(EvtModelResponse, map[string]any{"x": 2})
}

func TestStateSession_RecordIgnoresPruneFailure(t *testing.T) {
	store := &traceStateStoreStub{pruneErr: os.ErrPermission}
	session := NewStateFactory(store, guardrails.NewRedactor(), 1).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})

	session.Record(EvtModelRequest, map[string]any{"x": 1})

	require.Equal(t, []string{EvtChatStarted, EvtModelRequest}, traceStoreEventTypes(store.events))
	require.Equal(t, []int{1, 1}, store.pruneCaps)
}

func TestStateSession_Noops(t *testing.T) {
	var session *stateSession
	require.Equal(t, "", session.ID())
	session.Record("ignored", nil)
	session.Close()

	noop := &stateSession{noop: true}
	noop.Record("ignored", nil)
	noop.Close()
}

func TestStateSession_RecordAfterCloseIsIgnored(t *testing.T) {
	store := &traceStateStoreStub{}
	session := NewStateFactory(store, guardrails.NewRedactor(), 10).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})

	session.Close()
	session.Record(EvtModelRequest, map[string]any{"x": 1})

	require.Equal(t, []string{EvtChatStarted}, traceStoreEventTypes(store.events))
}

func TestStateSession_RecordSkipsPruneWhenCapIsNegative(t *testing.T) {
	store := &traceStateStoreStub{}
	session := NewStateFactory(store, guardrails.NewRedactor(), -1).
		OpenSession(context.Background(), testTraceSessionID, Metadata{})

	session.Record(EvtModelRequest, map[string]any{"x": 1})

	require.Equal(t, []string{EvtChatStarted, EvtModelRequest}, traceStoreEventTypes(store.events))
	require.Empty(t, store.pruneCaps)
}

func TestMultiFactory_FansOutRecords(t *testing.T) {
	left := &traceStateStoreStub{}
	right := &traceStateStoreStub{}
	factory := NewMultiFactory(
		NewStateFactory(left, guardrails.NewRedactor(), 10),
		NewStateFactory(right, guardrails.NewRedactor(), 10),
	)

	session := factory.OpenSession(context.Background(), testTraceSessionID, Metadata{})
	require.Equal(t, testTraceSessionID, session.ID())
	session.Record(EvtToolInvocationStarted, map[string]any{"name": "shell"})
	session.Close()

	require.Equal(t, []string{EvtChatStarted, EvtToolInvocationStarted}, traceStoreEventTypes(left.events))
	require.Equal(t, []string{EvtChatStarted, EvtToolInvocationStarted}, traceStoreEventTypes(right.events))
}

func TestMultiFactory_ReturnsNoopOrSingleFactory(t *testing.T) {
	require.Equal(t, "", NewMultiFactory(nil).OpenSession(context.Background(), testTraceSessionID, Metadata{}).ID())

	store := &traceStateStoreStub{}
	factory := NewStateFactory(store, guardrails.NewRedactor(), 10)
	require.Same(t, factory, NewMultiFactory(nil, factory))
}

func TestMultiFactory_ReturnsNoopWhenChildrenReturnNil(t *testing.T) {
	session := multiFactory{factories: []Factory{nilSessionFactory{}}}.
		OpenSession(context.Background(), testTraceSessionID, Metadata{})

	require.Equal(t, "", session.ID())
}

func TestMultiSession_IDFallsBackToFirstNonEmptySession(t *testing.T) {
	session := multiSession{sessions: []Session{
		NoopSession(),
		&stateSession{id: testTraceSessionID},
	}}

	require.Equal(t, testTraceSessionID, session.ID())
	require.Equal(t, "", multiSession{sessions: []Session{NoopSession()}}.ID())
}

type traceStateStoreStub struct {
	events    []storage.TraceEvent
	appendErr error
	listErr   error
	pruneErr  error
	pruneCaps []int
}

type nilSessionFactory struct{}

func (nilSessionFactory) OpenSession(context.Context, string, Metadata) Session {
	return nil
}

func (s *traceStateStoreStub) AppendTraceEvent(_ context.Context, event storage.TraceEvent) (storage.TraceEvent, error) {
	if s.appendErr != nil {
		return storage.TraceEvent{}, s.appendErr
	}

	event.ID = uint(len(s.events) + 1)
	event.Sequence = len(s.events) + 1
	s.events = append(s.events, event)
	return event, nil
}

func (s *traceStateStoreStub) ListTraceEvents(_ context.Context, query storage.TraceQuery) (storage.TraceResult, error) {
	if s.listErr != nil {
		return storage.TraceResult{}, s.listErr
	}

	events := make([]storage.TraceEvent, 0, len(s.events))
	for _, event := range s.events {
		if storage.TraceEventMatchesQuery(event, query) {
			events = append(events, event)
		}
	}
	if query.Limit > 0 && len(events) > query.Limit {
		events = events[:query.Limit]
	}
	return storage.TraceResult{Events: events}, nil
}

func (s *traceStateStoreStub) PruneTraceEvents(_ context.Context, _ string, maxEvents int) error {
	s.pruneCaps = append(s.pruneCaps, maxEvents)
	return s.pruneErr
}

func traceStoreEventTypes(events []storage.TraceEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}

	return types
}
