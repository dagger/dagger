package main

import (
	_ "embed"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: "codegen",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true
	},
}

func init() {
	rootCmd.AddCommand(introspectCmd)
	rootCmd.AddCommand(generateClientCmd)
	rootCmd.AddCommand(generateModuleCmd)
	rootCmd.AddCommand(generateLibraryCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
