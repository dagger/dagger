package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"github.com/iancoleman/strcase"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/schema"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/parallel"
)

// invalidateClientWorkspace drops the calling client's cached workspace
// detection so the next ensureWorkspaceLoaded re-detects it from the host.
//
// Registered as core.SetWorkspaceInvalidator and triggered after a changeset
// export writes workspace config files (e.g. a `dagger setup` migration that
// creates dagger.toml / removes the legacy dagger.json). The per-client cache
// would otherwise serve the pre-migration view for the client's whole lifetime;
// under nested execution that lifetime spans multiple sessions in one process
// (the client ID is pinned by DAGGER_SESSION_CLIENT_ID), so the post-migrate
// recommended-module install would still see the legacy dagger.json and fail.
func (srv *Server) invalidateClientWorkspace(ctx context.Context) error {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return err
	}
	client.workspaceMu.Lock()
	defer client.workspaceMu.Unlock()
	client.workspaceLoaded = false
	client.workspaceErr = nil
	client.workspace = nil
	client.pendingModules = nil
	return nil
}

// CurrentWorkspace returns the cached workspace for the current client.
func (srv *Server) CurrentWorkspace(ctx context.Context) (*core.Workspace, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.workspace == nil {
		return nil, fmt.Errorf("%w: workspace not loaded", core.ErrNoCurrentWorkspace)
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
// (not initializeDaggerClient) because it requires the client's session attachables
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

	// Wait for the client's session attachables to be available.
	// Don't mark as loaded on failure — allow retry on next request.
	if _, err := client.getClientCaller(ctx, client.clientID); err != nil {
		return fmt.Errorf("waiting for client session attachables: %w", err)
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

// workspaceEnvFromClientMetadata returns the explicitly declared workspace
// environment selection, if present.
func workspaceEnvFromClientMetadata(clientMD *engine.ClientMetadata) (string, bool) {
	if clientMD == nil {
		return "", false
	}
	if clientMD.WorkspaceEnv != nil {
		return *clientMD.WorkspaceEnv, true
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
	bk := client.engineUtilClient
	cwd, err := bk.AbsPath(ctx, hostPath)
	if err != nil {
		return fmt.Errorf("workspace detection: %w", err)
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		return filepath.Join(ws.Root, relPath)
	}

	return srv.detectAndLoadWorkspace(ctx, client,
		core.NewCallerStatFS(bk),
		bk.ReadCallerHostFile,
		cwd,
		resolveLocalRef,
		nil,
		true, // isLocal
	)
}

func (srv *Server) loadWorkspaceFromDeclaredRef(ctx context.Context, client *daggerClient, workspaceRef string) error {
	// Resolve as local path first (relative to the connecting client's cwd).
	// If not found, fall back to parsing as a git workspace ref.
	bk := client.engineUtilClient
	localPath, err := bk.AbsPath(ctx, workspaceRef)
	if err == nil {
		localStat, statErr := bk.StatCallerHostPath(ctx, localPath, true)
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

	tree, gitRef, err := srv.cloneGitTree(ctx, client.dag, parsedRef.cloneRef, parsedRef.version)
	if err != nil {
		return fmt.Errorf("remote workspace %q: %w", remoteRef, err)
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
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
			return remoteWorkspaceAddress(parsedRef.cloneRef, ws.Cwd, parsedRef.version)
		},
		false, // isLocal
		tree,  // pre-built rootfs for remote
		core.NewWorkspaceSourceGitRef(gitRef.Result, gitutil.IsCommitSHA(parsedRef.version)),
	)
}

// detectAndLoadWorkspace is the unified core of workspace detection and module loading
// for local workspaces.
//
//nolint:unparam
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
	return srv.detectAndLoadWorkspaceWithRootfs(ctx, client, statFS, readFile, cwd, resolveLocalRef, workspaceAddress, isLocal, dagql.ObjectResult[*core.Directory]{}, nil)
}

// pendingModule represents a module to be loaded from workspace discovery,
// compat parsing, or -m flags.
type legacyWorkspaceFieldPolicy uint8

const (
	legacyWorkspaceFieldPolicyDirect legacyWorkspaceFieldPolicy = iota
	legacyWorkspaceFieldPolicyRejectAsWorkspace
	legacyWorkspaceFieldPolicyStripCompatMain
)

type pendingModule struct {
	Kind moduleLoadKind

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

	// If true, this module came from legacy workspace fields whose
	// +defaultPath inputs resolve through DefaultPathContextSourceRef.
	LegacyDefaultPath bool

	// If true, disable find-up when resolving the module source.
	// Used for explicitly-targeted refs whose path is already final.
	DisableFindUp bool

	// Workspace config defaults to apply to the module.
	ConfigDefaults     map[string]any
	DefaultsFromDotEnv bool
	ArgCustomizations  []*modules.ModuleConfigArgument

	// If set, load this module's implementation from Ref but resolve
	// +defaultPath inputs from this source ref instead.
	DefaultPathContextSourceRef string
	DefaultPathContextSourcePin string
	// How legacy workspace-only dagger.json fields should be handled before
	// generic module loading.
	legacyFieldPolicy legacyWorkspaceFieldPolicy

	// For legacy blueprints, the caller module's own .env should still behave
	// like the "inner" env file even though the code now loads from the
	// blueprint source tree.
	LegacyCallerModuleDir string
}

type moduleLoadRequest struct {
	mod pendingModule
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

type moduleLoadKind string

const (
	moduleLoadKindAmbient moduleLoadKind = "ambient"
	moduleLoadKindExtra   moduleLoadKind = "extra"
)

const maxParallelModuleResolves = 8

func loadWorkspaceConfig(
	ctx context.Context,
	readFile func(context.Context, string) ([]byte, error),
	ws *workspace.Workspace,
) (*workspace.Config, error) {
	if ws.ConfigFile == "" {
		return nil, nil
	}
	configPath := filepath.Join(ws.Root, ws.ConfigFile)
	data, err := readFile(ctx, configPath)
	if err != nil {
		if isWorkspaceNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading workspace config %s: %w", configPath, err)
	}

	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parsing workspace config %s: %w", configPath, err)
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	return cfg, nil
}

func workspaceConfigPendingModules(
	ws *workspace.Workspace,
	cfg *workspace.Config,
	resolveLocalRef func(ws *workspace.Workspace, relPath string) string,
) []pendingModule {
	if cfg == nil || len(cfg.Modules) == 0 {
		return nil
	}
	configDir := filepath.Dir(ws.ConfigFile)

	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	slices.Sort(names)

	pending := make([]pendingModule, 0, len(names))
	for _, name := range names {
		entry := cfg.Modules[name]
		// A built-in SDK install entry (e.g. dang/go written by migration) has a
		// bare runtime name as its source, not a loadable module ref. It exists
		// only to carry the [modules.<sdk>.as-sdk] authoring metadata; the runtime
		// itself resolves in-engine when a consuming module loads. Skip it here so
		// the loader doesn't try to resolve the bare name as a local path.
		if entry.AsSDK != nil && coresdk.IsBuiltinSDKName(entry.Source) {
			continue
		}
		mod := pendingModule{
			Kind:               moduleLoadKindAmbient,
			Ref:                entry.Source,
			Name:               name,
			Entrypoint:         entry.Entrypoint,
			LegacyDefaultPath:  entry.LegacyDefaultPath,
			DisableFindUp:      true,
			ConfigDefaults:     entry.Settings,
			DefaultsFromDotEnv: cfg.DefaultsFromDotEnv,
			legacyFieldPolicy:  legacyWorkspaceFieldPolicyRejectAsWorkspace,
		}

		if core.FastModuleSourceKindCheck(entry.Source, "") == core.ModuleSourceKindLocal {
			resolved := workspace.ResolveModuleEntrySource(configDir, entry.Source)
			if filepath.IsAbs(resolved) {
				mod.Ref = resolved
			} else {
				mod.Ref = resolveLocalRef(ws, resolved)
			}
		}
		if mod.LegacyDefaultPath {
			mod.DefaultPathContextSourceRef = defaultPathContextRefForWorkspace(ws, resolveLocalRef)
		}

		pending = append(pending, mod)
	}

	return pending
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
		Kind:              moduleLoadKindAmbient,
		Ref:               ref,
		RefPin:            pin,
		Name:              name,
		Entrypoint:        entrypoint,
		LegacyDefaultPath: true,
		ConfigDefaults:    configDefaults,
		ArgCustomizations: argCustomizations,
		legacyFieldPolicy: legacyWorkspaceFieldPolicyRejectAsWorkspace,
	}
	mod.DefaultPathContextSourceRef = defaultPathContextRefForWorkspace(ws, resolveLocalRef)
	if kind == core.ModuleSourceKindLocal {
		mod.RefPin = ""
	}
	return mod
}

func defaultPathContextRefForWorkspace(
	ws *workspace.Workspace,
	resolveLocalRef func(ws *workspace.Workspace, relPath string) string,
) string {
	if ws == nil || resolveLocalRef == nil {
		return ""
	}
	base := "."
	if ws.ConfigFile != "" {
		base = filepath.Dir(filepath.Dir(ws.ConfigFile))
	}
	return resolveLocalRef(ws, base)
}

func legacyCallerModuleDir(isLocal bool, moduleDir string) string {
	if !isLocal || moduleDir == "" {
		return ""
	}
	return moduleDir
}

// detectAndLoadWorkspaceWithRootfs is the unified core of workspace discovery
// and module gathering.
//
// The workspace model is deliberately layered and this flow should stay
// readable in that order:
//  1. Detect the workspace root.
//  2. Detect native workspace config within that root.
//  3. Detect local lockfile binding within that root.
//  4. Normalize native config or compat dagger.json at this chokepoint.
//  5. Gather modules from the normalized config.
//  6. Load modules later through ensureModulesLoaded.
//
// Compat belongs here: once legacy dagger.json is projected into pending modules
// and effective config, most downstream code should not branch on compat.
//
// It works for both local and remote workspaces, parameterized by the filesystem
// abstraction (statFS/readFile) and reference resolution (resolveLocalRef).
// For remote workspaces, prebuiltRootfs provides the already-cloned git tree.
//
//nolint:gocyclo
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
	prebuiltSource core.WorkspaceSource,
) error {
	clientMD := client.clientMetadata
	loadModules := client.pendingWorkspaceLoad &&
		clientMD != nil &&
		clientMD.LoadWorkspaceModules &&
		!clientMD.SkipWorkspaceModules
	workspaceEnv, hasWorkspaceEnv := workspaceEnvFromClientMetadata(clientMD)

	// --- Detect workspace (pure — no dagger.json knowledge) ---
	pathExists := func(ctx context.Context, path string) (string, bool, error) {
		return core.StatFSExists(ctx, statFS, path)
	}
	var ws *workspace.Workspace
	var err error
	if isLocal {
		ws, err = workspace.Detect(ctx, pathExists, cwd)
	} else {
		ws, err = workspace.DetectInRoot(ctx, pathExists, cwd, ".")
	}
	if err != nil {
		return err
	}

	var wsConfig *workspace.Config
	if ws != nil && ws.ConfigFile != "" {
		wsConfig, err = loadWorkspaceConfig(ctx, readFile, ws)
		if err != nil {
			return err
		}
	}

	// --- Compat mode: build the ambient compat workspace from legacy dagger.json ---
	// Once a workspace config is selected, it owns ambient workspace module
	// loading. Legacy dagger.json compatibility remains only when no workspace
	// config is selected.
	var compatWorkspace *workspace.CompatWorkspace
	moduleDir, hasModuleConfig, _ := core.Host{}.FindUp(ctx, statFS, cwd, workspace.LegacyModuleConfigFileName)
	if hasModuleConfig && wsConfig != nil {
		wsDir := filepath.Clean(ws.Root)
		rel, err := filepath.Rel(wsDir, filepath.Clean(moduleDir))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			moduleDir = ""
			hasModuleConfig = false
		}
	}
	legacyCallerDir := legacyCallerModuleDir(isLocal, moduleDir)
	if wsConfig == nil && hasModuleConfig {
		cfgPath := filepath.Join(moduleDir, workspace.LegacyModuleConfigFileName)
		if data, readErr := readFile(ctx, cfgPath); readErr == nil {
			compatWorkspace, _ = workspace.ParseRuntimeCompatWorkspaceAt(data, cfgPath)
		}
		if compatWorkspace != nil {
			// Only module-consuming clients get the user-facing migration
			// warning: those loading workspace modules, and those loading an
			// explicit module (`dagger call` resolves the cwd module as an
			// extra module). Clients that opted out of module loading (SDK
			// codegen, client generators, internal tooling) would leak it
			// into the calling session's output.
			warnCompat := loadModules || (clientMD != nil && len(clientMD.ExtraModules) > 0)
			if warnCompat && (clientMD == nil || !clientMD.SuppressCompatWorkspaceWarning) {
				msg := legacyWorkspaceCompatMessage(cwd, cfgPath)
				console(ctx, msg)
				slog.Warn(msg,
					"config", cfgPath)
			}
			// Workspace detection found no native root (e.g. a legacy module
			// outside any git repo). Root the ambient compat workspace at the
			// legacy module directory so the module is still served, matching
			// the documented "fall back to the nearest dagger.json" behavior.
			if ws == nil {
				ws, err = workspace.DetectInRoot(ctx, pathExists, cwd, moduleDir)
				if err != nil {
					return err
				}
			}
		}
	}

	// No native workspace and no eligible legacy module: keep a rootless local
	// workspace for context-only APIs, but do not load modules.
	if ws == nil {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return fmt.Errorf("building rootless workspace: client metadata: %w", err)
		}
		coreWS := &core.Workspace{
			Address:  localWorkspaceAddress(cwd, "."),
			Cwd:      ".",
			ClientID: clientMetadata.ClientID,
		}
		coreWS.SetHostPath(cwd)
		coreWS.SetSource(core.NewWorkspaceSourceRootlessLocal(cwd))
		client.workspace = coreWS
		client.pendingModules = nil
		return nil
	}
	if wsConfig == nil && compatWorkspace == nil {
		slog.Info("No workspace modules detected.", "path", ws.Root)
	}

	// Build + cache core.Workspace.
	address := ""
	if workspaceAddress != nil {
		address = workspaceAddress(ws)
	}
	coreWS, err := srv.buildCoreWorkspace(ctx, client, ws, isLocal, prebuiltRootfs, prebuiltSource, address)
	if err != nil {
		return fmt.Errorf("building workspace: %w", err)
	}
	coreWS.SetCompatWorkspace(compatWorkspace)
	client.workspace = coreWS

	if !loadModules {
		return nil
	}

	if hasWorkspaceEnv {
		if wsConfig == nil {
			return fmt.Errorf("workspace env %q requires dagger.toml", workspaceEnv)
		}
		if err := func() (rerr error) {
			_, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("applying env: %s", workspaceEnv))
			defer telemetry.EndWithCause(span, &rerr)
			wsConfig, rerr = workspace.ApplyEnvOverlay(wsConfig, workspaceEnv)
			return rerr
		}(); err != nil {
			return err
		}
	}

	// --- Gather all modules to load ---
	var pending []pendingModule

	pending = workspaceConfigPendingModules(ws, wsConfig, resolveLocalRef)
	resolveCompatRef := resolveLocalRef
	if compatWorkspace != nil {
		configWS := *ws
		configWS.Root = moduleDir
		configWS.Cwd = "."
		resolveCompatRef = func(_ *workspace.Workspace, relPath string) string {
			return resolveLocalRef(&configWS, relPath)
		}
	}

	// (1) Ambient compat-workspace modules projected from legacy dagger.json.
	if compatWorkspace != nil {
		for _, legacyMod := range compatWorkspace.Modules {
			mod := pendingLegacyModule(
				ws,
				resolveCompatRef,
				legacyMod.Name,
				legacyMod.Source,
				legacyMod.Pin,
				legacyMod.Entry.Entrypoint,
				legacyMod.Entry.Settings,
				legacyMod.ArgCustomizations,
			)
			if legacyMod.Entry.Entrypoint {
				mod.LegacyCallerModuleDir = legacyCallerDir
			}
			pending = append(pending, mod)
		}
		if compatWorkspace.MainModule != nil {
			rel, _ := filepath.Rel(ws.Root, moduleDir)
			mod := pendingModule{
				Kind:              moduleLoadKindAmbient,
				Ref:               resolveLocalRef(ws, rel),
				Name:              compatWorkspace.MainModule.Name,
				Entrypoint:        compatWorkspace.MainModule.Entry.Entrypoint,
				legacyFieldPolicy: legacyWorkspaceFieldPolicyStripCompatMain,
			}
			pending = append(pending, mod)
		}
	}

	// (2) Extra modules from -m flag are stored separately in
	//     client.pendingExtraModules (already populated from clientMD).
	//     They go through the same loadModule chokepoint in ensureModulesLoaded.

	client.pendingModules = pending

	return nil
}

