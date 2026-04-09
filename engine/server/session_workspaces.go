package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/parallel"
)

// CurrentWorkspace returns the cached workspace for the current client.
func (srv *Server) CurrentWorkspace(ctx context.Context) (*core.Workspace, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.workspace == nil {
		return nil, fmt.Errorf("workspace not loaded")
	}
	return client.workspace, nil
}

func canonicalModuleReference(src *core.ModuleSource) string {
	sourceSubpath := src.SourceSubpath
	if sourceSubpath == "" {
		sourceSubpath = src.SourceRootSubpath
	}

	switch src.Kind {
	case core.ModuleSourceKindLocal:
		return filepath.Clean(filepath.Join(src.Local.ContextDirectoryPath, sourceSubpath))
	case core.ModuleSourceKindGit:
		return core.GitRefString(src.Git.CloneRef, sourceSubpath, "")
	default:
		// Fallback for non-local/non-git sources.
		return src.AsString()
	}
}

// ensureWorkspaceLoaded detects the workspace from the client's working directory
// and loads all configured modules onto the dagql server. Called from serveQuery
// (not initializeDaggerClient) because it requires the client's buildkit session
// to access the client's filesystem for workspace detection.
func (srv *Server) ensureWorkspaceLoaded(ctx context.Context, client *daggerClient) error {
	mode, workspaceRef := workspaceBindingMode(client)
	if mode == workspaceBindingInherit {
		return srv.inheritWorkspaceBinding(ctx, client)
	}

	client.workspaceMu.Lock()
	defer client.workspaceMu.Unlock()

	if client.workspaceLoaded {
		return client.workspaceErr
	}

	// Wait for the client's buildkit session to be available.
	// Don't mark as loaded on failure — allow retry on next request.
	if _, err := client.getClientCaller(client.clientID); err != nil {
		return fmt.Errorf("waiting for client session: %w", err)
	}

	var err error
	switch mode {
	case workspaceBindingDeclared:
		err = srv.loadWorkspaceFromDeclaredRef(ctx, client, workspaceRef)
	case workspaceBindingDetectHost:
		err = srv.loadWorkspaceFromHost(ctx, client)
	default:
		err = fmt.Errorf("unsupported workspace binding mode %q", mode)
	}
	if err != nil {
		client.workspaceErr = err
	}

	client.workspaceLoaded = true
	return client.workspaceErr
}

type workspaceBindingModeType string

const (
	workspaceBindingDeclared   workspaceBindingModeType = "declared"
	workspaceBindingDetectHost workspaceBindingModeType = "detect_host"
	workspaceBindingInherit    workspaceBindingModeType = "inherit"
)

// workspaceBindingMode resolves binding behavior for the current client:
// explicit workspace declaration, own host detection, or parent inheritance.
func workspaceBindingMode(client *daggerClient) (workspaceBindingModeType, string) {
	if workspaceRef, ok := workspaceRefFromClientMetadata(client.clientMetadata); ok {
		return workspaceBindingDeclared, workspaceRef
	}
	if client.pendingWorkspaceLoad {
		return workspaceBindingDetectHost, ""
	}
	return workspaceBindingInherit, ""
}

// workspaceRefFromClientMetadata returns the explicitly declared workspace
// binding, if present.
func workspaceRefFromClientMetadata(clientMD *engine.ClientMetadata) (string, bool) {
	if clientMD == nil {
		return "", false
	}
	if clientMD.Workspace != nil {
		return *clientMD.Workspace, true
	}
	return "", false
}

// inheritWorkspaceBinding copies the nearest available parent workspace binding
// onto the current client. This keeps nested clients aligned with their parent
// workspace for currentWorkspace() resolution.
func (srv *Server) inheritWorkspaceBinding(ctx context.Context, client *daggerClient) error {
	client.workspaceMu.Lock()
	if client.workspace != nil {
		client.workspaceMu.Unlock()
		return nil
	}
	client.workspaceMu.Unlock()

	for i := len(client.parents) - 1; i >= 0; i-- {
		parent := client.parents[i]
		if err := srv.ensureWorkspaceLoaded(ctx, parent); err != nil {
			return err
		}

		parent.workspaceMu.Lock()
		parentWorkspace := parent.workspace
		parent.workspaceMu.Unlock()
		if parentWorkspace == nil {
			continue
		}

		client.workspaceMu.Lock()
		if client.workspace == nil {
			client.workspace = parentWorkspace
		}
		client.workspaceMu.Unlock()
		return nil
	}

	return nil
}

