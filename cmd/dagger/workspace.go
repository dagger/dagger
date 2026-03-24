package main

import (
	"context"
	"fmt"
	"io"

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

type workspaceInfoView struct {
	Address    string
	Path       string
	ConfigPath string
}

func init() {
	workspaceCmd.AddCommand(workspaceInfoCmd)
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