func console(ctx context.Context, msg string, args ...any) {
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(telemetry.GlobalWriter(ctx, ""), msg, args...)
}

func legacyWorkspaceCompatMessage(cwd, cfgPath string) string {
	relPath := cfgPath
	if rel, err := filepath.Rel(cwd, cfgPath); err == nil {
		relPath = rel
	}
	return fmt.Sprintf("No workspace config found, inferring from %s.\nRun 'dagger setup' when ready. More info: https://docs.dagger.io/reference/upgrade-to-workspaces", relPath)
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
	prebuiltSource core.WorkspaceSource,
	address string,
) (*core.Workspace, error) {
	// Capture the current client ID for routing host filesystem operations.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}

	coreWS := &core.Workspace{
		Address:    address,
		Cwd:        detected.Cwd,
		ConfigFile: detected.ConfigFile,
		LockFile:   detected.LockFile,
		ClientID:   clientMetadata.ClientID,
	}
	if coreWS.Address == "" {
		coreWS.Address = localWorkspaceAddress(detected.Root, detected.Cwd)
	}
	if isLocal {
		// Local: store host path only. Directories are resolved lazily
		// via per-call host.directory() in resolveRootfs.
		coreWS.SetHostPath(detected.Root)
		if detected.HasGitRoot {
			coreWS.SetSource(core.NewWorkspaceSourceClientLocal(detected.Root))
		} else {
			coreWS.SetSource(core.NewWorkspaceSourceRootlessLocal(detected.Root))
		}
	} else {
		// Remote: store the cloned git tree.
		coreWS.SetRootfs(prebuiltRootfs)
		if prebuiltSource != nil {
			coreWS.SetSource(prebuiltSource)
		} else {
			coreWS.SetSource(core.NewWorkspaceSourceDirectory(prebuiltRootfs))
		}
	}

	return coreWS, nil
}

