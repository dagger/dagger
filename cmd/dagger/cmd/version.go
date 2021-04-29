package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

const (
	defaultVersion = "devel"
)

// set by goreleaser or other builder using
// -ldflags='-X dagger.io/go/cmd/dagger/cmd.version=<version>'
var (
	version = defaultVersion
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if bi, ok := debug.ReadBuildInfo(); ok && version == defaultVersion {
			// No specific version provided via version
			version = bi.Main.Version
		}
		fmt.Printf("dagger version %v %s/%s\n",
			version,
			runtime.GOOS, runtime.GOARCH,
		)
	},
}