// loadWorkspaceFromHost detects and loads the workspace from the client's host filesystem.
func (srv *Server) loadWorkspaceFromHost(ctx context.Context, client *daggerClient) error {
	return srv.loadWorkspaceFromHostPath(ctx, client, ".")
}

func (srv *Server) loadWorkspaceFromHostPath(ctx context.Context, client *daggerClient, hostPath string) error {
	cwd, err := client.engineUtilClient.AbsPath(ctx, hostPath)
	if err != nil {
		return fmt.Errorf("workspace detection: %w", err)
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		return filepath.Join(ws.Root, ws.Path, relPath)
	}

	return srv.detectAndLoadWorkspace(ctx, client,
		core.NewCallerStatFS(client.engineUtilClient),
		client.engineUtilClient.ReadCallerHostFile,
		cwd,
		resolveLocalRef,
		nil,
		true, // isLocal
	)
}

func (srv *Server) loadWorkspaceFromDeclaredRef(ctx context.Context, client *daggerClient, workspaceRef string) error {
	// Resolve as local path first (relative to the connecting client's cwd).
	// If not found, fall back to parsing as a git workspace ref.
	localPath, err := client.engineUtilClient.AbsPath(ctx, workspaceRef)
	if err == nil {
		localStat, statErr := client.engineUtilClient.StatCallerHostPath(ctx, localPath, true)
		switch {
		case statErr == nil:
			if !localStat.IsDir() {
				return fmt.Errorf("workspace %q: local path is not a directory", workspaceRef)
			}
			return srv.loadWorkspaceFromHostPath(ctx, client, localPath)
		case !isWorkspaceNotFound(statErr):
			return fmt.Errorf("workspace %q: checking local path: %w", workspaceRef, statErr)
		}
	}

	if remoteErr := srv.loadWorkspaceFromRemote(ctx, client, workspaceRef); remoteErr == nil {
		return nil
	} else if err == nil {
		return fmt.Errorf("workspace %q did not resolve as local path or git ref: %w", workspaceRef, remoteErr)
	}
	return fmt.Errorf("workspace %q: resolving local path: %w", workspaceRef, err)
}

func isWorkspaceNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound
}

type workspaceRemoteRef struct {
	cloneRef        string
	version         string
	workspaceSubdir string
}

func parseWorkspaceRemoteRef(ctx context.Context, remoteRef string) (workspaceRemoteRef, error) {
	// Fragment refs are parsed via the same git URL parser used by Address.*.
	if strings.Contains(remoteRef, "#") {
		gitURL, err := gitutil.ParseURL(remoteRef)
		if err != nil {
			return workspaceRemoteRef{}, err
		}
		version := ""
		subdir := "."
		if gitURL.Fragment != nil {
			version = gitURL.Fragment.Ref
			subdir = gitURL.Fragment.Subdir
		}
		workspaceSubdir, err := normalizeWorkspaceRemoteSubdir(subdir)
		if err != nil {
			return workspaceRemoteRef{}, fmt.Errorf("invalid git subdir in workspace ref %q: %w", remoteRef, err)
		}
		return workspaceRemoteRef{
			cloneRef:        gitURL.Remote(),
			version:         version,
			workspaceSubdir: workspaceSubdir,
		}, nil
	}

	// Preserve legacy @ref parsing semantics for existing workspace refs.
	parsedRef, err := core.ParseGitRefString(ctx, remoteRef)
	if err != nil {
		return workspaceRemoteRef{}, err
	}
	workspaceSubdir := "."
	if parsedRef.RepoRootSubdir != "/" && parsedRef.RepoRootSubdir != "." {
		workspaceSubdir = parsedRef.RepoRootSubdir
	}
	return workspaceRemoteRef{
		cloneRef:        parsedRef.SourceCloneRef,
		version:         parsedRef.ModVersion,
		workspaceSubdir: workspaceSubdir,
	}, nil
}

