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

// detectWorkspaceSDKCapabilities initializes the module schema and checks the
// main object for either function that makes a regular module an SDK.
func detectWorkspaceSDKCapabilities(
	ctx context.Context,
	source dagql.ObjectResult[*core.ModuleSource],
) (bool, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, fmt.Errorf("dagql server: %w", err)
	}

	var mod dagql.ObjectResult[*core.Module]
	if err := srv.Select(ctx, source, &mod, dagql.Selector{
		Field: "asModule",
		Args: []dagql.NamedInput{
			{Name: "forceDefaultFunctionCaching", Value: dagql.Opt(dagql.Boolean(true))},
		},
	}); err != nil {
		return false, fmt.Errorf("inspect installed module capabilities: %w", err)
	}
	return coresdk.HasInitializer(mod.Self()), nil
}

// resolvedWorkspaceSDKName derives a concise SDK command name and makes it
// unique across installed SDK aliases and module-entry names.
func resolvedWorkspaceSDKName(cfg *workspace.Config, moduleName string) string {
	base := workspace.ConventionalSDKName(moduleName)
	reserved := map[string]bool{}
	if cfg != nil {
		for installedName, entry := range cfg.Modules {
			if installedName == moduleName || entry.AsSDK == nil {
				continue
			}
			reserved[installedName] = true
			name := entry.AsSDK.Name
			if name == "" {
				name = installedName
			}
			reserved[name] = true
		}
	}
	if !reserved[base] {
		return base
	}
	if !reserved[moduleName] {
		return moduleName
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !reserved[candidate] {
			return candidate
		}
	}
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

// planWorkspaceEnvInstallConfig stages an install scoped to a workspace env:
// the module is recorded under env.<envName>.modules.* so it is only present
// when that env is selected. Installing is a write, so a missing env is
// created by it, matching env-scoped config writes. The overlay entry is
// recorded even when the base config has the same module, so the env keeps it
// if the base install is later removed.
//
// An existing overlay entry without a source is a settings-only overlay, not an
// install, so it is upgraded in place (keeping its settings) rather than
// treated as already installed. When the base config installs the same module
// from the same source, its pin is copied into the overlay entry: an overlay
// source is authoritative in [workspace.ApplyEnvOverlay] and its pin travels
// with it, so a pin-less overlay would silently unpin the module in that env.
// Otherwise the entry stays pin-less and resolves through dagger.lock, like
// base installs.
func planWorkspaceEnvInstallConfig(
	cfg *workspace.Config,
	envName string,
	args workspaceInstallArgs,
	name string,
	sourcePath string,
) (workspaceInstallConfigPlan, error) {
	plan := workspaceInstallConfigPlan{}
	if args.AsSdk {
		return plan, fmt.Errorf("SDKs cannot be installed in env %q; install SDKs in the base workspace config", envName)
	}

	if base, ok := cfg.Modules[name]; ok && base.AsSDK != nil {
		return plan, fmt.Errorf("module %q is an SDK; SDKs cannot be installed in env %q", name, envName)
	}

	if workspace.EnsureEnv(cfg, envName) {
		plan.Changed = true
	}
	env := cfg.Env[envName]
	entry := workspace.EnvModuleOverlay{Source: sourcePath}
	if existing, ok := env.Modules[name]; ok {
		if existing.Source == sourcePath {
			return plan, nil
		}
		if existing.Source != "" {
			return plan, fmt.Errorf(
				"module %q already exists in env %q with source %q (new source %q)",
				name,
				envName,
				existing.Source,
				sourcePath,
			)
		}
		entry.Settings = existing.Settings
	}
	if base, ok := cfg.Modules[name]; ok && base.Source == sourcePath {
		entry.Pin = base.Pin
	}

	if env.Modules == nil {
		env.Modules = map[string]workspace.EnvModuleOverlay{}
	}
	env.Modules[name] = entry
	cfg.Env[envName] = env
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
	ctx, err := withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return src, "", err
	}
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
	return s.resolveWorkspaceInstall(ctx, ws, ref, name, here)
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
