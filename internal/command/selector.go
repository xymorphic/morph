package command

import (
	"errors"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
)

var selectorGOOS = goruntime.GOOS

type Selector struct {
	Executable      string   `yaml:"executable"`
	ResolvedPath    string   `yaml:"resolvedPath"`
	ExactArguments  []string `yaml:"arguments"`
	ArgumentPrefix  []string `yaml:"argumentPrefix"`
	Modes           []Mode   `yaml:"modes"`
	AllowIndirect   bool     `yaml:"allowIndirect"`
	RequireComplete *bool    `yaml:"requireComplete"`
}

func (s Selector) Normalize() (Selector, error) {
	s.Executable = strings.TrimSpace(s.Executable)
	s.ResolvedPath = strings.TrimSpace(s.ResolvedPath)
	s.ExactArguments = slices.Clone(s.ExactArguments)
	s.ArgumentPrefix = slices.Clone(s.ArgumentPrefix)
	if s.RequireComplete != nil {
		s.RequireComplete = new(*s.RequireComplete)
	}
	if len(s.Modes) == 0 {
		s.Modes = []Mode{ModeDirect}
	}

	normalizedModes := make([]Mode, 0, len(s.Modes))
	for _, mode := range s.Modes {
		mode = Mode(strings.TrimSpace(strings.ToLower(string(mode))))
		if mode != ModeDirect && mode != ModePOSIXShell {
			return Selector{}, errors.New("command selector mode must be direct or posix_shell")
		}
		if !slices.Contains(normalizedModes, mode) {
			normalizedModes = append(normalizedModes, mode)
		}
	}
	s.Modes = normalizedModes
	slices.Sort(s.Modes)

	if s.Executable == "" && s.ResolvedPath == "" {
		return Selector{}, errors.New("command selector requires an executable or resolved path")
	}
	if s.ResolvedPath != "" && !isAbsoluteCommandPath(s.ResolvedPath) {
		return Selector{}, errors.New("command selector resolved path must be absolute")
	}
	if s.ExactArguments != nil && s.ArgumentPrefix != nil {
		return Selector{}, errors.New("command selector cannot combine exact arguments and an argument prefix")
	}
	for _, argument := range append(slices.Clone(s.ExactArguments), s.ArgumentPrefix...) {
		if strings.IndexByte(argument, 0) >= 0 {
			return Selector{}, errors.New("command selector argument contains a NUL byte")
		}
	}

	return s, nil
}

func (s Selector) Matches(target Target) bool {
	s, err := s.Normalize()
	if err != nil {
		return false
	}
	target, err = target.Normalize()
	if err != nil {
		return false
	}
	if !slices.Contains(s.Modes, target.Mode) {
		return false
	}
	if s.Executable != "" && !executablesEqual(s.Executable, target.Executable) {
		return false
	}
	if s.ResolvedPath != "" && !pathsEqual(s.ResolvedPath, target.ResolvedPath) {
		return false
	}
	if target.Indirect && !s.AllowIndirect {
		return false
	}
	if s.RequireComplete != nil && target.Complete != *s.RequireComplete {
		return false
	}
	if s.ExactArguments != nil && !slices.Equal(s.ExactArguments, target.Arguments) {
		return false
	}
	if s.ArgumentPrefix != nil {
		if len(s.ArgumentPrefix) > len(target.Arguments) ||
			!slices.Equal(s.ArgumentPrefix, target.Arguments[:len(s.ArgumentPrefix)]) {
			return false
		}
	}

	return true
}

func (s Selector) Fingerprint() string {
	s, err := s.Normalize()
	if err != nil {
		return ""
	}
	modes := make([]string, len(s.Modes))
	for index, mode := range s.Modes {
		modes[index] = string(mode)
	}
	complete := ""
	if s.RequireComplete != nil {
		complete = boolString(*s.RequireComplete)
	}
	exactArguments := ""
	if s.ExactArguments != nil {
		exactArguments = "exact:" + encodeStringList(s.ExactArguments)
	}
	argumentPrefix := ""
	if s.ArgumentPrefix != nil {
		argumentPrefix = "prefix:" + encodeStringList(s.ArgumentPrefix)
	}
	return strings.Join([]string{
		s.Executable,
		s.ResolvedPath,
		exactArguments,
		argumentPrefix,
		strings.Join(modes, ","),
		boolString(s.AllowIndirect),
		complete,
	}, "\x00")
}

func MatchSelectors(selectors []Selector, target Target) bool {
	if len(selectors) == 0 {
		return true
	}
	for _, selector := range selectors {
		if selector.Matches(target) {
			return true
		}
	}
	return false
}

