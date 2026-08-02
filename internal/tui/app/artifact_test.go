package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	browserdomain "github.com/xymorphic/morph/internal/browser"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestBrowserArtifact_ParsesSafeMetadataAndRendersTrustedControls(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	artifact := getBrowserArtifact("browser", `{
		"handle":"artifact_1","kind":"screenshot","name":"screenshot.png","mime_type":"image/png",
		"size":248156,"created_at":"2026-07-21T07:00:00Z","expires_at":"2026-07-22T08:00:00Z"
	}`)
	require.NotNil(t, artifact)
	require.NotEmpty(t, artifact.Token)
	rows := renderBrowserArtifactRows(*artifact, "", now)
	rendered := strings.Join(rows, "\n")
	plain := stripANSI(rendered)
	require.Contains(t, plain, "screenshot.png · 242 KB · expires in 1d")
	require.Contains(t, plain, "Open")
	require.Contains(t, plain, "Save As")
	require.NotContains(t, plain, ".bin")

	openColumn := strings.Index(stripANSI(rows[1]), "Open")
	token, action, ok := getArtifactActionAtRenderedLineColumn(rows[1], openColumn)
	require.True(t, ok)
	require.Equal(t, artifact.Token, token)
	require.Equal(t, "open", action)
	saveColumn := strings.Index(stripANSI(rows[1]), "Save As")
	token, action, ok = getArtifactActionAtRenderedLineColumn(rows[1], saveColumn)
	require.True(t, ok)
	require.Equal(t, artifact.Token, token)
	require.Equal(t, "save", action)
}

func TestBrowserArtifact_RejectsUntrustedOrInvalidToolResults(t *testing.T) {
	require.Nil(t, getBrowserArtifact("read_file", `{"handle":"artifact_1","kind":"screenshot","size":1}`))
	require.Nil(t, getBrowserArtifact("browser", `{"kind":"screenshot","size":1}`))
	require.Nil(t, getBrowserArtifact("browser", `{"handle":"artifact_1","kind":"screenshot","size":-1}`))
	_, _, ok := getArtifactActionAtRenderedLineColumn("Open browser artifact", 0)
	require.False(t, ok)
	_, _, ok = getArtifactActionAtRenderedPosition("Open", -1, 0)
	require.False(t, ok)
	_, _, ok = getArtifactActionAtRenderedPosition("Open", 1, 0)
	require.False(t, ok)
	_, _, ok = getArtifactActionAtRenderedLineColumn(artifactControlOSCPrefix+"broken", 0)
	require.False(t, ok)
}

func TestBrowserArtifact_FormatsSizeRetentionAndTerminalStates(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	require.Equal(t, "12 B", formatBrowserArtifactSize(12))
	require.Equal(t, "1.5 KB", formatBrowserArtifactSize(1536))
	require.Equal(t, "12 MB", formatBrowserArtifactSize(12*1024*1024))
	require.Equal(t, "2.0 GB", formatBrowserArtifactSize(2*1024*1024*1024))
	require.Empty(t, formatBrowserArtifactRetention(time.Time{}, now))
	require.Equal(t, "expired", formatBrowserArtifactRetention(now, now))
	require.Equal(t, "expires in <1m", formatBrowserArtifactRetention(now.Add(30*time.Second), now))
	require.Equal(t, "expires in 4m", formatBrowserArtifactRetention(now.Add(4*time.Minute), now))
	require.Equal(t, "expires in 3h", formatBrowserArtifactRetention(now.Add(3*time.Hour), now))
	require.Equal(t, "expires in 2d", formatBrowserArtifactRetention(now.Add(48*time.Hour), now))

	rows := renderBrowserArtifactRows(browserArtifact{
		Kind: browserdomain.ArtifactPDF, Size: 10, ExpiresAt: now.Add(-time.Second), Token: "token",
	}, "Artifact expired", now)
	require.Equal(t, []string{"pdf · 10 B · expired", "Artifact expired"}, rows)
}

