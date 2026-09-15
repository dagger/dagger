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

// removeSDKManagedModuleReference clears the installed module's SDK scope. It
// returns a workspace-relative path only when the matched source is safe to
// delete from the overlay. An SDK scope that cannot be resolved is invalid,
// so this function returns the error instead of treating it as a miss.
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
		if len(sdk.Scopes) == 0 {
			continue
		}

		for modulePath, scope := range sdk.Scopes {
			if !scope.IsModule {
				continue
			}
			managed, err := workspace.ResolveSDKManagedPath(configDir, modulePath)
			if err != nil {
				return "", false, fmt.Errorf("module managed by %q: %w", sdkName, err)
			}
			if managed == sourcePath {
				removed = true
				if len(scope.Clients) == 0 {
					delete(sdk.Scopes, modulePath)
				} else {
					scope.IsModule = false
					scope.Name = ""
					sdk.Scopes[modulePath] = scope
				}
			}
		}
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
