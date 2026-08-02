package execution

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

type CommandTarget struct {
	GOOS        string
	Shell       string
	PATH        []string
	Executables map[string]string
}

type SecretCatalogEntry struct {
	Name        string
	Description string
}

func (t CommandTarget) Resolve(name string) (string, error) {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "/") {
		for _, path := range t.Executables {
			if filepath.Clean(path) == filepath.Clean(name) {
				return filepath.Clean(path), nil
			}
		}
		return "", errors.New("command executable is absent from the sandbox contract")
	}
	if path := t.Executables[name]; path != "" {
		return path, nil
	}
	return "", errors.New("command executable is absent from the sandbox contract")
}

type Runtime interface {
	ExecutionService() Service
	PrepareExecutionSpec(context.Context, Operation) (Spec, error)
	PrepareExecutionPath(context.Context, string, FilesystemAction) (PreparedPath, error)
	ExecutionCommandTarget() (CommandTarget, bool)
}
