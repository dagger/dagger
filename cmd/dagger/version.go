package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger version",
	// Disable version hook here to avoid double version check
	PersistentPreRun: func(*cobra.Command, []string) {},
	Args:             cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(long())
	},
}

func short() string {
	return fmt.Sprintf("dagger %s (%s)", engine.Version, engine.EngineImageRepo)
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}
