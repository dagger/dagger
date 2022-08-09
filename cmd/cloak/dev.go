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

	err = engine.Start(context.Background(), startOpts, func(ctx context.Context, localDirs map[string]dagger.FSID, secrets map[string]string) (dagger.FSID, error) {
		if err := cfg.LoadExtensions(ctx, localDirs); err != nil {
			return "", err
		}
		return "", nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
