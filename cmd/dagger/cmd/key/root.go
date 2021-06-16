package key

import (
	"github.com/spf13/cobra"
)

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "key",
	Short: "Manage encryption keys",
}

func init() {
	Cmd.AddCommand(
		listCmd,
		generateCmd,
		exportCmd,
		importCmd,
	)
}
