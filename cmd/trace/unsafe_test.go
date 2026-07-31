package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/profile"
)

func TestUnsafeCommands_ListShowAndPurgeEvidence(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})
	store := guardrails.NewFileUnsafeEvidenceStore(datadir.UnsafeEvidenceDir())
	evidence, err := store.RecordUnsafeEvidence(context.Background(), guardrails.UnsafeEvidence{
		SessionID: "session",
		Source:    "assistant",
		Action:    "redacted",
		Redacted:  true,
		Original:  "before\nTOKEN=secret\nafter",
		Safe:      "before\nTOKEN=***\nafter",
	})
	require.NoError(t, err)

	var output bytes.Buffer
	command := NewCommand()
	command.Writer = &output
	require.NoError(t, command.Run(context.Background(), []string{"trace", "unsafe", "list"}))
	var summaries []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &summaries))
	require.Len(t, summaries, 1)
	require.Equal(t, evidence.ID, summaries[0]["id"])
	require.NotContains(t, summaries[0], "original")
	require.NotContains(t, summaries[0], "safe")

	output.Reset()
	command = NewCommand()
	command.Writer = &output
	require.NoError(t, command.Run(
		context.Background(),
		[]string{"trace", "unsafe", "show", evidence.ID},
	))
	require.Contains(t, output.String(), "Unsafe evidence: "+evidence.ID)
	require.Contains(t, output.String(), "Changes:")
	require.Contains(t, output.String(), "original line 2 -> safe line 2")
	require.Contains(t, output.String(), "column 7")
	require.Contains(t, output.String(), "- TOKEN=secret")
	require.Contains(t, output.String(), "+ TOKEN=***")
	require.Contains(t, output.String(), "morph trace unsafe show "+evidence.ID+" --json")

	output.Reset()
	command = NewCommand()
	command.Writer = &output
	require.NoError(t, command.Run(
		context.Background(),
		[]string{"trace", "unsafe", "show", evidence.ID, "--json"},
	))
	var shown guardrails.UnsafeEvidence
	require.NoError(t, json.Unmarshal(output.Bytes(), &shown))
	require.Equal(t, "before\nTOKEN=secret\nafter", shown.Original)
	require.Equal(t, "before\nTOKEN=***\nafter", shown.Safe)

	command = NewCommand()
	command.Writer = &output
	err = command.Run(context.Background(), []string{"trace", "unsafe", "purge"})
	require.EqualError(t, err, "purging unsafe evidence is permanent; rerun with --yes to confirm")

	output.Reset()
	command = NewCommand()
	command.Writer = &output
	require.NoError(t, command.Run(
		context.Background(),
		[]string{"trace", "unsafe", "purge", "--yes"},
	))
	require.Contains(t, output.String(), "Removed 1 unsafe evidence records.")

	entries, err := filepath.Glob(filepath.Join(datadir.UnsafeEvidenceDir(), "*.json"))
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestUnsafeShow_RejectsMissingID(t *testing.T) {
	command := NewCommand()
	command.Writer = &bytes.Buffer{}

	err := command.Run(context.Background(), []string{"trace", "unsafe", "show"})

	require.EqualError(t, err, "unsafe evidence ID is required")
}

func TestUnsafeShow_FocusesLongLinePreviewAroundChange(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})
	prefix := strings.Repeat("a", 300)
	store := guardrails.NewFileUnsafeEvidenceStore(datadir.UnsafeEvidenceDir())
	evidence, err := store.RecordUnsafeEvidence(context.Background(), guardrails.UnsafeEvidence{
		Source:   "tool.browser",
		Action:   "redacted",
		Redacted: true,
		Original: prefix + "secret",
		Safe:     prefix + "***",
	})
	require.NoError(t, err)
	var output bytes.Buffer
	command := NewCommand()
	command.Writer = &output

	require.NoError(t, command.Run(
		context.Background(),
		[]string{"trace", "unsafe", "show", evidence.ID},
	))

	require.Contains(t, output.String(), "column 301")
	require.Contains(t, output.String(), "     - …")
	require.Contains(t, output.String(), "secret")
	require.NotContains(t, output.String(), strings.Repeat("a", 240))
}

func TestUnsafeShow_CollapsesLargeChangedRegions(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})
	store := guardrails.NewFileUnsafeEvidenceStore(datadir.UnsafeEvidenceDir())
	evidence, err := store.RecordUnsafeEvidence(context.Background(), guardrails.UnsafeEvidence{
		Source:   "tool.browser",
		Action:   "redacted",
		Redacted: true,
		Original: strings.Join([]string{"one", "two", "three", "four", "five", "six", "seven"}, "\n"),
		Safe:     "one\n***\nseven",
	})
	require.NoError(t, err)
	var output bytes.Buffer
	command := NewCommand()
	command.Writer = &output

	require.NoError(t, command.Run(
		context.Background(),
		[]string{"trace", "unsafe", "show", evidence.ID},
	))

	require.Contains(t, output.String(), "original lines 2-6 -> safe line 2")
	require.Contains(t, output.String(), "… 1 line omitted …")
}

func TestUnsafeList_ReturnsEmptyJSONWhenNoEvidenceExists(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})
	var output bytes.Buffer
	command := NewCommand()
	command.Writer = &output

	require.NoError(t, command.Run(context.Background(), []string{"trace", "unsafe", "list"}))

	var summaries []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &summaries))
	require.Empty(t, summaries)
}

func TestUnsafeShow_RejectsInvalidID(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})
	command := NewCommand()
	command.Writer = &bytes.Buffer{}

	err := command.Run(context.Background(), []string{"trace", "unsafe", "show", "../secret"})

	require.EqualError(t, err, "unsafe evidence ID is invalid")
}
