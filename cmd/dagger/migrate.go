package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
)

var migrateList bool
var migrateForce bool

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

		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()

			migration := dag.CurrentWorkspace().Migrate(dagger.WorkspaceMigrateOpts{
				Force: migrateForce,
			})

			changes := migration.Changes()
			changesID, err := changes.ID(ctx)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			changes = dag.LoadChangesetFromID(changesID)

			isEmpty, err := changes.IsEmpty(ctx)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			if isEmpty {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No migration needed.")
				return err
			}

			warnings, err := migrationWarnings(ctx, migration)
			if err != nil {
				return fmt.Errorf("migration warnings: %w", err)
			}
			for _, warning := range warnings {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", warning); err != nil {
					return err
				}
			}

			return handleChangesetResponse(ctx, dag, changes, autoApply)
		})
	},
}

func init() {
	migrateCmd.Flags().BoolVarP(&migrateList, "list", "l", false, "List migratable modules instead of performing migration")
	migrateCmd.Flags().BoolVarP(&migrateForce, "force", "f", false, "Proceed even if modules cannot be loaded to generate settings hints")
	setWorkspaceFlagPolicy(migrateCmd, workspaceFlagPolicyDisallow)
}

type migrationTarget struct {
	ConfigPath  string
	ProjectRoot string
}

func migrationWarnings(ctx context.Context, migration *dagger.WorkspaceMigration) ([]string, error) {
	steps, err := migration.Steps(ctx)
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0)
	seen := make(map[string]struct{})
	for _, step := range steps {
		stepWarnings, err := step.Warnings(ctx)
		if err != nil {
			return nil, err
		}
		for _, warning := range stepWarnings {
			if warning == "" {
				continue
			}
			if _, ok := seen[warning]; ok {
				continue
			}
			seen[warning] = struct{}{}
			warnings = append(warnings, warning)
		}
	}

	return warnings, nil
}

func probeMigratableModuleConfig(dir string) (*migrationTarget, string, error) {
	configPath := filepath.Join(dir, workspacecfg.ModuleConfigFileName)
	switch _, err := os.Stat(configPath); {
	case os.IsNotExist(err):
		return nil, "No migration needed: no dagger.json found.", nil
	case err != nil:
		return nil, "", fmt.Errorf("checking %s: %w", configPath, err)
	}

	workspaceConfigPath := filepath.Join(dir, workspacecfg.LockDirName, workspacecfg.ConfigFileName)
	switch _, err := os.Stat(workspaceConfigPath); {
	case err == nil:
		return nil, "No migration needed: workspace already initialized.", nil
	case !os.IsNotExist(err):
		return nil, "", fmt.Errorf("checking %s: %w", workspaceConfigPath, err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", configPath, err)
	}
	compatWorkspace, err := workspacecfg.ParseCompatWorkspace(data)
	if err != nil {
		return nil, "", fmt.Errorf("parsing %s: %w", configPath, err)
	}
	if compatWorkspace == nil {
		return nil, "No migration needed: legacy dagger.json does not create compat workspace.", nil
	}

	return &migrationTarget{
		ConfigPath:  configPath,
		ProjectRoot: dir,
	}, "", nil
}

func detectMigrationTarget(cwd string) (*migrationTarget, string, error) {
	return probeMigratableModuleConfig(cwd)
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

		target, _, err := probeMigratableModuleConfig(filepath.Dir(path))
		if err != nil {
			return err
		}
		if target == nil {
			return nil
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
