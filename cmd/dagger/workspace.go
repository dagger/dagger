package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
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
			ws := dag.CurrentWorkspace()

			wsPath, err := ws.Path(ctx)
			if err != nil {
				return fmt.Errorf("workspace: %w", err)
			}
			initialized, err := ws.Initialized(ctx)
			if err != nil {
				return fmt.Errorf("workspace: %w", err)
			}
			hasConfig, err := ws.HasConfig(ctx)
			if err != nil {
				return fmt.Errorf("workspace: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace path: %s\n", wsPath)
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized:    %t\n", initialized)
			if hasConfig {
				configPath, err := ws.ConfigPath(ctx)
				if err != nil {
					return fmt.Errorf("workspace: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Config:         %s\n", configPath)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Config:         none\n")
			}
			return nil
		})
	},
}

var workspaceInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new workspace",
	Long:  "Initialize a new workspace in the current directory, creating .dagger/config.toml.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ws := dag.CurrentWorkspace()

			path, err := ws.Init(ctx)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Initialized workspace in %s\n", path)
			return nil
		})
	},
}

var moduleInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Aliases: []string{"use"},
	Short:   "Install a module dependency",
	Long: `Install a module, either into the workspace or as a module dependency.

If a workspace exists (.dagger/config.toml), the module is added to the workspace config.
If no workspace exists but a dagger.json is present (standalone module), the module is
added as a dependency in dagger.json. Use 'dagger module install' to explicitly install
a module dependency regardless of workspace context.`,
	Example: `dagger install github.com/shykes/daggerverse/hello@v0.3.0
  dagger install github.com/dagger/dagger/modules/wolfi --name=alpine
  dagger install ./path/to/local/module`,
	GroupID: moduleGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		if remoteWorkdir != "" {
			return fmt.Errorf("cannot install with a remote workdir")
		}

		// If no workspace is initialized but a standalone module exists,
		// fall back to module dependency install.
		if shouldInstallModuleDep() {
			return moduleDepInstallCmd.RunE(cmd, extraArgs)
		}

		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			ws := dag.CurrentWorkspace()

			msg, err := ws.Install(ctx, extraArgs[0], dagger.WorkspaceInstallOpts{
				Name:      installName,
				Blueprint: installBlueprint,
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

// shouldInstallModuleDep returns true when `dagger install` should add a
// module dependency (to dagger.json) rather than a workspace entry.
//
// This is the case when:
//   - The -m flag explicitly targets a module, or
//   - The current directory has a standalone module (dagger.json) but no
//     initialized workspace (.dagger/config.toml).
func shouldInstallModuleDep() bool {
	// Explicit -m flag always means module dependency install.
	if moduleURL != "" {
		return true
	}

	cwd, err := os.Getwd()
	if err != nil {
		return false
	}

	// Check for an initialized workspace in cwd.
	configPath := filepath.Join(cwd, workspace.WorkspaceDirName, workspace.ConfigFileName)
	if _, err := os.Stat(configPath); err == nil {
		return false // workspace exists, use workspace install
	}

	// No workspace — check for standalone module config.
	modulePath := filepath.Join(cwd, workspace.ModuleConfigFileName)
	if _, err := os.Stat(modulePath); err == nil {
		return true // standalone module, use module dep install
	}

	return false // neither — default to workspace install (will auto-create)
}

var migrateList bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate a legacy dagger.json project to the workspace format",
	Long:  "Converts a legacy dagger.json to the .dagger/config.toml workspace format.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if migrateList {
			return migrateListModules(cmd)
		}

		if remoteWorkdir != "" {
			return fmt.Errorf("workspace on git remote cannot be modified")
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		// Structural detection: dagger.json exists, no .dagger/config.toml
		configPath := filepath.Join(cwd, workspace.ModuleConfigFileName)
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "No migration needed: no dagger.json found.")
			return nil
		}
		tomlPath := filepath.Join(cwd, workspace.WorkspaceDirName, workspace.ConfigFileName)
		if _, err := os.Stat(tomlPath); err == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "No migration needed: workspace already initialized.")
			return nil
		}

		migErr := &workspace.ErrMigrationRequired{
			ConfigPath:  configPath,
			ProjectRoot: cwd,
		}
		result, err := workspace.Migrate(cmd.Context(), workspace.LocalMigrationIO{}, migErr, nil)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		fmt.Fprint(cmd.OutOrStdout(), result.Summary())
		return nil
	},
}

// migrateListModules walks the current directory tree, finds all dagger.json
// files that match migration triggers, and prints their paths.
func migrateListModules(cmd *cobra.Command) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	found := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip .dagger workspace config dirs and hidden dirs (e.g. .git)
			if d.Name() == workspace.WorkspaceDirName || (d.Name() != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != workspace.ModuleConfigFileName {
			return nil
		}

		dir := filepath.Dir(path)

		// If .dagger/config.toml already exists, this module has already been
		// migrated. The dagger.json is kept for compatibility with older engines.
		configPath := filepath.Join(dir, workspace.WorkspaceDirName, workspace.ConfigFileName)
		if _, err := os.Stat(configPath); err == nil {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		fmt.Fprintln(cmd.OutOrStdout(), rel)
		found++
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory tree: %w", err)
	}

	if found == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "No migrateable modules found.")
	}
	return nil
}

var workspaceConfigCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "Get or set workspace configuration",
	Long: `Get or set workspace configuration values in .dagger/config.toml.

With no arguments, prints the full configuration.
With one argument, prints the value at the given key.
With two arguments, sets the value at the given key.

Works like "git config" for workspace settings.`,
	Example: `  dagger workspace config
  dagger workspace config modules.mymod.source
  dagger workspace config modules.mymod.alias false
  dagger workspace config modules.mymod.config.tags main,develop`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ws := dag.CurrentWorkspace()

			switch len(args) {
			case 0, 1:
				var key string
				if len(args) == 1 {
					key = args[0]
				}
				val, err := ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{
					Key: key,
				})
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(val, "\n"))
				return nil
			case 2:
				_, err := ws.ConfigWrite(ctx, args[0], args[1])
				return err
			default:
				return fmt.Errorf("expected 0-2 arguments, got %d", len(args))
			}
		})
	},
}

func init() {
	workspaceCmd.AddCommand(workspaceInitCmd)
	workspaceCmd.AddCommand(workspaceInfoCmd)
	workspaceCmd.AddCommand(workspaceConfigCmd)

	migrateCmd.Flags().BoolVarP(&migrateList, "list", "l", false, "List migrateable modules instead of performing migration")

	moduleInstallCmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")
	moduleInstallCmd.Flags().BoolVar(&installBlueprint, "blueprint", false, "Install the module as a blueprint (functions aliased to Query root)")
	moduleInstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(moduleInstallCmd, moduleInstallCmd.Flags(), false)
}