func normalizeWorkspaceRemoteSubdir(subdir string) (string, error) {
	if subdir == "" {
		return ".", nil
	}
	subdir = filepath.Clean(subdir)
	subdir = strings.TrimPrefix(subdir, string(filepath.Separator))
	if subdir == "" || subdir == "." {
		return ".", nil
	}
	if !filepath.IsLocal(subdir) {
		return "", fmt.Errorf("path points outside repository: %q", subdir)
	}
	return subdir, nil
}

// loadWorkspaceFromRemote clones a git repo and detects/loads the workspace from it.
func (srv *Server) loadWorkspaceFromRemote(ctx context.Context, client *daggerClient, remoteRef string) error {
	parsedRef, err := parseWorkspaceRemoteRef(ctx, remoteRef)
	if err != nil {
		return fmt.Errorf("remote workspace %q: parsing git ref: %w", remoteRef, err)
	}

	tree, err := srv.cloneGitTree(ctx, client.dag, parsedRef.cloneRef, parsedRef.version)
	if err != nil {
		return fmt.Errorf("remote workspace %q: %w", remoteRef, err)
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, ws.Path, relPath)
		return core.GitRefString(parsedRef.cloneRef, subPath, parsedRef.version)
	}

	return srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		&core.DirectoryStatFS{Dir: tree},
		func(ctx context.Context, path string) ([]byte, error) {
			return core.DirectoryReadFile(ctx, tree, path)
		},
		parsedRef.workspaceSubdir,
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress(parsedRef.cloneRef, ws.Path, parsedRef.version)
		},
		false, // isLocal
		tree,  // pre-built rootfs for remote
	)
}

// detectAndLoadWorkspace is the unified core of workspace detection and module loading
// for local workspaces.
func (srv *Server) detectAndLoadWorkspace(
	ctx context.Context,
	client *daggerClient,
	statFS core.StatFS,
	readFile func(context.Context, string) ([]byte, error),
	cwd string,
	resolveLocalRef func(ws *workspace.Workspace, relPath string) string,
	workspaceAddress func(ws *workspace.Workspace) string,
	isLocal bool,
) error {
	return srv.detectAndLoadWorkspaceWithRootfs(ctx, client, statFS, readFile, cwd, resolveLocalRef, workspaceAddress, isLocal, dagql.ObjectResult[*core.Directory]{})
}

// pendingModule represents a module to be loaded from compat parsing,
// -m flags, or the implicit CWD module.
type pendingModule struct {
	// Source reference (local path or git URL).
	Ref string

	// Requested pin for git/module refs. Empty means resolve live.
	RefPin string

	// Name override (empty = derive from module).
	Name string

	// If true, this module is the workspace entrypoint: its main-object
	// methods are proxied onto the Query root in addition to its namespaced
	// constructor.
	Entrypoint bool

	// If true, resolve +defaultPath from workspace root instead of module source.
	// Used for legacy blueprints/toolchains migrated to workspace modules.
	LegacyDefaultPath bool

	// If true, disable find-up when resolving the module source.
	// Used for explicitly-targeted refs whose path is already final.
	DisableFindUp bool

	// Workspace config defaults to apply to the module.
	ConfigDefaults     map[string]any
	DefaultsFromDotEnv bool
	ArgCustomizations  []*modules.ModuleConfigArgument

	// For legacy blueprints, the caller module's own .env should still behave
	// like the "inner" env file even though the code now loads from the
	// blueprint source tree.
	LegacyCallerModuleDir string
}

type moduleLoadRequest struct {
	mod   pendingModule
	extra bool
}

type resolvedModuleLoad struct {
	primary           dagql.ObjectResult[*core.Module]
	primaryEntrypoint bool
	related           []resolvedServedModule
}

type resolvedServedModule struct {
	mod        dagql.ObjectResult[*core.Module]
	entrypoint bool
}

const maxParallelModuleResolves = 8

// cwdModuleName reads the module name from the dagger.json in moduleDir.
func cwdModuleName(ctx context.Context, readFile func(context.Context, string) ([]byte, error), moduleDir string) string {
	data, err := readFile(ctx, filepath.Join(moduleDir, workspace.ModuleConfigFileName))
	if err != nil {
		return ""
	}
	var cfg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.Name
}

