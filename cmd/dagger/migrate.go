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

		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()

			migration, err := currentWorkspaceMigration(ctx, dag)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			if migration.Changes.IsEmpty {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No migration needed.")
				return err
			}

			for i := range migration.Steps {
				if i > 0 {
					if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "MIGRATION %s\n", migration.Steps[i].Code); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), migration.Steps[i].Description); err != nil {
					return err
				}
				for _, warning := range migration.Steps[i].Warnings {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", warning); err != nil {
						return err
					}
				}
			}

			return handleChangesetResponse(ctx, dag, dag.LoadChangesetFromID(migration.Changes.ID), autoApply)
		})
	},
}

type workspaceMigrationResponse struct {
	Changes struct {
		ID      dagger.ChangesetID `json:"id"`
		IsEmpty bool               `json:"isEmpty"`
	} `json:"changes"`
	Steps []workspaceMigrationStepResponse `json:"steps"`
}

type workspaceMigrationStepResponse struct {
	Code        string   `json:"code"`
	Description string   `json:"description"`
	Warnings    []string `json:"warnings"`
}

func currentWorkspaceMigration(ctx context.Context, dag *dagger.Client) (*workspaceMigrationResponse, error) {
	var migration workspaceMigrationResponse
	err := dag.QueryBuilder().
		Select("currentWorkspace").
		Select("migrate").
		SelectMultiple(
			"changes{id isEmpty}",
			"steps{code description warnings}",
		).
		Bind(&migration).
		Execute(ctx)
	if err != nil {
		return nil, err
	}
	return &migration, nil
}

func init() {
	migrateCmd.Flags().BoolVarP(&migrateList, "list", "l", false, "List migratable modules instead of performing migration")
	setWorkspaceFlagPolicy(migrateCmd, workspaceFlagPolicyDisallow)
}

type migrationTarget struct {
	ConfigPath  string
	ProjectRoot string
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
