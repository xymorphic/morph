package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	browserdomain "github.com/wandxy/morph/internal/browser"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/pkg/str"
)

const artifactControlOSCPrefix = "\x1b]777;artifact;"

var openBrowserArtifactFile = defaultOpenBrowserArtifactFile

type browserArtifact struct {
	Handle    string                     `json:"handle"`
	Kind      browserdomain.ArtifactKind `json:"kind"`
	Name      string                     `json:"name"`
	MIMEType  string                     `json:"mime_type"`
	Size      int64                      `json:"size"`
	CreatedAt time.Time                  `json:"created_at"`
	ExpiresAt time.Time                  `json:"expires_at"`
	Token     string                     `json:"-"`
}

type browserArtifactCopy struct {
	Path      string
	Directory string
	ExpiresAt time.Time
}

type browserArtifactActionResultMsg struct {
	Artifact browserArtifact
	Action   string
	Path     string
	Copy     browserArtifactCopy
	Err      error
}

type browserArtifactExpiryMsg struct {
	Handle string
	Path   string
}

func getBrowserArtifact(name string, content string) *browserArtifact {
	if normalizeToolDisplayName(name) != "browser" {
		return nil
	}
	var artifact browserArtifact
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &artifact); err != nil {
		return nil
	}
	artifact.Handle = strings.TrimSpace(artifact.Handle)
	artifact.Name = strings.TrimSpace(artifact.Name)
	artifact.MIMEType = strings.TrimSpace(artifact.MIMEType)
	if artifact.Handle == "" || artifact.Kind == "" || artifact.Size < 0 {
		return nil
	}
	artifact.Token = newBrowserArtifactToken()
	return &artifact
}

func newBrowserArtifactToken() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return ""
	}
	return hex.EncodeToString(value)
}

func renderBrowserArtifactRows(artifact browserArtifact, status string, now time.Time) []string {
	name := artifact.Name
	if name == "" {
		name = string(artifact.Kind)
	}
	metadata := name + " · " + formatBrowserArtifactSize(artifact.Size)
	if retention := formatBrowserArtifactRetention(artifact.ExpiresAt, now); retention != "" {
		metadata += " · " + retention
	}
	rows := []string{metadata}
	if artifact.Token != "" && (artifact.ExpiresAt.IsZero() || now.Before(artifact.ExpiresAt)) {
		rows = append(rows, renderBrowserArtifactControl(artifact.Token, "open", "Open")+
			"  "+renderBrowserArtifactControl(artifact.Token, "save", "Save As"))
	}
	if status != "" {
		rows = append(rows, status)
	}
	return rows
}

func formatBrowserArtifactSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB"}
	value := float64(size)
	unit := "B"
	for _, candidate := range units {
		value /= 1024
		unit = candidate
		if value < 1024 {
			break
		}
	}
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, unit)
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func formatBrowserArtifactRetention(expiresAt time.Time, now time.Time) string {
	if expiresAt.IsZero() {
		return ""
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return "expired"
	}
	if remaining < time.Minute {
		return "expires in <1m"
	}
	if remaining < time.Hour {
		return "expires in " + strconv.Itoa(int(remaining.Round(time.Minute)/time.Minute)) + "m"
	}
	if remaining < 24*time.Hour {
		return "expires in " + strconv.Itoa(int(remaining.Round(time.Hour)/time.Hour)) + "h"
	}
	return "expires in " + strconv.Itoa(int(remaining.Round(24*time.Hour)/(24*time.Hour))) + "d"
}

func renderBrowserArtifactControl(token string, action string, label string) string {
	start := artifactControlOSCPrefix + token + ";" + action + "\a"
	end := artifactControlOSCPrefix + ";\a"
	return start + lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Underline(true).
		Render(label) + end
}

func getArtifactActionAtRenderedPosition(rendered string, row int, column int) (string, string, bool) {
	if row < 0 || column < 0 {
		return "", "", false
	}
	lines := strings.Split(rendered, "\n")
	if row >= len(lines) {
		return "", "", false
	}
	return getArtifactActionAtRenderedLineColumn(lines[row], column)
}

