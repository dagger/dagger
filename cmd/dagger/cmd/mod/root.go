package mod

import "github.com/spf13/cobra"

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "mod",
	Short: "Manage an environment's dependencies",
}

func init() {
	Cmd.AddCommand(
		getCmd,
	)
}
