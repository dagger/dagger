package main

import (
	"os"

	"github.com/dagger/dagger/engine"
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
	// By default, introspect the API view this engine serves a client of its
	// own version: the schema SDK clients are generated from.
	rootCmd.PersistentFlags().StringVar(&version, "version", engine.APIViewVersion(engine.Version), "version of the schema to generate")

	rootCmd.AddCommand(introspectCmd)
	rootCmd.AddCommand(schemaCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