func localWorkspaceAddress(root, workspaceCwd string) string {
	workspaceDir := filepath.Join(root, workspaceCwd)
	return (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(workspaceDir),
	}).String()
}

func remoteWorkspaceAddress(cloneRef, workspaceCwd, version string) string {
	return core.GitRefString(cloneRef, workspaceCwd, version)
}

// cloneGitTree clones a git repository and returns its selected ref and tree.
func (srv *Server) cloneGitTree(ctx context.Context, dag *dagql.Server, cloneRef, version string) (dagql.ObjectResult[*core.Directory], dagql.ObjectResult[*core.GitRef], error) {
	// Build the ref selector — use "head" if no version specified.
	refSelector := dagql.Selector{Field: "head"}
	if version != "" {
		refSelector = dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(version)}},
		}
	}

	var gitRef dagql.ObjectResult[*core.GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(cloneRef)},
			},
		},
		refSelector,
	)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, gitRef, fmt.Errorf("resolving repo ref: %w", err)
	}

	var tree dagql.ObjectResult[*core.Directory]
	err = dag.Select(ctx, gitRef, &tree,
		dagql.Selector{
			Field: "tree",
			Args: []dagql.NamedInput{
				{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
			},
		},
	)
	if err != nil {
		return tree, gitRef, fmt.Errorf("cloning repo: %w", err)
	}
	return tree, gitRef, nil
}

