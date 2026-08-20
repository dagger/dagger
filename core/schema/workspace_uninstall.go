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
// from any sdks.<name>.claimed.modules list. It returns a workspace-relative
// path only when the matched source is safe to delete from the overlay. An
// SDK claim that cannot be resolved is a corruption every other reader fails
// on, so it fails here too rather than being mistaken for a miss.
func removeSDKManagedModuleReference(cfg *workspace.Config, configDir, moduleName string, entry workspace.ModuleEntry) (string, bool, error) {
	if cfg == nil || !workspace.IsLocalRef(entry.Source, entry.Pin) {
		return "", false, nil
	}
	if _, isSDK := workspace.SDKNameForModule(cfg, moduleName); isSDK {
		return "", false, nil
	}

	resolvedSource := workspace.ResolveModuleEntrySource(configDir, entry.Source)
	sourcePath := filepath.ToSlash(resolvedSource)
	removed := false
	for sdkName, sdk := range cfg.SDKs {
		if len(sdk.Claimed.Modules) == 0 {
			continue
		}

		kept := make([]string, 0, len(sdk.Claimed.Modules))
		for _, modulePath := range sdk.Claimed.Modules {
			managed, err := workspace.ResolveSDKManagedPath(configDir, modulePath)
			if err != nil {
				return "", false, fmt.Errorf("module managed by %q: %w", sdkName, err)
			}
			if managed == sourcePath {
				removed = true
				continue
			}
			kept = append(kept, modulePath)
		}
		sdk.Claimed.Modules = kept
		cfg.SDKs[sdkName] = sdk
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
