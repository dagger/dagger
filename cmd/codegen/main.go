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
	rootCmd.AddCommand(generateTypeDefsCmd)

	rootCmd.PersistentFlags().StringVar(&lang, "lang", "go", "language to generate")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.PersistentFlags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")
	rootCmd.PersistentFlags().BoolVar(&bundle, "bundle", false, "generate the client in bundle mode")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
