package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceInitClientArgs struct {
	Path       string
	SDK        string
	Module     string
	Args       core.JSON `default:""`
	Here       bool      `default:"false"`
	NoGenerate bool      `default:"false"`
}

// initClientChanges stages the workspace config edit recording the new client
// plus whatever the SDK's initClient scaffolds. The returned initScope names
// what was created, so the caller can generate for exactly it (see
// workspaceSchema.withScopedGeneration).
func (s *workspaceSchema) initClientChanges(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitClientArgs,
) (res dagql.ObjectResult[*core.Changeset], scope initScope, _ error) {
	ws := parent.Self()
	if args.Path == "" {
		return res, scope, fmt.Errorf("client path is required")
	}
	if args.SDK == "" {
		return res, scope, fmt.Errorf("SDK name is required")
	}
	if args.Module == "" {
		return res, scope, fmt.Errorf("module ref is required")
	}

	clientPath, err := resolveWorkspaceClientPath(args.Path, ws.Cwd)
	if err != nil {
		return res, scope, err
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, args.Here)
	if err != nil {
		return res, scope, err
	}
	moduleLoadRef, configModuleRef, err := resolveWorkspaceClientModuleRef(ws, args.Module, staged.ConfigDir)
	if err != nil {
		return res, scope, err
	}
	cfg := staged.Config
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(cfg, args.SDK)
	if err != nil {
		return res, scope, err
	}

	workspaceCtx := ctx
	if ws.ClientID != "" {
		workspaceCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return res, scope, fmt.Errorf("workspace client context: %w", err)
		}
	}
	targetModule, err := s.resolveClientTargetModule(workspaceCtx, ws, moduleLoadRef, "")
	if err != nil {
		return res, scope, err
	}
	modulePin := targetModule.Self().Pin()

	// dagger.toml records paths relative to the directory holding it, while
	// everything downstream of here is workspace-root-relative.
	configClientPath, err := workspace.SDKManagedPathFor(staged.ConfigDir, clientPath)
	if err != nil {
		return res, scope, err
	}

	if err := removeClientEntryAtPath(cfg, staged.ConfigDir, clientPath); err != nil {
		return res, scope, err
	}
	sdkEntry = cfg.Modules[sdkName]
	sdkEntry.AsSDK.Clients = append(sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:   configClientPath,
		Module: configModuleRef,
		Pin:    modulePin,
	})
	cfg.Modules[sdkName] = sdkEntry

	newConfigBytes, err := workspace.UpdateConfigBytes(staged.Data, cfg)
	if err != nil {
		return res, scope, fmt.Errorf("update workspace config: %w", err)
	}

	configRelPath := staged.ConfigFile
	baseDir, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return res, scope, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, scope, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes)
	if err != nil {
		return res, scope, fmt.Errorf("stage workspace config update: %w", err)
	}

	engineChanges, err := workspaceMigrationChanges(ctx, updatedDir, baseDir)
	if err != nil {
		return res, scope, err
	}

	sdkArgs, err := coresdk.DecodeInitArgs(args.Args)
	if err != nil {
		return res, scope, err
	}
	loadedSDK, err := s.loadWorkspaceSDK(ctx, ws, staged.ConfigDir, sdkRef)
	if err != nil {
		return res, scope, err
	}
	clientInitializer, ok := loadedSDK.AsClientInitializer()
	if !ok {
		return res, scope, fmt.Errorf("%q does not support client init", args.SDK)
	}
	sdkWorkspace, err := rootAnchoredWorkspace(ctx, parent)
	if err != nil {
		return res, scope, err
	}
	sdkChanges, err := clientInitializer.InitClient(ctx, sdkWorkspace, clientPath, moduleLoadRef, sdkArgs)
	if err != nil {
		return res, scope, fmt.Errorf("sdk client init: %w", err)
	}

	res, err = mergeWorkspaceInitChangeset(ctx, engineChanges, sdkChanges)
	if err != nil {
		return res, scope, err
	}

	// Create the client directory, even when the SDK's initClient scaffolds
	// nothing into it (the Go SDK stages an empty changeset). Generation runs
	// with this path as the workspace cwd, and a cwd that exists in neither the
	// overlay nor the host cannot be resolved — "stat <path>: no such file or
	// directory". Module init gets this for free from the dagger-module.toml the
	// engine writes; a client has no engine-owned file of its own.
	res, err = changesetWithDirectoryMode(ctx, res, clientPath, 0o755)
	if err != nil {
		return res, scope, err
	}
	return res, initScope{sdk: sdkName, path: clientPath}, nil
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

