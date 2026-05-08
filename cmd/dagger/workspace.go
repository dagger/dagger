package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Short:   "Manage the current workspace",
	GroupID: workspaceGroup.ID,
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
			cwd, err := ws.Cwd(ctx)
			if err != nil {
				return fmt.Errorf("load workspace cwd: %w", err)
			}
			configFile, err := ws.ConfigFile(ctx)
			if err != nil {
				return fmt.Errorf("load workspace config file: %w", err)
			}

			return writeWorkspaceInfo(cmd.OutOrStdout(), workspaceInfoView{
				Address:    address,
				Cwd:        cwd,
				ConfigFile: configFile,
			})
		})
	},
}

var workspaceRootCmd = &cobra.Command{
	Use:   "root",
	Short: "Print the workspace root",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := engineClient.Dagger().CurrentWorkspace()
			address, err := ws.Address(ctx)
			if err != nil {
				return fmt.Errorf("load workspace address: %w", err)
			}
			cwd, err := ws.Cwd(ctx)
			if err != nil {
				return fmt.Errorf("load workspace cwd: %w", err)
			}
			root, err := workspaceRootFromAddress(address, cwd)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), root)
			return err
		})
	},
}

var workspaceCwdCmd = &cobra.Command{
	Use:   "cwd",
	Short: "Print the workspace cwd",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			cwd, err := engineClient.Dagger().CurrentWorkspace().Cwd(ctx)
			if err != nil {
				return fmt.Errorf("load workspace cwd: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), cwd)
			return err
		})
	},
}

var workspaceConfigFileCmd = &cobra.Command{
	Use:   "config-file",
	Short: "Print the selected workspace config file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			configFile, err := engineClient.Dagger().CurrentWorkspace().ConfigFile(ctx)
			if err != nil {
				return fmt.Errorf("load workspace config file: %w", err)
			}
			if configFile == "" {
				configFile = "none"
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), configFile)
			return err
		})
	},
}

var workspaceInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create workspace config",
	Long:  "Create .dagger/config.toml for the current workspace.",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceInit,
}

var initCmd = &cobra.Command{
	Use:        moduleInitCmd.Use,
	Short:      "Initialize a new module",
	Long:       "Deprecated alias for `dagger module init`.",
	Example:    moduleInitCmd.Example,
	Args:       validateModuleInitArgs,
	GroupID:    moduleGroup.ID,
	Hidden:     true,
	Deprecated: `use "dagger module init" instead`,
	RunE:       runModuleInit,
}

var workspaceConfigCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "Get or set workspace configuration",
	Long: `Get or set workspace configuration values in .dagger/config.toml.

With no arguments, prints the full configuration.
With one argument, prints the value at the given key.
With two arguments, sets the value at the given key.

With --env, reads show the effective env-applied view while writes target that
environment's overlay. Explicit env.* keys always address raw overlay storage.

Local module source values are stored relative to .dagger/config.toml.`,
	Args: cobra.MaximumNArgs(2),
	RunE: runWorkspaceConfig,
}

type workspaceInfoView struct {
	Address    string
	Cwd        string
	ConfigFile string
}

func init() {
	workspaceCmd.AddCommand(workspaceConfigCmd)
	workspaceCmd.AddCommand(workspaceConfigFileCmd)
	workspaceCmd.AddCommand(workspaceCwdCmd)
	workspaceCmd.AddCommand(workspaceInitCmd)
	workspaceCmd.AddCommand(workspaceInfoCmd)
	workspaceCmd.AddCommand(workspaceRootCmd)

	addWorkspaceHereFlag(workspaceConfigCmd)
	addWorkspaceHereFlag(workspaceInitCmd)

	setWorkspaceFlagPolicy(workspaceInitCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(initCmd, workspaceFlagPolicyLocalOnly)
}

func addWorkspaceHereFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&workspaceHere, "here", false, "Write workspace config at the selected workspace cwd")
}

func runWorkspaceInit(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		configDir, err := initWorkspace(ctx, engineClient.Dagger())
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created workspace config in %s\n", configDir)
		return err
	})
}

func runWorkspaceConfig(cmd *cobra.Command, args []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
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
}

func initWorkspace(ctx context.Context, dag *dagger.Client) (string, error) {
	return dag.CurrentWorkspace().Init(ctx, dagger.WorkspaceInitOpts{Here: workspaceHere})
}

func printWorkspaceConfig(ctx context.Context, out io.Writer, ws *dagger.Workspace, key string) error {
	value, err := ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{Key: key})
	if err != nil {
		return err
	}

	value = strings.TrimRight(value, "\n")
	if key == "" && value == "" {
		return nil
	}

	_, err = fmt.Fprintln(out, value)
	return err
}

func writeWorkspaceConfig(ctx context.Context, ws *dagger.Workspace, key, value string) error {
	_, err := ws.ConfigWrite(ctx, key, value, dagger.WorkspaceConfigWriteOpts{Here: workspaceHere})
	return err
}

func installWorkspaceModule(ctx context.Context, out io.Writer, dag *dagger.Client, ref, name string, here bool) error {
	msg, err := dag.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: name, Here: here})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, msg)
	return err
}

type workspaceModuleInitOptions struct {
	Name      string
	SDK       string
	Source    string
	Include   []string
	SelfCalls bool
	Here      bool
}

func initWorkspaceModule(ctx context.Context, out io.Writer, dag *dagger.Client, opts workspaceModuleInitOptions) error {
	ws := dag.CurrentWorkspace()

	msg, err := ws.ModuleInit(ctx, opts.Name, dagger.WorkspaceModuleInitOpts{
		SDK:       opts.SDK,
		Source:    opts.Source,
		Include:   opts.Include,
		SelfCalls: opts.SelfCalls,
		Here:      opts.Here,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, msg)
	return err
}

func writeWorkspaceInfo(w io.Writer, info workspaceInfoView) error {
	configFile := info.ConfigFile
	if configFile == "" {
		configFile = "none"
	}

	_, err := fmt.Fprintf(w,
		"Address: %s\nCwd:     %s\nConfig:  %s\n",
		info.Address,
		info.Cwd,
		configFile,
	)
	return err
}

func workspaceRootFromAddress(address, cwd string) (string, error) {
	if cwd == "" || cwd == "." {
		return fileURLPathOrAddress(address), nil
	}

	if parsed, err := url.Parse(address); err == nil && parsed.Scheme == "file" {
		root := strings.TrimSuffix(filepath.Clean(parsed.Path), string(filepath.Separator)+filepath.Clean(cwd))
		return root, nil
	}

	version := ""
	base := address
	if idx := strings.LastIndex(address, "@"); idx > strings.LastIndex(address, "/") {
		base = address[:idx]
		version = address[idx:]
	}
	root := strings.TrimSuffix(filepath.ToSlash(base), "/"+filepath.ToSlash(filepath.Clean(cwd)))
	return root + version, nil
}

func fileURLPathOrAddress(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "file" {
		return address
	}
	return parsed.Path
}
