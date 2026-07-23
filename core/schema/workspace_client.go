package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceInitClientArgs struct {
	Path   string
	SDK    string
	Module string
	Args   core.JSON `default:""`
	Here   bool      `default:"false"`
}

func (s *workspaceSchema) initClientChanges(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitClientArgs,
) (res dagql.ObjectResult[*core.Changeset], _ error) {
	ws := parent.Self()
	lockMode := ""
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
		lockMode = clientMetadata.LockMode
	}
	if args.Path == "" {
		return res, fmt.Errorf("client path is required")
	}
	if args.SDK == "" {
		return res, fmt.Errorf("SDK name is required")
	}
	if args.Module == "" {
		return res, fmt.Errorf("module ref is required")
	}

	clientPath, err := cleanWorkspaceClientPath(args.Path)
	if err != nil {
		return res, err
	}
	moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(ws, args.Module)
	if err != nil {
		return res, err
	}

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, args.Here)
	if err != nil {
		return res, err
	}
	cfg := staged.Config
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(cfg, args.SDK)
	if err != nil {
		return res, err
	}

	workspaceCtx := ctx
	if ws.ClientID != "" {
		workspaceCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return res, fmt.Errorf("workspace client context: %w", err)
		}
	}
	if lockMode != "" {
		workspaceCtx = workspaceInstallContextWithLockMode(workspaceCtx, workspace.LockMode(lockMode))
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	targetModule, err := s.resolveClientTargetModule(workspaceCtx, ws, moduleLoadRef, "")
	if err != nil {
		return res, err
	}
	modulePin := targetModule.Self().Pin()

	removeClientEntryAtPath(cfg, clientPath)
	sdkEntry = cfg.Modules[sdkName]
	sdkEntry.AsSDK.Clients = append(sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:   clientPath,
		Module: moduleRef,
		Pin:    modulePin,
	})
	cfg.Modules[sdkName] = sdkEntry

	newConfigBytes, err := workspace.UpdateConfigBytes(staged.Data, cfg)
	if err != nil {
		return res, fmt.Errorf("update workspace config: %w", err)
	}

	configRelPath := staged.ConfigFile
	baseDir, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return res, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes)
	if err != nil {
		return res, fmt.Errorf("stage workspace config update: %w", err)
	}

	engineChanges, err := workspaceMigrationChanges(ctx, updatedDir, baseDir)
	if err != nil {
		return res, err
	}

	sdkArgs, err := coresdk.DecodeInitArgs(args.Args)
	if err != nil {
		return res, err
	}
	loadedSDK, err := s.loadWorkspaceSDK(ctx, ws, staged.ConfigDir, sdkRef)
	if err != nil {
		return res, err
	}
	clientInitializer, ok := loadedSDK.AsClientInitializer()
	if !ok {
		return res, fmt.Errorf("%q does not support client init", args.SDK)
	}
	sdkChanges, err := clientInitializer.InitClient(ctx, parent, clientPath, moduleRef, sdkArgs)
	if err != nil {
		return res, fmt.Errorf("sdk client init: %w", err)
	}

	return mergeWorkspaceInitChangeset(ctx, engineChanges, sdkChanges)
}

func (s *workspaceSchema) resolveClientTargetModule(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	pin string,
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, fmt.Errorf("dagql server: %w", err)
	}
	if workspace.IsLocalRef(ref, "") {
		root, err := s.workspaceOverlayRootfs(ctx, ws)
		if err != nil {
			return src, err
		}
		if err := srv.Select(ctx, root, &src, dagql.Selector{
			Field: "asModuleSource",
			Args: []dagql.NamedInput{
				{Name: "sourceRootPath", Value: dagql.String(filepath.ToSlash(ref))},
			},
		}); err != nil {
			return src, fmt.Errorf("load module source: %w", err)
		}
	} else if err := srv.Select(ctx, srv.Root(), &src, workspaceClientModuleSourceSelector(ref, pin)); err != nil {
		return src, fmt.Errorf("load module source: %w", err)
	}
	if src.Self() == nil {
		return src, fmt.Errorf("load module source: empty result")
	}
	if !src.Self().ConfigExists {
		return src, fmt.Errorf("ref %q does not point to an initialized module", ref)
	}
	return src, nil
}

func cleanWorkspaceClientPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return "", fmt.Errorf("client path must point to a directory below the workspace root")
	}
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("client path %q must be workspace-relative, not absolute", path)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("client path %q must not escape the workspace root", path)
	}
	return cleaned, nil
}

func resolveWorkspaceClientModuleRef(ws *core.Workspace, ref string) (configRef string, loadRef string, _ error) {
	if !workspace.IsLocalRef(ref, "") {
		return ref, ref, nil
	}
	cleaned := filepath.Clean(ref)
	if filepath.IsAbs(cleaned) {
		hostRoot, ok := ws.LocalSourceHostPath()
		if !ok {
			return "", "", fmt.Errorf("absolute module ref %q requires a local workspace source", ref)
		}
		rel, err := filepath.Rel(hostRoot, cleaned)
		if err != nil {
			return "", "", fmt.Errorf("compute workspace-relative module path: %w", err)
		}
		cleaned = rel
	}
	if cleaned == "." || cleaned == "" {
		cleaned = "."
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("module ref %q must not escape the workspace root", ref)
	}
	return filepath.ToSlash(cleaned), filepath.ToSlash(cleaned), nil
}

func workspaceClientModuleSourceSelector(ref string, pin string) dagql.Selector {
	args := []dagql.NamedInput{
		{Name: "refString", Value: dagql.String(ref)},
		{Name: "disableFindUp", Value: dagql.Boolean(true)},
	}
	if pin != "" {
		args = append(args, dagql.NamedInput{Name: "refPin", Value: dagql.String(pin)})
	}
	return dagql.Selector{
		Field: "moduleSource",
		Args:  args,
	}
}

func removeClientEntryAtPath(cfg *workspace.Config, clientPath string) {
	if cfg == nil {
		return
	}
	cleanPath := filepath.Clean(clientPath)
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		entry.AsSDK.Clients = slices.DeleteFunc(entry.AsSDK.Clients, func(client workspace.SDKManagedClient) bool {
			return filepath.Clean(client.Path) == cleanPath
		})
		cfg.Modules[moduleName] = entry
	}
}
