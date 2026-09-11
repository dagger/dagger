package schema

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
)

// Migrate the selected config before the installed-module phase. Both phases
// share the same snapshot and are exported as one reviewed changeset.
func (stage *workspaceMigrationStage) migrateConfig(ctx context.Context) error {
	data, err := core.DirectoryReadFile(ctx, stage.staged, filepath.ToSlash(stage.configPath))
	if err != nil {
		return err
	}
	updated, err := workspace.MigrateConfigBytes(data, filepath.Dir(stage.configPath))
	if err != nil {
		return fmt.Errorf("migrate %s: %w", stage.configPath, err)
	}
	if bytes.Equal(data, updated) {
		return nil
	}
	after, err := withWorkspaceMigrationFile(ctx, stage.staged, stage.configPath, updated, "migrate SDK configuration")
	if err != nil {
		return err
	}
	changes, err := workspaceMigrationChanges(ctx, after, stage.staged)
	if err != nil {
		return err
	}
	stage.staged = after
	stage.legacy.Steps = append(stage.legacy.Steps, &core.WorkspaceMigrationStep{
		Code:        "legacy-sdk-config",
		Description: "Moved legacy SDK configuration to sdks",
		Changes:     changes.Self(),
	})
	return nil
}
