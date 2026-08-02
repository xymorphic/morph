package config

import (
	"errors"
	"fmt"
	"path/filepath"

	commandplan "github.com/xymorphic/morph/internal/command"
)

func (c *Config) validateExecSettings() error {
	if len(c.Exec.Allow) > 0 || len(c.Exec.Ask) > 0 || len(c.Exec.Deny) > 0 {
		return errors.New(
			"exec.allow, exec.ask, and exec.deny are no longer supported; " +
				"use typed command selectors in exec.allowCommands, exec.askCommands, and exec.denyCommands",
		)
	}
	if c.Exec.Shell != "" && !filepath.IsAbs(c.Exec.Shell) {
		return errors.New("exec shell must be an absolute path")
	}
	for _, group := range []struct {
		name       string
		selectors  []commandplan.Selector
		normalizer func([]commandplan.Selector) ([]commandplan.Selector, error)
	}{
		{name: "allowCommands", selectors: c.Exec.AllowCommands, normalizer: commandplan.NormalizeSelectors},
		{name: "askCommands", selectors: c.Exec.AskCommands, normalizer: commandplan.NormalizeSelectors},
		{name: "denyCommands", selectors: c.Exec.DenyCommands, normalizer: commandplan.NormalizeDenySelectors},
	} {
		for index, selector := range group.selectors {
			if _, err := group.normalizer([]commandplan.Selector{selector}); err != nil {
				return fmt.Errorf("exec.%s[%d]: %w", group.name, index, err)
			}
		}
	}
	return nil
}