func TestBrowserArtifact_LiveAndHydratedCellsPreserveStructuredMetadata(t *testing.T) {
	content := `{
		"handle":"artifact_1","kind":"screenshot","name":"screenshot.png","mime_type":"image/png",
		"size":3,"created_at":"2026-07-21T07:00:00Z","expires_at":"2026-07-22T08:00:00Z"
	}`
	completed, ok := toolInvocationCompletedMsgFromMessage(morphmsg.Message{
		Role: morphmsg.RoleTool, Name: "browser", ToolCallID: "call_1", Content: content,
	}, time.Now())
	require.True(t, ok)
	cell := defaultTranscriptCellFactory.FromTUIMessage(completed).(toolTranscriptCell)
	require.True(t, cell.hasArtifact)
	require.Equal(t, "artifact_1", cell.artifact.Handle)

	hydrated := defaultTranscriptCellFactory.FromTimelineMessage(morphmsg.Message{
		Role: morphmsg.RoleTool, Name: "browser", ToolCallID: "call_1", Content: content,
	}, map[string]timelineToolCallDetail{"call_1": {detail: "screenshot:Full page"}}).(toolTranscriptCell)
	require.True(t, hydrated.hasArtifact)
	require.Equal(t, "artifact_1", hydrated.artifact.Handle)

	runModel := newModel()
	runModel.messages = []transcriptCell{cell, hydrated}
	latest, found := runModel.getBrowserArtifactByHandle("")
	require.True(t, found)
	require.Equal(t, "artifact_1", latest.Handle)
	byToken, found := runModel.getBrowserArtifactByToken(hydrated.artifact.Token)
	require.True(t, found)
	require.Equal(t, "artifact_1", byToken.Handle)
	_, found = runModel.getBrowserArtifactByToken("missing")
	require.False(t, found)
	_, found = runModel.getBrowserArtifactByHandle("missing")
	require.False(t, found)
}

func TestBrowserArtifact_OpenFetchesOnceAndReusesTemporaryCopy(t *testing.T) {
	now := time.Now().Add(time.Hour)
	api := &artifactBrowserAPI{content: browserdomain.ArtifactContent{
		Artifact: browserdomain.Artifact{Handle: "artifact_1", Size: 3}, Data: []byte("png"),
	}}
	originalOpen := openBrowserArtifactFile
	opened := []string{}
	openBrowserArtifactFile = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	t.Cleanup(func() { openBrowserArtifactFile = originalOpen })
	runModel := newModel()
	runModel.browserClient = api
	runModel.sessionID = "default"
	artifact := browserArtifact{
		Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Name: "screenshot.png",
		MIMEType: "image/png", Size: 3, ExpiresAt: now, Token: "token_1",
	}
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id: "call_1", action: "Browser", detail: "screenshot:Full page", completed: true,
		artifact: artifact, hasArtifact: true,
	}}

	msg := runModel.startBrowserArtifactAction(artifact, "open")()
	result := msg.(browserArtifactActionResultMsg)
	require.NoError(t, result.Err)
	require.NotNil(t, runModel.completeBrowserArtifactAction(result))
	require.Equal(t, 1, api.readCalls)
	require.Len(t, opened, 1)
	require.FileExists(t, opened[0])

	msg = runModel.startBrowserArtifactAction(artifact, "open")()
	result = msg.(browserArtifactActionResultMsg)
	require.NoError(t, result.Err)
	require.Equal(t, 1, api.readCalls)
	require.Len(t, opened, 2)

	staleDirectory := filepath.Dir(opened[0])
	require.NoError(t, os.Remove(opened[0]))
	msg = runModel.startBrowserArtifactAction(artifact, "open")()
	result = msg.(browserArtifactActionResultMsg)
	require.NoError(t, result.Err)
	require.NoDirExists(t, staleDirectory)
	require.Equal(t, 2, api.readCalls)
	require.Len(t, opened, 3)
	require.NotNil(t, runModel.completeBrowserArtifactAction(result))
	require.NotEqual(t, staleDirectory, result.Copy.Directory)
	runModel.cleanupBrowserArtifactCopies()
	require.NoFileExists(t, opened[2])
}

func TestBrowserArtifact_SaveWritesWithoutReplacingAndReportsFailure(t *testing.T) {
	api := &artifactBrowserAPI{content: browserdomain.ArtifactContent{
		Artifact: browserdomain.Artifact{Handle: "artifact_1", Size: 3}, Data: []byte("png"),
	}}
	artifact := browserArtifact{Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Size: 3, Token: "token_1"}
	destination := filepath.Join(t.TempDir(), "saved.png")
	result := readBrowserArtifactCmd(
		context.Background(), api, artifact, "default", "save", destination,
	)().(browserArtifactActionResultMsg)
	require.NoError(t, result.Err)
	canonicalDestination, err := browserdomain.ResolveArtifactExportPath(destination)
	require.NoError(t, err)
	require.Equal(t, canonicalDestination, result.Path)
	content, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, []byte("png"), content)

	result = readBrowserArtifactCmd(
		context.Background(), api, artifact, "default", "save", destination,
	)().(browserArtifactActionResultMsg)
	require.ErrorIs(t, result.Err, os.ErrExist)
	require.Equal(t, "Destination already exists", getBrowserArtifactFailureStatus(result.Err))
}

