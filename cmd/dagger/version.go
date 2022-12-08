package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

const (
	developmentVersion = "devel"
	engineImageRepo    = "ghcr.io/dagger/engine"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
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
}

// version holds the complete version number. Filled in at linking time.
var version = developmentVersion

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
	return fmt.Sprintf("dagger %s (%s)", version, revision())
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}

func engineImageRef() string {
	// if this is a release, use the release image
	if version != developmentVersion {
		return fmt.Sprintf("%s:v%s", engineImageRepo, version)
	}
	// fallback to using the latest image from main
	return fmt.Sprintf("%s:main", engineImageRepo)
}
