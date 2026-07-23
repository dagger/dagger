package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceInstallArgs struct {
	Ref       string
	Name      string `default:""`
	Here      bool   `default:"false"`
	AsSdk     bool   `default:"false"`
	AsSdkName string `default:""`
}

type workspaceInstallConfigPlan struct {
	Changed bool
	Added   bool
}

func planWorkspaceInstallConfig(
	cfg *workspace.Config,
	args workspaceInstallArgs,
	name string,
	sourcePath string,
) (workspaceInstallConfigPlan, error) {
	plan := workspaceInstallConfigPlan{}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}

	if existing, ok := cfg.Modules[name]; ok {
		if existing.Source != sourcePath {
			return plan, fmt.Errorf(
				"module %q already exists in workspace config with source %q (new source %q)",
				name,
				existing.Source,
				sourcePath,
			)
		}
		if args.AsSdk && (existing.AsSDK == nil || existing.AsSDK.Name == "" && args.AsSdkName != "") {
			if existing.AsSDK == nil {
				existing.AsSDK = &workspace.ModuleAsSDK{}
			}
			if args.AsSdkName != "" {
				existing.AsSDK.Name = args.AsSdkName
			}
			cfg.Modules[name] = existing
			plan.Changed = true
			return plan, nil
		}
		if args.AsSdk && args.AsSdkName != "" && existing.AsSDK.Name != args.AsSdkName {
			return plan, fmt.Errorf(
				"module %q is already marked as SDK %q (new SDK name %q)",
				name,
				existing.AsSDK.Name,
				args.AsSdkName,
			)
		}
		return plan, nil
	}

	entry := workspace.ModuleEntry{Source: sourcePath}
	if args.AsSdk {
		entry.AsSDK = &workspace.ModuleAsSDK{Name: args.AsSdkName}
	}
	cfg.Modules[name] = entry
	plan.Changed = true
	plan.Added = true
	return plan, nil
}

type workspaceInstallResolution struct {
	Name         string
	ConfigSource string
	ModuleSource dagql.ObjectResult[*core.ModuleSource]
}

func (s *workspaceSchema) resolveWorkspaceInstall(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	name string,
	here bool,
) (workspaceInstallResolution, error) {
	var resolved workspaceInstallResolution
	ctx = workspaceInstallLookupContext(ctx)

	configDir := workspaceConfigDirectoryForWrite(ws, here)
	src, sourcePath, err := s.resolveWorkspaceInstallSource(ctx, ws, ref, configDir)
	if err != nil {
		return resolved, err
	}
	source := src.Self()
	if source == nil {
		return resolved, fmt.Errorf("load module source: empty result")
	}
	if !source.ConfigExists {
		return resolved, fmt.Errorf("ref %q does not point to an initialized module", ref)
	}
	if name == "" {
		name = source.ModuleName
	}
	if name == "" {
		return resolved, fmt.Errorf("ref %q does not point to an initialized module", ref)
	}

	resolved.Name = name
	resolved.ConfigSource = filepath.ToSlash(sourcePath)
	resolved.ModuleSource = src
	return resolved, nil
}

func (s *workspaceSchema) resolveWorkspaceInstallSource(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	configDir string,
) (dagql.ObjectResult[*core.ModuleSource], string, error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, "", fmt.Errorf("dagql server: %w", err)
	}

	kind := core.FastModuleSourceKindCheck(ref, "")
	var workspaceRoot dagql.ObjectResult[*core.Directory]
	if kind == "" {
		workspaceRoot, err = s.workspaceOverlayRootfs(ctx, ws)
		if err != nil {
			return src, "", err
		}
		parsed, err := core.ParseRefString(ctx, &core.DirectoryStatFS{Dir: workspaceRoot}, ref, "")
		if err != nil {
			return src, "", fmt.Errorf("parse module ref %q: %w", ref, err)
		}
		kind = parsed.Kind
	}

	if kind == core.ModuleSourceKindGit {
		if err := srv.Select(ctx, srv.Root(), &src, workspaceInstallModuleSourceSelector(ref)); err != nil {
			return src, "", fmt.Errorf("load module source: %w", err)
		}
		return src, ref, nil
	}

	if filepath.IsAbs(ref) {
		hostRoot, ok := ws.LocalSourceHostPath()
		if !ok {
			return src, "", fmt.Errorf("absolute local module ref %q requires a local workspace source", ref)
		}
		workspacePath, inside, err := relativePathWithinRoot(hostRoot, ref)
		if err != nil {
			return src, "", err
		}
		if !inside {
			return s.resolveExternalWorkspaceInstallSource(ctx, ws, ref, hostRoot, configDir)
		}
		return s.resolveWorkspaceInstallSourceFromRoot(ctx, srv, ws, workspaceRoot, ref, workspacePath, configDir)
	}

	resolvedPath, err := resolveWorkspacePath(ref, ws.Cwd)
	if err != nil {
		return src, "", err
	}
	return s.resolveWorkspaceInstallSourceFromRoot(ctx, srv, ws, workspaceRoot, ref, resolvedPath, configDir)
}

