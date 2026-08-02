package tui

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/xymorphic/morph/pkg/str"
)

var openExternalLink = defaultOpenExternalLink

const thinkingTranscriptLinkScheme = "morph-thinking"

func (m *model) openTranscriptLinkAtMouse(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	mouse := msg.Mouse()
	if !isLinkOpenMouseClick(mouse) || !m.isMouseInTranscript(mouse) {
		return nil, false
	}

	row := mouse.Y - m.getTranscriptTop()
	if token, action, ok := getArtifactActionAtRenderedPosition(m.transcript.View(), row, mouse.X); ok {
		artifact, found := m.getBrowserArtifactByToken(token)
		if !found {
			return m.setStatus("browser artifact action unavailable"), true
		}
		return m.startBrowserArtifactAction(artifact, action), true
	}
	if link, ok := linkAtRenderedPosition(m.transcript.View(), row, mouse.X); ok {
		if id, internal := parseThinkingTranscriptLink(link); internal {
			if !m.toggleThinkingTranscriptCell(id) {
				return m.setStatus("thinking summary unavailable"), true
			}
			return nil, true
		}
		return m.runEffect(openLinkEffect{URL: link}), true
	}

	return nil, false
}

func isLinkOpenMouseClick(mouse tea.Mouse) bool {
	return mouse.Button == tea.MouseLeft
}

func linkAtRenderedPosition(rendered string, row int, column int) (string, bool) {
	if row < 0 || column < 0 {
		return "", false
	}

	lines := strings.Split(rendered, "\n")
	if row >= len(lines) {
		return "", false
	}

	return linkAtRenderedLineColumn(lines[row], column)
}

func linkAtRenderedLineColumn(line string, column int) (string, bool) {
	activeLink := ""
	cell := 0

	for index := 0; index < len(line); {
		if line[index] == '\x1b' {
			next, link, ok := parseTerminalHyperlinkEscape(line, index)
			if ok {
				activeLink = link
				index = next
				continue
			}

			if next, ok := skipANSIEscape(line, index); ok {
				index = next
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(line[index:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if size <= 0 {
			size = 1
		}

		width := max(xansi.StringWidth(string(r)), 0)
		if width > 0 && column >= cell && column < cell+width && activeLink != "" {
			return activeLink, true
		}

		cell += width
		index += size
	}

	return "", false
}

func parseTerminalHyperlinkEscape(line string, start int) (int, string, bool) {
	if !strings.HasPrefix(line[start:], "\x1b]8;") {
		return start, "", false
	}

	payloadStart := start + len("\x1b]")
	payloadEnd, terminatorWidth, ok := findOSCEnd(line, payloadStart)
	if !ok {
		return start, "", false
	}

	payload := line[payloadStart:payloadEnd]
	parts := strings.SplitN(payload, ";", 3)
	if len(parts) != 3 || parts[0] != "8" {
		return payloadEnd + terminatorWidth, "", true
	}

	return payloadEnd + terminatorWidth, sanitizeTranscriptLink(parts[2]), true
}

func findOSCEnd(line string, start int) (int, int, bool) {
	for index := start; index < len(line); index++ {
		switch line[index] {
		case '\a':
			return index, 1, true
		case '\x1b':
			if index+1 < len(line) && line[index+1] == '\\' {
				return index, 2, true
			}
		}
	}

	return 0, 0, false
}

func skipANSIEscape(line string, start int) (int, bool) {
	if start+1 >= len(line) || line[start] != '\x1b' {
		return start, false
	}

	switch line[start+1] {
	case '[':
		for index := start + 2; index < len(line); index++ {
			if line[index] >= 0x40 && line[index] <= 0x7e {
				return index + 1, true
			}
		}
	case ']':
		if end, terminatorWidth, ok := findOSCEnd(line, start+2); ok {
			return end + terminatorWidth, true
		}
	default:
		return start + 2, true
	}

	return start, false
}

func sanitizeClickableLink(raw string) string {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if raw == "" {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return raw
	default:
		return ""
	}
}

func sanitizeTranscriptLink(raw string) string {
	if external := sanitizeClickableLink(raw); external != "" {
		return external
	}
	if _, ok := parseThinkingTranscriptLink(raw); ok {
		return raw
	}
	return ""
}

func getThinkingTranscriptLink(id string) string {
	idValue := str.String(id)
	id = idValue.Trim()
	if !isThinkingTranscriptCellID(id) {
		return ""
	}

	return thinkingTranscriptLinkScheme + "://" + id
}

func parseThinkingTranscriptLink(raw string) (string, bool) {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if raw == "" {
		return "", false
	}

	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.Scheme != thinkingTranscriptLinkScheme ||
		parsed.Host == "" ||
		parsed.Path != "" ||
		parsed.User != nil ||
		parsed.Port() != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		!isThinkingTranscriptCellID(parsed.Host) {
		return "", false
	}

	return parsed.Host, true
}

func isThinkingTranscriptCellID(id string) bool {
	var sequence string
	switch {
	case strings.HasPrefix(id, "live-"):
		sequence = strings.TrimPrefix(id, "live-")
	case strings.HasPrefix(id, "trace-"):
		sequence = strings.TrimPrefix(id, "trace-")
	default:
		return false
	}

	value, err := strconv.ParseUint(sequence, 10, 64)
	return err == nil && value > 0
}

func defaultOpenExternalLink(raw string) error {
	raw = sanitizeClickableLink(raw)
	if raw == "" {
		return errors.New("unsupported link")
	}

	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", raw).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHand", raw).Start()
	default:
		return exec.Command("xdg-open", raw).Start()
	}
}