func pendingLegacyModule(
	ws *workspace.Workspace,
	resolveLocalRef func(ws *workspace.Workspace, relPath string) string,
	name, source, pin string,
	entrypoint bool,
	configDefaults map[string]any,
	argCustomizations []*modules.ModuleConfigArgument,
) pendingModule {
	kind := core.FastModuleSourceKindCheck(source, pin)
	ref := source
	if kind == core.ModuleSourceKindLocal {
		ref = resolveLocalRef(ws, source)
	}

	mod := pendingModule{
		Ref:               ref,
		RefPin:            pin,
		Name:              name,
		Entrypoint:        entrypoint,
		LegacyDefaultPath: true,
		ConfigDefaults:    configDefaults,
		ArgCustomizations: argCustomizations,
	}
	if kind == core.ModuleSourceKindLocal {
		mod.RefPin = ""
	}
	return mod
}

func legacyCallerModuleDir(isLocal bool, moduleDir string) string {
	if !isLocal || moduleDir == "" {
		return ""
	}
	return moduleDir
}

// detectAndLoadWorkspaceWithRootfs is the unified core of workspace detection
// and module gathering. It detects the current workspace root, applies legacy
// dagger.json compat, and gathers all modules to be loaded later by
// ensureModulesLoaded.
//
// It works for both local and remote workspaces, parameterized by the filesystem
// abstraction (statFS/readFile) and reference resolution (resolveLocalRef).
// For remote workspaces, prebuiltRootfs provides the already-cloned git tree.
func (srv *Server) detectAndLoadWorkspaceWithRootfs(
	ctx context.Context,
	client *daggerClient,
	statFS core.StatFS,
	readFile func(context.Context, string) ([]byte, error),
	cwd string,
	resolveLocalRef func(ws *workspace.Workspace, relPath string) string,
	workspaceAddress func(ws *workspace.Workspace) string,
	isLocal bool,
	prebuiltRootfs dagql.ObjectResult[*core.Directory],
) error {
	clientMD := client.clientMetadata
	skipModules := !client.pendingWorkspaceLoad || (clientMD != nil && clientMD.SkipWorkspaceModules)

	// --- Detect workspace (pure — no dagger.json knowledge) ---
	ws, err := workspace.Detect(ctx, func(ctx context.Context, path string) (string, bool, error) {
		return core.StatFSExists(ctx, statFS, path)
	}, readFile, cwd)
	if err != nil {
		return err
	}

	// --- Compat mode: extract toolchains/blueprints from legacy dagger.json ---
	// In the foundation split, initialized workspace config is deferred, so a
	// nearby dagger.json is the only source of workspace-level enrichment.
	var legacyToolchains []workspace.LegacyToolchain
	var legacyBlueprint *workspace.LegacyBlueprint
	moduleDir, hasModuleConfig, _ := core.Host{}.FindUp(ctx, statFS, cwd, workspace.ModuleConfigFileName)
	legacyCallerDir := legacyCallerModuleDir(isLocal, moduleDir)
	if hasModuleConfig {
		cfgPath := filepath.Join(moduleDir, workspace.ModuleConfigFileName)
		if data, readErr := readFile(ctx, cfgPath); readErr == nil {
			legacyToolchains, _ = workspace.ParseLegacyToolchains(data)
			legacyBlueprint, _ = workspace.ParseLegacyBlueprint(data)
		}
		if len(legacyToolchains) > 0 || legacyBlueprint != nil {
			slog.Warn("Inferring workspace behavior from legacy module config.",
				"config", cfgPath)
		}
	} else {
		wsDir := filepath.Join(ws.Root, ws.Path)
		slog.Info("No workspace modules detected.", "path", wsDir)
	}

	// Build + cache core.Workspace.
	address := ""
	if workspaceAddress != nil {
		address = workspaceAddress(ws)
	}
	coreWS, err := srv.buildCoreWorkspace(ctx, client, ws, isLocal, prebuiltRootfs, address)
	if err != nil {
		return fmt.Errorf("building workspace: %w", err)
	}
	client.workspace = coreWS

	if skipModules {
		return nil
	}

	// --- Gather all modules to load ---
	var pending []pendingModule

	// resolveConfigRef resolves module source paths declared in dagger.json
	// relative to the config file location rather than the client's CWD.
	// When a client connects from a subdirectory, ws.Path points there,
	// but module sources in the config are relative to the config itself.
	resolveConfigRef := resolveLocalRef
	if hasModuleConfig && isLocal {
		configDir := moduleDir
		resolveConfigRef = func(_ *workspace.Workspace, relPath string) string {
			return filepath.Join(configDir, relPath)
		}
	}

	// (1a) Legacy toolchains (from compat mode, extracted above)
	for _, tc := range legacyToolchains {
		pending = append(pending, pendingLegacyModule(
			ws,
			resolveConfigRef,
			tc.Name,
			tc.Source,
			tc.Pin,
			false,
			tc.ConfigDefaults,
			tc.Customizations,
		))
	}

	// (1b) Legacy blueprint (from compat mode, extracted above)
	if legacyBlueprint != nil {
		blueprint := pendingLegacyModule(
			ws,
			resolveConfigRef,
			legacyBlueprint.Name,
			legacyBlueprint.Source,
			legacyBlueprint.Pin,
			true,
			nil,
			nil,
		)
		blueprint.LegacyCallerModuleDir = legacyCallerDir
		pending = append(pending, blueprint)
	}

	// (2) Implicit module (dagger.json near CWD)
	{
		moduleDir, hasModuleConfig, _ := core.Host{}.FindUp(ctx, statFS, cwd, workspace.ModuleConfigFileName)
		if hasModuleConfig {
			wsDir := filepath.Join(ws.Root, ws.Path)
			rel, _ := filepath.Rel(wsDir, moduleDir)
			name := cwdModuleName(ctx, readFile, moduleDir)
			pending = append(pending, pendingModule{
				Ref:  resolveLocalRef(ws, rel),
				Name: name,
				// If the root module references a separate blueprint, only that
				// blueprint should contribute Query-root entrypoint proxies.
				// The root app module still needs to be served, but only as a
				// namespaced module.
				Entrypoint: legacyBlueprint == nil,
			})
		}
	}

	// (3) Extra modules from -m flag are stored separately in
	//     client.pendingExtraModules (already populated from clientMD).
	//     They go through the same loadModule chokepoint in ensureModulesLoaded.

	client.pendingModules = pending

	return nil
}

