package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/cloak/cmd/cloak/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use: "dev",
	Run: Dev,
}

func Dev(cmd *cobra.Command, args []string) {
	cfg, err := config.ParseFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	startOpts := &engine.Config{
		LocalDirs: cfg.LocalDirs(),
		DevServer: devServerPort,
	}

	err = engine.Start(context.Background(), startOpts, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		localDirs, err := loadLocalDirs(ctx, cl, cfg.LocalDirs())
		if err != nil {
			return err
		}
		if err := cfg.LoadExtensions(ctx, localDirs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