// ensureModulesLoaded loads pending modules (workspace, compat, and -m) on
// demand. filter picks which pending workspace modules this request needs (nil
// = all); the rest stay pending for a later request or resolver. Loading is
// additive, so narrowing is deferral, not exclusion. Mutex+flags (not
// sync.Once) keep transient failures retriable.
//
// With bestEffort, a module that fails to load is skipped with a warning
// instead of failing the whole batch: the demanding operation (dagger generate)
// may be exactly what repairs it — e.g. a dagger-module.toml module whose
// committed generated files don't exist yet, which loads only after its SDK
// generator runs. The skipped modules' failure messages are returned so the
// caller can surface them (e.g. GeneratorGroup.loadFailures). Genuine engine
// errors (batch resolution, arbitration, serving) stay fatal regardless.
func (srv *Server) ensureModulesLoadedMode(ctx context.Context, client *daggerClient, filter func([]pendingModule) []pendingModule, bestEffort bool) (loadFailures []string, _ error) {
	return srv.ensureModulesLoadedModeWithSuccess(ctx, client, filter, bestEffort, nil)
}

// ensureModulesLoadedModeWithSuccess runs onSuccessLocked after a successful
// load while modulesMu is still held. Callers use it for state transitions
// that must be atomic with the load becoming visible to another request.
func (srv *Server) ensureModulesLoadedModeWithSuccess(ctx context.Context, client *daggerClient, filter func([]pendingModule) []pendingModule, bestEffort bool, onSuccessLocked func()) (loadFailures []string, rerr error) {
	client.modulesMu.Lock()
	defer client.modulesMu.Unlock()
	defer func() {
		if rerr == nil && onSuccessLocked != nil {
			onSuccessLocked()
		}
	}()

	if err := srv.ensureExtraModulesLoadedLocked(ctx, client); err != nil {
		return nil, err
	}

	if len(client.pendingModules) == 0 {
		return nil, nil
	}

	demand := client.pendingModules
	if filter != nil {
		demand = filter(client.pendingModules)
	}
	// Multiple ambient entrypoint declarations may dedupe to one module; load
	// everything so the existing conflict detection still runs.
	if len(demand) > 0 && len(pendingWorkspaceEntrypointIndexes(client.pendingModules)) > 1 {
		demand = client.pendingModules
	}

	// A failed module stays pending; surface its recorded error rather than
	// reloading it. Best-effort loads skip it instead, collecting its message.
	if bestEffort {
		kept := make([]pendingModule, 0, len(demand))
		for _, mod := range demand {
			if err, ok := client.failedModules[moduleProgressName(mod)]; ok {
				loadFailures = append(loadFailures, err.Error())
				continue
			}
			kept = append(kept, mod)
		}
		demand = kept
	} else {
		for _, mod := range demand {
			if err, ok := client.failedModules[moduleProgressName(mod)]; ok {
				return nil, err
			}
		}
	}
	if len(demand) == 0 {
		return loadFailures, nil
	}

	// Wait for the client's session attachables to be available.
	// Transient failure — allow retry on next request.
	if _, err := client.getClientCaller(ctx, client.clientID); err != nil {
		return nil, fmt.Errorf("waiting for client session attachables: %w", err)
	}

	loads := gatherModuleLoadRequests(demand, nil)
	resolvedLoads, resolveErrs, err := srv.resolveModuleLoadBatch(ctx, client, loads)
	if err != nil {
		return nil, err
	}
	var firstErr error
	okLoads := make([]moduleLoadRequest, 0, len(loads))
	okResolved := make([]resolvedModuleLoad, 0, len(loads))
	served := make([]pendingModule, 0, len(loads))
	for i, load := range loads {
		if resolveErrs[i] != nil {
			loadErr := moduleLoadErr(load, resolveErrs[i])
			client.recordFailedModule(load.mod, loadErr)
			if bestEffort {
				reportSkippedModule(ctx, moduleProgressName(load.mod), loadErr)
				loadFailures = append(loadFailures, loadErr.Error())
				continue
			}
			if firstErr == nil {
				firstErr = loadErr
			}
			continue
		}
		okLoads = append(okLoads, load)
		okResolved = append(okResolved, resolvedLoads[i])
		served = append(served, load.mod)
	}
	if firstErr != nil {
		return nil, firstErr
	}

	loads, resolvedLoads = dedupeResolvedModuleLoads(okLoads, okResolved)
	if err := client.arbitrateAmbientEntrypoints(loads, resolvedLoads); err != nil {
		return nil, err
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if err := srv.serveResolvedModuleLoadsLocked(client, loads, resolvedLoads); err != nil {
		return nil, err
	}
	client.markEntrypointServed(resolvedLoads)
	client.removePendingModules(served)
	return loadFailures, nil
}

// ensureExtraModulesLoadedLocked loads -m modules. They are explicitly
// requested, so they load eagerly with sticky failures (unlike on-demand
// workspace modules). client.modulesMu must be held.
func (srv *Server) ensureExtraModulesLoadedLocked(ctx context.Context, client *daggerClient) error {
	if client.extraModulesLoaded {
		return client.extraModulesErr
	}
	if len(client.pendingExtraModules) == 0 {
		return nil
	}

	// Wait for the client's session attachables to be available.
	// Transient failure — allow retry on next request.
	if _, err := client.getClientCaller(ctx, client.clientID); err != nil {
		return fmt.Errorf("waiting for client session attachables: %w", err)
	}

	stick := func(err error) error {
		client.extraModulesErr = err
		client.extraModulesLoaded = true
		return err
	}

	loads := gatherModuleLoadRequests(nil, client.pendingExtraModules)
	resolvedLoads, resolveErrs, err := srv.resolveModuleLoadBatch(ctx, client, loads)
	if err != nil {
		return stick(err)
	}
	for i, load := range loads {
		if resolveErrs[i] != nil {
			return stick(moduleLoadErr(load, resolveErrs[i]))
		}
	}

	loads, resolvedLoads = dedupeResolvedModuleLoads(loads, resolvedLoads)
	if err := arbitrateResolvedModuleLoads(loads, resolvedLoads); err != nil {
		return stick(err)
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if err := srv.serveResolvedModuleLoadsLocked(client, loads, resolvedLoads); err != nil {
		return stick(err)
	}
	client.markEntrypointServed(resolvedLoads)

	client.extraModulesLoaded = true
	return nil
}

// resolveModuleLoadBatch resolves a batch of module loads in parallel,
// collecting per-load errors in deterministic order.
func (srv *Server) resolveModuleLoadBatch(
	ctx context.Context,
	client *daggerClient,
	loads []moduleLoadRequest,
) ([]resolvedModuleLoad, []error, error) {
	resolvedLoads := make([]resolvedModuleLoad, len(loads))
	resolveErrs := make([]error, len(loads))

	jobs := parallel.New().
		WithContextualTracer(true).
		WithLimit(moduleLoadParallelism(len(loads)))
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
		return nil, nil, fmt.Errorf("resolving modules: %w", err)
	}
	return resolvedLoads, resolveErrs, nil
}

// arbitrateAmbientEntrypoints picks whether this ambient batch's entrypoint
// candidate wins: only when none is served yet (extras outrank ambient).
// Multiple candidates in one batch are a workspace configuration error.
func (client *daggerClient) arbitrateAmbientEntrypoints(loads []moduleLoadRequest, resolved []resolvedModuleLoad) error {
	var candidates []int
	for i := range resolved {
		if resolved[i].primaryEntrypoint {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) > 1 {
		return entrypointConflictError(moduleLoadKindAmbient, candidates, loads)
	}
	if client.entrypointServed {
		for _, i := range candidates {
			resolved[i].primaryEntrypoint = false
		}
	}
	return nil
}

// markEntrypointServed flags that an entrypoint (primary or blueprint) was
// served, so later ambient candidates are demoted. client.modulesMu must be held.
func (client *daggerClient) markEntrypointServed(resolved []resolvedModuleLoad) {
	for i := range resolved {
		if resolved[i].primaryEntrypoint {
			client.entrypointServed = true
			return
		}
		for _, related := range resolved[i].related {
			if related.entrypoint {
				client.entrypointServed = true
				return
			}
		}
	}
}

func (client *daggerClient) recordFailedModule(mod pendingModule, err error) {
	// Cancellation/deadline is the request's fault, not the module's; keep it
	// retriable.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if client.failedModules == nil {
		client.failedModules = make(map[string]error)
	}
	client.failedModules[moduleProgressName(mod)] = err
}

// removePendingModules drops served modules from the pending set and records
// their names so demand filters still recognize them. Failed modules stay
// pending to keep reporting their error. client.modulesMu must be held.
func (client *daggerClient) removePendingModules(served []pendingModule) {
	names := make(map[string]struct{}, len(served))
	for _, mod := range served {
		names[moduleProgressName(mod)] = struct{}{}
	}
	remaining := client.pendingModules[:0]
	for _, mod := range client.pendingModules {
		if _, ok := names[moduleProgressName(mod)]; ok {
			if client.servedWorkspaceModuleNames == nil {
				client.servedWorkspaceModuleNames = make(map[string]struct{})
			}
			client.servedWorkspaceModuleNames[moduleProgressName(mod)] = struct{}{}
			continue
		}
		remaining = append(remaining, mod)
	}
	client.pendingModules = remaining
}

// EnsureWorkspaceModules loads the pending workspace modules a selector
// resolver (checks/generators/services) demands. Those fields validate against
// the core schema, so loading waits until resolution where include is native.
// With bestEffort, modules that fail to load are skipped with a warning instead
// of failing the operation, and their failure messages are returned for the
// caller to surface (see ensureModulesLoadedMode).
func (srv *Server) EnsureWorkspaceModules(ctx context.Context, include []string, bestEffort bool) ([]string, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return srv.ensureModulesLoadedMode(ctx, client, func(mods []pendingModule) []pendingModule {
		// runs under client.modulesMu, which also guards servedWorkspaceModuleNames
		return filterPendingWorkspaceModulesBySelectorInclude(mods, client.servedWorkspaceModuleNames, include)
	}, bestEffort)
}

// canonicalWorkspaceModuleName kebab-normalizes a name or pattern segment for
// comparison, matching the include matchers (ModTreePath.Glob/CliCase) and CLI
// command names: "myMod", "my-mod", "MyMod" are the same module. Glob
// metacharacters survive, so a glob never equals a module name.
func canonicalWorkspaceModuleName(name string) string {
	return strcase.ToKebab(name)
}

// pendingModuleCliName is the canonical name an include pattern matches against.
func pendingModuleCliName(mod pendingModule) string {
	if mod.Name != "" {
		return canonicalWorkspaceModuleName(mod.Name)
	}
	return canonicalWorkspaceModuleName(moduleProgressName(mod))
}

// knownWorkspaceModuleNames is the canonical-name set of all pending and
// already-served workspace modules.
func knownWorkspaceModuleNames(mods []pendingModule, served map[string]struct{}) map[string]struct{} {
	known := make(map[string]struct{}, len(mods)+len(served))
	for _, mod := range mods {
		known[pendingModuleCliName(mod)] = struct{}{}
	}
	for name := range served {
		known[canonicalWorkspaceModuleName(name)] = struct{}{}
	}
	return known
}

// resolveIncludePatternModules maps each pattern's leading segment (before ':')
// to a known module name. It returns the demanded names and whether any pattern
// matched nothing known; the caller decides the fallback.
func resolveIncludePatternModules(mods []pendingModule, served map[string]struct{}, include []string) (wanted map[string]struct{}, unknown bool) {
	known := knownWorkspaceModuleNames(mods, served)
	wanted = make(map[string]struct{}, len(include))
	for _, pattern := range include {
		modName, _, _ := strings.Cut(pattern, ":")
		modName = canonicalWorkspaceModuleName(modName)
		if _, ok := known[modName]; !ok {
			unknown = true
			continue
		}
		wanted[modName] = struct{}{}
	}
	return wanted, unknown
}

// filterPendingWorkspaceModulesBySelectorInclude selects the modules named by
// `dagger generate`/`check`/`up` patterns ("module" or "module:item"). A
// pattern naming no known module (an entrypoint-proxied item, or a typo)
// selects all, so the usual error surfaces. served modules are recognized but
// contribute nothing to load.
func filterPendingWorkspaceModulesBySelectorInclude(mods []pendingModule, served map[string]struct{}, include []string) []pendingModule {
	if len(mods) == 0 || len(include) == 0 {
		return mods
	}

	wanted, unknown := resolveIncludePatternModules(mods, served, include)
	if unknown {
		return mods
	}

	// Already-served wanted modules contribute nothing; result may be empty.
	filtered := make([]pendingModule, 0, len(mods))
	for _, mod := range mods {
		if _, ok := wanted[pendingModuleCliName(mod)]; ok {
			filtered = append(filtered, mod)
		}
	}
	return filtered
}

// filterPendingWorkspaceModulesForRootFields selects the pending modules a
// request's root fields reference. served modules are recognized without
// loading.
func filterPendingWorkspaceModulesForRootFields(mods []pendingModule, served map[string]struct{}, rootFields []string) []pendingModule {
	if len(mods) == 0 || rootFieldsRequireFullWorkspaceSchema(rootFields) {
		return mods
	}

	servedFields := make(map[string]struct{}, len(served))
	for name := range served {
		servedFields[strcase.ToLowerCamel(name)] = struct{}{}
	}

	selected := make([]bool, len(mods))
	unknownRootField := false
	for _, field := range rootFields {
		if isCoreRootField(field) {
			continue
		}
		if _, ok := servedFields[field]; ok {
			continue
		}
		matched := false
		for i, mod := range mods {
			if pendingModuleRootFieldName(mod) == field {
				selected[i] = true
				matched = true
			}
		}
		if !matched {
			// load<Type>FromID can reference a module type without naming the
			// module, so load everything to be safe.
			if strings.HasPrefix(field, "load") && strings.HasSuffix(field, "FromID") {
				return mods
			}
			unknownRootField = true
		}
	}

	if unknownRootField {
		entrypoints := pendingWorkspaceEntrypointIndexes(mods)
		switch len(entrypoints) {
		case 0:
			// Leave the field unresolved; GraphQL validation will report the real error.
		case 1:
			selected[entrypoints[0]] = true
		default:
			// More than one possible entrypoint could serve the field. Preserve the
			// existing behavior, including any conflict error from arbitration.
			return mods
		}
	}

	filtered := make([]pendingModule, 0, len(mods))
	for i, mod := range mods {
		if selected[i] {
			filtered = append(filtered, mod)
		}
	}
	return filtered
}

// filterPendingWorkspaceModulesForScopedRootFields applies the client-declared
// workspace module scope on top of the root-field demand: when the request's
// only full-schema demand is currentTypeDefs, the scope replaces its
// load-everything contribution with the scoped module set. Any other
// full-schema field keeps loading everything, scope untouched. The second
// result reports whether the scope was applied, so the caller can consume it.
func filterPendingWorkspaceModulesForScopedRootFields(mods []pendingModule, served map[string]struct{}, rootFields []string, scope string, entrypointServed bool) ([]pendingModule, bool) {
	if scope == "" || len(mods) == 0 {
		return filterPendingWorkspaceModulesForRootFields(mods, served, rootFields), false
	}

	hasCurrentTypeDefs := false
	remaining := make([]string, 0, len(rootFields))
	for _, field := range rootFields {
		if field == "currentTypeDefs" {
			hasCurrentTypeDefs = true
			continue
		}
		remaining = append(remaining, field)
	}
	if !hasCurrentTypeDefs || rootFieldsRequireFullWorkspaceSchema(remaining) {
		return filterPendingWorkspaceModulesForRootFields(mods, served, rootFields), false
	}

	wanted := make(map[string]struct{})
	for _, mod := range filterPendingWorkspaceModulesForRootFields(mods, served, remaining) {
		wanted[moduleProgressName(mod)] = struct{}{}
	}
	for _, mod := range resolveWorkspaceModuleScope(mods, served, scope, entrypointServed) {
		wanted[moduleProgressName(mod)] = struct{}{}
	}
	selected := make([]pendingModule, 0, len(wanted))
	for _, mod := range mods {
		if _, ok := wanted[moduleProgressName(mod)]; ok {
			selected = append(selected, mod)
		}
	}
	return selected, true
}

// resolveWorkspaceModuleScope maps the scope token to the pending modules it
// demands: the named module plus the pending entrypoint module(s) -- the token
// may be one of their root-proxied functions, and the command tree wants their
// Query-root proxies either way. A token naming nothing known demands the
// entrypoint alone when one is pending, or nothing when it is already served;
// with no entrypoint to resolve it, everything loads (conservative: the token
// could be anything).
func resolveWorkspaceModuleScope(mods []pendingModule, served map[string]struct{}, scope string, entrypointServed bool) []pendingModule {
	scopeName := canonicalWorkspaceModuleName(scope)
	_, isModule := knownWorkspaceModuleNames(mods, served)[scopeName]

	selected := make([]pendingModule, 0, len(mods))
	for _, mod := range mods {
		if (!entrypointServed && mod.Entrypoint) || (isModule && pendingModuleCliName(mod) == scopeName) {
			selected = append(selected, mod)
		}
	}
	if !isModule && !entrypointServed && len(selected) == 0 {
		return mods
	}
	return selected
}

func rootFieldsRequireFullWorkspaceSchema(fields []string) bool {
	for _, field := range fields {
		switch field {
		case "__schema",
			"__type",
			"__schemaJSONFile",
			"__workspaceModule",
			"currentModule",
			// currentTypeDefs returns the full served schema (bare `dagger
			// functions`, the in-engine MCP/LLM tool builder), so it needs
			// every workspace module.
			"currentTypeDefs",
			// env's resolver snapshots the served deps, so it needs every module
			"env":
			return true
		}
	}
	return false
}

func pendingWorkspaceEntrypointIndexes(mods []pendingModule) []int {
	var indexes []int
	for i, mod := range mods {
		if mod.Entrypoint {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func pendingModuleRootFieldName(mod pendingModule) string {
	if mod.Name != "" {
		return strcase.ToLowerCamel(mod.Name)
	}
	return strcase.ToLowerCamel(moduleProgressName(mod))
}

func isCoreRootField(field string) bool {
	switch field {
	case "__typename",
		"__schemaJSONFile",
		"__loadInputTypeDef",
		"__function",
		"__functionArg",
		"__functionArgExact",
		"__fieldTypeDef",
		"__fieldTypeDefExact",
		"__enumMemberTypeDef",
		"__enumValueTypeDef",
		"__listTypeDef",
		"__objectTypeDef",
		"__interfaceTypeDef",
		"__inputTypeDef",
		"__scalarTypeDef",
		"__enumTypeDef",
		"__workspaceModule",
		"_builtinContainer",
		"_clientFilesyncMirror",
		"_httpState",
		"_remoteGitMirror",
		"address",
		"cacheVolume",
		"changeset",
		"cloud",
		"container",
		"currentFunctionCall",
		"currentModule",
		// currentWorkspace's selector resolvers load on demand from their
		// include argument, so the root field demands nothing here
		"currentWorkspace",
		"defaultPlatform",
		"directory",
		"engine",
		// NOTE: "env" is intentionally absent — it needs the full workspace
		// (see rootFieldsRequireFullWorkspaceSchema)
		"envFile",
		"error",
		"file",
		"function",
		"generatedCode",
		"git",
		"host",
		"http",
		"json",
		"llm",
		"module",
		"moduleSource",
		"pipeline",
		"secret",
		"setSecret",
		"sourceMap",
		"typeDef",
		"version":
		return true
	default:
		return false
	}
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
	if load.mod.Kind != moduleLoadKindExtra {
		return resolved, nil
	}

	if !primary.Self().Source.Valid || primary.Self().Source.Value.Self() == nil {
		return resolved, nil
	}
	src := primary.Self().Source.Value
	defaultPathContextSrc := src
	if primary.Self().ContextSource.Valid && primary.Self().ContextSource.Value.Self() != nil {
		defaultPathContextSrc = primary.Self().ContextSource.Value
	}

	for i, toolchainSrc := range src.Self().Toolchains {
		if toolchainSrc.Self() == nil {
			continue
		}
		var cfg *modules.ModuleConfigDependency
		if i < len(src.Self().ConfigToolchains) {
			cfg = src.Self().ConfigToolchains[i]
		}
		pending := pendingRelatedModule(defaultPathContextSrc, toolchainSrc.Self(), cfg, false)
		toolchainMod, err := srv.resolveModuleSourceAsModule(ctx, dag, toolchainSrc, pending)
		if err != nil {
			return resolvedModuleLoad{}, fmt.Errorf("resolving toolchain module: %w", err)
		}
		resolved.related = append(resolved.related, resolvedServedModule{
			mod:        toolchainMod,
			entrypoint: false,
		})
	}

	if src.Self().Blueprint.Self() != nil {
		pending := pendingRelatedModule(defaultPathContextSrc, src.Self().Blueprint.Self(), src.Self().ConfigBlueprint, true)
		blueprintMod, err := srv.resolveModuleSourceAsModule(ctx, dag, src.Self().Blueprint, pending)
		if err != nil {
			return resolvedModuleLoad{}, fmt.Errorf("resolving blueprint module: %w", err)
		}
		resolved.related = append(resolved.related, resolvedServedModule{
			mod:        blueprintMod,
			entrypoint: true,
		})
		resolved.primaryEntrypoint = false
	}

	return resolved, nil
}

// serveResolvedModuleLoadsLocked serves resolved primary modules and their
// related modules (blueprints, toolchains-of-toolchains), skipping any whose
// identity an earlier batch already served. client.stateMu and client.modulesMu
// must be held.
//
// Transitive dependencies are only served for the entrypoint module — the one
// the user is interacting with via `dagger call` or `dagger shell`. This is
// needed so the client schema can resolve concrete types behind interfaces
// (e.g. a Mallard backing a Duck). Toolchain deps are NOT served globally;
// each module's deps are available in its own internal schema (mod.Deps) for
// type resolution during function calls.
func (srv *Server) serveResolvedModuleLoadsLocked(client *daggerClient, loads []moduleLoadRequest, resolved []resolvedModuleLoad) error {
	for i := range loads {
		load := resolved[i]
		key := resolvedModuleLoadIdentity(load.primary)
		if _, ok := client.servedModuleKeys[key]; ok {
			continue
		}
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
		if client.servedModuleKeys == nil {
			client.servedModuleKeys = make(map[string]struct{})
		}
		client.servedModuleKeys[key] = struct{}{}
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
				Kind:       moduleLoadKindExtra,
				Ref:        extra.Ref,
				Name:       extra.Name,
				Entrypoint: extra.Entrypoint,
			},
		})
	}
	return loads
}

func moduleLoadParallelism(moduleCount int) int {
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
	if load.mod.Kind == moduleLoadKindExtra {
		prefix = "load extra module: "
	}
	return prefix + moduleProgressName(load.mod)
}

// reportSkippedModule surfaces a best-effort load failure as its own span,
// named by the module and marked failed, so the TUI renders it like a check
// that did not pass — a concise red row with the error nested — instead of a
// verbose console line. Reveal lifts it into the primary view (e.g. the zoomed
// generators span) and the roll-up attrs collapse the load's internal spans so
// the row stays terse. GenerateSkippedAttr collects it into the persisted
// "SKIPPED MODULES" final report so it survives the live tree collapsing when
// generate exits 0.
func reportSkippedModule(ctx context.Context, name string, cause error) {
	_, span := core.Tracer(ctx).Start(ctx, name,
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.Bool(telemetryattrs.GenerateSkippedAttr, true),
		),
	)
	telemetry.EndWithCause(span, &cause)
}

