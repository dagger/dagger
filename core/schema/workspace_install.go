package schema

import (
	"context"
	"encoding/json"
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
	workspaceRoot dagql.ObjectResult[*core.Directory],
) (workspaceInstallResolution, error) {
	var resolved workspaceInstallResolution
	ctx = workspaceInstallLookupContext(ctx)

	configDir := workspaceConfigDirectoryForWrite(ws, here)
	src, sourcePath, err := s.resolveWorkspaceInstallSource(ctx, ws, ref, configDir, workspaceRoot)
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
	workspaceRoot dagql.ObjectResult[*core.Directory],
) (dagql.ObjectResult[*core.ModuleSource], string, error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, "", fmt.Errorf("dagql server: %w", err)
	}

	kind := core.FastModuleSourceKindCheck(ref, "")
	if kind == "" {
		if workspaceRoot.Self() == nil {
			workspaceRoot, err = s.workspaceOverlayRootfs(ctx, ws)
			if err != nil {
				return src, "", err
			}
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
	workspaceRoot dagql.ObjectResult[*core.Directory],
) (workspaceInstallResolution, error) {
	return s.resolveWorkspaceInstall(
		workspaceInstallContextWithLockMode(ctx, workspace.LockModePinned),
		ws,
		ref,
		name,
		here,
		workspaceRoot,
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

// workspaceInstallOutcome is a resolved install plus its deferred added-module
// effects. Installs record lock lookups as they resolve; the hints phase (a
// full module load) both produces settings hints and records more lookups, and
// only applies when the config entry is actually added, so it stays deferred
// behind applyAddedEffects.
type workspaceInstallOutcome struct {
	Name         string
	ConfigSource string
	// applyAddedEffects finishes the added-module side of the install: it
	// applies the hints-phase lock recordings to the overlay lock and returns
	// the settings hints to inject into the config.
	applyAddedEffects func() (map[string][]workspace.ConstructorArgHint, error)
}

// resolveInstallOutcomeForOverlay resolves an install ref for the workspace
// overlay, routing git refs through the content-addressed resolution cache.
//
// Live-path installs run under withWorkspaceLookupLockOverride, whose fresh
// per-client cache scope forces full re-resolution on every call: the override
// lock rides in context, invisible to dagql cache keys, so results resolved
// under one lock must not be served to another. Git refs escape that cost:
// their resolution depends only on the ref and the base lock contents, both of
// which __workspaceInstallResolution captures in its cache key. Local refs
// read live host or workspace state that the base lock cannot
// content-address, so they stay on the live path.
//
// Ambiguous refs (e.g. github.com/org/repo without an explicit scheme) need a
// stat against the workspace rootfs to classify; the rootfs is passed down to
// the live path so it is only synced once.
func (s *workspaceSchema) resolveInstallOutcomeForOverlay(
	ctx context.Context,
	ws *core.Workspace,
	overlayLock *workspaceOverlayLock,
	args workspaceInstallArgs,
) (*workspaceInstallOutcome, error) {
	kind := core.FastModuleSourceKindCheck(args.Ref, "")
	var workspaceRoot dagql.ObjectResult[*core.Directory]
	if kind == "" {
		var err error
		workspaceRoot, err = s.workspaceOverlayRootfs(ctx, ws)
		if err != nil {
			return nil, err
		}
		parsed, err := core.ParseRefString(ctx, &core.DirectoryStatFS{Dir: workspaceRoot}, args.Ref, "")
		if err != nil {
			return nil, fmt.Errorf("parse module ref %q: %w", args.Ref, err)
		}
		kind = parsed.Kind
	}
	if kind == core.ModuleSourceKindGit {
		return s.cachedGitInstallOutcome(ctx, overlayLock, args)
	}

	lookupCtx := withWorkspaceLookupLockOverride(ctx, overlayLock.Lock)
	resolved, err := s.resolveWorkspaceInstallForOverlay(lookupCtx, ws, args.Ref, args.Name, args.Here, workspaceRoot)
	if err != nil {
		return nil, err
	}
	return &workspaceInstallOutcome{
		Name:         resolved.Name,
		ConfigSource: resolved.ConfigSource,
		applyAddedEffects: func() (map[string][]workspace.ConstructorArgHint, error) {
			// Recordings land directly in the overlay lock via lookupCtx.
			return collectWorkspaceSettingsHintsFromSource(lookupCtx, resolved.Name, resolved.ModuleSource), nil
		},
	}, nil
}

// cachedGitInstallOutcome resolves a git install ref through the
// __workspaceInstallResolution cache and replays the returned lock deltas onto
// the overlay lock, reproducing exactly what a live resolution would have
// recorded.
func (s *workspaceSchema) cachedGitInstallOutcome(
	ctx context.Context,
	overlayLock *workspaceOverlayLock,
	args workspaceInstallArgs,
) (*workspaceInstallOutcome, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}
	baseLockData, err := overlayLock.Lock.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal base workspace lock: %w", err)
	}

	var payloadJSON dagql.String
	if err := srv.Select(ctx, srv.Root(), &payloadJSON, dagql.Selector{
		Field: "__workspaceInstallResolution",
		Args: []dagql.NamedInput{
			{Name: "ref", Value: dagql.String(args.Ref)},
			{Name: "baseLock", Value: dagql.String(baseLockData)},
		},
	}); err != nil {
		return nil, err
	}
	var payload workspaceInstallResolutionPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("decode workspace install resolution: %w", err)
	}

	resolveDelta, err := workspace.ParseLock([]byte(payload.ResolveDelta))
	if err != nil {
		return nil, fmt.Errorf("parse install resolve delta: %w", err)
	}
	if err := overlayLock.Lock.Merge(resolveDelta); err != nil {
		return nil, fmt.Errorf("apply install resolve delta: %w", err)
	}

	name := args.Name
	if name == "" {
		name = payload.ModuleName
	}
	if name == "" {
		return nil, fmt.Errorf("ref %q does not point to an initialized module", args.Ref)
	}

	return &workspaceInstallOutcome{
		Name:         name,
		ConfigSource: filepath.ToSlash(args.Ref),
		applyAddedEffects: func() (map[string][]workspace.ConstructorArgHint, error) {
			hintsDelta, err := workspace.ParseLock([]byte(payload.HintsDelta))
			if err != nil {
				return nil, fmt.Errorf("parse install hints delta: %w", err)
			}
			if err := overlayLock.Lock.Merge(hintsDelta); err != nil {
				return nil, fmt.Errorf("apply install hints delta: %w", err)
			}
			if len(payload.ConstructorHints) == 0 {
				return nil, nil
			}
			return map[string][]workspace.ConstructorArgHint{name: payload.ConstructorHints}, nil
		},
	}, nil
}

