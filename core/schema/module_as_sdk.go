package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
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
	orderedModules, err := orderSDKManagedModules(ctx, ws, entry.AsSDK.Modules)
	if err != nil {
		return nil, err
	}
	for _, modPath := range orderedModules {
		result.Modules = append(result.Modules, &core.CurrentModuleAsSDKModule{Path: modPath})
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

// orderSDKManagedModules returns the managed module paths ordered leaf-first —
// a module's locally-managed dependencies before it — so SDK generators that
// fold over asSDK.modules in list order regenerate dependencies first. It reads
// each managed module's dagger-module.toml (workspace-root-relative) for its
// dependency edges; a module whose config can't be read or parsed contributes
// no edges (treated as a leaf) rather than failing generation for the whole
// workspace — a genuinely broken config surfaces later at module load.
func orderSDKManagedModules(
	ctx context.Context,
	ws *core.Workspace,
	managed []workspace.SDKManagedModule,
) ([]string, error) {
	nodes := make([]workspace.SDKManagedModuleConfig, 0, len(managed))
	for _, m := range managed {
		nodes = append(nodes, workspace.SDKManagedModuleConfig{
			Path:   m.Path,
			Config: readManagedModuleConfig(ctx, ws, m.Path),
		})
	}
	return workspace.OrderSDKModulesLeafFirst(nodes)
}

func readManagedModuleConfig(ctx context.Context, ws *core.Workspace, modPath string) *modules.ModuleConfig {
	// Boundary guard: never read a config outside the workspace root, so an
	// absolute or root-escaping managed path degrades to a leaf.
	cleaned := path.Clean(filepath.ToSlash(modPath))
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return nil
	}
	data, err := readWorkspaceFileBytes(ctx, ws, path.Join(modPath, modules.Filename))
	if err != nil {
		return nil
	}
	modCfg, err := modules.ParseModuleConfigForFilename(data, modules.Filename)
	if err != nil {
		return nil
	}
	return &modCfg.ModuleConfig
}

func (s *moduleSchema) currentModuleAsSDKClients(
	_ context.Context,
	parent *core.CurrentModuleAsSDK,
	_ struct{},
) ([]*core.CurrentModuleAsSDKClient, error) {
	return parent.Clients, nil
}

// currentModuleAsSDKClientModuleSource resolves the module a client is bound to
// from its stored {module, pin}. Resolution goes through the same
// resolveClientTargetModule the workspace client-generation path uses, so local
// (workspace-relative) refs are expanded to host paths, the pin is applied, and
// the load happens in the workspace client context -- correct for both local
// and remote/git refs, unlike resolving the ref string directly.
func (s *moduleSchema) currentModuleAsSDKClientModuleSource(
	ctx context.Context,
	client *core.CurrentModuleAsSDKClient,
	_ struct{},
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var res dagql.ObjectResult[*core.ModuleSource]

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	ws, err := query.Server.CurrentWorkspace(ctx)
	if err != nil {
		return res, fmt.Errorf("get current workspace: %w", err)
	}
	if isSyntheticWorkspace(ws) || ws.ConfigFile == "" {
		return res, fmt.Errorf("current module is not installed as an SDK in this workspace")
	}

	_, moduleLoadRef, err := resolveWorkspaceClientModuleRef(ws, client.Module)
	if err != nil {
		return res, err
	}

	wsSchema := &workspaceSchema{}
	workspaceCtx := ctx
	if ws.ClientID != "" {
		workspaceCtx, err = wsSchema.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return res, fmt.Errorf("workspace client context: %w", err)
		}
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	return wsSchema.resolveClientTargetModule(workspaceCtx, ws, moduleLoadRef, client.Pin)
}