func TestBrowserArtifact_ActionStateAndSaveCommandView(t *testing.T) {
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	originalTime := currentTime
	currentTime = func() time.Time { return now }
	t.Cleanup(func() { currentTime = originalTime })
	artifact := browserArtifact{
		Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Name: "capture.png",
		Size: 3, ExpiresAt: now.Add(time.Hour), Token: "token_1",
	}
	runModel := newModel()
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id: "call_1", action: "Browser", completed: true, artifact: artifact, hasArtifact: true,
	}}

	require.NotNil(t, runModel.startBrowserArtifactAction(artifact, "save"))
	require.True(t, runModel.isBrowserArtifactSaveCommandView())
	require.Contains(t, stripANSI(
		runModel.renderBrowserArtifactSaveCommandViewContent(commandViewContent{Width: 40}),
	), "Destination")
	next, cmd := runModel.updateBrowserArtifactSaveCommandView(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)
	require.False(t, next.(model).commandView.Visible)

	artifact.ExpiresAt = now
	require.NotNil(t, runModel.startBrowserArtifactAction(artifact, "open"))
	require.Equal(t, "Artifact expired", runModel.messages[0].(toolTranscriptCell).artifactStatus)
	artifact.ExpiresAt = now.Add(time.Hour)
	require.NotNil(t, runModel.startBrowserArtifactAction(artifact, "unknown"))
	require.Equal(t, "browser artifact action unavailable", runModel.status.Text())
	require.NotNil(t, runModel.completeBrowserArtifactAction(browserArtifactActionResultMsg{
		Artifact: artifact, Action: "open", Path: "/tmp/capture.png",
	}))
	require.Equal(t, "browser artifact opened", runModel.status.Text())
}

func TestBrowserArtifact_StatusInvalidatesRenderedTranscript(t *testing.T) {
	runModel := newTranscriptWindowTestModel(80, 8)
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id:          "call_1",
		action:      "Browser",
		detail:      "screenshot:page",
		completed:   true,
		artifact:    browserArtifact{Token: "token_1", Kind: browserdomain.ArtifactScreenshot},
		hasArtifact: true,
	}}
	runModel.setTranscriptContent()
	generation := runModel.transcriptGeneration

	runModel.setBrowserArtifactStatus("token_1", "Artifact unavailable")
	runModel.setTranscriptContent()

	require.Equal(t, generation+1, runModel.transcriptGeneration)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "Artifact unavailable")
}

func TestBrowserArtifact_ReadFailuresDoNotMutateCaptureState(t *testing.T) {
	artifact := browserArtifact{Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Size: 3, Token: "token_1"}
	result := readBrowserArtifactCmd(
		context.Background(), nil, artifact, "default", "open", "",
	)().(browserArtifactActionResultMsg)
	require.EqualError(t, result.Err, "artifact access unavailable")

	api := &artifactBrowserAPI{err: context.Canceled}
	result = readBrowserArtifactCmd(
		context.Background(), api, artifact, "default", "open", "",
	)().(browserArtifactActionResultMsg)
	require.ErrorIs(t, result.Err, context.Canceled)

	api = &artifactBrowserAPI{content: browserdomain.ArtifactContent{
		Artifact: browserdomain.Artifact{Handle: "other", Size: 3}, Data: []byte("png"),
	}}
	result = readBrowserArtifactCmd(
		context.Background(), api, artifact, "default", "open", "",
	)().(browserArtifactActionResultMsg)
	require.EqualError(t, result.Err, "artifact response was incomplete")

	api = &artifactBrowserAPI{content: browserdomain.ArtifactContent{
		Artifact: browserdomain.Artifact{Handle: "artifact_1", Size: 3}, Data: []byte("png"),
	}}
	originalOpen := openBrowserArtifactFile
	openedPath := ""
	openBrowserArtifactFile = func(path string) error {
		openedPath = path
		return errors.New("viewer unavailable")
	}
	t.Cleanup(func() { openBrowserArtifactFile = originalOpen })
	result = readBrowserArtifactCmd(
		context.Background(), api, artifact, "default", "open", "",
	)().(browserArtifactActionResultMsg)
	require.EqualError(t, result.Err, "viewer unavailable")
	require.NotEmpty(t, openedPath)
	require.NoDirExists(t, filepath.Dir(openedPath))

	runModel := newModel()
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id: "call_1", action: "Browser", completed: true, artifact: artifact, hasArtifact: true,
	}}
	require.NotNil(t, runModel.completeBrowserArtifactAction(result))
	cell := runModel.messages[0].(toolTranscriptCell)
	require.True(t, cell.completed)
	require.Equal(t, "Artifact unavailable", cell.artifactStatus)
}

