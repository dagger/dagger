package main

import (
	"fmt"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/tracing"
	"github.com/spf13/cobra"
)

var (
	configPath string
	workdir    string

	debugLogs bool
)

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "dagger",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
			return err
		},
	}

	cmd.PersistentFlags().StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")
	cmd.PersistentFlags().BoolVar(&debugLogs, "debug", false, "show buildkit debug logs")

	cmd.AddCommand(
		listenCmd(),
		doCmd(),
		versionCmd(),
		queryCmd(),
		runCmd(),
	)

	return cmd
}

func main() {
	closer := tracing.Init()
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
}