type workspaceInstallResolutionArgs struct {
	Ref      string
	BaseLock string
}

// workspaceInstallResolutionPayload is the JSON value returned by the internal
// __workspaceInstallResolution field. Lock deltas are marshaled dagger.lock
// documents; replaying them onto an overlay lock reproduces the recordings a
// live resolution would have made, which is what makes the field safe to
// cache: everything the resolution writes is part of its value.
type workspaceInstallResolutionPayload struct {
	ModuleName       string                         `json:"moduleName"`
	ResolveDelta     string                         `json:"resolveDelta"`
	HintsDelta       string                         `json:"hintsDelta"`
	ConstructorHints []workspace.ConstructorArgHint `json:"constructorHints,omitempty"`
}

// workspaceInstallResolution resolves a git module install ref against a base
// lockfile. It is cached per client keyed on its args: the ref and the full
// base lock contents cover every input of a git resolution, so identical
// installs (the common SDK pattern of evaluating one workspace chain several
// times: changes, then export) reuse the first resolution instead of redoing
// the module fetch, SDK load, and codegen behind it.
func (s *workspaceSchema) workspaceInstallResolution(
	ctx context.Context,
	parent *core.Query,
	args workspaceInstallResolutionArgs,
) (dagql.String, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("dagql server: %w", err)
	}
	baseLock, err := workspace.ParseLock([]byte(args.BaseLock))
	if err != nil {
		return "", fmt.Errorf("parse base lock: %w", err)
	}
	overlay, err := baseLock.Clone()
	if err != nil {
		return "", fmt.Errorf("clone base lock: %w", err)
	}

	// Match the live install path: pins resolve through the overlay lock in
	// pinned mode, and one lookup context spans both phases so the hints-phase
	// module load reuses the resolve phase's results.
	lookupCtx := withWorkspaceLookupLockOverride(
		workspaceInstallContextWithLockMode(ctx, workspace.LockModePinned),
		overlay,
	)

	var src dagql.ObjectResult[*core.ModuleSource]
	if err := srv.Select(lookupCtx, srv.Root(), &src, workspaceInstallModuleSourceSelector(args.Ref)); err != nil {
		return "", fmt.Errorf("load module source: %w", err)
	}
	source := src.Self()
	if source == nil {
		return "", fmt.Errorf("load module source: empty result")
	}
	if !source.ConfigExists {
		return "", fmt.Errorf("ref %q does not point to an initialized module", args.Ref)
	}

	resolveDelta, err := overlay.Diff(baseLock)
	if err != nil {
		return "", fmt.Errorf("diff resolve lock recordings: %w", err)
	}
	resolveDeltaData, err := resolveDelta.Marshal()
	if err != nil {
		return "", fmt.Errorf("marshal resolve delta: %w", err)
	}

	afterResolve, err := overlay.Clone()
	if err != nil {
		return "", fmt.Errorf("snapshot lock after resolve: %w", err)
	}
	hints := workspaceSettingsHintsFromSource(lookupCtx, source.ModuleName, src)
	hintsDelta, err := overlay.Diff(afterResolve)
	if err != nil {
		return "", fmt.Errorf("diff hints lock recordings: %w", err)
	}
	hintsDeltaData, err := hintsDelta.Marshal()
	if err != nil {
		return "", fmt.Errorf("marshal hints delta: %w", err)
	}

	data, err := json.Marshal(workspaceInstallResolutionPayload{
		ModuleName:       source.ModuleName,
		ResolveDelta:     string(resolveDeltaData),
		HintsDelta:       string(hintsDeltaData),
		ConstructorHints: hints,
	})
	if err != nil {
		return "", fmt.Errorf("encode workspace install resolution: %w", err)
	}
	return dagql.String(data), nil
}
