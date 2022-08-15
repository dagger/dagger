package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use: "dev",
	Run: Dev,
}

func Dev(cmd *cobra.Command, args []string) {
	localDirs := map[string]string{
		projectContextLocalName: projectContext,
	}
	startOpts := &engine.Config{
		LocalDirs: localDirs,
		DevServer: devServerPort,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		localDirMapping, err := loadLocalDirs(ctx, cl, localDirs)
		if err != nil {
			return err
		}

		if _, err := installProject(ctx, cl, localDirMapping[projectContextLocalName]); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
