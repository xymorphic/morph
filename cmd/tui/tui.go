// Package tui provides Morph's interactive terminal UI command.
package tui

import (
	"context"

	cli "github.com/urfave/cli/v3"

	tuiapp "github.com/xymorphic/morph/internal/tui/app"
)

func Run(ctx context.Context, cmd *cli.Command) error {
	model, cleanup, err := loadCommandModel(ctx, cmd)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	finalModel, err := newProgram(model).Run()
	tuiapp.CleanupTemporaryBrowserArtifacts(finalModel)
	return err
}
