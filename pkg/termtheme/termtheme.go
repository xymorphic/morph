package termtheme

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/xymorphic/morph/pkg/str"
	"golang.org/x/sys/unix"
)

type Result struct {
	Theme      string `json:"theme"`
	Background string `json:"background,omitempty"`
	Source     string `json:"source"`
	Error      string `json:"error,omitempty"`
}

type terminal interface {
	Fd() uintptr
	Read([]byte) (int, error)
	WriteString(string) (int, error)
	Close() error
}

var (
	openTTY = func() (terminal, error) {
		return os.OpenFile("/dev/tty", os.O_RDWR, 0)
	}
	makeRaw     = term.MakeRaw
	restoreTerm = term.Restore
	lookupEnv   = os.LookupEnv
	setNonblock = unix.SetNonblock
)

func Detect(timeout time.Duration) Result {
	if result, ok := detectExplicitEnvironment(); ok {
		return result
	}

	terminal, err := openTTY()
	if err != nil {
		return detectEnvironmentFallback(Result{Theme: "unknown", Source: "tty", Error: err.Error()})
	}
	defer func() { _ = terminal.Close() }()

	response, err := queryBackground(terminal, timeout)
	if err != nil {
		return detectEnvironmentFallback(Result{Theme: "unknown", Source: "osc11", Error: err.Error()})
	}

	background, err := ParseOSC11(response)
	if err != nil {
		return detectEnvironmentFallback(Result{Theme: "unknown", Source: "osc11", Error: err.Error()})
	}

	return Result{
		Theme:      ThemeFromBackground(background),
		Background: background,
		Source:     "osc11",
	}
}

func detectExplicitEnvironment() (Result, bool) {
	if value, ok := lookupEnv("MORPH_TUI_BACKGROUND"); ok {
		valueText := str.String(value)
		background := valueText.Normalized()
		if _, err := parseHexBackground(background); err == nil {
			return Result{
				Theme:      ThemeFromBackground(background),
				Background: background,
				Source:     "environment",
			}, true
		}
	}

	return Result{}, false
}

func detectEnvironmentFallback(fallback Result) Result {
	if result, ok := detectThemeEnvironmentFallback(); ok {
		return result
	}

	return fallback
}

func detectThemeEnvironmentFallback() (Result, bool) {
	value, ok := lookupEnv("COLORFGBG")
	if !ok {
		return Result{}, false
	}

	parts := strings.Split(value, ";")
	if len(parts) == 0 {
		return Result{}, false
	}
	partsValue := str.String(parts[len(parts)-1])
	backgroundIndex, err := strconv.Atoi(partsValue.Trim())
	if err != nil {
		return Result{}, false
	}
	if backgroundIndex >= 0 && backgroundIndex <= 6 {
		return Result{Theme: "dark", Background: "#000000", Source: "environment"}, true
	}
	if backgroundIndex >= 7 && backgroundIndex <= 15 {
		return Result{Theme: "light", Background: "#ffffff", Source: "environment"}, true
	}

	return Result{}, false
}

func ParseOSC11(response string) (string, error) {
	_, after, ok := strings.Cut(response, "]11;")
	if !ok {
		return "", fmt.Errorf("OSC 11 response not found: %q", response)
	}

	value := after
	value = strings.TrimSuffix(value, "\x07")
	value = strings.TrimSuffix(value, "\x1b\\")
	value2 := str.String(value)
	value = value2.Trim()

	if after0, ok0 := strings.CutPrefix(value, "rgb:"); ok0 {
		return rgbToHex(after0)
	}

	if strings.HasPrefix(value, "#") && len(value) == 7 {
		return strings.ToLower(value), nil
	}

	return "", fmt.Errorf("unsupported OSC 11 color format: %q", value)
}

func ThemeFromBackground(background string) string {
	color, err := parseHexBackground(background)
	if err != nil {
		return "unknown"
	}

	red := color[0]
	green := color[1]
	blue := color[2]
	luminance := 0.2126*float64(red)/255 + 0.7152*float64(green)/255 + 0.0722*float64(blue)/255

	if luminance > 0.5 {
		return "light"
	}

	return "dark"
}

func parseHexBackground(background string) ([3]int64, error) {
	var color [3]int64
	if len(background) != 7 || !strings.HasPrefix(background, "#") {
		return color, fmt.Errorf("invalid background color: %q", background)
	}

	red, err := strconv.ParseInt(background[1:3], 16, 32)
	if err != nil {
		return color, err
	}
	green, err := strconv.ParseInt(background[3:5], 16, 32)
	if err != nil {
		return color, err
	}
	blue, err := strconv.ParseInt(background[5:7], 16, 32)
	if err != nil {
		return color, err
	}

	color[0] = red
	color[1] = green
	color[2] = blue
	return color, nil
}

func queryBackground(terminal terminal, timeout time.Duration) (string, error) {
	fd := terminal.Fd()
	state, err := makeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() { _ = restoreTerm(fd, state) }()

	if err := setNonblock(int(fd), true); err != nil {
		return "", err
	}
	defer func() { _ = setNonblock(int(fd), false) }()

	if _, err := terminal.WriteString("\x1b]11;?\x07"); err != nil {
		return "", err
	}

	return readResponse(terminal, timeout)
}

func readResponse(terminal terminal, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var builder strings.Builder
	buffer := make([]byte, 64)

	for {
		n, err := terminal.Read(buffer)
		if n > 0 {
			builder.Write(buffer[:n])
			value := builder.String()
			if strings.Contains(value, "\x07") || strings.Contains(value, "\x1b\\") {
				return value, nil
			}
		}
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, io.EOF) {
				if time.Now().After(deadline) {
					return "", errors.New("terminal did not respond to OSC 11")
				}

				time.Sleep(time.Millisecond)
				continue
			}

			return "", err
		}
		if n == 0 {
			if time.Now().After(deadline) {
				return "", errors.New("terminal did not respond to OSC 11")
			}

			time.Sleep(time.Millisecond)
		}
	}
}

func rgbToHex(value string) (string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid rgb color: %q", value)
	}

	values := make([]int, 3)
	for i, part := range parts {
		if len(part) == 0 {
			return "", fmt.Errorf("invalid rgb component: %q", value)
		}

		parsed, err := strconv.ParseUint(part, 16, 16)
		if err != nil {
			return "", err
		}

		maxValue := uint64(1<<(len(part)*4)) - 1
		values[i] = int((parsed*255 + maxValue/2) / maxValue)
	}

	return fmt.Sprintf("#%02x%02x%02x", values[0], values[1], values[2]), nil
}
