package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
)

// workspaceEntrypointNames uses the active install policy, including explicit
// -m modules and legacy workspaces. A value workspace owns its configuration;
// a pending config edit also takes precedence over the session snapshot.
func workspaceEntrypointNames(ctx context.Context, ws *core.Workspace) (map[string]bool, error) {
	names := map[string]bool{}
	if !ws.IsValueWorkspace() {
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		served, err := query.Server.CurrentServedDeps(ctx)
		if err != nil {
			return nil, fmt.Errorf("current served deps: %w", err)
		}
		for _, mod := range served.EntrypointMods() {
			names[mod.Name()] = true
		}
		if ws.ConfigFile == "" || !ws.OverlayPathTouched(ws.ConfigFile) {
			return names, nil
		}
	}

	cfg, err := workspaceConfigWithCompatFallback(ctx, ws)
	if err != nil {
		return nil, err
	}
	cfg, err = workspace.ApplyUserOverlay(cfg, ws.UserConfigOverlay())
	if err != nil {
		return nil, err
	}
	if envName, ok := selectedWorkspaceEnv(ctx, ws); ok {
		cfg, err = workspace.ApplyEnvOverlay(cfg, envName)
		if err != nil {
			return nil, err
		}
	}
	for name, entry := range cfg.Modules {
		names[name] = entry.Entrypoint
	}
	return names, nil
}
