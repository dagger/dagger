package schema

import (
	"context"
	"fmt"
	"path/filepath"

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
	if core.FastModuleSourceKindCheck(sdkRef, "") == core.ModuleSourceKindLocal {
		workspaceRoot, err := s.workspaceOverlayRootfs(ctx, ws)
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

	provider, err := sdkmodule.Load(ctx, root.Self(), sdkRef, workspaceSource, settings)
	if err != nil {
		return nil, fmt.Errorf("load SDK module %q: %w", sdkRef, err)
	}
	return provider, nil
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
		for key, value := range scope.Settings {
			settings[key] = value
		}
	}
	if len(settings) == 0 {
		return nil, nil
	}
	return settings, nil
}
