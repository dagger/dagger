package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Short:   "Manage the current workspace",
	GroupID: moduleGroup.ID,
}

var workspaceInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show workspace information",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ws := dag.Workspace()

			root, err := ws.Root(ctx)
			if err != nil {
				return fmt.Errorf("workspace: %w", err)
			}
			hasConfig, err := ws.HasConfig(ctx)
			if err != nil {
				return fmt.Errorf("workspace: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Root:   %s\n", root)
			if hasConfig {
				configPath, err := ws.ConfigPath(ctx)
				if err != nil {
					return fmt.Errorf("workspace: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Config: %s\n", configPath)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Config: none\n")
			}
			return nil
		})
	},
}

var moduleInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Aliases: []string{"use"},
	Short:   "Add a module to the workspace",
	Long:    "Add a module to the workspace, making its functions available via 'dagger call'.",
	Example: `dagger install github.com/shykes/daggerverse/hello@v0.3.0
  dagger install github.com/dagger/dagger/modules/wolfi --name=alpine`,
	GroupID: moduleGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		if remoteWorkdir != "" {
			return fmt.Errorf("cannot install with a remote workdir")
		}
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			ws := dag.Workspace(dagger.WorkspaceOpts{
				SkipMigrationCheck: true,
			})

			msg, err := ws.Install(ctx, extraArgs[0], dagger.WorkspaceInstallOpts{
				Name: installName,
			})
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), msg)

			analytics.Ctx(ctx).Capture(ctx, "workspace_install", map[string]string{
				"module":       extraArgs[0],
				"install_name": installName,
			})

			return nil
		})
	},
}

func init() {
	workspaceCmd.AddCommand(workspaceInfoCmd)

	moduleInstallCmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")
	moduleInstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(moduleInstallCmd, moduleInstallCmd.Flags(), false)
}
