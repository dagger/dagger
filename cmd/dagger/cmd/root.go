package cmd

import (
	"dagger.cloud/go/dagger/ui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dagger",
	Short: "Open-source workflow engine",
}

func init() {
	// --debug
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")
	// --workspace
	rootCmd.AddCommand(
		computeCmd,
		// Create an env
		// Change settings on an env
		// View or edit env serti
		//		settingsCmd,
		// Query the state of an env
		//		getCmd,
		//		unsetCmd,
		//		computeCmd,
		//		listCmd,
	)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		ui.Fatal(err)
	}
}
