package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceClaimArgs struct {
	SDK  string
	Path string
}

type workspaceClaimedClientArgs struct {
	SDK    string
	Path   string
	Module string
}

type workspaceClaimKind string

const (
	workspaceModuleClaim workspaceClaimKind = "module"
	workspaceClientClaim workspaceClaimKind = "client"
)

type preparedWorkspaceClaim struct {
	ws         *core.Workspace
	path       string
	configPath string
	staged     *stagedWorkspaceConfig
	sdkName    string
	sdkEntry   workspace.ModuleEntry
}

func (s *workspaceSchema) withClaimedModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	claim, err := s.prepareWorkspaceClaim(ctx, parent, args, workspaceModuleClaim)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	owner, claimed, err := claimedOwner(claim.staged.Config, claim.staged.ConfigDir, workspaceModuleClaim, claim.path)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if claimed {
		if owner == claim.sdkName {
			return parent, nil
		}
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module at %q is already claimed by SDK %q", claim.path, owner)
	}

	root, err := s.workspaceOverlayRootfs(ctx, claim.ws)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	_, found, err := moduleConfigInDir(ctx, &core.DirectoryStatFS{Dir: root}, claim.path)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect module at %q: %w", claim.path, err)
	}
	if !found {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("path %q does not point to an initialized module", claim.path)
	}

	claim.sdkEntry.AsSDK.Modules = append(claim.sdkEntry.AsSDK.Modules, workspace.SDKManagedModule{Path: claim.configPath})
	return s.stageWorkspaceClaim(ctx, parent, claim)
}

func (s *workspaceSchema) withoutClaimedModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.withoutClaim(ctx, parent, args, workspaceModuleClaim)
}

func (s *workspaceSchema) withClaimedClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimedClientArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Module == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module ref is required")
	}
	claim, err := s.prepareWorkspaceClaim(ctx, parent, workspaceClaimArgs{SDK: args.SDK, Path: args.Path}, workspaceClientClaim)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	moduleLoadRef, configModuleRef, err := resolveWorkspaceClientModuleRef(claim.ws, args.Module, claim.staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	exists, err := s.workspaceReadPathExists(ctx, claim.ws, claim.path)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect client path %q: %w", claim.path, err)
	}
	if !exists {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client path %q does not exist", claim.path)
	}
	owner, claimed, err := claimedOwner(claim.staged.Config, claim.staged.ConfigDir, workspaceClientClaim, claim.path)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if claimed && owner != claim.sdkName {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client at %q is already claimed by SDK %q", claim.path, owner)
	}

	workspaceCtx := ctx
	if claim.ws.ClientID != "" {
		workspaceCtx, err = s.withWorkspaceClientContext(ctx, claim.ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace client context: %w", err)
		}
	}
	if clientMetadata, metadataErr := engine.ClientMetadataFromContext(ctx); metadataErr == nil && clientMetadata.LockMode != "" {
		workspaceCtx = workspaceInstallContextWithLockMode(workspaceCtx, workspace.LockMode(clientMetadata.LockMode))
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)
	targetModule, err := s.resolveClientTargetModule(workspaceCtx, claim.ws, moduleLoadRef, "")
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	if err := removeClientEntryAtPath(claim.staged.Config, claim.staged.ConfigDir, claim.path); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	claim.sdkEntry = claim.staged.Config.Modules[claim.sdkName]
	claim.sdkEntry.AsSDK.Clients = append(claim.sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:   claim.configPath,
		Module: configModuleRef,
		Pin:    targetModule.Self().Pin(),
	})
	return s.stageWorkspaceClaim(ctx, parent, claim)
}

func (s *workspaceSchema) withoutClaimedClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.withoutClaim(ctx, parent, args, workspaceClientClaim)
}

