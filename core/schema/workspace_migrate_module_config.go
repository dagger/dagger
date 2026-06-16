package schema

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
)

type workspaceMigrationModuleConfigConversion struct {
	ProjectRoot string
	ConfigData  []byte
}

func workspaceMigrationModuleConfigConversions(
	compatWorkspaces []*workspace.CompatWorkspace,
) ([]workspaceMigrationModuleConfigConversion, error) {
	// This is filename-format migration, not workspace compat projection:
	// workspace-shaped legacy configs are handled by PlanMigration, while plain
	// module-shaped legacy configs are converted in place to dagger-module.toml.
	conversions := make([]workspaceMigrationModuleConfigConversion, 0, len(compatWorkspaces))

	for _, compatWorkspace := range compatWorkspaces {
		if compatWorkspace == nil || compatWorkspace.Config == nil {
			continue
		}
		if compatWorkspace.MustMigrateToWorkspaceConfig() {
			continue
		}
		if compatWorkspace.Config.SDK != nil && !workspaceMigrationProjectRootInDefaultModules(compatWorkspace.ProjectRoot) {
			continue
		}
		if compatWorkspace.ProjectRoot == "" {
			return nil, fmt.Errorf("legacy module config project root is required")
		}

		configData, err := legacyModuleConfigAsCurrent(compatWorkspace.Config)
		if err != nil {
			return nil, fmt.Errorf("converting legacy module config %s: %w", compatWorkspace.ConfigPath, err)
		}
		conversions = append(conversions, workspaceMigrationModuleConfigConversion{
			ProjectRoot: compatWorkspace.ProjectRoot,
			ConfigData:  configData,
		})
	}

	if len(conversions) == 0 {
		return nil, nil
	}
	return conversions, nil
}

func legacyModuleConfigAsCurrent(cfg *modules.ModuleConfig) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("module config is required")
	}
	cloned := *cfg
	if cloned.Source == "." {
		cloned.Source = ""
	}
	return modules.MarshalModuleConfigForFormat(&modules.ModuleConfigWithUserFields{
		ModuleConfig: cloned,
	}, modules.ConfigFormatCurrent)
}

func workspaceMigrationProjectRootInDefaultModules(projectRoot string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(projectRoot)), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == workspace.LockDirName && parts[i+1] == "modules" && parts[i+2] != "" {
			return true
		}
	}
	return false
}
