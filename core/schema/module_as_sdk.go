package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// currentModuleAsSDK treats the currently executing module as an SDK installed
// in the supplied workspace and returns its persisted as-sdk role data (the
// modules and clients it authors/manages). This is the engine-owned source of
// truth that SDK generators use to discover their workspace-managed modules,
// rather than scanning the workspace filesystem themselves.
func (s *moduleSchema) currentModuleAsSDK(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct {
		Workspace dagql.ID[*core.Workspace]
	},
) (*core.CurrentModuleAsSDK, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	// The workspace is passed explicitly: a module resolving its SDK role — as a
	// dependency driven by another module, or as a generator run by the framework
	// — hands in the workspace it was given rather than inheriting the caller's
	// ambient one. Config reads route through that workspace's own rootfs/owner
	// client, so there is no sandbox concern, and a synthetic (value) workspace
	// that carries config is honored just like a detected one.
	wsResult, err := args.Workspace.Load(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("load workspace argument: %w", err)
	}
	ws := wsResult.Self()
	if ws.ConfigFile == "" {
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

	// as-sdk entries are recorded against the config directory; SDKs are
	// handed workspace-root-relative paths, the same currency as ws.Cwd and
	// the changesets they return.
	configDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return nil, err
	}

	result := &core.CurrentModuleAsSDK{Name: sdkName}
	for _, mod := range entry.AsSDK.Modules {
		modPath, err := workspace.ResolveSDKManagedPath(configDir, mod.Path)
		if err != nil {
			return nil, fmt.Errorf("module managed by %q: %w", name, err)
		}
		result.Modules = append(result.Modules, &core.CurrentModuleAsSDKModule{Path: modPath})
	}
	result.Modules = currentModuleAsSDKModulesForCwd(result.Modules, ws.Cwd)
	for _, client := range entry.AsSDK.Clients {
		clientPath, err := workspace.ResolveSDKManagedPath(configDir, client.Path)
		if err != nil {
			return nil, fmt.Errorf("client managed by %q: %w", name, err)
		}
		clientModule, err := resolveSDKManagedClientModule(configDir, client.Module)
		if err != nil {
			return nil, fmt.Errorf("client managed by %q: %w", name, err)
		}
		result.Clients = append(result.Clients, &core.CurrentModuleAsSDKClient{
			Path:           clientPath,
			Module:         clientModule,
			Pin:            client.Pin,
			BoundWorkspace: wsResult,
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

// currentModuleAsSDKModulesForCwd applies the same cwd selection policy
// SDKs previously reconstructed with polyfill.findConfigDirs: all registered
// modules at or below cwd, and, when cwd itself is not registered, the nearest
// enclosing registered module. Results are the nearest ancestor first followed
// by descendants in workspace-config order.
func currentModuleAsSDKModulesForCwd(
	modules []*core.CurrentModuleAsSDKModule,
	cwd string,
) []*core.CurrentModuleAsSDKModule {
	cwd = cleanWorkspaceRelPath(cwd)

	var descendants []*core.CurrentModuleAsSDKModule
	var nearestAncestor *core.CurrentModuleAsSDKModule
	hasExact := false
	seen := map[string]bool{}

	for _, mod := range modules {
		if mod == nil {
			continue
		}
		modPath := cleanWorkspaceRelPath(mod.Path)
		if seen[modPath] {
			continue
		}
		seen[modPath] = true

		if cwd == "." || modPath == cwd || strings.HasPrefix(modPath, cwd+string(filepath.Separator)) {
			descendants = append(descendants, mod)
			if modPath == cwd {
				hasExact = true
			}
			continue
		}

		if modPath == "." || strings.HasPrefix(cwd, modPath+string(filepath.Separator)) {
			if nearestAncestor == nil || len(modPath) > len(cleanWorkspaceRelPath(nearestAncestor.Path)) {
				nearestAncestor = mod
			}
		}
	}

	if hasExact || nearestAncestor == nil {
		return descendants
	}
	return append([]*core.CurrentModuleAsSDKModule{nearestAncestor}, descendants...)
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

	// Resolve against the workspace asSDK was called on, carried on the client,
	// rather than the session's ambient workspace: a dependency-driven or overlaid
	// SDK is handed its workspace explicitly and no longer inherits an ambient one.
	ws := client.BoundWorkspace.Self()
	if ws == nil {
		return res, fmt.Errorf("current module as-sdk client has no bound workspace")
	}
	if ws.ConfigFile == "" {
		return res, fmt.Errorf("current module is not installed as an SDK in this workspace")
	}

	// client.Module was already resolved to workspace-root coordinates by
	// currentModuleAsSDK, so it needs no further anchoring here.
	moduleLoadRef, _, err := resolveWorkspaceClientModuleRef(ws, client.Module, ".")
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

	return wsSchema.resolveClientTargetModule(workspaceCtx, ws, moduleLoadRef, client.Pin)
}