func (s *workspaceSchema) resolveWorkspaceInstallSourceFromRoot(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	root dagql.ObjectResult[*core.Directory],
	ref string,
	resolvedPath string,
	configDir string,
) (dagql.ObjectResult[*core.ModuleSource], string, error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	var err error
	if root.Self() == nil {
		root, err = s.workspaceOverlayRootfs(ctx, ws)
		if err != nil {
			return src, "", err
		}
	}
	_, found, err := moduleConfigInDir(ctx, &core.DirectoryStatFS{Dir: root}, filepath.ToSlash(resolvedPath))
	if err != nil {
		return src, "", fmt.Errorf("check module source %q: %w", ref, err)
	}
	if !found {
		return src, "", fmt.Errorf("ref %q does not point to an initialized module", ref)
	}
	if err := srv.Select(ctx, root, &src, dagql.Selector{
		Field: "asModuleSource",
		Args: []dagql.NamedInput{
			{Name: "sourceRootPath", Value: dagql.String(filepath.ToSlash(resolvedPath))},
		},
	}); err != nil {
		return src, "", fmt.Errorf("load module source: %w", err)
	}
	sourcePath, err := filepath.Rel(configDir, resolvedPath)
	if err != nil {
		return src, "", fmt.Errorf("compute relative install path: %w", err)
	}
	return src, sourcePath, nil
}

func (s *workspaceSchema) resolveExternalWorkspaceInstallSource(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	hostRoot string,
	configDir string,
) (dagql.ObjectResult[*core.ModuleSource], string, error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	lockMode := ""
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
		lockMode = clientMetadata.LockMode
	}
	ctx, err := withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return src, "", err
	}
	if lockMode != "" {
		ctx = workspaceInstallContextWithLockMode(ctx, workspace.LockMode(lockMode))
	}
	ctx = workspaceInstallLookupContext(ctx)
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, "", fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &src, workspaceInstallModuleSourceSelector(ref)); err != nil {
		return src, "", fmt.Errorf("load module source: %w", err)
	}
	sourcePath, err := filepath.Rel(filepath.Join(hostRoot, configDir), filepath.Clean(ref))
	if err != nil {
		return src, "", fmt.Errorf("compute relative install path: %w", err)
	}
	return src, sourcePath, nil
}

func relativePathWithinRoot(root, target string) (string, bool, error) {
	rel, err := filepath.Rel(root, filepath.Clean(target))
	if err != nil {
		return "", false, fmt.Errorf("resolve absolute module path: %w", err)
	}
	outside := rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator))
	return rel, !outside, nil
}

func (s *workspaceSchema) resolveWorkspaceInstallForOverlay(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	name string,
	here bool,
) (workspaceInstallResolution, error) {
	return s.resolveWorkspaceInstall(
		workspaceInstallContextWithLockMode(ctx, workspace.LockModePinned),
		ws,
		ref,
		name,
		here,
	)
}

func workspaceInstallContextWithLockMode(ctx context.Context, mode workspace.LockMode) context.Context {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ctx
	}
	updated := *clientMetadata
	updated.LockMode = string(mode)
	return engine.ContextWithClientMetadata(ctx, &updated)
}

func workspaceInstallLookupContext(ctx context.Context) context.Context {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || clientMetadata.LockMode != "" {
		return ctx
	}

	refreshed := *clientMetadata
	refreshed.LockMode = string(workspace.LockModePinned)
	return engine.ContextWithClientMetadata(ctx, &refreshed)
}

func workspaceInstallModuleSourceSelector(ref string) dagql.Selector {
	return dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(ref)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
		},
	}
}
