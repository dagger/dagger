package main

import (
	"io"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/tracing"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	configPath string
	workdir    string

	debugLogs bool
)

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	//
	// NB(vito): we now use Logrus in other places so it's a bit unfortunate to
	// disable this, solution TBD
	logrus.StandardLogger().SetOutput(io.Discard)

	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")
	rootCmd.PersistentFlags().BoolVar(&debugLogs, "debug", false, "show buildkit debug logs")

	rootCmd.PersistentFlags().StringVarP(&configPath, "project", "p", "", "")
	rootCmd.PersistentFlags().MarkHidden("project")

	rootCmd.AddCommand(
		listenCmd,
		doCmd,
		versionCmd,
		queryCmd,
		runCmd,
		sessionCmd(),
	)
}

var rootCmd = &cobra.Command{
	Use: "dagger",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
		return err
	},
}

func main() {
	closer := tracing.Init()
	if err := rootCmd.Execute(); err != nil {
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
}
