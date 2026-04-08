package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "Get or set workspace configuration",
	Long: strings.TrimSpace(`
Alias for "dagger workspace config".

Get or set workspace configuration values in .dagger/config.toml.

With no arguments, prints the full configuration.
With one argument, prints the value at the given key.
With two arguments, sets the value at the given key.

Local module source values are stored relative to .dagger/config.toml, so they may
look different from the resolved paths shown by "dagger workspace list".`,
	),
	Args:    cobra.MaximumNArgs(2),
	GroupID: workspaceGroup.ID,
	RunE:    runWorkspaceConfig,
}
