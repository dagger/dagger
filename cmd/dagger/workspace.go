package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/juju/ansiterm/tabwriter"
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

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace modules",
	Long: `List all modules defined in the workspace configuration.

Note:
- Source paths are relative to the workspace root.
- * means the module is a blueprint, with all its functions aliased to the root level.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			modules, err := engineClient.Dagger().CurrentWorkspace().ModuleList(ctx)
			if err != nil {
				return err
			}
			return writeWorkspaceModuleList(cmd.OutOrStdout(), modules)
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
	workspaceCmd.AddCommand(workspaceListCmd)
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

func installWorkspaceModule(ctx context.Context, out io.Writer, dag *dagger.Client, ref, name string) error {
	msg, err := dag.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: name})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, msg)
	return err
}

type workspaceModuleInitOptions struct {
	Name                  string
	SDK                   string
	Source                string
	Include               []string
	Blueprint             string
	SelfCalls             bool
	SearchExistingLicense bool
}

func initWorkspaceModule(ctx context.Context, out io.Writer, dag *dagger.Client, cwd string, opts workspaceModuleInitOptions) error {
	ws := dag.CurrentWorkspace()

	msg, err := ws.ModuleInit(ctx, opts.Name, opts.SDK, dagger.WorkspaceModuleInitOpts{
		Source:    opts.Source,
		Include:   opts.Include,
		Blueprint: opts.Blueprint,
		SelfCalls: opts.SelfCalls,
	})
	if err != nil {
		return err
	}

	if opts.SDK != "" {
		wsPath, err := ws.Path(ctx)
		if err != nil {
			return fmt.Errorf("load workspace path: %w", err)
		}
		modulePath, err := workspaceModulePath(cwd, wsPath, opts.Name)
		if err != nil {
			return err
		}
		if err := findOrCreateLicense(ctx, modulePath, opts.SearchExistingLicense); err != nil {
			return err
		}
	}

	_, err = fmt.Fprintln(out, msg)
	return err
}

func workspaceModulePath(cwd, wsPath, name string) (string, error) {
	boundary := cwd
	for _, segment := range strings.Split(filepath.ToSlash(filepath.Clean(wsPath)), "/") {
		if segment == "" || segment == "." {
			continue
		}
		boundary = filepath.Dir(boundary)
	}
	return filepath.Join(boundary, ".dagger", "modules", name), nil
}

func writeWorkspaceModuleList(out io.Writer, modules []dagger.WorkspaceModule) error {
	if _, err := fmt.Fprintln(out, "Source paths are relative to the workspace root"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "* indicates a module is a blueprint, with all its functions aliased to the root level"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	if _, err := fmt.Fprintln(tw, "Name\tSource"); err != nil {
		return err
	}
	for _, mod := range modules {
		name := mod.Name
		if mod.Blueprint {
			name += "*"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", name, mod.Source); err != nil {
			return err
		}
	}
	return tw.Flush()
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
