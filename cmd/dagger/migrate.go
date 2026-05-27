package main

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
)

var migrateForce bool

var migrateCmd = &cobra.Command{
	Use:     "migrate",
	Short:   "Migrate a legacy dagger.json project to the workspace format",
	Long:    "Converts a legacy dagger.json to the .dagger/config.toml workspace format.",
	GroupID: workspaceGroup.ID,
	Args:    cobra.NoArgs,
	Annotations: map[string]string{
		showFinalProgressKey: "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules:           true,
			SuppressCompatWorkspaceWarning: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			currentWorkspace := dag.CurrentWorkspace()

			migration := currentWorkspace.Migrate(dagger.WorkspaceMigrateOpts{
				Force: migrateForce,
			})

			changes := migration.Changes()
			changesID, err := changes.ID(ctx)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			changes = dagger.Ref[*dagger.Changeset](dag, changesID)

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

			exportPath, err := currentWorkspaceExportPath(ctx, currentWorkspace)
			if err != nil {
				return err
			}
			return handleChangesetResponseAt(ctx, dag, changes, autoApply, exportPath)
		})
	},
}

func init() {
	migrateCmd.Flags().BoolVarP(&migrateForce, "force", "f", false, "Proceed even if modules cannot be loaded to generate settings hints")
	setWorkspaceFlagPolicy(migrateCmd, workspaceFlagPolicyDisallow)
}

func currentWorkspaceExportPath(ctx context.Context, ws *dagger.Workspace) (string, error) {
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return "", fmt.Errorf("workspace cwd: %w", err)
	}
	address, err := ws.Address(ctx)
	if err != nil {
		return "", fmt.Errorf("workspace address: %w", err)
	}
	wd, err := localWorkspaceAddressPath(address)
	if err != nil {
		return "", err
	}
	root, err := workspaceRootFromCwd(wd, cwd)
	if err != nil {
		return "", err
	}
	return root, nil
}

func localWorkspaceAddressPath(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("workspace address %q: %w", address, err)
	}
	if u.Scheme != "file" || u.Path == "" {
		return "", fmt.Errorf("workspace migration requires a local file workspace, got %q", address)
	}
	return filepath.FromSlash(u.Path), nil
}

func workspaceRootFromCwd(wd, workspaceCwd string) (string, error) {
	root, err := filepath.Abs(wd)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	workspaceCwd = filepath.Clean(filepath.FromSlash(workspaceCwd))
	if workspaceCwd == "" || workspaceCwd == "." {
		return root, nil
	}
	if filepath.IsAbs(workspaceCwd) || workspaceCwd == ".." || strings.HasPrefix(workspaceCwd, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace cwd %q escapes workspace root", workspaceCwd)
	}
	for _, part := range strings.Split(workspaceCwd, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		root = filepath.Dir(root)
	}
	return root, nil
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
