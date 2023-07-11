package main

import (
	"fmt"
	"os"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

var checksCmd = &cobra.Command{
	Use:          "checks",
	Aliases:      []string{"tests"},
	Long:         `List your environment's checks.`,
	RunE:         ListChecks,
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(checksCmd)
}

func ListChecks(cmd *cobra.Command, args []string) error {
	focus = doFocus

	if len(ProgrockEnv.Checks) == 0 {
		return fmt.Errorf("no checks defined")
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("check name").Bold(), termenv.String("description").Bold())
	}

	for _, c := range ProgrockEnv.Checks {
		fmt.Fprintf(tw, "%s\t%s\n", c.Name, c.Description)
	}

	return tw.Flush()
}
