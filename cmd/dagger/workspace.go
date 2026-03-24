package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
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
		return withEngine(cmd.Context(), client.Params{
			// This command only needs workspace metadata, not workspace modules.
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := engineClient.Dagger().CurrentWorkspace()

			address, err := ws.Address(ctx)
			if err != nil {
				return fmt.Errorf("load workspace address: %w", err)
			}
			path, err := ws.Path(ctx)
			if err != nil {
				return fmt.Errorf("load workspace path: %w", err)
			}
			configPath, err := ws.ConfigPath(ctx)
			if err != nil {
				return fmt.Errorf("load workspace config path: %w", err)
			}

			return writeWorkspaceInfo(cmd.OutOrStdout(), workspaceInfoView{
				Address:    address,
				Path:       path,
				ConfigPath: configPath,
			})
		})
	},
}

var workspaceInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new workspace",
	Long:  "Initialize a new workspace in the current directory, creating .dagger/config.toml.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			configDir, err := initWorkspace(ctx, engineClient.Dagger())
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Initialized workspace in %s\n", configDir)
			return err
		})
	},
}

var workspaceConfigCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "Get or set workspace configuration",
	Long: `Get or set workspace configuration values in .dagger/config.toml.

With no arguments, prints the full configuration.
With one argument, prints the value at the given key.
With two arguments, sets the value at the given key.`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := engineClient.Dagger().CurrentWorkspace()

			switch len(args) {
			case 0:
				return printWorkspaceConfig(ctx, cmd.OutOrStdout(), ws, "")
			case 1:
				return printWorkspaceConfig(ctx, cmd.OutOrStdout(), ws, args[0])
			case 2:
				return writeWorkspaceConfig(ctx, ws, args[0], args[1])
			default:
				return fmt.Errorf("expected 0-2 arguments, got %d", len(args))
			}
		})
	},
}

type workspaceInfoView struct {
	Address    string
	Path       string
	ConfigPath string
}

func init() {
	workspaceCmd.AddCommand(workspaceConfigCmd)
	workspaceCmd.AddCommand(workspaceInitCmd)
	workspaceCmd.AddCommand(workspaceInfoCmd)
}

func initWorkspace(ctx context.Context, dag *dagger.Client) (string, error) {
	return dag.CurrentWorkspace().Init(ctx)
}

func printWorkspaceConfig(ctx context.Context, out io.Writer, ws *dagger.Workspace, key string) error {
	value, err := ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{Key: key})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, strings.TrimRight(value, "\n"))
	return err
}

func writeWorkspaceConfig(ctx context.Context, ws *dagger.Workspace, key, value string) error {
	_, err := ws.ConfigWrite(ctx, key, value)
	return err
}

func writeWorkspaceInfo(w io.Writer, info workspaceInfoView) error {
	configPath := info.ConfigPath
	if configPath == "" {
		configPath = "none"
	}

	_, err := fmt.Fprintf(w,
		"Address: %s\nPath:    %s\nConfig:  %s\n",
		info.Address,
		info.Path,
		configPath,
	)
	return err
}