func moduleLoadErr(load moduleLoadRequest, err error) error {
	prefix := "loading module"
	if load.mod.Kind == moduleLoadKindExtra {
		prefix = "loading extra module"
	}
	return fmt.Errorf("%s %q: %w", prefix, load.mod.Ref, err)
}

func entrypointTierPriority(kind moduleLoadKind) int {
	switch kind {
	case moduleLoadKindExtra:
		return 3
	case moduleLoadKindAmbient:
		return 1
	default:
		return 0
	}
}

func shouldPreferEntrypointNomination(
	currentLoad moduleLoadRequest,
	currentResolved resolvedModuleLoad,
	nextLoad moduleLoadRequest,
	nextResolved resolvedModuleLoad,
) bool {
	if !nextResolved.primaryEntrypoint {
		return false
	}
	if !currentResolved.primaryEntrypoint {
		return true
	}
	return entrypointTierPriority(nextLoad.mod.Kind) > entrypointTierPriority(currentLoad.mod.Kind)
}

func dedupeResolvedModuleLoads(
	loads []moduleLoadRequest,
	resolved []resolvedModuleLoad,
) ([]moduleLoadRequest, []resolvedModuleLoad) {
	if len(loads) <= 1 {
		return loads, resolved
	}

	type dedupedLoad struct {
		index    int
		load     moduleLoadRequest
		resolved resolvedModuleLoad
	}

	seen := make(map[string]int, len(loads))
	deduped := make([]dedupedLoad, 0, len(loads))

	for i := range loads {
		key := resolvedModuleLoadIdentity(resolved[i].primary)
		if existingIdx, ok := seen[key]; ok {
			if shouldPreferEntrypointNomination(deduped[existingIdx].load, deduped[existingIdx].resolved, loads[i], resolved[i]) {
				deduped[existingIdx].index = i
				deduped[existingIdx].load = loads[i]
				deduped[existingIdx].resolved = resolved[i]
			}
			continue
		}
		deduped = append(deduped, dedupedLoad{
			index:    i,
			load:     loads[i],
			resolved: resolved[i],
		})
		seen[key] = len(deduped) - 1
	}

	slices.SortFunc(deduped, func(a, b dedupedLoad) int {
		switch {
		case a.index < b.index:
			return -1
		case a.index > b.index:
			return 1
		default:
			return 0
		}
	})

	dedupLoads := make([]moduleLoadRequest, len(deduped))
	dedupResolved := make([]resolvedModuleLoad, len(deduped))
	for i := range deduped {
		dedupLoads[i] = deduped[i].load
		dedupResolved[i] = deduped[i].resolved
	}

	return dedupLoads, dedupResolved
}

