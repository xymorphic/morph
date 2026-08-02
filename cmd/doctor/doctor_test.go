package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/diagnostics"
	"github.com/xymorphic/morph/internal/diagnostics/readiness"
	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestNewCommand_PrintsPassingReport(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"doctor",
	})
	require.NoError(t, err)
	require.NotContains(t, output.String(), "config:\n")
	require.Contains(t, output.String(), "\nprofile:")
	requireInOrder(
		t,
		output.String(),
		"config:",
		"env:",
		"config validation: configuration is valid",
	)
	require.Contains(t, output.String(), "[\x1b[32mPASS\x1b[0m] config validation: configuration is valid")
	require.Contains(t, output.String(), "\ndaemon:")
	require.Contains(t, output.String(), "\nmodels:")
	require.Contains(t, output.String(), "\nsession:")
	require.Contains(t, output.String(), "compaction: enabled, triggerPercent=0.85, warnPercent=0.95, recentSessionTail=8")
	require.Contains(t, output.String(), "\nmemory:")
	require.Contains(t, output.String(), "\nsearch:")
	require.Contains(t, output.String(), "\nsafety:")
	require.Contains(t, output.String(), "[\x1b[32mPASS\x1b[0m] policy: input=enabled, output=enabled, pii=enabled")
	require.Contains(t, output.String(), "\ntools:")
	require.Contains(t, output.String(), "fix: \x1b[97mmorph daemon\x1b[0m\x1b[90m - start the daemon for this profile\x1b[0m")
	require.Contains(t, output.String(), "\n[OK] doctor checks passed")
	require.NotContains(t, output.String(), "flag-key")
}

func TestNewCommand_PrintsSafetyModeFromConfig(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: flag-agent
models:
  providers:
    openrouter:
      apiKey: flag-key
  main:
    name: gpt-4o-mini
    provider: openrouter
safety:
  input: false
  output: true
  pii: true
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"doctor",
	})
	require.NoError(t, err)
	require.Contains(t, output.String(), "\nsafety:")
	require.Contains(t, output.String(), "policy: input=disabled, output=enabled, pii=enabled")
}

func TestNewCommand_PrintsFailureReport(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"doctor",
	})
	require.ErrorContains(t, err, "model provider is required")
	require.NotContains(t, output.String(), "config:\n")
	require.Contains(t, output.String(), "\nprofile:")
	requireInOrder(
		t,
		output.String(),
		"config:",
		"env:",
		"config validation",
	)
	require.Contains(t, output.String(), "[\x1b[31mFAIL\x1b[0m] config validation")
	require.Contains(t, output.String(), "model provider is required")
	require.Contains(t, output.String(), "fix: \x1b[97m/providers\x1b[0m")
	require.Contains(t, output.String(), "fix: \x1b[97m/models\x1b[0m")
	require.Contains(t, output.String(), "[\x1b[32mPASS\x1b[0m] policy: input=enabled, output=enabled, pii=enabled")
}

func TestNewCommand_DisablesColorWhenRequested(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--log.no-color",
		"doctor",
	})
	require.NoError(t, err)
	require.NotContains(t, output.String(), "config:\n")
	require.Contains(t, output.String(), "\nprofile:")
	requireInOrder(
		t,
		output.String(),
		"config:",
		"env:",
		"config validation: configuration is valid",
	)
	require.Contains(t, output.String(), "[PASS] config validation: configuration is valid")
	require.Contains(t, output.String(), "[WARN] runtime: runtime metadata is not present")
	require.Contains(t, output.String(), "fix: `morph daemon` - start the daemon for this profile")
	require.Contains(t, output.String(), "[PASS] policy: input=enabled, output=enabled, pii=enabled")
	require.NotRegexp(t, regexp.MustCompile(`\x1b\[[0-9;]*m`), output.String())
}

func TestNewCommand_PrintsJSONReport(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"doctor",
		"--json",
	})
	require.NoError(t, err)
	require.NotRegexp(t, regexp.MustCompile(`\x1b\[[0-9;]*m`), output.String())
	require.NotContains(t, output.String(), "flag-key")

	var payload jsonReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.True(t, payload.OK)
	require.Equal(t, "doctor checks passed", payload.Summary)
	require.Equal(t, "input=enabled, output=enabled, pii=enabled", payload.Safety)
	require.NotEmpty(t, payload.Groups)
	require.Equal(t, "profile", payload.Groups[0].Name)
	require.NotEmpty(t, findJSONCheck(t, payload.Groups, "models", "embedding").Message)
	requireInJSONCheckOrder(
		t,
		payload.Groups,
		"profile",
		"env",
		"config validation",
	)
}