func TestBrowserArtifact_TemporaryFilenameAndExpiryCleanup(t *testing.T) {
	require.Equal(t, "capture.png", getBrowserArtifactFilename(browserArtifact{Name: "../capture.png"}))
	require.Equal(t, "browser-artifact.png", getBrowserArtifactFilename(browserArtifact{MIMEType: "image/png"}))
	require.Equal(t, "browser-artifact", getBrowserArtifactFilename(browserArtifact{}))

	directory := t.TempDir()
	path := filepath.Join(directory, "capture.png")
	require.NoError(t, os.WriteFile(path, []byte("png"), 0o600))
	runModel := newModel()
	runModel.artifactCopies["artifact_1"] = browserArtifactCopy{Path: path, Directory: directory}
	runModel.expireBrowserArtifactCopy(browserArtifactExpiryMsg{Handle: "artifact_1", Path: "other"})
	require.FileExists(t, path)
	runModel.expireBrowserArtifactCopy(browserArtifactExpiryMsg{Handle: "artifact_1", Path: path})
	require.NoDirExists(t, directory)

	directory = t.TempDir()
	path = filepath.Join(directory, "capture.png")
	require.NoError(t, os.WriteFile(path, []byte("png"), 0o600))
	runModel.artifactCopies["artifact_2"] = browserArtifactCopy{Path: path, Directory: directory}
	CleanupTemporaryBrowserArtifacts(runModel)
	require.NoDirExists(t, directory)

	_, _, err := writeTemporaryBrowserArtifact(browserArtifact{Name: "invalid\x00.png"}, []byte("png"))
	require.Error(t, err)
}

func TestBrowserArtifact_SlashCommandUsesMostRecentArtifact(t *testing.T) {
	artifact := browserArtifact{Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Size: 3, Token: "token_1"}
	runModel := newModel()
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id: "call_1", action: "Browser", completed: true, artifact: artifact, hasArtifact: true,
	}}
	require.NotNil(t, runModel.handleBrowserArtifactCommand("save"))
	require.True(t, runModel.isBrowserArtifactSaveCommandView())
	require.NotNil(t, runModel.handleBrowserArtifactCommand("invalid"))
	require.Equal(t, "usage: /artifact open [handle] or /artifact save [handle] [path]", runModel.status.Text())
	require.NotNil(t, runModel.handleBrowserArtifactCommand("open missing"))
	require.Equal(t, "browser artifact not found in transcript", runModel.status.Text())
}

func TestBrowserArtifact_MouseActionRequiresStructuredToolResult(t *testing.T) {
	artifact := browserArtifact{
		Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Name: "capture.png",
		Size: 3, Token: "token_1",
	}
	runModel := newModel()
	runModel.width = 100
	runModel.height = 20
	runModel.resize()
	runModel.messages = transcriptWindowTestCells(300)
	runModel.messages = append(runModel.messages, toolTranscriptCell{
		id: "call_1", action: "Browser", detail: "screenshot:Full page", completed: true,
		artifact: artifact, hasArtifact: true,
	})
	runModel.setTranscriptContent()
	lines := strings.Split(stripANSI(runModel.transcript.View()), "\n")
	row := indexLineContaining(lines, "Open")
	require.NotEqual(t, -1, row)
	column := strings.Index(lines[row], "Open")

	updated, cmd := runModel.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft, X: column, Y: runModel.getTranscriptTop() + row,
	}))
	require.NotNil(t, cmd)
	require.False(t, updated.(model).selection.active)

	forged := newModel()
	forged.width = 100
	forged.height = 20
	forged.resize()
	forged.messages = transcriptWindowTestCells(300)
	forged.messages = append(forged.messages, assistantTranscriptCell{text: "Open Save As artifact_1"})
	forged.setTranscriptContent()
	lines = strings.Split(stripANSI(forged.transcript.View()), "\n")
	row = indexLineContaining(lines, "Open")
	column = strings.Index(lines[row], "Open")
	_, cmd = forged.Update(tea.MouseClickMsg(tea.Mouse{
		Button: tea.MouseLeft, X: column, Y: forged.getTranscriptTop() + row,
	}))
	require.Nil(t, cmd)
}