func getArtifactActionAtRenderedLineColumn(line string, column int) (string, string, bool) {
	token := ""
	action := ""
	cell := 0
	for index := 0; index < len(line); {
		if line[index] == '\x1b' {
			if next, nextToken, nextAction, ok := parseArtifactControlEscape(line, index); ok {
				token = nextToken
				action = nextAction
				index = next
				continue
			}
			if next, ok := skipANSIEscape(line, index); ok {
				index = next
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		if size <= 0 {
			break
		}
		width := max(xansi.StringWidth(string(r)), 0)
		if width > 0 && column >= cell && column < cell+width && token != "" && action != "" {
			return token, action, true
		}
		cell += width
		index += size
	}
	return "", "", false
}

func parseArtifactControlEscape(line string, start int) (int, string, string, bool) {
	if !strings.HasPrefix(line[start:], artifactControlOSCPrefix) {
		return start, "", "", false
	}
	payloadStart := start + len("\x1b]")
	payloadEnd, terminatorWidth, ok := findOSCEnd(line, payloadStart)
	if !ok {
		return start, "", "", false
	}
	parts := strings.Split(line[payloadStart:payloadEnd], ";")
	if len(parts) != 4 || parts[0] != "777" || parts[1] != "artifact" {
		return payloadEnd + terminatorWidth, "", "", true
	}
	return payloadEnd + terminatorWidth, parts[2], parts[3], true
}

func (m *model) getBrowserArtifactByToken(token string) (browserArtifact, bool) {
	for index := len(m.messages) - 1; index >= 0; index-- {
		cell, ok := m.messages[index].(toolTranscriptCell)
		if ok && cell.hasArtifact && cell.artifact.Token == token {
			return cell.artifact, true
		}
	}
	return browserArtifact{}, false
}

func (m *model) getBrowserArtifactByHandle(handle string) (browserArtifact, bool) {
	handle = strings.TrimSpace(handle)
	for index := len(m.messages) - 1; index >= 0; index-- {
		cell, ok := m.messages[index].(toolTranscriptCell)
		if ok && cell.hasArtifact && (handle == "" || cell.artifact.Handle == handle) {
			return cell.artifact, true
		}
	}
	return browserArtifact{}, false
}

func (m *model) startBrowserArtifactAction(artifact browserArtifact, action string) tea.Cmd {
	if !artifact.ExpiresAt.IsZero() && !currentTime().Before(artifact.ExpiresAt) {
		m.setBrowserArtifactStatus(artifact.Token, "Artifact expired")
		return m.setStatus("browser artifact expired")
	}
	switch action {
	case "open":
		if copy, ok := m.artifactCopies[artifact.Handle]; ok {
			if (copy.ExpiresAt.IsZero() || currentTime().Before(copy.ExpiresAt)) && isRegularFile(copy.Path) {
				return openCachedBrowserArtifactCmd(artifact, copy)
			}
			_ = os.RemoveAll(copy.Directory)
			delete(m.artifactCopies, artifact.Handle)
		}
		return readBrowserArtifactCmd(m.chatCtx, m.browserClient, artifact, m.getCurrentSessionID(), "open", "")
	case "save":
		return m.showBrowserArtifactSavePrompt(artifact)
	default:
		return m.setStatus("browser artifact action unavailable")
	}
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func readBrowserArtifactCmd(
	ctx context.Context,
	client rpcclient.BrowserAPI,
	artifact browserArtifact,
	ownerSession string,
	action string,
	destination string,
) tea.Cmd {
	if client == nil {
		return func() tea.Msg {
			return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Err: errors.New("artifact access unavailable")}
		}
	}
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		content, err := client.ReadArtifact(ctx, artifact.Handle, ownerSession, "")
		if err != nil {
			return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Err: err}
		}
		if content.Artifact.Handle != artifact.Handle || int64(len(content.Data)) != content.Artifact.Size {
			return browserArtifactActionResultMsg{
				Artifact: artifact, Action: action, Err: errors.New("artifact response was incomplete"),
			}
		}
		if action == "save" {
			path, resolveErr := browserdomain.ResolveArtifactExportPath(expandBrowserArtifactDestination(destination))
			if resolveErr != nil {
				return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Err: resolveErr}
			}
			if writeErr := browserdomain.WriteArtifactExport(path, content.Data); writeErr != nil {
				return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Err: writeErr}
			}
			return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Path: path}
		}
		directory, path, writeErr := writeTemporaryBrowserArtifact(artifact, content.Data)
		if writeErr != nil {
			return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Err: writeErr}
		}
		if openErr := openBrowserArtifactFile(path); openErr != nil {
			_ = os.RemoveAll(directory)
			return browserArtifactActionResultMsg{Artifact: artifact, Action: action, Err: openErr}
		}
		return browserArtifactActionResultMsg{
			Artifact: artifact, Action: action, Path: path,
			Copy: browserArtifactCopy{Path: path, Directory: directory, ExpiresAt: artifact.ExpiresAt},
		}
	}
}

