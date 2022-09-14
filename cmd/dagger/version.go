package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	ver "go.dagger.io/dagger/internal/version"
)

const (
	developmentVersion = "devel"
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

// version holds the complete version number. Filled in at linking time.
var version = developmentVersion

func short() string {
	rev, err := ver.Revision()
	if err != nil {
		rev = ""
	}
	return fmt.Sprintf("cloak %s (%s)", version, rev)
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}
