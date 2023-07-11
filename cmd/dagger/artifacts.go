package main

import (
	"fmt"
	"os"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

var artifactsCmd = &cobra.Command{
	Use:          "artifacts",
	Long:         `List your environment's artifacts.`,
	RunE:         ListArtifacts,
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(artifactsCmd)
}

func ListArtifacts(cmd *cobra.Command, args []string) error {
	focus = doFocus

	if len(ProgrockEnv.Artifacts) == 0 {
		return fmt.Errorf("no artifacts defined")
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("artifact name").Bold(), termenv.String("description").Bold())
	}

	for _, c := range ProgrockEnv.Artifacts {
		fmt.Fprintf(tw, "%s\t%s\n", c.Name, c.Description)
	}

	return tw.Flush()
}
