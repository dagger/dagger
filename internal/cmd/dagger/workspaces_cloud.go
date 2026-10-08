package daggercmd

import (
	"github.com/spf13/cobra"
)

// cloudWorkspacesCmd is the `dagger cloud workspaces` group. It is a
// placeholder for now: subcommands are not implemented yet, so the group only
// prints its help. It exists so the command surface is discoverable and stable
// while the Cloud workspaces feature is built out. The `workspace` alias
// accepts the singular spelling too.
//
// Note: `dagger activity` lives under `dagger workspace activity`; whether it
// should (also) appear here is an open question for the Cloud command surface.
var cloudWorkspacesCmd = &cobra.Command{
	Use:     "workspaces",
	Aliases: []string{"workspace"},
	Short:   "Manage Dagger Cloud workspaces",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	cloudCmd.AddCommand(cloudWorkspacesCmd)
}
