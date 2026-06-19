package schema

import (
	"context"
	"fmt"

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

	result := &core.CurrentModuleAsSDK{Name: sdkName}
	for _, mod := range entry.AsSDK.Modules {
		result.Modules = append(result.Modules, &core.CurrentModuleAsSDKModule{Path: mod.Path})
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

// resolveCurrentModuleSDKEntry matches the currently executing module to its
// workspace install entry's as-sdk role data.
//
// The workspace install identity is not threaded through generator execution,
// so we match by install name (the same name<->module convention the workspace
// uses for per-module skip patterns). When the running module's name does not
// match an install entry, we fall back to the sole installed SDK entry — which
// the design permits only when exactly one installed SDK could match. Multiple
// candidate SDK installs without a name match are ambiguous and error rather
// than guess.
func resolveCurrentModuleSDKEntry(
	curName string,
	cfg *workspace.Config,
) (string, workspace.ModuleEntry, error) {
	type sdkInstall struct {
		name  string
		entry workspace.ModuleEntry
	}
	var sdkInstalls []sdkInstall
	for name, entry := range cfg.Modules {
		if entry.AsSDK != nil {
			sdkInstalls = append(sdkInstalls, sdkInstall{name: name, entry: entry})
		}
	}
	if len(sdkInstalls) == 0 {
		return "", workspace.ModuleEntry{}, fmt.Errorf("current module is not installed as an SDK in this workspace")
	}

	for _, install := range sdkInstalls {
		if install.name == curName {
			return install.name, install.entry, nil
		}
	}

	if len(sdkInstalls) == 1 {
		return sdkInstalls[0].name, sdkInstalls[0].entry, nil
	}
	return "", workspace.ModuleEntry{}, fmt.Errorf(
		"multiple installed SDK entries could match the current module %q; cannot determine its SDK identity", curName)
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