func expandBrowserArtifactDestination(path string) string {
	path = strings.TrimSpace(path)
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func openCachedBrowserArtifactCmd(artifact browserArtifact, copy browserArtifactCopy) tea.Cmd {
	return func() tea.Msg {
		err := openBrowserArtifactFile(copy.Path)
		return browserArtifactActionResultMsg{Artifact: artifact, Action: "open", Path: copy.Path, Copy: copy, Err: err}
	}
}

func writeTemporaryBrowserArtifact(artifact browserArtifact, data []byte) (string, string, error) {
	directory, err := os.MkdirTemp("", "morph-browser-artifact-*")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	directory = resolvedDirectory
	path := filepath.Join(directory, getBrowserArtifactFilename(artifact))
	if err := browserdomain.WriteArtifactExport(path, data); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return directory, path, nil
}

func getBrowserArtifactFilename(artifact browserArtifact) string {
	name := filepath.Base(strings.TrimSpace(artifact.Name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "browser-artifact"
	}
	if filepath.Ext(name) == "" {
		if extensions, err := mime.ExtensionsByType(artifact.MIMEType); err == nil && len(extensions) > 0 {
			name += extensions[0]
		}
	}
	return name
}

func defaultOpenBrowserArtifactFile(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func newArtifactPathInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Destination path"
	input.CharLimit = 4096
	input.Focus()
	styles := input.Styles()
	styles.Focused.Text = styles.Focused.Text.Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).UnsetBackground()
	styles.Focused.Placeholder = styles.Focused.Placeholder.Foreground(lipgloss.Color(defaultTUITheme.MutedText)).UnsetBackground()
	styles.Focused.Prompt = styles.Focused.Prompt.UnsetBackground()
	styles.Cursor.Blink = false
	input.SetStyles(styles)
	return input
}

func (m *model) showBrowserArtifactSavePrompt(artifact browserArtifact) tea.Cmd {
	m.pendingArtifact = artifact
	m.artifactPathInput = newArtifactPathInput()
	m.showCommandView(commandViewPayload{
		Kind: commandViewKindArtifactSave, TitleLeft: "Save browser artifact", TitleSubtext: artifact.Name,
		TitleRight: "enter to save · esc to close",
	})
	return m.setStatus("choose artifact destination")
}

func (m model) isBrowserArtifactSaveCommandView() bool {
	return m.commandView.Visible && m.commandView.Kind == commandViewKindArtifactSave
}

func (m model) renderBrowserArtifactSaveCommandViewContent(content commandViewContent) string {
	input := m.artifactPathInput
	input.SetWidth(max(content.Width, 1))
	return input.View()
}

func (m *model) updateBrowserArtifactSaveCommandView(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		msg.Content = strings.TrimRight(msg.Content, "\r\n")
		var cmd tea.Cmd
		m.artifactPathInput, cmd = m.artifactPathInput.Update(msg)
		return *m, inputHandledCmd(cmd)
	case tea.KeyPressMsg:
		switch msg.Key().Code {
		case tea.KeyEsc:
			next := m.hideCommandView()
			return next, next.setStatus("artifact save cancelled")
		case tea.KeyEnter:
			return m.submitBrowserArtifactSave()
		}
	}
	var cmd tea.Cmd
	m.artifactPathInput, cmd = m.artifactPathInput.Update(msg)
	return *m, inputHandledCmdForMsg(msg, cmd)
}

func (m *model) submitBrowserArtifactSave() (tea.Model, tea.Cmd) {
	destination := str.String(m.artifactPathInput.Value()).Trim()
	if destination == "" || m.pendingArtifact.Handle == "" {
		return *m, m.setStatus("artifact destination is required")
	}
	artifact := m.pendingArtifact
	m.applyAction(hideCommandViewAction{})
	m.resize()
	return *m, tea.Batch(
		m.setStatus("saving browser artifact"),
		readBrowserArtifactCmd(m.chatCtx, m.browserClient, artifact, m.getCurrentSessionID(), "save", destination),
	)
}