func arbitrateResolvedModuleLoads(
	loads []moduleLoadRequest,
	resolved []resolvedModuleLoad,
) error {
	if len(loads) == 0 {
		return nil
	}

	candidatesByTier := map[moduleLoadKind][]int{
		moduleLoadKindAmbient: nil,
		moduleLoadKindExtra:   nil,
	}
	for i := range loads {
		if !resolved[i].primaryEntrypoint {
			continue
		}
		candidatesByTier[loads[i].mod.Kind] = append(candidatesByTier[loads[i].mod.Kind], i)
	}

	for _, kind := range []moduleLoadKind{moduleLoadKindAmbient, moduleLoadKindExtra} {
		if len(candidatesByTier[kind]) > 1 {
			return entrypointConflictError(kind, candidatesByTier[kind], loads)
		}
	}

	winner := -1
	for _, kind := range []moduleLoadKind{moduleLoadKindExtra, moduleLoadKindAmbient} {
		if len(candidatesByTier[kind]) == 1 {
			winner = candidatesByTier[kind][0]
			break
		}
	}

	for i := range resolved {
		resolved[i].primaryEntrypoint = i == winner
	}

	return nil
}

func entrypointConflictError(kind moduleLoadKind, indexes []int, loads []moduleLoadRequest) error {
	names := make([]string, 0, len(indexes))
	for _, i := range indexes {
		names = append(names, moduleProgressName(loads[i].mod))
	}
	switch kind {
	case moduleLoadKindAmbient:
		return fmt.Errorf("invalid workspace configuration: multiple distinct ambient entrypoint modules: %s", strings.Join(names, ", "))
	case moduleLoadKindExtra:
		return fmt.Errorf("invalid extra-module request: multiple distinct extra-module entrypoints: %s", strings.Join(names, ", "))
	default:
		return fmt.Errorf("multiple distinct entrypoint modules: %s", strings.Join(names, ", "))
	}
}

