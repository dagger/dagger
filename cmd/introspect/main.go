package main

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{}

var introspectCmd = &cobra.Command{
	Use:  "introspect",
	RunE: Introspect,
}

var schemaCmd = &cobra.Command{
	Use:  "schema",
	RunE: Schema,
}

var (
	version string
)

func init() {
	rootCmd.PersistentFlags().StringVar(&version, "version", "", "version of the schema to generate")

	rootCmd.AddCommand(introspectCmd)
	rootCmd.AddCommand(schemaCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
