package schema

import (
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/workspace"
)

type workspaceUninstallArgs struct {
	Name string
	Here bool `default:"false"`
}

// removeSDKManagedModuleReference removes the installed module's source path
// from any [[modules.<sdk>.as-sdk.modules]] list. It returns a workspace-relative
// path only when the matched source is safe to delete from the overlay. An
// as-sdk entry that cannot be resolved is a corruption every other reader fails
// on, so it fails here too rather than being mistaken for a miss.
func removeSDKManagedModuleReference(cfg *workspace.Config, configDir string, entry workspace.ModuleEntry) (string, bool, error) {
	if cfg == nil || entry.AsSDK != nil || !workspace.IsLocalRef(entry.Source, entry.Pin) {
		return "", false, nil
	}

	resolvedSource := workspace.ResolveModuleEntrySource(configDir, entry.Source)
	sourcePath := filepath.ToSlash(resolvedSource)
	removed := false
	for moduleName, sdkEntry := range cfg.Modules {
		if sdkEntry.AsSDK == nil || len(sdkEntry.AsSDK.Modules) == 0 {
			continue
		}

		kept := make([]workspace.SDKManagedModule, 0, len(sdkEntry.AsSDK.Modules))
		for _, mod := range sdkEntry.AsSDK.Modules {
			managed, err := workspace.ResolveSDKManagedPath(configDir, mod.Path)
			if err != nil {
				return "", false, fmt.Errorf("module managed by %q: %w", moduleName, err)
			}
			if managed == sourcePath {
				removed = true
				continue
			}
			kept = append(kept, mod)
		}
		sdkEntry.AsSDK.Modules = kept
		cfg.Modules[moduleName] = sdkEntry
	}
	if !removed {
		return "", false, nil
	}

	// Local installed modules can point outside the workspace (for example,
	// "../dep"). Clean up the TOML reference above, but only delete authored
	// SDK module directories that resolve inside the workspace.
	if filepath.IsAbs(resolvedSource) {
		return "", false, nil
	}
	deletePath := filepath.Clean(filepath.FromSlash(sourcePath))
	if deletePath == "." || !filepath.IsLocal(deletePath) {
		return "", false, nil
	}
	return filepath.ToSlash(deletePath), true, nil
}
