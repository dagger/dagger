package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

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

// revision returns the VCS revision being used to build or empty string
// if none.
func revision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value[:9]
		}
	}

	return ""
}

func short() string {
	return fmt.Sprintf("dagger %s (%s)", engine.Version, revision())
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}