func NormalizeSelectors(selectors []Selector) ([]Selector, error) {
	result := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		normalized, err := selector.Normalize()
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(result, func(existing Selector) bool {
			return existing.Fingerprint() == normalized.Fingerprint()
		}) {
			result = append(result, normalized)
		}
	}
	slices.SortFunc(result, func(left, right Selector) int {
		return strings.Compare(left.Fingerprint(), right.Fingerprint())
	})
	return result, nil
}

func NormalizeDenySelectors(selectors []Selector) ([]Selector, error) {
	values := make([]Selector, len(selectors))
	for index, selector := range selectors {
		if len(selector.Modes) == 0 {
			selector.Modes = []Mode{ModeDirect, ModePOSIXShell}
		}
		values[index] = selector
	}
	return NormalizeSelectors(values)
}

func IntersectSelectors(left []Selector, right []Selector) []Selector {
	if len(left) == 0 {
		result, _ := NormalizeSelectors(right)
		return result
	}
	if len(right) == 0 {
		result, _ := NormalizeSelectors(left)
		return result
	}

	result := make([]Selector, 0)
	for _, leftSelector := range left {
		for _, rightSelector := range right {
			if selector, ok := intersectSelector(leftSelector, rightSelector); ok {
				result = append(result, selector)
			}
		}
	}
	result, _ = NormalizeSelectors(result)
	return result
}

func intersectSelector(left Selector, right Selector) (Selector, bool) {
	left, leftErr := left.Normalize()
	right, rightErr := right.Normalize()
	if leftErr != nil || rightErr != nil {
		return Selector{}, false
	}

	var result Selector
	switch {
	case left.Executable == "":
		result.Executable = right.Executable
	case right.Executable == "":
		result.Executable = left.Executable
	case commandNamesEqual(left.Executable, right.Executable):
		result.Executable = left.Executable
	default:
		return Selector{}, false
	}
	switch {
	case left.ResolvedPath == "":
		result.ResolvedPath = right.ResolvedPath
	case right.ResolvedPath == "":
		result.ResolvedPath = left.ResolvedPath
	case pathsEqual(left.ResolvedPath, right.ResolvedPath):
		result.ResolvedPath = left.ResolvedPath
	default:
		return Selector{}, false
	}
	for _, mode := range left.Modes {
		if slices.Contains(right.Modes, mode) {
			result.Modes = append(result.Modes, mode)
		}
	}
	if len(result.Modes) == 0 {
		return Selector{}, false
	}
	result.AllowIndirect = left.AllowIndirect && right.AllowIndirect
	result.RequireComplete = intersectComplete(left.RequireComplete, right.RequireComplete)
	if left.RequireComplete != nil && right.RequireComplete != nil && result.RequireComplete == nil {
		return Selector{}, false
	}

	switch {
	case left.ExactArguments != nil && right.ExactArguments != nil:
		if !slices.Equal(left.ExactArguments, right.ExactArguments) {
			return Selector{}, false
		}
		result.ExactArguments = slices.Clone(left.ExactArguments)
	case left.ExactArguments != nil:
		if !matchesArgumentPrefix(right.ArgumentPrefix, left.ExactArguments) {
			return Selector{}, false
		}
		result.ExactArguments = slices.Clone(left.ExactArguments)
	case right.ExactArguments != nil:
		if !matchesArgumentPrefix(left.ArgumentPrefix, right.ExactArguments) {
			return Selector{}, false
		}
		result.ExactArguments = slices.Clone(right.ExactArguments)
	default:
		switch {
		case matchesArgumentPrefix(left.ArgumentPrefix, right.ArgumentPrefix):
			result.ArgumentPrefix = slices.Clone(right.ArgumentPrefix)
		case matchesArgumentPrefix(right.ArgumentPrefix, left.ArgumentPrefix):
			result.ArgumentPrefix = slices.Clone(left.ArgumentPrefix)
		default:
			return Selector{}, false
		}
	}

	normalized, err := result.Normalize()
	return normalized, err == nil
}

func intersectComplete(left *bool, right *bool) *bool {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if *left != *right {
		return nil
	}
	value := *left
	return &value
}

func matchesArgumentPrefix(prefix []string, arguments []string) bool {
	return prefix == nil || len(prefix) <= len(arguments) && slices.Equal(prefix, arguments[:len(prefix)])
}

func pathsEqual(left string, right string) bool {
	if selectorGOOS == "windows" {
		left = path.Clean(strings.ReplaceAll(left, `\`, "/"))
		right = path.Clean(strings.ReplaceAll(right, `\`, "/"))
		return strings.EqualFold(left, right)
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	return left == right
}

func commandNamesEqual(left string, right string) bool {
	if selectorGOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func executablesEqual(selector string, target string) bool {
	if hasPathSeparator(selector) || hasPathSeparator(target) {
		return pathsEqual(selector, target)
	}
	return commandNamesEqual(selector, target)
}

func isAbsoluteCommandPath(value string) bool {
	if filepath.IsAbs(value) {
		return true
	}
	if len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return true
	}
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `//`)
}
