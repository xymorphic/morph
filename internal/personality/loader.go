package personality

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/pkg/promptio"
	"github.com/wandxy/morph/pkg/str"
)

const fileName = constants.PersonalityFileName
const maxContentLength = constants.PersonalityMaxContentLength

var (
	getwd              = os.Getwd
	readFile           = os.ReadFile
	resolveDisplayPath = getDisplayPath
)

// Result contains loaded instruction text and related metadata.
type Result struct {
	Content        string
	Found          bool
	SafetyEvents   []guardrails.SafetyTracePayloadOptions
	UnsafeEvidence []guardrails.UnsafeEvidence
}

// LoadOptions selects the profile, workspace, and configured personality sources to load.
type LoadOptions struct {
	ProfileHome       string
	WorkspaceRoot     string
	PersonalityName   string
	PersonalityConfig config.PersonalityConfig
	AllowWorkspace    bool
}

// Load reads the configured personality instructions.
func Load(opts LoadOptions) (Result, error) {
	profileHomeValue := str.String(opts.ProfileHome)
	opts.ProfileHome = profileHomeValue.Trim()
	if opts.ProfileHome == "" {
		opts.ProfileHome = datadir.ProjectHomeDir()
	}
	workspaceRootValue := str.String(opts.WorkspaceRoot)
	if opts.AllowWorkspace && workspaceRootValue.Trim() == "" {
		root, err := getwd()
		if err != nil {
			return Result{}, fmt.Errorf("resolve workspace root: %w", err)
		}
		opts.WorkspaceRoot = root
	}

	return loadWithOptions(opts)
}

func loadWithOptions(opts LoadOptions) (Result, error) {
	sections := make([]string, 0, 3)
	safetyEvents := make([]guardrails.SafetyTracePayloadOptions, 0, 2)
	unsafeEvidence := make([]guardrails.UnsafeEvidence, 0, 2)
	seenPaths := make(map[string]struct{}, 2)
	profileHomeValue2 := str.String(opts.ProfileHome)
	profileHome := profileHomeValue2.Trim()
	workspaceRootValue2 := str.String(opts.WorkspaceRoot)
	workspaceRoot := workspaceRootValue2.Trim()
	personalityNameValue := str.String(opts.PersonalityName)
	personalityName := personalityNameValue.Trim()

	if personalityName == "" {
		globalPath := filepath.Join(profileHome, fileName)
		globalSection, foundGlobal, events, evidence, err := loadFile(
			globalPath,
			workspaceRoot,
			seenPaths,
			loadFileOptions{Label: "Profile SOUL.md"},
		)
		if err != nil {
			return Result{}, err
		}
		if foundGlobal {
			sections = append(sections, globalSection)
		}
		safetyEvents = append(safetyEvents, events...)
		unsafeEvidence = append(unsafeEvidence, evidence...)
	} else {
		section, found, events, evidence, err := loadNamedPersonality(opts, seenPaths)
		if err != nil {
			return Result{}, err
		}
		if found {
			sections = append(sections, section)
		}
		safetyEvents = append(safetyEvents, events...)
		unsafeEvidence = append(unsafeEvidence, evidence...)
	}

	if opts.AllowWorkspace && workspaceRoot != "" {
		info, err := os.Stat(workspaceRoot)
		if err != nil {
			if !os.IsNotExist(err) {
				return Result{}, fmt.Errorf("stat workspace root %q: %w", workspaceRoot, err)
			}
		} else if info.IsDir() {
			workspacePath := filepath.Join(workspaceRoot, fileName)
			workspaceSection, foundWorkspace, events, evidence, err := loadFile(
				workspacePath,
				workspaceRoot,
				seenPaths,
				loadFileOptions{Label: "Workspace SOUL.md"},
			)
			if err != nil {
				return Result{}, err
			}
			if foundWorkspace {
				sections = append(sections, workspaceSection)
			}
			safetyEvents = append(safetyEvents, events...)
			unsafeEvidence = append(unsafeEvidence, evidence...)
		}
	}

	if len(sections) == 0 {
		return Result{SafetyEvents: safetyEvents, UnsafeEvidence: unsafeEvidence}, nil
	}

	return Result{
		Content:        truncate(strings.Join(sections, "\n\n")),
		Found:          true,
		SafetyEvents:   safetyEvents,
		UnsafeEvidence: unsafeEvidence,
	}, nil
}

