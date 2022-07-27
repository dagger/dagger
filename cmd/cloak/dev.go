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

	startOpts := &engine.StartOpts{
		LocalDirs: cfg.LocalDirs(),
		Secrets:   make(map[string]string),
		DevServer: devServerPort,
	}

	err = engine.Start(context.Background(), startOpts, func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
		if err := cfg.Import(ctx, localDirs); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
