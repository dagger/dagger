package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
)

var migrateList bool

var migrateCmd = &cobra.Command{
	Use:     "migrate",
	Short:   "Migrate a legacy dagger.json project to the workspace format",
	Long:    "Converts a legacy dagger.json to the .dagger/config.toml workspace format.",
	GroupID: workspaceGroup.ID,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if migrateList {
			return migrateListModules(cmd)
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		migErr, status, err := detectMigrationTarget(cwd)
		if err != nil {
			return err
		}
		if migErr == nil {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), status)
			return err
		}

		result, err := workspacecfg.Migrate(cmd.Context(), workspacecfg.LocalMigrationIO{}, migErr)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		if err := populateMigratedModuleLookups(cmd.Context(), result.LookupSources); err != nil {
			return fmt.Errorf("refresh migrated module lookups: %w", err)
		}

		_, err = fmt.Fprint(cmd.OutOrStdout(), result.Summary())
		return err
	},
}

func init() {
	migrateCmd.Flags().BoolVarP(&migrateList, "list", "l", false, "List migratable modules instead of performing migration")
}

func detectMigrationTarget(cwd string) (*workspacecfg.ErrMigrationRequired, string, error) {
	configPath := filepath.Join(cwd, workspacecfg.ModuleConfigFileName)
	switch _, err := os.Stat(configPath); {
	case os.IsNotExist(err):
		return nil, "No migration needed: no dagger.json found.", nil
	case err != nil:
		return nil, "", fmt.Errorf("checking %s: %w", configPath, err)
	}

	workspaceConfigPath := filepath.Join(cwd, workspacecfg.LockDirName, workspacecfg.ConfigFileName)
	switch _, err := os.Stat(workspaceConfigPath); {
	case err == nil:
		return nil, "No migration needed: workspace already initialized.", nil
	case !os.IsNotExist(err):
		return nil, "", fmt.Errorf("checking %s: %w", workspaceConfigPath, err)
	}

	return &workspacecfg.ErrMigrationRequired{
		ConfigPath:  configPath,
		ProjectRoot: cwd,
	}, "", nil
}

func migrateListModules(cmd *cobra.Command) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	paths, err := findMigratableModuleConfigs(root)
	if err != nil {
		return fmt.Errorf("walking directory tree: %w", err)
	}
	if len(paths) == 0 {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "No migratable modules found.")
		return err
	}
	for _, path := range paths {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), path); err != nil {
			return err
		}
	}
	return nil
}

func findMigratableModuleConfigs(root string) ([]string, error) {
	var paths []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == workspacecfg.LockDirName || (d.Name() != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != workspacecfg.ModuleConfigFileName {
			return nil
		}

		dir := filepath.Dir(path)
		workspaceConfigPath := filepath.Join(dir, workspacecfg.LockDirName, workspacecfg.ConfigFileName)
		if _, err := os.Stat(workspaceConfigPath); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return paths, nil
}

func populateMigratedModuleLookups(ctx context.Context, sources []string) error {
	if len(sources) == 0 {
		return nil
	}

	return withEngine(ctx, client.Params{
		SkipWorkspaceModules: true,
		LockMode:             string(workspacecfg.LockModePinned),
	}, func(ctx context.Context, engineClient *client.Client) error {
		return syncMigratedModuleSources(ctx, engineClient.Dagger(), sources)
	})
}

func syncMigratedModuleSources(ctx context.Context, dag *dagger.Client, sources []string) error {
	for _, source := range sources {
		if _, err := dag.ModuleSource(source).Sync(ctx); err != nil {
			return fmt.Errorf("resolve migrated module source %q: %w", source, err)
		}
	}
	return nil
}