func (m *model) completeBrowserArtifactAction(msg browserArtifactActionResultMsg) tea.Cmd {
	if msg.Err != nil {
		status := getBrowserArtifactFailureStatus(msg.Err)
		m.setBrowserArtifactStatus(msg.Artifact.Token, status)
		m.refreshTranscriptContentAfterMessageUpdate()
		return m.setStatus(strings.ToLower(status))
	}
	status := "Opened in default application"
	if msg.Action == "save" {
		status = "Saved to " + msg.Path
	} else if msg.Copy.Path != "" {
		m.artifactCopies[msg.Artifact.Handle] = msg.Copy
	}
	m.setBrowserArtifactStatus(msg.Artifact.Token, status)
	m.refreshTranscriptContentAfterMessageUpdate()
	if msg.Action == "open" {
		if !msg.Copy.ExpiresAt.IsZero() {
			return tea.Batch(
				m.setStatus("browser artifact opened"),
				getBrowserArtifactExpiryCmd(msg.Artifact.Handle, msg.Copy),
			)
		}
		return m.setStatus("browser artifact opened")
	}
	return m.setStatus("browser artifact saved")
}

func getBrowserArtifactFailureStatus(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "expired"):
		return "Artifact expired"
	case strings.Contains(message, "permission"), strings.Contains(message, "denied"), strings.Contains(message, "owner"):
		return "Artifact access denied"
	case errors.Is(err, os.ErrExist):
		return "Destination already exists"
	case errors.Is(err, context.Canceled):
		return "Artifact retrieval cancelled"
	default:
		return "Artifact unavailable"
	}
}

func (m *model) setBrowserArtifactStatus(token string, status string) {
	for index := range m.messages {
		cell, ok := m.messages[index].(toolTranscriptCell)
		if !ok || !cell.hasArtifact || cell.artifact.Token != token {
			continue
		}
		cell.artifactStatus = status
		m.applyAction(replaceTranscriptCellAction{Index: index, Cell: cell})
		return
	}
}

func getBrowserArtifactExpiryCmd(handle string, copy browserArtifactCopy) tea.Cmd {
	remaining := copy.ExpiresAt.Sub(currentTime())
	if remaining < 0 {
		remaining = 0
	}
	return tea.Tick(remaining, func(time.Time) tea.Msg {
		return browserArtifactExpiryMsg{Handle: handle, Path: copy.Path}
	})
}

func (m *model) expireBrowserArtifactCopy(msg browserArtifactExpiryMsg) {
	copy, ok := m.artifactCopies[msg.Handle]
	if !ok || copy.Path != msg.Path {
		return
	}
	_ = os.RemoveAll(copy.Directory)
	delete(m.artifactCopies, msg.Handle)
}

func (m *model) cleanupBrowserArtifactCopies() {
	for handle, copy := range m.artifactCopies {
		_ = os.RemoveAll(copy.Directory)
		delete(m.artifactCopies, handle)
	}
}

// CleanupTemporaryBrowserArtifacts removes opened artifact copies when the TUI program exits.
func CleanupTemporaryBrowserArtifacts(value tea.Model) {
	switch typed := value.(type) {
	case model:
		typed.cleanupBrowserArtifactCopies()
	case *model:
		typed.cleanupBrowserArtifactCopies()
	}
}

func (m *model) handleBrowserArtifactCommand(args string) tea.Cmd {
	action, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	handle, destination, _ := strings.Cut(strings.TrimSpace(rest), " ")
	artifact, ok := m.getBrowserArtifactByHandle(handle)
	if !ok {
		return m.setStatus("browser artifact not found in transcript")
	}
	switch strings.ToLower(action) {
	case "open":
		return m.startBrowserArtifactAction(artifact, "open")
	case "save":
		if strings.TrimSpace(destination) == "" {
			return m.showBrowserArtifactSavePrompt(artifact)
		}
		return readBrowserArtifactCmd(
			m.chatCtx, m.browserClient, artifact, m.getCurrentSessionID(), "save", strings.TrimSpace(destination),
		)
	default:
		return m.setStatus("usage: /artifact open [handle] or /artifact save [handle] [path]")
	}
}
