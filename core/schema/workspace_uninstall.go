package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceUninstallArgs struct {
	Name string
	Here bool `default:"false"`
}

func (s *workspaceSchema) uninstall(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceUninstallArgs,
) (dagql.String, error) {
	if parent.CompatWorkspace() != nil {
		return "", fmt.Errorf("workspace is using legacy dagger.json config; run dagger setup first")
	}
	if args.Name == "" {
		return "", fmt.Errorf("module name is required")
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigMustExist, args.Here)
	if err != nil {
		return "", err
	}

	entry, ok := cfg.Modules[args.Name]
	if !ok {
		return "", fmt.Errorf("module %q is not installed in the workspace", args.Name)
	}

	configDir, err := workspaceConfigDirectory(parent)
	if err != nil {
		return "", err
	}
	managedModulePath, removeManagedModuleDir := removeSDKManagedModuleReference(cfg, configDir, entry)

	delete(cfg.Modules, args.Name)
	if err := writeWorkspaceConfig(ctx, parent, cfg); err != nil {
		return "", err
	}
	if removeManagedModuleDir {
		if err := s.removeWorkspaceDirectoryFromHost(ctx, parent, managedModulePath); err != nil {
			return "", err
		}
	}

	cfgPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}

	return dagql.String(fmt.Sprintf("Uninstalled module %q from %s", args.Name, cfgPath)), nil
}

// removeSDKManagedModuleReference removes the installed module's source path
// from any [[modules.<sdk>.as-sdk.modules]] list. It returns a host-removal
// path only when the matched source is workspace-relative and safe to delete.
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
			// as-sdk module paths are config-dir-relative, like module entry
			// sources; resolve both sides before comparing.
			if filepath.ToSlash(filepath.Clean(filepath.Join(configDir, mod.Path))) == sourcePath {
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

// removeWorkspaceDirectoryFromHost applies the same Directory.withoutDirectory
// diff/export path used by other workspace filesystem mutations.
func (s *workspaceSchema) removeWorkspaceDirectoryFromHost(ctx context.Context, parent *core.Workspace, dir string) error {
	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return fmt.Errorf("resolve workspace rootfs: %w", err)
	}
	updatedDir, err := workspaceMigrationSelectDirectory(ctx, baseDir, "withoutDirectory", []dagql.NamedInput{
		{Name: "path", Value: dagql.String(path.Clean(filepath.ToSlash(dir)))},
	})
	if err != nil {
		return fmt.Errorf("stage workspace directory removal %q: %w", dir, err)
	}

	changes, err := workspaceMigrationChanges(ctx, updatedDir, baseDir)
	if err != nil {
		return err
	}
	if changes.Self() == nil {
		return nil
	}
	return changes.Self().Export(ctx, parent.HostPath())
}
