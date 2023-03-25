package main

import (
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"runtime/trace"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/tracing"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	configPath string
	workdir    string

	cpuprofile string
	debugLogs  bool
)

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	logrus.StandardLogger().SetOutput(io.Discard)

	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")
	rootCmd.PersistentFlags().BoolVar(&debugLogs, "debug", false, "show buildkit debug logs")
	rootCmd.PersistentFlags().StringVar(&cpuprofile, "cpuprofile", "", "collect CPU profile to path, and trace at path.trace")

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
		if cpuprofile != "" {
			profF, err := os.Create(cpuprofile)
			if err != nil {
				return fmt.Errorf("create profile: %w", err)
			}

			pprof.StartCPUProfile(profF)

			tracePath := cpuprofile + ".trace"

			traceF, err := os.Create(tracePath)
			if err != nil {
				return fmt.Errorf("create trace: %w", err)
			}

			if err := trace.Start(traceF); err != nil {
				return fmt.Errorf("start trace: %w", err)
			}
		}

		var err error
		workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
		return err
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		pprof.StopCPUProfile()
		trace.Stop()
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