func (s *workspaceSchema) withoutClaim(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimArgs,
	kind workspaceClaimKind,
) (dagql.ObjectResult[*core.Workspace], error) {
	claim, err := s.prepareWorkspaceClaim(ctx, parent, args, kind)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	removed := false
	switch kind {
	case workspaceModuleClaim:
		kept := make([]workspace.SDKManagedModule, 0, len(claim.sdkEntry.AsSDK.Modules))
		for _, module := range claim.sdkEntry.AsSDK.Modules {
			resolved, err := workspace.ResolveSDKManagedPath(claim.staged.ConfigDir, module.Path)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module managed by %q: %w", claim.sdkName, err)
			}
			if resolved == claim.path {
				removed = true
				continue
			}
			kept = append(kept, module)
		}
		claim.sdkEntry.AsSDK.Modules = kept
	case workspaceClientClaim:
		kept := make([]workspace.SDKManagedClient, 0, len(claim.sdkEntry.AsSDK.Clients))
		for _, client := range claim.sdkEntry.AsSDK.Clients {
			resolved, err := workspace.ResolveSDKManagedPath(claim.staged.ConfigDir, client.Path)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client managed by %q: %w", claim.sdkName, err)
			}
			if resolved == claim.path {
				removed = true
				continue
			}
			kept = append(kept, client)
		}
		claim.sdkEntry.AsSDK.Clients = kept
	}
	if !removed {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("%s at %q is not claimed by SDK %q", kind, claim.path, args.SDK)
	}
	return s.stageWorkspaceClaim(ctx, parent, claim)
}

func (s *workspaceSchema) prepareWorkspaceClaim(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceClaimArgs,
	kind workspaceClaimKind,
) (preparedWorkspaceClaim, error) {
	if args.SDK == "" {
		return preparedWorkspaceClaim{}, fmt.Errorf("SDK name is required")
	}
	if args.Path == "" {
		return preparedWorkspaceClaim{}, fmt.Errorf("%s path is required", kind)
	}

	ws := parent.Self()
	var claimPath string
	var err error
	switch kind {
	case workspaceModuleClaim:
		claimPath, err = resolveWorkspacePath(args.Path, ws.Cwd)
		if err != nil {
			return preparedWorkspaceClaim{}, fmt.Errorf("module path %q must not escape the workspace root", args.Path)
		}
	case workspaceClientClaim:
		claimPath, err = resolveWorkspaceClientPath(args.Path, ws.Cwd)
		if err != nil {
			return preparedWorkspaceClaim{}, err
		}
	}
	claimPath = filepath.ToSlash(claimPath)

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return preparedWorkspaceClaim{}, err
	}
	configPath, err := workspace.SDKManagedPathFor(staged.ConfigDir, claimPath)
	if err != nil {
		return preparedWorkspaceClaim{}, err
	}
	sdkName, sdkEntry, _, err := installedSDKSource(staged.Config, args.SDK)
	if err != nil {
		return preparedWorkspaceClaim{}, err
	}
	return preparedWorkspaceClaim{
		ws:         ws,
		path:       claimPath,
		configPath: configPath,
		staged:     staged,
		sdkName:    sdkName,
		sdkEntry:   sdkEntry,
	}, nil
}

func (s *workspaceSchema) stageWorkspaceClaim(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	claim preparedWorkspaceClaim,
) (dagql.ObjectResult[*core.Workspace], error) {
	claim.staged.Config.Modules[claim.sdkName] = claim.sdkEntry
	updated, err := workspace.UpdateConfigBytes(claim.staged.Data, claim.staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, claim.staged, updated)
}

func claimedOwner(cfg *workspace.Config, configDir string, kind workspaceClaimKind, claimPath string) (string, bool, error) {
	if cfg == nil {
		return "", false, nil
	}
	cleanPath := filepath.ToSlash(filepath.Clean(claimPath))
	for sdkName, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		switch kind {
		case workspaceModuleClaim:
			for _, module := range entry.AsSDK.Modules {
				resolved, err := workspace.ResolveSDKManagedPath(configDir, module.Path)
				if err != nil {
					return "", false, fmt.Errorf("module managed by %q: %w", sdkName, err)
				}
				if resolved == cleanPath {
					return sdkName, true, nil
				}
			}
		case workspaceClientClaim:
			for _, client := range entry.AsSDK.Clients {
				resolved, err := workspace.ResolveSDKManagedPath(configDir, client.Path)
				if err != nil {
					return "", false, fmt.Errorf("client managed by %q: %w", sdkName, err)
				}
				if resolved == cleanPath {
					return sdkName, true, nil
				}
			}
		}
	}
	return "", false, nil
}
