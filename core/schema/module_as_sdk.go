package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
)

// currentModuleAsSDK treats the currently executing module as an SDK installed
// in the active workspace and returns its persisted as-sdk role data (the
// modules and clients it authors/manages). This is the engine-owned source of
// truth that SDK generators use to discover their workspace-managed modules,
// rather than scanning the workspace filesystem themselves.
func (s *moduleSchema) currentModuleAsSDK(
	ctx context.Context,
	curMod *core.CurrentModule,
	_ struct{},
) (*core.CurrentModuleAsSDK, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := query.Server.CurrentWorkspace(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current workspace: %w", err)
	}
	if isSyntheticWorkspace(ws) || ws.ConfigFile == "" {
		return nil, fmt.Errorf("current module is not installed as an SDK in this workspace")
	}

	cfg, err := readWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, err
	}

	var curName string
	if mod := curMod.Module.Self(); mod != nil {
		curName = mod.Name()
	}
	name, entry, err := resolveCurrentModuleSDKEntry(curName, cfg)
	if err != nil {
		return nil, err
	}

	sdkName := entry.AsSDK.Name
	if sdkName == "" {
		sdkName = name
	}

	// as-sdk module paths are stored relative to the config file's directory
	// (like module sources), while API consumers expect workspace-root-relative
	// paths; resolve before exposing them. Client paths are still stored and
	// consumed workspace-root-relative (see workspace_client.go), so they pass
	// through unchanged.
	configDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return nil, err
	}

	result := &core.CurrentModuleAsSDK{Name: sdkName}
	for _, mod := range entry.AsSDK.Modules {
		result.Modules = append(result.Modules, &core.CurrentModuleAsSDKModule{
			Path: filepath.Clean(filepath.Join(configDir, mod.Path)),
		})
	}
	for _, client := range entry.AsSDK.Clients {
		result.Clients = append(result.Clients, &core.CurrentModuleAsSDKClient{
			Path:   client.Path,
			Module: client.Module,
			Pin:    client.Pin,
		})
	}
	return result, nil
}

// resolveCurrentModuleSDKEntry matches the current module to a workspace entry
// installed as an SDK. A non-matching module must not inherit a lone SDK install.
func resolveCurrentModuleSDKEntry(
	curName string,
	cfg *workspace.Config,
) (string, workspace.ModuleEntry, error) {
	if cfg == nil {
		return "", workspace.ModuleEntry{}, fmt.Errorf("current module is not installed as an SDK in this workspace")
	}
	if entry, ok := cfg.Modules[curName]; ok && entry.AsSDK != nil {
		return curName, entry, nil
	}
	return "", workspace.ModuleEntry{}, fmt.Errorf("current module is not installed as an SDK in this workspace")
}

func (s *moduleSchema) currentModuleAsSDKModules(
	_ context.Context,
	parent *core.CurrentModuleAsSDK,
	_ struct{},
) ([]*core.CurrentModuleAsSDKModule, error) {
	return parent.Modules, nil
}

func (s *moduleSchema) currentModuleAsSDKClients(
	_ context.Context,
	parent *core.CurrentModuleAsSDK,
	_ struct{},
) ([]*core.CurrentModuleAsSDKClient, error) {
	return parent.Clients, nil
}
