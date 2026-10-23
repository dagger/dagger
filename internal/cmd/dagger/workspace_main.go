package daggercmd

import (
	"context"
	"fmt"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var workspaceMainUnset bool

var workspaceMainCmd = &cobra.Command{
	Use:     "main [NAME]",
	Aliases: []string{"entrypoint"},
	Short:   "Get or set the workspace main",
	Long: `Print the installed name of the current workspace main.
Print nothing if no workspace main is set.

With NAME, select that installed module and clear the previous workspace main.
Use --unset to clear the selection. NAME must be an exact installed name.
Changes are written to the selected dagger.toml.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
			return err
		}
		if workspaceMainUnset && len(args) != 0 {
			return fmt.Errorf("--unset cannot be used with NAME")
		}
		if len(args) == 1 && args[0] == "" {
			return fmt.Errorf("NAME must be an installed module name")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		write := workspaceMainUnset || len(args) == 1
		if write && workspaceEnv != "" {
			return fmt.Errorf("workspace main does not support --env; it is set in the base workspace config")
		}
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules:           true,
			SuppressCompatWorkspaceWarning: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := core.NewQuery(engineClient.Dagger()).CurrentWorkspace()
			if !write {
				name, err := ws.Main(ctx)
				if err != nil || name == "" {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return writeWorkspaceMain(ctx, ws, name)
		})
	},
}

func init() {
	workspaceMainCmd.Flags().BoolVar(&workspaceMainUnset, "unset", false, "Clear the workspace main")
	workspaceCmd.AddCommand(workspaceMainCmd)
}

func writeWorkspaceMain(ctx context.Context, ws *core.Workspace, name string) error {
	if name == "" {
		return ws.WithoutMain().Export(ctx)
	}
	return ws.WithMain(name).Export(ctx)
}
