package schema

import (
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/core/workspace"
)

type workspaceUninstallArgs struct {
	Name string
	Here bool `default:"false"`
}

// removeSDKManagedModuleReference removes the installed module's source path
// from any [[modules.<sdk>.as-sdk.modules]] list. It returns a workspace-relative
// path only when the matched source is safe to delete from the overlay.
func removeSDKManagedModuleReference(cfg *workspace.Config, configDir string, entry workspace.ModuleEntry) (string, bool) {
	if cfg == nil || entry.AsSDK != nil || !workspace.IsLocalRef(entry.Source, entry.Pin) {
		return "", false
	}

	resolvedSource := workspace.ResolveModuleEntrySource(configDir, entry.Source)
	sourcePath := filepath.ToSlash(resolvedSource)
	removed := false
	for moduleName, sdkEntry := range cfg.Modules {
		if sdkEntry.AsSDK == nil || len(sdkEntry.AsSDK.Modules) == 0 {
			continue
		}

		sdkEntry.AsSDK.Modules = slices.DeleteFunc(sdkEntry.AsSDK.Modules, func(mod workspace.SDKManagedModule) bool {
			if filepath.ToSlash(filepath.Clean(mod.Path)) == sourcePath {
				removed = true
				return true
			}
			return false
		})
		cfg.Modules[moduleName] = sdkEntry
	}
	if !removed {
		return "", false
	}

	// Local installed modules can point outside the workspace (for example,
	// "../dep"). Clean up the TOML reference above, but only delete authored
	// SDK module directories that resolve inside the workspace.
	if filepath.IsAbs(resolvedSource) {
		return "", false
	}
	deletePath, err := resolveWorkspacePath(sourcePath, ".")
	if err != nil || deletePath == "." {
		return "", false
	}
	return deletePath, true
}
