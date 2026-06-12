package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceClientInitArgs struct {
	Path   string
	SDK    string
	Module string
	Args   core.JSON `default:""`
	Here   bool      `default:"false"`
}

func (s *workspaceSchema) clientInit(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceClientInitArgs,
) (*core.Changeset, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("client path is required")
	}
	if args.SDK == "" {
		return nil, fmt.Errorf("SDK name is required")
	}
	if args.Module == "" {
		return nil, fmt.Errorf("module ref is required")
	}

	clientPath, err := cleanWorkspaceClientPath(args.Path)
	if err != nil {
		return nil, err
	}
	moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(parent, args.Module)
	if err != nil {
		return nil, err
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigMustExist, args.Here)
	if err != nil {
		return nil, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	sdkEntry, sdkRef, err := installedSDKSource(cfg, args.SDK)
	if err != nil {
		return nil, err
	}

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	targetModule, err := s.resolveClientTargetModule(workspaceCtx, moduleLoadRef, "")
	if err != nil {
		return nil, err
	}
	modulePin := targetModule.Self().Pin()

	removeClientEntryAtPath(cfg, clientPath)
	sdkEntry = cfg.Modules[args.SDK]
	sdkEntry.AsSDK.Clients = append(sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:   clientPath,
		Module: moduleRef,
		Pin:    modulePin,
	})
	cfg.Modules[args.SDK] = sdkEntry

	existingConfigBytes, err := readConfigBytes(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("read workspace config: %w", err)
	}
	newConfigBytes, err := workspace.UpdateConfigBytes(existingConfigBytes, cfg)
	if err != nil {
		return nil, fmt.Errorf("update workspace config: %w", err)
	}

	configRelPath, err := workspaceConfigFile(parent)
	if err != nil {
		return nil, err
	}

	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes, 0o644)
	if err != nil {
		return nil, fmt.Errorf("stage workspace config update: %w", err)
	}

	engineChanges, err := workspaceMigrationChanges(ctx, updatedDir, baseDir)
	if err != nil {
		return nil, err
	}

	sdkArgs, err := coresdk.DecodeInitArgs(args.Args)
	if err != nil {
		return nil, err
	}
	loadedSDK, err := s.loadWorkspaceSDK(ctx, sdkRef)
	if err != nil {
		return nil, err
	}
	clientInitializer, ok := loadedSDK.AsClientInitializer()
	if !ok {
		return nil, fmt.Errorf("%q does not support client init", args.SDK)
	}
	workspaceObj, err := s.currentWorkspaceObject(ctx)
	if err != nil {
		return nil, err
	}
	sdkChanges, err := clientInitializer.InitClient(ctx, workspaceObj, clientPath, moduleRef, sdkArgs)
	if err != nil {
		return nil, fmt.Errorf("sdk client init: %w", err)
	}

	return mergeWorkspaceInitChangeset(ctx, engineChanges, sdkChanges)
}

func (s *workspaceSchema) clientGenerate(
	ctx context.Context,
	parent *core.Workspace,
	args struct{},
) (*core.Changeset, error) {
	if isSyntheticWorkspace(parent) {
		return core.NewEmptyChangeset(ctx)
	}

	cfg, err := workspaceConfigWithCompatFallback(ctx, parent)
	if err != nil {
		return nil, err
	}

	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace rootfs: %w", err)
	}
	updatedDir := baseDir

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}

	for sdkName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		sdkRef := moduleEntrySourceWithPin(entry)
		if sdkRef == "" {
			return nil, fmt.Errorf("SDK module %q has no source", sdkName)
		}
		for _, client := range entry.AsSDK.Clients {
			moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(parent, client.Module)
			if err != nil {
				return nil, err
			}
			targetModule, err := s.resolveClientTargetModule(workspaceCtx, moduleLoadRef, client.Pin)
			if err != nil {
				return nil, fmt.Errorf("generate client %q for module %q: %w", client.Path, moduleRef, err)
			}
			generatedClient, err := s.workspaceClientInitGeneratedDiff(workspaceCtx, targetModule, sdkRef, client.Path)
			if err != nil {
				return nil, fmt.Errorf("generate client %q: %w", client.Path, err)
			}
			updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, generatedClient)
			if err != nil {
				return nil, fmt.Errorf("stage generated client %q: %w", client.Path, err)
			}
		}
	}

	return workspaceMigrationChanges(ctx, updatedDir, baseDir)
}

func (s *workspaceSchema) resolveClientTargetModule(
	ctx context.Context,
	ref string,
	pin string,
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &src, workspaceClientModuleSourceSelector(ref, pin)); err != nil {
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

func (s *workspaceSchema) workspaceClientInitGeneratedDiff(
	ctx context.Context,
	targetModule dagql.ObjectResult[*core.ModuleSource],
	sdkRef string,
	clientPath string,
) (dagql.ObjectResult[*core.Directory], error) {
	var out dagql.ObjectResult[*core.Directory]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return out, fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &out, dagql.Selector{Field: "directory"}); err != nil {
		return out, fmt.Errorf("create empty generated-client directory: %w", err)
	}

	return (&moduleSourceSchema{}).runClientGenerator(ctx, targetModule, out, &modules.ModuleConfigClient{
		Generator: sdkRef,
		Directory: clientPath,
	})
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
		rel, err := filepath.Rel(ws.HostPath(), cleaned)
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
	loadPath, err := workspaceHostPath(ws, cleaned)
	if err != nil {
		return "", "", err
	}
	return cleaned, loadPath, nil
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
