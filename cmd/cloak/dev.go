package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.dagger.io/dagger/engine"
)

var devCmd = &cobra.Command{
	Use: "dev",
	Run: Dev,
}

var corsOrigins []string

func Dev(cmd *cobra.Command, args []string) {
	cmd.Flags().Parse(args)
	localDirs := getKVInput(localDirsInput)
	startOpts := &engine.Config{
		LocalDirs:         localDirs,
		DevServer:         devServerPort,
		Workdir:           workdir,
		ConfigPath:        configPath,
		RouterCorsOrigins: corsOrigins,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	devCmd.Flags().StringSliceVarP(&corsOrigins, "cors-origins", "", []string{}, "CORS origins overrides")
}
