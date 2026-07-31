package schema

import (
	"fmt"

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
		if compatWorkspace.DiscoveredLocalModule {
			// A discovered local toolchain/dependency converts in place even
			// when its source is a non-root subdir (a normal toolchain would
			// otherwise be treated as workspace-shaped). Only a genuine nested
			// workspace (its own toolchains/blueprint) is left as legacy.
			if workspaceMigrationLeavesModuleLegacy(compatWorkspace) {
				continue
			}
		} else if compatWorkspace.MustMigrateToWorkspaceConfig() {
			// PlanMigration writes this config's dagger-module.toml alongside
			// its dagger.toml.
			continue
		}
		// Reaching here non-discovered means the selected config is a plain
		// SDK module with its source at the project root — the "repo is just
		// a dagger module" shape. It converts in place; the minimal workspace
		// config pinning its runtime comes from the parent-plan flow.
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

// workspaceMigrationLeavesModuleLegacy reports whether migration leaves this
// module's config in legacy format: a discovered nested workspace (own
// toolchains/blueprint) is neither converted in place nor routed through
// PlanMigration.
func workspaceMigrationLeavesModuleLegacy(compatWorkspace *workspace.CompatWorkspace) bool {
	return compatWorkspace.DiscoveredLocalModule &&
		workspace.HasOwnWorkspaceSemantics(compatWorkspace.Config)
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
