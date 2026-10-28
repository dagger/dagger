package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
)

// workspaceMainNames uses the active install policy, including explicit
// -m modules and legacy workspaces. A value workspace owns its configuration;
// a pending config edit also takes precedence over the session snapshot.
func workspaceMainNames(ctx context.Context, ws *core.Workspace) (map[string]bool, error) {
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
		unrecorded, err := unrecordedClientModules(ctx, ws)
		if err != nil {
			return nil, err
		}
		for _, mod := range served.MainMods() {
			if !unrecorded[mod.Name()] {
				names[mod.Name()] = true
			}
		}
		for _, mod := range ws.ClientModules() {
			names[mod.Name] = mod.Main
		}
		if ws.ConfigFile == "" || !ws.OverlayPathTouched(ws.ConfigFile) {
			return names, nil
		}
	}

	cfg, err := workspaceEffectiveConfig(ctx, ws)
	if err != nil {
		return nil, err
	}
	for name, entry := range cfg.Modules {
		names[name] = entry.Main
	}
	return names, nil
}
