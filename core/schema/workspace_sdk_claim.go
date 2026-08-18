package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceClaimedModuleArgs struct {
	SDK  string
	Path string
}

type workspaceClaimedClientArgs struct {
	SDK    string
	Path   string
	Module string
}

func (s *workspaceSchema) withClaimedModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimedModuleArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.SDK == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK name is required")
	}
	if args.Path == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module path is required")
	}

	ws := parent.Self()
	modulePath, err := resolveWorkspacePath(args.Path, ws.Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module path %q must not escape the workspace root", args.Path)
	}
	modulePath = filepath.ToSlash(modulePath)

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(staged.Config, args.SDK)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if owner, claimed := claimedModuleOwner(staged.Config, modulePath); claimed {
		if owner == sdkName {
			return parent, nil
		}
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module at %q is already claimed by SDK %q", modulePath, owner)
	}

	root, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	_, found, err := moduleConfigInDir(ctx, &core.DirectoryStatFS{Dir: root}, modulePath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect module at %q: %w", modulePath, err)
	}
	if !found {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("path %q does not point to an initialized module", modulePath)
	}

	loadedSDK, err := s.loadWorkspaceSDK(ctx, ws, staged.ConfigDir, sdkRef)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if _, ok := loadedSDK.AsModuleInitializer(); !ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("%q does not support module init", args.SDK)
	}

	sdkEntry.AsSDK.Modules = append(sdkEntry.AsSDK.Modules, workspace.SDKManagedModule{Path: modulePath})
	staged.Config.Modules[sdkName] = sdkEntry
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withoutClaimedModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimedModuleArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.SDK == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK name is required")
	}
	if args.Path == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module path is required")
	}

	ws := parent.Self()
	modulePath, err := resolveWorkspacePath(args.Path, ws.Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module path %q must not escape the workspace root", args.Path)
	}
	modulePath = filepath.ToSlash(modulePath)

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sdkName, sdkEntry, _, err := installedSDKSource(staged.Config, args.SDK)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	before := len(sdkEntry.AsSDK.Modules)
	sdkEntry.AsSDK.Modules = slices.DeleteFunc(sdkEntry.AsSDK.Modules, func(module workspace.SDKManagedModule) bool {
		return filepath.Clean(module.Path) == filepath.Clean(modulePath)
	})
	if len(sdkEntry.AsSDK.Modules) == before {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module at %q is not claimed by SDK %q", modulePath, args.SDK)
	}

	staged.Config.Modules[sdkName] = sdkEntry
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withClaimedClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimedClientArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.SDK == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK name is required")
	}
	if args.Path == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client path is required")
	}
	if args.Module == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module ref is required")
	}

	ws := parent.Self()
	clientPath, err := resolveWorkspaceClientPath(args.Path, ws.Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	clientPath = filepath.ToSlash(clientPath)
	moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(ws, args.Module)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	exists, err := s.workspaceReadPathExists(ctx, ws, clientPath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect client path %q: %w", clientPath, err)
	}
	if !exists {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client path %q does not exist", clientPath)
	}
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(staged.Config, args.SDK)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if owner, claimed := claimedClientOwner(staged.Config, clientPath); claimed {
		if owner == sdkName {
			return parent, nil
		}
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client at %q is already claimed by SDK %q", clientPath, owner)
	}

	loadedSDK, err := s.loadWorkspaceSDK(ctx, ws, staged.ConfigDir, sdkRef)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if _, ok := loadedSDK.AsClientInitializer(); !ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("%q does not support client init", args.SDK)
	}

	workspaceCtx := ctx
	if ws.ClientID != "" {
		workspaceCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace client context: %w", err)
		}
	}
	if clientMetadata, metadataErr := engine.ClientMetadataFromContext(ctx); metadataErr == nil && clientMetadata.LockMode != "" {
		workspaceCtx = workspaceInstallContextWithLockMode(workspaceCtx, workspace.LockMode(clientMetadata.LockMode))
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)
	targetModule, err := s.resolveClientTargetModule(workspaceCtx, ws, moduleLoadRef, "")
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	sdkEntry.AsSDK.Clients = append(sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:   clientPath,
		Module: moduleRef,
		Pin:    targetModule.Self().Pin(),
	})
	staged.Config.Modules[sdkName] = sdkEntry
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withoutClaimedClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimedModuleArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.SDK == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK name is required")
	}
	if args.Path == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client path is required")
	}

	ws := parent.Self()
	clientPath, err := resolveWorkspaceClientPath(args.Path, ws.Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	clientPath = filepath.ToSlash(clientPath)

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sdkName, sdkEntry, _, err := installedSDKSource(staged.Config, args.SDK)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	before := len(sdkEntry.AsSDK.Clients)
	sdkEntry.AsSDK.Clients = slices.DeleteFunc(sdkEntry.AsSDK.Clients, func(client workspace.SDKManagedClient) bool {
		return filepath.Clean(client.Path) == filepath.Clean(clientPath)
	})
	if len(sdkEntry.AsSDK.Clients) == before {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client at %q is not claimed by SDK %q", clientPath, args.SDK)
	}

	staged.Config.Modules[sdkName] = sdkEntry
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func claimedModuleOwner(cfg *workspace.Config, modulePath string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	cleanPath := filepath.Clean(modulePath)
	for sdkName, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		for _, module := range entry.AsSDK.Modules {
			if filepath.Clean(module.Path) == cleanPath {
				return sdkName, true
			}
		}
	}
	return "", false
}

func claimedClientOwner(cfg *workspace.Config, clientPath string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	cleanPath := filepath.Clean(clientPath)
	for sdkName, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		for _, client := range entry.AsSDK.Clients {
			if filepath.Clean(client.Path) == cleanPath {
				return sdkName, true
			}
		}
	}
	return "", false
}
