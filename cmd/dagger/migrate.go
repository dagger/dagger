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
			migrations, err := currentWorkspaceMigrations(ctx, engineClient)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			if len(migrations) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No migration needed.")
				return err
			}

			for _, migration := range migrations {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), migration.Description); err != nil {
					return err
				}
				for _, warning := range migration.Warnings {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", warning); err != nil {
						return err
					}
				}
			}

			changesets := make([]*dagger.Changeset, 0, len(migrations))
			for _, migration := range migrations {
				changesets = append(changesets, engineClient.Dagger().LoadChangesetFromID(dagger.ChangesetID(migration.ChangesID)))
			}
			return handleChangesetResponse(ctx, engineClient.Dagger(), engineClient.Dagger().Changeset().WithChangesets(changesets), true)
		})
	},
}

func init() {
	migrateCmd.Flags().BoolVarP(&migrateList, "list", "l", false, "List migratable modules instead of performing migration")
	setWorkspaceFlagPolicy(migrateCmd, workspaceFlagPolicyDisallow)
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

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		compatWorkspace, err := workspacecfg.ParseCompatWorkspace(data)
		if err != nil {
			return err
		}
		if compatWorkspace == nil {
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

type workspaceMigration struct {
	Description string
	Warnings    []string
	ChangesID   string
}

func currentWorkspaceMigrations(ctx context.Context, engineClient *client.Client) ([]workspaceMigration, error) {
	const query = `
query CurrentWorkspaceMigrations {
  currentWorkspace {
    migrate {
      description
      warnings
      changes {
        id
      }
    }
  }
}`

	var res struct {
		CurrentWorkspace struct {
			Migrate []struct {
				Description string   `json:"description"`
				Warnings    []string `json:"warnings"`
				Changes     struct {
					ID string `json:"id"`
				} `json:"changes"`
			} `json:"migrate"`
		} `json:"currentWorkspace"`
	}
	if err := engineClient.Do(ctx, query, "CurrentWorkspaceMigrations", nil, &res); err != nil {
		return nil, err
	}

	out := make([]workspaceMigration, 0, len(res.CurrentWorkspace.Migrate))
	for _, migration := range res.CurrentWorkspace.Migrate {
		out = append(out, workspaceMigration{
			Description: migration.Description,
			Warnings:    append([]string(nil), migration.Warnings...),
			ChangesID:   migration.Changes.ID,
		})
	}
	return out, nil
}
