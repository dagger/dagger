package output

import "github.com/spf13/cobra"

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "output",
	Short: "Manage a deployment's outputs",
}

func init() {
	Cmd.AddCommand(
		dirCmd,
	)
}
