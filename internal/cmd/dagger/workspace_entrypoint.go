package daggercmd

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var workspaceEntrypointUnset bool

var workspaceEntrypointCmd = &cobra.Command{
	Use:   "entrypoint [NAME]",
	Short: "Get or set the workspace entrypoint",
	Long: `Print the installed name of the current entrypoint.
Print nothing if no entrypoint is set.

With NAME, select that installed module and clear the previous entrypoint.
Use --unset to clear the selection. NAME must be an exact installed name.
Changes are written to the selected dagger.toml.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
			return err
		}
		if workspaceEntrypointUnset && len(args) != 0 {
			return fmt.Errorf("--unset cannot be used with NAME")
		}
		if len(args) == 1 && args[0] == "" {
			return fmt.Errorf("NAME must be an installed module name")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		write := workspaceEntrypointUnset || len(args) == 1
		if write && workspaceEnv != "" {
			return fmt.Errorf("workspace entrypoint does not support --env; entrypoints are set in the base workspace config")
		}
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules:           true,
			SuppressCompatWorkspaceWarning: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := engineClient.Dagger().CurrentWorkspace()
			if !write {
				data, err := ws.ConfigRead(ctx)
				if err != nil {
					return err
				}
				cfg, err := workspace.ParseConfig([]byte(data))
				if err != nil {
					return err
				}
				name, err := workspace.EntrypointName(cfg)
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
			return writeWorkspaceEntrypoint(ctx, ws, name)
		})
	},
}

func init() {
	workspaceEntrypointCmd.Flags().BoolVar(&workspaceEntrypointUnset, "unset", false, "Clear the entrypoint")
	workspaceCmd.AddCommand(workspaceEntrypointCmd)
}

func writeWorkspaceEntrypoint(ctx context.Context, ws *dagger.Workspace, name string) error {
	configFile, err := ws.ConfigFile(ctx)
	if err != nil {
		return err
	}
	if configFile == "" {
		return workspace.SetEntrypoint(nil, ".", name)
	}
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return err
	}
	configFile, err = workspaceConfigRootPathFromCwd(configFile, cwd)
	if err != nil {
		return err
	}
	// ConfigRead can include user or environment overlays. Read the selected
	// file for mutation so those overlays are never copied into base storage.
	root := ws.WithWorkdir(".")
	data, err := root.File(configFile).Contents(ctx)
	if err != nil {
		return err
	}
	cfg, err := workspace.ParseConfig([]byte(data))
	if err != nil {
		return err
	}
	if err := workspace.SetEntrypoint(cfg, filepath.Dir(configFile), name); err != nil {
		return err
	}
	updated, err := workspace.UpdateConfigBytes([]byte(data), cfg)
	if err != nil {
		return err
	}
	if bytes.Equal([]byte(data), updated) {
		return nil
	}
	return root.WithNewFile(configFile, string(updated)).Export(ctx)
}
