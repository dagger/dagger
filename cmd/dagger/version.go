package main

import (
	"fmt"
	"runtime"

	"github.com/dagger/dagger/internal/engine"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger version",
	// Disable version hook here to avoid double version check
	PersistentPreRun:  func(*cobra.Command, []string) {},
	PersistentPostRun: func(*cobra.Command, []string) {},
	Args:              cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(long())
	},
}

func short() string {
	return fmt.Sprintf("dagger %s", engine.Version)
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}
