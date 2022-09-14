package main

import (
	"github.com/spf13/cobra"
	buildkitd "go.dagger.io/dagger/internal/buildkitd/bundling"
)

var buildkitdCmd = &cobra.Command{
	Use: "buildkitd",
	Run: Buildkitd,
}

func Buildkitd(cmd *cobra.Command, args []string) {
	buildkitd.Run()
}
