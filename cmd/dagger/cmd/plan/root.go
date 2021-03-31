package plan

import "github.com/spf13/cobra"

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "plan",
	Short: "Manage a deployment's plan",
}

func init() {
	Cmd.AddCommand(
		packageCmd,
		dirCmd,
		gitCmd,
		fileCmd,
	)
}
