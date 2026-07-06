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
	"github.com/dagger/dagger/engine/client/pathutil"
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
) (res dagql.ObjectResult[*core.Changeset], _ error) {
	if err := requireLocalWorkspace(parent, "client init"); err != nil {
		return res, err
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

	// Resolve the client path per the workspace path contract: relative paths
	// resolve from the caller cwd, absolute paths from the workspace root.
	// clientPath is workspace-root-relative from here on, and so is the local
	// form of moduleRef.
	clientPath, err := resolveWorkspacePath(args.Path, parent.Cwd)
	if err != nil {
		return res, fmt.Errorf("client path %q: %w", args.Path, err)
	}
	if clientPath == "." {
		return res, fmt.Errorf("client path must point to a directory below the workspace root")
	}
	moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(parent, args.Module, parent.Cwd)
	if err != nil {
		return res, err
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigMustExist, args.Here)
	if err != nil {
		return res, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(cfg, args.SDK)
	if err != nil {
		return res, err
	}
	configRelPath, err := workspaceConfigFile(parent)
	if err != nil {
		return res, err
	}
	// Paths recorded in dagger.toml are relative to the config file's
	// directory (see workspace.ResolveModuleEntrySource); clientPath and
	// local moduleRef are workspace-root-relative, so convert before writing
	// entries.
	configDir := filepath.Dir(configRelPath)
	entryPath, err := filepath.Rel(configDir, clientPath)
	if err != nil {
		return res, fmt.Errorf("client path %q relative to config directory %q: %w", clientPath, configDir, err)
	}
	entryPath = filepath.Clean(entryPath)
	entryModule := moduleRef
	if workspace.IsLocalRef(moduleRef, "") {
		entryModule, err = filepath.Rel(configDir, moduleRef)
		if err != nil {
			return res, fmt.Errorf("module ref %q relative to config directory %q: %w", moduleRef, configDir, err)
		}
		entryModule = filepath.Clean(entryModule)
	}

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return res, fmt.Errorf("workspace client context: %w", err)
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	targetModule, err := s.resolveClientTargetModule(workspaceCtx, moduleLoadRef, "")
	if err != nil {
		return res, err
	}
	modulePin := targetModule.Self().Pin()

	removeClientEntryAtPath(cfg, configDir, clientPath)
	sdkEntry = cfg.Modules[sdkName]
	sdkEntry.AsSDK.Clients = append(sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:   entryPath,
		Module: entryModule,
		Pin:    modulePin,
	})
	cfg.Modules[sdkName] = sdkEntry

	existingConfigBytes, err := readConfigBytes(ctx, parent)
	if err != nil {
		return res, fmt.Errorf("read workspace config: %w", err)
	}
	newConfigBytes, err := workspace.UpdateConfigBytes(existingConfigBytes, cfg)
	if err != nil {
		return res, fmt.Errorf("update workspace config: %w", err)
	}

	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return res, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes, 0o644)
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
	loadedSDK, err := s.loadWorkspaceSDK(ctx, sdkRef)
	if err != nil {
		return res, err
	}
	clientInitializer, ok := loadedSDK.AsClientInitializer()
	if !ok {
		return res, fmt.Errorf("%q does not support client init", args.SDK)
	}
	workspaceObj, err := s.currentWorkspaceObject(ctx)
	if err != nil {
		return res, err
	}
	sdkChanges, err := clientInitializer.InitClient(ctx, workspaceObj, clientPath, moduleRef, sdkArgs)
	if err != nil {
		return res, fmt.Errorf("sdk client init: %w", err)
	}
	// SDK initClient implementations return changesets in caller-cwd
	// coordinates (standalone clients apply changesets relative to their
	// cwd), while engineChanges and the final export target are rooted at
	// the workspace root. Re-root before merging.
	sdkChanges, err = rerootChangesetAtWorkspaceRoot(ctx, sdkChanges, parent.Cwd)
	if err != nil {
		return res, fmt.Errorf("sdk client init: %w", err)
	}

	return mergeWorkspaceInitChangeset(ctx, engineChanges, sdkChanges)
}

