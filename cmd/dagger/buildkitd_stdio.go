package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	stdio "go.dagger.io/dagger/internal/stdio"
)

var buildkitdStdioCmd = &cobra.Command{
	Use:    "buildkitd-stdio",
	Run:    BuildkitdStdio,
	Hidden: true,
}

// Hack to connect to buildkit using stdio:
// https://github.com/moby/buildkit/blob/f567525314aa6b37970cad1c6f43bef449b71e04/client/connhelper/dockercontainer/dockercontainer.go#L32
func BuildkitdStdio(cmd *cobra.Command, args []string) {
	err := stdio.ProxyIO()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