func resolvedModuleLoadIdentity(mod dagql.ObjectResult[*core.Module]) string {
	self := mod.Self()
	if self == nil || self.GetSource() == nil {
		if self == nil {
			return ""
		}
		return "name:" + self.Name()
	}
	return canonicalModuleReference(self.GetSource()) + "|" + self.GetSource().Pin()
}

// resolveModule resolves a module through the dagql pipeline.
// Handles all module sources uniformly: legacy compat modules and -m flag modules.
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

	if src.Self().UsesLegacyWorkspaceFields() {
		switch mod.legacyFieldPolicy {
		case legacyWorkspaceFieldPolicyDirect:
			return dagql.ObjectResult[*core.Module]{}, src.Self().DirectLegacyWorkspaceLoadError()
		case legacyWorkspaceFieldPolicyStripCompatMain:
			srcCall, err := src.ResultCall()
			if err != nil {
				return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("failed to get module source call for %q: %w", mod.Ref, err)
			}
			stripped, err := dagql.NewObjectResultForCall(src.Self().StripLegacyWorkspaceFields(), dag, srcCall)
			if err != nil {
				return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("failed to strip legacy workspace fields from %q: %w", mod.Ref, err)
			}
			src = stripped
		case legacyWorkspaceFieldPolicyRejectAsWorkspace:
			return dagql.ObjectResult[*core.Module]{}, src.Self().NestedLegacyWorkspaceLoadError()
		}
	}

	return srv.resolveModuleSourceAsModule(ctx, dag, src, mod)
}

