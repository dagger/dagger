package main

import "github.com/spf13/cobra"

var artifactCmd = &cobra.Command{
	Use:          "artifact",
	Long:         `Interact with your environment's artifacts.`,
	SilenceUsage: true,
}

func init() {
	artifactCmd.AddCommand(
		&cobra.Command{
			Use:          "list",
			Long:         `List your environment's artifacts.`,
			SilenceUsage: true,
			RunE:         ListArtifacts,
		},
		&cobra.Command{
			Use:          "get",
			Long:         `Export an artifact to the current directory.`,
			SilenceUsage: true,
			RunE:         Export,
		},
	)

	rootCmd.AddCommand(artifactCmd)
}
