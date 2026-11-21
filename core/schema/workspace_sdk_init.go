package schema

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/sdkmodule"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) loadWorkspaceSDKModule(
	ctx context.Context,
	ws *core.Workspace,
	configDir string,
	sdkRef string,
	settings map[string]any,
) (*sdkmodule.Provider, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}
	root, ok := srv.Root().(dagql.ObjectResult[*core.Query])
	if !ok {
		return nil, fmt.Errorf("dagql root: unexpected type %T", srv.Root())
	}

	var workspaceSource *core.ModuleSource
	if workspace.IsLocalRef(sdkRef, "") {
		workspaceRoot, err := s.filteredWorkspaceRootfs(ctx, ws)
		if err != nil {
			return nil, fmt.Errorf("load workspace SDK-module root: %w", err)
		}
		configDir = filepath.ToSlash(cleanWorkspaceRelPath(configDir))
		workspaceSource = &core.ModuleSource{
			ModuleName:        "workspace",
			SourceRootSubpath: configDir,
			ContextDirectory:  workspaceRoot,
			Kind:              core.ModuleSourceKindDir,
			DirSrc: &core.DirModuleSource{
				OriginalContextDir:        workspaceRoot,
				OriginalSourceRootSubpath: configDir,
			},
		}
	}

	provider, err := sdkmodule.Load(ctx, root.Self(), sdkRef, workspaceSource, settings, workspaceSettingsUntrusted(ws))
	if err != nil {
		return nil, fmt.Errorf("load SDK module %q: %w", sdkRef, err)
	}
	return provider, nil
}

// workspaceSettingsUntrusted reports whether settings read from ws can come
// from somewhere other than the configuration that the session loaded for its
// client: a workspace value, or a workspace with staged edits.
func workspaceSettingsUntrusted(ws *core.Workspace) bool {
	if ws.IsValueWorkspace() {
		return true
	}
	_, staged := ws.OverlayChanges()
	return staged
}

func effectiveSDKModuleSettings(
	ctx context.Context,
	ws *core.Workspace,
	cfg *workspace.Config,
	sdkName string,
	scopePath string,
) (map[string]any, error) {
	if cfg == nil {
		return nil, fmt.Errorf("workspace config is required")
	}
	sdkEntry, ok := cfg.SDKs[sdkName]
	if !ok {
		return nil, fmt.Errorf("SDK %q is not installed", sdkName)
	}

	effective, err := workspace.ApplyUserOverlay(cfg, ws.UserConfigOverlay())
	if err != nil {
		return nil, err
	}
	if envName, ok := selectedWorkspaceEnv(ctx, ws); ok {
		effective, err = workspace.ApplyEnvOverlay(effective, envName)
		if err != nil {
			return nil, err
		}
	}

	settings := map[string]any{}
	for key, value := range effective.Modules[sdkEntry.Module].Settings {
		settings[key] = value
	}
	if scope, ok := cfg.SDKs[sdkName].Scopes[scopePath]; ok {
		// Constructor defaults match settings without regard to case, so a
		// scope may not record two keys that differ only in case, and a
		// provider key that differs from a scope key only in case would
		// compete with it.
		keys := slices.Sorted(maps.Keys(scope.Settings))
		for i, key := range keys {
			for _, other := range keys[i+1:] {
				if strings.EqualFold(key, other) {
					return nil, fmt.Errorf("SDK %q scope %q records settings %q, which all match %q; keep one in dagger.toml", sdkName, scopePath, []string{key, other}, key)
				}
			}
		}
		for provided := range settings {
			for key := range scope.Settings {
				if strings.EqualFold(provided, key) {
					delete(settings, provided)
				}
			}
		}
		for key, value := range scope.Settings {
			settings[key] = value
		}
	}
	if len(settings) == 0 {
		return nil, nil
	}
	return settings, nil
}
