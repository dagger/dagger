package daggercmd

import (
	"github.com/spf13/cobra"
)

// cloudWorkspacesCmd is the `dagger cloud workspaces` group. Aside from the
// `activity` subcommand (moved here from the top-level `dagger activity`), it is
// a placeholder for now and only prints its help. The `workspace` alias accepts
// the singular spelling too (`dagger cloud workspace activity`).
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
	// `dagger activity` now lives under `dagger cloud workspaces activity`.
	// activityCmd is defined in workspace.go; its root command group is cleared
	// (it was "daily") so it isn't tied to a group its new parent lacks.
	activityCmd.GroupID = ""
	cloudWorkspacesCmd.AddCommand(activityCmd)
	cloudCmd.AddCommand(cloudWorkspacesCmd)
}
