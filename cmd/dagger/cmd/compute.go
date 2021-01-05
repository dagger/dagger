package cmd

import (
	"context"
	"fmt"

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
		// FIXME: boot and bootdir should be config fields, not args
		c, err := dagger.NewClient(ctx, "", "", args[0])
		if err != nil {
			ui.Fatal(err)
		}
		// FIXME: configure which config to compute (duh)
		// FIXME: configure inputs
		ui.Info("Running")
		output, err := c.Compute(ctx)
		if err != nil {
			ui.Fatal(err)
		}
		ui.Info("Processing output")
		fmt.Println(output.JSON())
	},
}

func init() {
	//	computeCmd.Flags().StringP("catalog", "c", "", "Cue package catalog")
}
