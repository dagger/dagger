package daggercmd

import (
	"github.com/spf13/cobra"
)

// cloudEnginesCmd is the `dagger cloud engines` group. It is a placeholder for
// now: the subcommands (list, provision, etc.) are not implemented yet, so the
// group only prints its help. It exists so the command surface is discoverable
// and stable while the engines feature is built out.
var cloudEnginesCmd = &cobra.Command{
	Use:   "engines",
	Short: "Manage Dagger Cloud engines",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	cloudCmd.AddCommand(cloudEnginesCmd)
}
