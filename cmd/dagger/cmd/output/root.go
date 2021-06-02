package output

import "github.com/spf13/cobra"

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "output",
	Short: "Manage an environment's outputs",
}

func init() {
	// Cmd.AddCommand(dirCmd)
	Cmd.AddCommand(listCmd)
}