// buildCoreWorkspace converts the internal workspace detection result into
// the public core.Workspace. For local workspaces, it stores the host path
// (directories are resolved lazily). For remote, it stores the prebuiltRootfs.
func (srv *Server) buildCoreWorkspace(
	ctx context.Context,
	_ *daggerClient,
	detected *workspace.Workspace,
	isLocal bool,
	prebuiltRootfs dagql.ObjectResult[*core.Directory],
	address string,
) (*core.Workspace, error) {
	// Capture the current client ID for routing host filesystem operations.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}

	coreWS := &core.Workspace{
		Address:  address,
		Path:     detected.Path,
		ClientID: clientMetadata.ClientID,
	}
	if coreWS.Address == "" {
		coreWS.Address = localWorkspaceAddress(detected.Root, detected.Path)
	}

	if isLocal {
		// Local: store host path only. Directories are resolved lazily
		// via per-call host.directory() in resolveRootfs.
		coreWS.SetHostPath(detected.Root)
	} else {
		// Remote: store the cloned git tree.
		coreWS.SetRootfs(prebuiltRootfs)
	}

	return coreWS, nil
}

func localWorkspaceAddress(root, workspacePath string) string {
	workspaceDir := filepath.Join(root, workspacePath)
	return (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(workspaceDir),
	}).String()
}

func remoteWorkspaceAddress(cloneRef, workspacePath, version string) string {
	return core.GitRefString(cloneRef, workspacePath, version)
}

// cloneGitTree clones a git repository and returns its directory tree.
func (srv *Server) cloneGitTree(ctx context.Context, dag *dagql.Server, cloneRef, version string) (dagql.ObjectResult[*core.Directory], error) {
	// Build the ref selector — use "head" if no version specified.
	refSelector := dagql.Selector{Field: "head"}
	if version != "" {
		refSelector = dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(version)}},
		}
	}

	var tree dagql.ObjectResult[*core.Directory]
	err := dag.Select(ctx, dag.Root(), &tree,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(cloneRef)},
			},
		},
		refSelector,
		dagql.Selector{Field: "tree"},
	)
	if err != nil {
		return tree, fmt.Errorf("cloning repo: %w", err)
	}
	return tree, nil
}