func TestNewCommand_PrintsJSONFailureReport(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"doctor",
		"--json",
	})

	require.ErrorContains(t, err, "doctor checks failed")
	var payload jsonReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.False(t, payload.OK)
	require.Contains(t, payload.Summary, "model auth")
	require.NotEmpty(t, payload.Groups)
}

func TestNewCommand_ReadinessFailureAffectsExit(t *testing.T) {
	isolateProfile(t)
	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(home, "runtime.json"), []byte(`{`), 0o600))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    "test",
		HomeDir: home,
	}))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: flag-agent
models:
  providers:
    openrouter:
      apiKey: flag-key
  main:
    name: gpt-4o-mini
    provider: openrouter
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newRootCommandForTest()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"doctor",
	})

	require.ErrorContains(t, err, "daemon runtime")
	require.Contains(t, output.String(), "[\x1b[31mFAIL\x1b[0m] runtime: parse runtime metadata")
	require.NotContains(t, output.String(), "flag-key")
}

func TestRenderReadinessReport(t *testing.T) {
	var output bytes.Buffer
	report := readiness.Report{Groups: []readiness.Group{
		{
			Name: "models",
			Checks: []readiness.Check{
				{
					Name:    "main",
					Status:  readiness.StatusWarn,
					Message: "missing",
					Actions: []readiness.Action{
						{Command: "morph provider login openai", Description: "login"},
						{Command: "/models"},
					},
				},
			},
		},
	}}

	err := renderReadinessReport(&output, report, &config.Config{})

	require.NoError(t, err)
	require.Contains(t, output.String(), "models:")
	require.Contains(t, output.String(), "fix: \x1b[97mmorph provider login openai\x1b[0m\x1b[90m - login\x1b[0m")
	require.Contains(t, output.String(), "fix: \x1b[97m/models\x1b[0m")
	require.Equal(
		t,
		"`morph provider login openai` - login",
		formatAction(readiness.Action{Command: "morph provider login openai", Description: "login"}, &config.Config{Log: config.LogConfig{NoColor: true}}),
	)
	require.Equal(
		t,
		"\x1b[97m/models\x1b[0m\x1b[90m - choose after morph daemon; then continue\x1b[0m",
		formatAction(readiness.Action{Command: "/models", Description: "choose after morph daemon; then continue"}, &config.Config{}),
	)
	require.Error(t, renderReadinessReport(failingWriter{}, report, &config.Config{}))
}

func TestRenderCheckLineWrapsMessageWithHangingIndent(t *testing.T) {
	originalWidth := doctorOutputWidth
	t.Cleanup(func() {
		doctorOutputWidth = originalWidth
	})
	doctorOutputWidth = func() int { return 100 }

	var output bytes.Buffer
	err := renderCheckLine(
		&output,
		"WARN",
		"config validation",
		`embedding API key is required for provider "openai"; set a provider API key, provider env var, or role apiKey`,
		&config.Config{Log: config.LogConfig{NoColor: true}},
	)

	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	require.Greater(t, len(lines), 1)
	require.True(t, strings.HasPrefix(lines[0], "[WARN] config validation: "))
	require.True(t, strings.HasPrefix(lines[1], strings.Repeat(" ", len("[WARN] config validation: "))))
	for _, line := range lines {
		require.LessOrEqual(t, len(line), 100)
	}
}