// resolveWorkspaceClientPath resolves the client's output directory the way
// module init resolves --path, and every other workspace path a user types:
// relative to where they are standing, with a leading "/" meaning the
// workspace root. The result is workspace-root-relative, which is what the
// as-sdk client entry records and what generation is scoped to.
func resolveWorkspaceClientPath(pathArg, cwd string) (string, error) {
	resolved, err := resolveWorkspacePath(pathArg, cwd)
	if err != nil {
		return "", fmt.Errorf("client path %q must not escape the workspace root", pathArg)
	}
	if resolved == "." {
		return "", fmt.Errorf("client path must point to a directory below the workspace root")
	}
	return resolved, nil
}

// resolveWorkspaceClientModuleRef normalizes a client's module ref into the two
// forms it is needed in: loadRef, workspace-root-relative, is what module
// loading reads from, while configRef is how the entry is spelled in the
// dagger.toml at configDir. A canonical ref has no anchor, so it is both.
func resolveWorkspaceClientModuleRef(ws *core.Workspace, ref, configDir string) (loadRef string, configRef string, _ error) {
	if !workspace.IsLocalRef(ref, "") {
		return ref, ref, nil
	}
	// A local ref may be spelled with Windows separators; every path below this
	// point is addressed on the engine, where filepath treats a backslash as an
	// ordinary character.
	cleaned := filepath.Clean(strings.ReplaceAll(ref, `\`, "/"))
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
	loadRef = filepath.ToSlash(cleaned)
	configRef, err := workspace.SDKManagedPathFor(configDir, loadRef)
	if err != nil {
		return "", "", err
	}
	// A spelling that would read back as a git ref — a dot in its first segment,
	// like "sdk.v1/foo" — keeps an explicit local marker.
	if !workspace.IsLocalRef(configRef, "") {
		configRef = "./" + configRef
	}
	return loadRef, configRef, nil
}

// resolveSDKManagedClientModule reads back what resolveWorkspaceClientModuleRef
// stored: a local ref anchors like every other as-sdk path, a canonical ref has
// no anchor and stays verbatim.
func resolveSDKManagedClientModule(configDir, ref string) (string, error) {
	if !workspace.IsLocalRef(ref, "") {
		return ref, nil
	}
	return workspace.ResolveSDKManagedPath(configDir, ref)
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

// removeClientEntryAtPath drops any client recorded at clientPath, which is
// workspace-root-relative while the entries themselves are recorded against
// configDir. An entry that cannot be resolved is a corruption every other
// reader fails on, so it fails here too rather than being mistaken for a miss.
func removeClientEntryAtPath(cfg *workspace.Config, configDir, clientPath string) error {
	if cfg == nil {
		return nil
	}
	cleanPath := filepath.ToSlash(cleanWorkspaceRelPath(clientPath))
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		kept := make([]workspace.SDKManagedClient, 0, len(entry.AsSDK.Clients))
		for _, client := range entry.AsSDK.Clients {
			resolved, err := workspace.ResolveSDKManagedPath(configDir, client.Path)
			if err != nil {
				return fmt.Errorf("client managed by %q: %w", moduleName, err)
			}
			if resolved == cleanPath {
				continue
			}
			kept = append(kept, client)
		}
		entry.AsSDK.Clients = kept
		cfg.Modules[moduleName] = entry
	}
	return nil
}