func loadNamedPersonality(
	opts LoadOptions,
	seenPaths map[string]struct{},
) (string, bool, []guardrails.SafetyTracePayloadOptions, []guardrails.UnsafeEvidence, error) {
	personalityNameValue2 := str.String(opts.PersonalityName)
	name := personalityNameValue2.Trim()
	personalityConfig := opts.PersonalityConfig
	sections := make([]string, 0, 2)
	safetyEvents := make([]guardrails.SafetyTracePayloadOptions, 0, 2)
	unsafeEvidence := make([]guardrails.UnsafeEvidence, 0, 2)
	soulValue := str.String(personalityConfig.Soul)
	if soulValue.Trim() != "" {
		section, found, events, evidence, err := loadFile(
			personalityConfig.Soul,
			opts.WorkspaceRoot,
			seenPaths,
			loadFileOptions{Label: fmt.Sprintf("Personality %s SOUL.md", name), Required: true},
		)
		if err != nil {
			return "", false, nil, nil, err
		}
		if found {
			sections = append(sections, section)
		}
		safetyEvents = append(safetyEvents, events...)
		unsafeEvidence = append(unsafeEvidence, evidence...)
	}
	instructValue := str.String(personalityConfig.Instruct)
	if instructValue.Trim() != "" {
		displayName := fmt.Sprintf("personality:%s.instruct", name)
		instructValue2 := str.String(personalityConfig.Instruct)
		content := instructValue2.Trim()
		scanned := guardrails.SafetyScan(content, displayName)
		if scanned.Blocked {
			event, evidence := loadedContentSafetyEvent(displayName, content, scanned.Content, scanned.Findings)
			safetyEvents = append(safetyEvents, event)
			unsafeEvidence = append(unsafeEvidence, evidence)
		}
		sections = append(sections, fmt.Sprintf("## Personality %s instruct\n%s", name, scanned.Content))
	}

	if len(sections) == 0 {
		return "", false, safetyEvents, unsafeEvidence, nil
	}

	return strings.Join(sections, "\n\n"), true, safetyEvents, unsafeEvidence, nil
}

type loadFileOptions struct {
	Label    string
	Required bool
}

func loadFile(
	path string,
	workspaceRoot string,
	seenPaths map[string]struct{},
	opts loadFileOptions,
) (string, bool, []guardrails.SafetyTracePayloadOptions, []guardrails.UnsafeEvidence, error) {
	if absolutePath, err := filepath.Abs(path); err == nil {
		path = absolutePath
	}
	if _, ok := seenPaths[path]; ok {
		return "", false, nil, nil, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if opts.Required {
				return "", false, nil, nil, fmt.Errorf("personality file %q is required", path)
			}

			return "", false, nil, nil, nil
		}

		return "", false, nil, nil, fmt.Errorf("stat personality file %q: %w", path, err)
	}

	if info.IsDir() {
		if opts.Required {
			return "", false, nil, nil, fmt.Errorf("personality file %q is a directory", path)
		}

		return "", false, nil, nil, nil
	}

	seenPaths[path] = struct{}{}

	content, err := readFile(path)
	if err != nil {
		return "", false, nil, nil, fmt.Errorf("read personality file %q: %w", path, err)
	}
	contentValue := str.String(string(content))
	contentText := contentValue.Trim()
	if contentText == "" {
		return "", false, nil, nil, nil
	}

	displayPath, err := resolveDisplayPath(path, workspaceRoot)
	if err != nil {
		return "", false, nil, nil, fmt.Errorf("resolve personality file path %q: %w", path, err)
	}

	scanned := guardrails.SafetyScan(contentText, displayPath)
	safetyEvents := []guardrails.SafetyTracePayloadOptions(nil)
	unsafeEvidence := []guardrails.UnsafeEvidence(nil)
	if scanned.Blocked {
		event, evidence := loadedContentSafetyEvent(displayPath, contentText, scanned.Content, scanned.Findings)
		safetyEvents = append(safetyEvents, event)
		unsafeEvidence = append(unsafeEvidence, evidence)
	}
	labelValue := str.String(opts.Label)
	label := labelValue.Trim()
	if label == "" {
		label = displayPath
	}

	return fmt.Sprintf("## %s\n%s", label, scanned.Content), true, safetyEvents, unsafeEvidence, nil
}

func loadedContentSafetyEvent(
	source string,
	content string,
	safe string,
	findings []guardrails.SafetyFinding,
) (guardrails.SafetyTracePayloadOptions, guardrails.UnsafeEvidence) {
	event := guardrails.SafetyTracePayloadOptions{
		Source:        source,
		Action:        "blocked",
		ContentLength: len([]rune(content)),
		Blocked:       true,
		Findings:      findings,
	}
	evidence := guardrails.UnsafeEvidence{
		Source:   source,
		Action:   "blocked",
		Blocked:  true,
		Findings: guardrails.SafetyFindingLogFields(findings),
		Original: content,
		Safe:     safe,
	}
	return event, evidence
}

func getDisplayPath(path, workspaceRoot string) (string, error) {
	if workspaceRoot != "" {
		relativePath, err := filepath.Rel(workspaceRoot, path)
		if err == nil {
			relativePath = filepath.ToSlash(relativePath)
			if relativePath != "." && !strings.HasPrefix(relativePath, "../") {
				return relativePath, nil
			}
		}
	}
	projectHomeDirValue := str.String(datadir.ProjectHomeDir())
	if cleanedHome := projectHomeDirValue.Trim(); cleanedHome != "" {
		relativePath, err := filepath.Rel(cleanedHome, path)
		if err == nil {
			relativePath = filepath.ToSlash(relativePath)
			if relativePath != "." && !strings.HasPrefix(relativePath, "../") {
				return relativePath, nil
			}
		}
	}

	return filepath.ToSlash(path), nil
}

func truncate(content string) string {
	return promptio.TruncateMiddle(content, maxContentLength, "\n\n[... personality overlay truncated ...]\n\n")
}
