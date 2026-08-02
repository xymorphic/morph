package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/pkg/str"
)

func newDatabaseCommand() *cli.Command {
	return &cli.Command{
		Name:  "db",
		Usage: "Manage the configured local database",
		Commands: []*cli.Command{
			{
				Name:  "reset",
				Usage: "Delete the configured SQLite database for a fresh start",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "force",
						Usage: "Confirm deletion of the configured database",
					},
				},
				Action: resetConfiguredDatabase,
			},
		},
	}
}

func resetConfiguredDatabase(_ context.Context, cmd *cli.Command) error {
	if !cmd.Bool("force") {
		return errors.New("database reset requires --force")
	}

	cfg, inputs, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return err
	}

	morphcli.ApplyConfigOverrides(cmd, cfg)
	morphcli.AddStartupFilesystemRoots(cfg, inputs)
	backendValue := str.String(cfg.Storage.Backend)
	if backendValue.Normalized() != "sqlite" {
		return errors.New("database reset requires sqlite storage backend")
	}

	paths := getConfiguredDatabasePaths()
	for _, path := range paths {
		if err := removeDatabasePath(path); err != nil {
			return err
		}
	}

	_, err = fmt.Fprintf(rootOutput, "Reset database: %s\n", paths[0])
	return err
}

func getConfiguredDatabasePaths() []string {
	path := datadir.StateDBPath()
	return []string{
		path,
		path + "-wal",
		path + "-shm",
		path + "-journal",
	}
}

func removeDatabasePath(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove database file %q: %w", path, err)
	}

	return nil
}