func (s *workspaceSchema) clientGenerate(
	ctx context.Context,
	parent *core.Workspace,
	args struct{},
) (res dagql.ObjectResult[*core.Changeset], _ error) {
	if isSyntheticWorkspace(parent) {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return res, err
		}
		if err := srv.Select(ctx, srv.Root(), &res, dagql.Selector{Field: "changeset"}); err != nil {
			return res, err
		}
		return res, nil
	}

	cfg, err := workspaceConfigWithCompatFallback(ctx, parent)
	if err != nil {
		return res, err
	}

	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return res, fmt.Errorf("resolve workspace rootfs: %w", err)
	}
	updatedDir := baseDir

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return res, fmt.Errorf("workspace client context: %w", err)
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("dagql server: %w", err)
	}

	// Client entries in dagger.toml are stored relative to the config file's
	// directory; resolve them to workspace-root coordinates before use.
	// Compat workspaces have no native config file; their fallback config
	// reads as rooted at the workspace root.
	configDir := "."
	if parent.ConfigFile != "" {
		configDir = filepath.Dir(cleanWorkspaceRelPath(parent.ConfigFile))
	}

	for sdkName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		sdkRef := moduleEntrySourceWithPin(entry)
		if sdkRef == "" {
			return res, fmt.Errorf("SDK module %q has no source", sdkName)
		}
		for _, client := range entry.AsSDK.Clients {
			clientPath := filepath.Clean(filepath.Join(configDir, client.Path))
			moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(parent, client.Module, configDir)
			if err != nil {
				return res, err
			}
			targetModule, err := s.resolveClientTargetModule(workspaceCtx, moduleLoadRef, client.Pin)
			if err != nil {
				return res, fmt.Errorf("generate client %q for module %q: %w", client.Path, moduleRef, err)
			}
			sdkOutputPath, err := workspaceClientSDKOutputPath(clientPath, moduleRef)
			if err != nil {
				return res, fmt.Errorf("resolve client output path %q for module %q: %w", client.Path, moduleRef, err)
			}
			generatedClient, err := s.workspaceClientInitGeneratedDiff(workspaceCtx, targetModule, sdkRef, sdkOutputPath)
			if err != nil {
				return res, fmt.Errorf("generate client %q: %w", client.Path, err)
			}
			updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, generatedClient)
			if err != nil {
				return res, fmt.Errorf("stage generated client %q: %w", client.Path, err)
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

// resolveWorkspaceClientModuleRef resolves a client module ref into a
// workspace-root-relative form plus the absolute host path used to load it.
// base is where relative local refs resolve from: the caller cwd for user
// input, the config directory for refs stored in dagger.toml. Host-absolute
// paths keep their historical meaning and are converted from the workspace
// host path.
func resolveWorkspaceClientModuleRef(ws *core.Workspace, ref, base string) (rootRef string, loadRef string, _ error) {
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
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("module ref %q must not escape the workspace root", ref)
		}
	} else {
		var err error
		cleaned, err = resolveWorkspacePath(cleaned, base)
		if err != nil {
			return "", "", fmt.Errorf("module ref %q: %w", ref, err)
		}
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

// removeClientEntryAtPath drops any client entry that resolves to the given
// workspace-root-relative path. Stored client paths are config-dir-relative,
// so both sides are resolved before comparing.
func removeClientEntryAtPath(cfg *workspace.Config, configDir, clientPath string) {
	if cfg == nil {
		return
	}
	cleanPath := filepath.Clean(clientPath)
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		entry.AsSDK.Clients = slices.DeleteFunc(entry.AsSDK.Clients, func(client workspace.SDKManagedClient) bool {
			return filepath.Clean(filepath.Join(configDir, client.Path)) == cleanPath
		})
		cfg.Modules[moduleName] = entry
	}
}

func workspaceClientSDKOutputPath(clientPath, moduleRef string) (string, error) {
	cleanClientPath := filepath.Clean(clientPath)
	if !workspace.IsLocalRef(moduleRef, "") {
		return cleanClientPath, nil
	}

	moduleRoot := filepath.Clean(moduleRef)
	if moduleRoot == "" {
		moduleRoot = "."
	}

	rel, err := pathutil.LexicalRelativePath(
		filepath.Join("/", moduleRoot),
		filepath.Join("/", cleanClientPath),
	)
	if err != nil {
		return "", err
	}
	if rel == "" {
		rel = "."
	}
	return filepath.ToSlash(rel), nil
}
