package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

func splitAndTrimCSV(value string) []string {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return nil
	}

	parts := strings.Split(valueText, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		partValue := str.String(part)
		trimmed := partValue.Trim()
		if trimmed == "" {
			continue
		}
		values = append(values, trimmed)
	}

	return values
}

func dedupeAndTrim(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value2 := str.String(value)
		trimmed := value2.Trim()
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	return out
}

func normalizeFSRoots(values []string) []string {
	values = dedupeAndTrim(values)
	if len(values) == 0 {
		return nil
	}

	roots := make([]string, 0, len(values))
	for _, value := range values {
		if filepath.IsAbs(value) {
			roots = append(roots, filepath.Clean(value))
			continue
		}

		cwd, err := getwd()
		if err != nil {
			roots = append(roots, filepath.Clean(value))
			continue
		}
		roots = append(roots, filepath.Clean(filepath.Join(cwd, value)))
	}

	return dedupeAndTrim(roots)
}

func getPathsFromBase(values []string, baseDir string) []string {
	values = dedupeAndTrim(values)
	if len(values) == 0 {
		return nil
	}
	baseDirValue := str.String(baseDir)
	baseDir = baseDirValue.Trim()
	if baseDir == "" {
		return values
	}

	resolved := make([]string, 0, len(values))
	for _, value := range values {
		if filepath.IsAbs(value) {
			resolved = append(resolved, value)
			continue
		}

		resolved = append(resolved, filepath.Join(baseDir, value))
	}

	return resolved
}

func getWorkingDirectory() string {
	cwd, err := getwd()
	if err != nil {
		return ""
	}

	return cwd
}

func getDefaultFSRoots() []string {
	cwd, err := getwd()
	if err != nil {
		return []string{"."}
	}

	return []string{filepath.Clean(cwd)}
}

func parseOptionalBoolEnv(key string) (bool, bool) {
	envValue := str.String(os.Getenv(key))
	value := envValue.Normalized()
	if value == "" {
		return false, false
	}

	return value == "1" || value == "true" || value == "yes", true
}

func parseDurationOrZero(value string) time.Duration {
	value3 := str.String(value)
	parsed, err := time.ParseDuration(value3.Trim())
	if err != nil {
		return 0
	}

	return parsed
}

func getBoolValue(value *bool) bool {
	if value == nil {
		return false
	}

	return *value
}

func getBoolValueDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}

	return *value
}

func normalizeRulePaths(files []string) []string {
	normalized := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))

	for _, file := range files {
		fileValue := str.String(file)
		path := fileValue.Trim()
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}

	return normalized
}