func TestBrowserArtifact_SaveCommandViewValidatesAndSubmitsDestination(t *testing.T) {
	runModel := newModel()
	artifact := browserArtifact{Handle: "artifact_1", Kind: browserdomain.ArtifactScreenshot, Size: 3, Token: "token_1"}
	runModel.pendingArtifact = artifact
	runModel.showCommandView(commandViewPayload{Kind: commandViewKindArtifactSave})

	next, cmd := runModel.submitBrowserArtifactSave()
	require.NotNil(t, cmd)
	require.True(t, next.(model).commandView.Visible)
	require.Equal(t, "artifact destination is required", runModel.status.Text())

	next, cmd = runModel.updateBrowserArtifactSaveCommandView(tea.PasteMsg{Content: "saved.png\n"})
	require.NotNil(t, cmd)
	runModel = next.(model)
	require.Equal(t, "saved.png", runModel.artifactPathInput.Value())
	next, cmd = runModel.updateBrowserArtifactSaveCommandView(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.False(t, next.(model).commandView.Visible)

	runModel = newModel()
	runModel.messages = []transcriptCell{toolTranscriptCell{
		id: "call_1", action: "Browser", completed: true, artifact: artifact, hasArtifact: true,
	}}
	require.NotNil(t, runModel.completeBrowserArtifactAction(browserArtifactActionResultMsg{
		Artifact: artifact, Action: "save", Path: "/tmp/saved.png",
	}))
	require.Equal(t, "Saved to /tmp/saved.png", runModel.messages[0].(toolTranscriptCell).artifactStatus)
}

func TestBrowserArtifactDestination_ExpandsCurrentUserHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, home, expandBrowserArtifactDestination("~"))
	require.Equal(t, filepath.Join(home, "Desktop", "capture.png"),
		expandBrowserArtifactDestination("~/Desktop/capture.png"))
	require.Equal(t, "capture.png", expandBrowserArtifactDestination("capture.png"))
}

type artifactBrowserAPI struct {
	content   browserdomain.ArtifactContent
	err       error
	readCalls int
}

func (a *artifactBrowserAPI) Status(context.Context) (browserdomain.Status, error) {
	return browserdomain.Status{}, nil
}

func (a *artifactBrowserAPI) Profiles(context.Context) ([]browserdomain.Profile, error) {
	return nil, nil
}

func (a *artifactBrowserAPI) Sessions(context.Context) ([]browserdomain.Session, error) {
	return nil, nil
}

func (a *artifactBrowserAPI) Start(context.Context, string, string) (browserdomain.Session, error) {
	return browserdomain.Session{}, nil
}

func (a *artifactBrowserAPI) Stop(context.Context, string, string) (browserdomain.Session, error) {
	return browserdomain.Session{}, nil
}

func (a *artifactBrowserAPI) ReadArtifact(
	context.Context,
	string,
	string,
	string,
) (browserdomain.ArtifactContent, error) {
	a.readCalls++
	return a.content, a.err
}

func (a *artifactBrowserAPI) EffectiveConfig(context.Context) (rpcclient.BrowserEffectiveConfig, error) {
	return rpcclient.BrowserEffectiveConfig{}, nil
}

func TestBrowserArtifact_FailureStatusIsConcise(t *testing.T) {
	require.Equal(t, "Artifact expired", getBrowserArtifactFailureStatus(errors.New("artifact expired")))
	require.Equal(t, "Artifact access denied", getBrowserArtifactFailureStatus(errors.New("permission denied")))
	require.Equal(t, "Artifact retrieval cancelled", getBrowserArtifactFailureStatus(context.Canceled))
	require.Equal(t, "Artifact unavailable", getBrowserArtifactFailureStatus(errors.New("network failed")))
}