// pendingRelatedModule adapts a related module of a primary module into the
// existing pendingModule loading path.
//
// Related modules are modules attached through the primary module source's
// blueprint or toolchains. They are resolved separately from regular
// dependencies but still need the same legacy compat handling as modules loaded
// from workspace discovery, such as legacy default-path resolution and
// dagger.json argument customizations.
//
// defaultPathContextSrc is the consuming module source for +defaultPath
// arguments. For a plain module this is the module source itself; for a module
// already resolving +defaultPath through another source, it is that outer
// ContextSource.
func pendingRelatedModule(
	defaultPathContextSrc dagql.ObjectResult[*core.ModuleSource],
	related *core.ModuleSource,
	cfg *modules.ModuleConfigDependency,
	entrypoint bool,
) pendingModule {
	mod := pendingModule{
		Kind:       moduleLoadKindExtra,
		Ref:        related.AsString(),
		RefPin:     related.Pin(),
		Entrypoint: entrypoint,
		// LegacyDefaultPath is intentionally not set here. Related modules
		// are loaded as siblings of an explicit -m entrypoint: their
		// +defaultPath must resolve against that entrypoint's repo (the
		// -m argument), not against the session's currentWorkspace — the
		// latter is the user's CWD, which may be empty, partial, or a
		// different checkout entirely.
	}
	if defaultPathContextSrc.Self() != nil {
		mod.DefaultPathContextSourceRef = defaultPathContextSrc.Self().AsString()
		mod.DefaultPathContextSourcePin = defaultPathContextSrc.Self().Pin()
	}
	if cfg != nil {
		if cfg.Name != "" {
			mod.Name = cfg.Name
		}
		mod.ConfigDefaults = workspace.ExtractConfigDefaults(cfg.Customizations)
		mod.ArgCustomizations = cfg.Customizations
	}
	if entrypoint && defaultPathContextSrc.Self() != nil && defaultPathContextSrc.Self().Kind == core.ModuleSourceKindLocal {
		mod.LegacyCallerModuleDir = defaultPathContextSrc.Self().AsString()
	}
	return mod
}

func (srv *Server) resolveModuleSourceAsModule(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
	mod pendingModule,
) (dagql.ObjectResult[*core.Module], error) {
	if mod.Ref == "" && src.Self() != nil {
		mod.Ref = src.Self().AsString()
	}
	if mod.RefPin == "" && src.Self() != nil {
		mod.RefPin = src.Self().Pin()
	}
	if mod.LegacyCallerModuleDir != "" && mod.Entrypoint {
		if err := srv.mergeLegacyCallerEnvDefaults(ctx, dag, src.Self(), mod.LegacyCallerModuleDir); err != nil {
			return dagql.ObjectResult[*core.Module]{}, err
		}
	}

	asModuleArgs, err := asModuleArgsForPendingModule(mod)
	if err != nil {
		return dagql.ObjectResult[*core.Module]{}, err
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

func asModuleArgsForPendingModule(mod pendingModule) ([]dagql.NamedInput, error) {
	// Delegates to the shared builder so the workspace entrypoint path and the
	// dependency-graph toolchain load path (loadDependencyModules) produce a
	// byte-identical AsModuleVariantDigest salt for the same logical module.
	args, err := schema.BuildLegacyAsModuleArgs(
		mod.Name,
		mod.LegacyDefaultPath,
		mod.DefaultPathContextSourceRef,
		mod.DefaultPathContextSourcePin,
		mod.ConfigDefaults,
		mod.DefaultsFromDotEnv,
		mod.ArgCustomizations,
	)
	if err != nil {
		return nil, fmt.Errorf("build asModule args for %q: %w", mod.Ref, err)
	}
	return args, nil
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
