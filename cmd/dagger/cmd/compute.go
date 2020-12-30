package cmd

import (
	"context"
	"fmt"
	"os"

	"dagger.cloud/go/dagger"
	"dagger.cloud/go/dagger/ui"
	"github.com/spf13/cobra"
)

var computeCmd = &cobra.Command{
	Use:   "compute CONFIG",
	Short: "Compute a configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.TODO()
		c, err := dagger.NewClient(ctx, "")
		if err != nil {
			ui.Fatal(err)
		}
		target := args[0]
		if target == "-" {
			ui.Info("Assembling config from stdin\n")
			// FIXME: include cue.mod/pkg from *somewhere* so stdin config can import
			if err := c.SetConfig(os.Stdin); err != nil {
				ui.Fatal(err)
			}
		} else {
			ui.Info("Assembling config from %q\n", target)
			if err := c.SetConfig(target); err != nil {
				ui.Fatal(err)
			}
		}
		ui.Info("Running")
		output, err := c.Run(ctx, "compute")
		if err != nil {
			ui.Fatal(err)
		}
		ui.Info("Processing output")
		//		output.Print(os.Stdout)
		fmt.Println(output.JSON())
		// FIXME: write computed values back to env dir
	},
}

func init() {
	//	computeCmd.Flags().StringP("catalog", "c", "", "Cue package catalog")
}
