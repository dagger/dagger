package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
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
		cwd, err := pathutil.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		ws, err := workspace.DetectLocal(cwd)
		if err != nil {
			return fmt.Errorf("failed to detect workspace: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Root:   %s\n", ws.Root)
		configPath := filepath.Join(ws.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)
		if ws.Config != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Config: %s\n", configPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Config: none\n")
		}
		return nil
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
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			// Skip workspace loading — install does its own CLI-side detection
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			// 1. Detect workspace root (CLI-side)
			cwd, err := pathutil.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %w", err)
			}
			ws, err := workspace.DetectLocal(cwd)
			if err != nil {
				return fmt.Errorf("failed to detect workspace: %w", err)
			}

			// 2. Load existing config (or start empty)
			cfg := ws.Config
			if cfg == nil {
				cfg = &workspace.Config{}
			}
			if cfg.Modules == nil {
				cfg.Modules = make(map[string]workspace.ModuleEntry)
			}

			// 3. Resolve module name via engine
			depRefStr := extraArgs[0]
			depSrc := dag.ModuleSource(depRefStr, dagger.ModuleSourceOpts{
				DisableFindUp: true,
			})

			moduleName, err := depSrc.ModuleName(ctx)
			if err != nil {
				return fmt.Errorf("failed to resolve module name: %w", err)
			}
			if installName != "" {
				moduleName = installName
			}

			// 4. Determine source path
			sourcePath := depRefStr
			depKind, err := depSrc.Kind(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module source kind: %w", err)
			}
			if depKind == dagger.ModuleSourceKindLocalSource {
				// Make path relative to .dagger/ directory
				contextDirPath, err := depSrc.LocalContextDirectoryPath(ctx)
				if err != nil {
					return fmt.Errorf("failed to get local context directory: %w", err)
				}
				depRootSubpath, err := depSrc.SourceRootSubpath(ctx)
				if err != nil {
					return fmt.Errorf("failed to get source root subpath: %w", err)
				}
				depAbsPath := filepath.Join(contextDirPath, depRootSubpath)
				daggerDir := filepath.Join(ws.Root, workspace.WorkspaceDirName)
				relPath, err := filepath.Rel(daggerDir, depAbsPath)
				if err != nil {
					return fmt.Errorf("failed to compute relative path: %w", err)
				}
				sourcePath = relPath
			}

			// 5. Check if already installed with same source
			if existing, ok := cfg.Modules[moduleName]; ok && existing.Source == sourcePath {
				fmt.Fprintf(cmd.OutOrStdout(), "Module %q is already installed\n", moduleName)
				return nil
			}

			// 6. Add module to config
			cfg.Modules[moduleName] = workspace.ModuleEntry{Source: sourcePath}

			// 7. Write config.toml
			daggerDir := filepath.Join(ws.Root, workspace.WorkspaceDirName)
			if err := os.MkdirAll(daggerDir, 0o755); err != nil {
				return fmt.Errorf("failed to create %s: %w", daggerDir, err)
			}
			configPath := filepath.Join(daggerDir, workspace.ConfigFileName)
			if err := os.WriteFile(configPath, workspace.SerializeConfig(cfg), 0o644); err != nil {
				return fmt.Errorf("failed to write %s: %w", configPath, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Installed module %q in %s\n", moduleName, configPath)

			// 8. Analytics
			analytics.Ctx(ctx).Capture(ctx, "workspace_install", map[string]string{
				"module_name":  moduleName,
				"source":       sourcePath,
				"source_kind":  string(depKind),
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