// ensureModulesLoaded loads all pending modules (from compat parsing,
// the implicit CWD module, and -m flags). Called from serveQuery after
// ensureWorkspaceLoaded. Uses a mutex+flag instead of sync.Once so that
// transient failures (e.g. session not yet registered) can be retried.
func (srv *Server) ensureModulesLoaded(ctx context.Context, client *daggerClient) error {
	if len(client.pendingModules) == 0 && len(client.pendingExtraModules) == 0 {
		return nil
	}

	client.modulesMu.Lock()
	defer client.modulesMu.Unlock()

	if client.modulesLoaded {
		return client.modulesErr
	}

	// Wait for the client's buildkit session to be available.
	// Don't mark as loaded on failure — allow retry on next request.
	if _, err := client.getClientCaller(client.clientID); err != nil {
		return fmt.Errorf("waiting for client session: %w", err)
	}

	loads := gatherModuleLoadRequests(client.pendingModules, client.pendingExtraModules)
	resolvedLoads := make([]resolvedModuleLoad, len(loads))
	resolveErrs := make([]error, len(loads))

	// Resolve modules in parallel, then apply to client state in deterministic order.
	jobs := parallel.New().
		WithContextualTracer(true).
		WithLimit(moduleResolveParallelism(len(loads)))
	for i, load := range loads {
		i := i
		load := load
		jobs = jobs.WithJob(moduleLoadJobName(load), func(ctx context.Context) error {
			resolved, err := srv.resolveModuleLoad(ctx, client.dag, load)
			if err != nil {
				resolveErrs[i] = err
				return nil //nolint:nilerr // errors collected for deterministic ordering
			}
			resolvedLoads[i] = resolved
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		client.modulesErr = fmt.Errorf("resolving modules: %w", err)
		client.modulesLoaded = true
		return client.modulesErr
	}

	for i, load := range loads {
		if resolveErrs[i] != nil {
			client.modulesErr = moduleLoadErr(load, resolveErrs[i])
			client.modulesLoaded = true
			return client.modulesErr
		}
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if err := srv.serveAllResolvedModuleLoads(client, loads, resolvedLoads); err != nil {
		client.modulesErr = err
		client.modulesLoaded = true
		return client.modulesErr
	}

	client.modulesLoaded = true
	return nil
}

func (srv *Server) resolveModuleLoad(
	ctx context.Context,
	dag *dagql.Server,
	load moduleLoadRequest,
) (resolvedModuleLoad, error) {
	primary, err := srv.resolveModule(ctx, dag, load.mod)
	if err != nil {
		return resolvedModuleLoad{}, err
	}

	resolved := resolvedModuleLoad{
		primary:           primary,
		primaryEntrypoint: load.mod.Entrypoint,
	}
	if !load.extra {
		return resolved, nil
	}

	src := primary.Self().GetSource()
	if src == nil {
		return resolved, nil
	}

	for _, toolchainSrc := range src.Toolchains {
		if toolchainSrc.Self() == nil {
			continue
		}
		toolchainMod, err := srv.resolveModuleSourceAsModule(ctx, dag, toolchainSrc)
		if err != nil {
			return resolvedModuleLoad{}, fmt.Errorf("resolving toolchain module: %w", err)
		}
		resolved.related = append(resolved.related, resolvedServedModule{
			mod:        toolchainMod,
			entrypoint: false,
		})
	}

	if src.Blueprint.Self() != nil {
		blueprintMod, err := srv.resolveModuleSourceAsModule(ctx, dag, src.Blueprint)
		if err != nil {
			return resolvedModuleLoad{}, fmt.Errorf("resolving blueprint module: %w", err)
		}
		resolved.related = append(resolved.related, resolvedServedModule{
			mod:        blueprintMod,
			entrypoint: true,
		})
		// When the selected module points at a separate blueprint, only the
		// blueprint should contribute Query-root entrypoint proxies.
		resolved.primaryEntrypoint = false
	}

	return resolved, nil
}

func (srv *Server) resolveModuleSourceAsModule(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Module], error) {
	var resolved dagql.ObjectResult[*core.Module]
	err := dag.Select(ctx, src, &resolved,
		dagql.Selector{Field: "asModule"},
	)
	if err != nil {
		return dagql.ObjectResult[*core.Module]{}, err
	}
	return resolved, nil
}

// serveAllResolvedModuleLoads serves all resolved primary modules and their
// related modules (blueprints, toolchains-of-toolchains).
//
// Transitive dependencies are only served for the entrypoint module — the one
// the user is interacting with via `dagger call` or `dagger shell`. This is
// needed so the client schema can resolve concrete types behind interfaces
// (e.g. a Mallard backing a Duck). Toolchain deps are NOT served globally;
// each module's deps are available in its own internal schema (mod.Deps) for
// type resolution during function calls.
func (srv *Server) serveAllResolvedModuleLoads(client *daggerClient, loads []moduleLoadRequest, resolved []resolvedModuleLoad) error {
	for i := range loads {
		load := resolved[i]
		for _, related := range load.related {
			if err := srv.serveModule(client, core.NewUserMod(related.mod), core.InstallOpts{Entrypoint: related.entrypoint}); err != nil {
				return fmt.Errorf("error serving related module %s: %w", related.mod.Self().Name(), err)
			}
		}
		if err := srv.serveModule(client, core.NewUserMod(load.primary), core.InstallOpts{Entrypoint: load.primaryEntrypoint}); err != nil {
			return moduleLoadErr(loads[i], err)
		}
		// For the entrypoint module (the one the user targets via dagger call),
		// also serve its direct dependencies so the client schema can resolve
		// concrete types behind interfaces. This mirrors the includeDependencies
		// behavior from `main`. Toolchain/non-entrypoint deps stay internal.
		if load.primaryEntrypoint {
			for _, dep := range load.primary.Self().Deps.Mods() {
				if err := srv.serveModule(client, dep, core.InstallOpts{SkipConstructor: true}); err != nil {
					return fmt.Errorf("error serving entrypoint dependency %s: %w", dep.Name(), err)
				}
			}
		}
	}

	return nil
}

func gatherModuleLoadRequests(pending []pendingModule, extras []engine.ExtraModule) []moduleLoadRequest {
	loads := make([]moduleLoadRequest, 0, len(pending)+len(extras))
	for _, mod := range pending {
		loads = append(loads, moduleLoadRequest{mod: mod})
	}
	for _, extra := range extras {
		loads = append(loads, moduleLoadRequest{
			mod: pendingModule{
				Ref:        extra.Ref,
				Name:       extra.Name,
				Entrypoint: extra.Entrypoint,
			},
			extra: true,
		})
	}
	return loads
}

func moduleResolveParallelism(moduleCount int) int {
	if moduleCount <= 1 {
		return 1
	}
	if moduleCount > maxParallelModuleResolves {
		return maxParallelModuleResolves
	}
	return moduleCount
}

func moduleProgressName(mod pendingModule) string {
	if mod.Name != "" {
		return mod.Name
	}
	if mod.Ref != "" {
		return mod.Ref
	}
	return "<unknown>"
}

func moduleLoadJobName(load moduleLoadRequest) string {
	prefix := "load module: "
	if load.extra {
		prefix = "load extra module: "
	}
	return prefix + moduleProgressName(load.mod)
}

func moduleLoadErr(load moduleLoadRequest, err error) error {
	prefix := "loading module"
	if load.extra {
		prefix = "loading extra module"
	}
	return fmt.Errorf("%s %q: %w", prefix, load.mod.Ref, err)
}

// resolveModule resolves a module through the dagql pipeline.
// Handles all module sources uniformly: legacy compat modules,
// implicit CWD modules, and -m flag modules.
//
// Legacy settings (LegacyDefaultPath, ArgCustomizations, WorkspaceConfig, etc.)
// are passed as internal args to asModule so they are applied BEFORE the module
// is installed into dagql — mutating the result after Select is incorrect because
// dagql may return a cached (and already-installed) pointer.
func (srv *Server) resolveModule(
	ctx context.Context,
	dag *dagql.Server,
	mod pendingModule,
) (dagql.ObjectResult[*core.Module], error) {
	srcArgs := []dagql.NamedInput{
		{Name: "refString", Value: dagql.String(mod.Ref)},
	}
	if mod.RefPin != "" {
		srcArgs = append(srcArgs, dagql.NamedInput{Name: "refPin", Value: dagql.String(mod.RefPin)})
	}
	if mod.DisableFindUp {
		srcArgs = append(srcArgs, dagql.NamedInput{Name: "disableFindUp", Value: dagql.Boolean(true)})
	}

	var src dagql.ObjectResult[*core.ModuleSource]
	err := dag.Select(ctx, dag.Root(), &src,
		dagql.Selector{Field: "moduleSource", Args: srcArgs},
	)
	if err != nil {
		return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("resolving module source %q: %w", mod.Ref, err)
	}

	if mod.LegacyCallerModuleDir != "" && mod.Entrypoint {
		if err := srv.mergeLegacyCallerEnvDefaults(ctx, dag, src.Self(), mod.LegacyCallerModuleDir); err != nil {
			return dagql.ObjectResult[*core.Module]{}, err
		}
	}

	// Build asModule args — legacy settings flow through dagql so they are
	// applied before Install runs inside moduleSourceAsModule.
	asModuleArgs := []dagql.NamedInput{}
	if mod.Name != "" {
		asModuleArgs = append(asModuleArgs, dagql.NamedInput{
			Name: "legacyNameOverride", Value: dagql.String(mod.Name),
		})
	}
	if mod.LegacyDefaultPath {
		asModuleArgs = append(asModuleArgs, dagql.NamedInput{
			Name: "legacyDefaultPath", Value: dagql.Boolean(true),
		})
	}
	if len(mod.ConfigDefaults) > 0 {
		wsJSON, err := json.Marshal(mod.ConfigDefaults)
		if err != nil {
			return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("encoding workspace config for %q: %w", mod.Ref, err)
		}
		asModuleArgs = append(asModuleArgs,
			dagql.NamedInput{Name: "legacyWorkspaceConfigJson", Value: dagql.String(string(wsJSON))},
		)
		if mod.DefaultsFromDotEnv {
			asModuleArgs = append(asModuleArgs, dagql.NamedInput{
				Name: "legacyDefaultsFromDotEnv", Value: dagql.Boolean(true),
			})
		}
	}
	if len(mod.ArgCustomizations) > 0 {
		custJSON, err := json.Marshal(mod.ArgCustomizations)
		if err != nil {
			return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("encoding arg customizations for %q: %w", mod.Ref, err)
		}
		asModuleArgs = append(asModuleArgs, dagql.NamedInput{
			Name: "legacyArgCustomizationsJson", Value: dagql.String(string(custJSON)),
		})
	}

	var resolved dagql.ObjectResult[*core.Module]
	err = dag.Select(ctx, src, &resolved,
		dagql.Selector{Field: "asModule", Args: asModuleArgs},
	)
	if err != nil {
		return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("resolving module source %q: %w", mod.Ref, err)
	}

	return resolved, nil
}

func (srv *Server) mergeLegacyCallerEnvDefaults(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
	callerModuleDir string,
) error {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("get current query for legacy caller env: %w", err)
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("get engine client for legacy caller env: %w", err)
	}

	envPath := filepath.Join(callerModuleDir, ".env")
	stat, err := bk.StatCallerHostPath(ctx, envPath, true)
	switch {
	case errors.Is(err, os.ErrNotExist), status.Code(err) == codes.NotFound:
		return nil
	case err != nil:
		return fmt.Errorf("stat legacy caller env %q: %w", envPath, err)
	case stat.IsDir():
		return nil
	}

	var callerEnv *core.EnvFile
	if err := dag.Select(ctx, dag.Root(), &callerEnv,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(envPath)},
			},
		},
		dagql.Selector{
			Field: "asEnvFile",
			Args: []dagql.NamedInput{
				{Name: "expand", Value: dagql.Opt(dagql.NewBoolean(true))},
			},
		},
	); err != nil {
		return fmt.Errorf("load legacy caller env %q: %w", envPath, err)
	}

	src.UserDefaults = core.NewEnvFile(true).WithEnvFiles(src.UserDefaults, callerEnv)
	return nil
}
