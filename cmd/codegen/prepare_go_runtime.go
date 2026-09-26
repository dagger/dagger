package main

import (
	gogenerator "github.com/dagger/dagger/cmd/codegen/generator/go"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:    "prepare-go-runtime",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return gogenerator.PrepareCollectionRuntime(".")
		},
	})
}
