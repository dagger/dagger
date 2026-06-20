package daggercmd

import (
	"context"

	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// installedCmd lists installed modules in the current workspace.
// Past-participle reading: "show me what's installed."
// Precedent: `pip list`, `gem list --installed`.
var installedCmd = &cobra.Command{
	Use:   "installed",
	Short: "List installed modules",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return listWorkspaceModules(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}
