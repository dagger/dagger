package layout

import "github.com/spf13/cobra"

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "layout",
	Short: "Manage a deployment's layout",
}

func init() {
	Cmd.AddCommand(
		packageCmd,
		dirCmd,
		gitCmd,
		fileCmd,
	)
}