func TestRenderJSONReport(t *testing.T) {
	var output bytes.Buffer
	diagnosticsReport := diagnostics.Report{Checks: []diagnostics.Check{{
		Name:    "config",
		Status:  diagnostics.StatusPass,
		Message: "valid",
	}}}
	readinessReport := readiness.Report{Groups: []readiness.Group{{
		Name: "models",
		Checks: []readiness.Check{{
			Name:    "main",
			Status:  readiness.StatusWarn,
			Message: "missing",
			Actions: []readiness.Action{{
				Command:     "/models",
				Description: "choose model",
			}},
		}},
	}}}

	err := renderJSONReport(&output, diagnosticsReport, readinessReport, " safety ")

	require.NoError(t, err)
	var payload jsonReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.True(t, payload.OK)
	require.Equal(t, "safety", payload.Safety)
	require.Len(t, payload.Groups, 1)
	require.Equal(t, "models", payload.Groups[0].Name)
	require.Equal(t, "/models", payload.Groups[0].Checks[0].Actions[0].Command)
	require.Error(t, renderJSONReport(failingWriter{}, diagnosticsReport, readinessReport, ""))
}

func TestRenderJSONReportUsesConfigGroupWhenReadinessIsEmpty(t *testing.T) {
	var output bytes.Buffer
	diagnosticsReport := diagnostics.Report{Checks: []diagnostics.Check{{
		Name:    "config validation",
		Status:  diagnostics.StatusFail,
		Message: "invalid",
	}}}

	err := renderJSONReport(&output, diagnosticsReport, readiness.Report{}, "")

	require.NoError(t, err)
	var payload jsonReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.Len(t, payload.Groups, 1)
	require.Equal(t, "config", payload.Groups[0].Name)
	require.Equal(t, "config validation", payload.Groups[0].Checks[0].Name)
}

func TestDoctorSummaryUsesReadinessFailure(t *testing.T) {
	diagnosticsReport := diagnostics.Report{Checks: []diagnostics.Check{{
		Name:    "config",
		Status:  diagnostics.StatusPass,
		Message: "valid",
	}}}
	readinessReport := readiness.Report{Groups: []readiness.Group{{
		Name: "daemon",
		Checks: []readiness.Check{{
			Name:    "runtime",
			Status:  readiness.StatusFail,
			Message: "invalid runtime metadata",
		}},
	}}}

	require.Equal(t, "daemon runtime: invalid runtime metadata", getDoctorSummary(diagnosticsReport, readinessReport))
}

func newRootCommandForTest() *cli.Command {
	envFile := ".env"
	configFile := "config.yaml"

	return &cli.Command{
		Name:  "morph",
		Flags: morphcli.RootFlags(&envFile, &configFile),
		Commands: []*cli.Command{
			NewCommand(),
		},
	}
}

func isolateProfile(t *testing.T) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{})
	t.Setenv("HOME", t.TempDir())
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG", "MORPH_SAFETY_INPUT", "MORPH_SAFETY_OUTPUT", "MORPH_SAFETY_PII")
}

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	keys = append(keys, "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN")

	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				require.NoError(t, os.Setenv(key, original))
			})
		} else {
			t.Cleanup(func() {
				require.NoError(t, os.Unsetenv(key))
			})
		}
		require.NoError(t, os.Unsetenv(key))
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func findJSONCheck(t *testing.T, groups []jsonGroup, groupName string, checkName string) jsonCheck {
	t.Helper()

	for _, group := range groups {
		if group.Name != groupName {
			continue
		}

		for _, check := range group.Checks {
			if check.Name == checkName {
				return check
			}
		}
	}

	require.Failf(t, "missing json check", "%s/%s", groupName, checkName)
	return jsonCheck{}
}

func requireInJSONCheckOrder(t *testing.T, groups []jsonGroup, groupName string, checkNames ...string) {
	t.Helper()

	groupChecks := make([]jsonCheck, 0)
	for _, group := range groups {
		if group.Name == groupName {
			groupChecks = group.Checks
			break
		}
	}
	require.NotEmpty(t, groupChecks)

	offset := 0
	for _, checkName := range checkNames {
		found := false
		for offset < len(groupChecks) {
			if groupChecks[offset].Name == checkName {
				found = true
				offset++
				break
			}

			offset++
		}
		require.Truef(t, found, "missing check %q after offset %d", checkName, offset)
	}
}

func requireInOrder(t *testing.T, value string, fragments ...string) {
	t.Helper()

	offset := 0
	for _, fragment := range fragments {
		index := strings.Index(value[offset:], fragment)
		require.NotEqualf(t, -1, index, "missing fragment %q after offset %d", fragment, offset)
		offset += index + len(fragment)
	}
}
