package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	telemetry "github.com/dagger/otel-go"
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
		return filepath.Join(ws.Root, ws.Path, relPath)
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

	// If set, load this module's implementation from Ref but resolve
	// +defaultPath inputs from this source ref instead.
	DefaultPathContextSourceRef string
	DefaultPathContextSourcePin string

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
	moduleLoadKindCWD     moduleLoadKind = "cwd"
	moduleLoadKindExtra   moduleLoadKind = "extra"
)

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

func loadWorkspaceConfig(
	ctx context.Context,
	readFile func(context.Context, string) ([]byte, error),
	ws *workspace.Workspace,
) (*workspace.Config, error) {
	configPath := filepath.Join(ws.Root, ws.Path, workspace.LockDirName, workspace.ConfigFileName)
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

	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	slices.Sort(names)

	pending := make([]pendingModule, 0, len(names))
	for _, name := range names {
		entry := cfg.Modules[name]
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
			resolved := workspace.ResolveModuleEntrySource(workspace.LockDirName, entry.Source)
			if filepath.IsAbs(resolved) {
				mod.Ref = resolved
			} else {
				mod.Ref = resolveLocalRef(ws, resolved)
			}
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

func hasPendingExtraModules(client *daggerClient) bool {
	return len(client.pendingExtraModules) > 0
}

func suppressPendingCWDModules(mods []pendingModule) []pendingModule {
	if len(mods) == 0 {
		return nil
	}
	filtered := mods[:0]
	for _, mod := range mods {
		if mod.Kind == moduleLoadKindCWD {
			continue
		}
		filtered = append(filtered, mod)
	}
	return filtered
}

func suppressCWDModuleForCompatWorkspace(compatWorkspace *workspace.CompatWorkspace, moduleDir string) bool {
	if compatWorkspace == nil || compatWorkspace.ProjectRoot == "" || moduleDir == "" {
		return false
	}
	return filepath.Clean(compatWorkspace.ProjectRoot) == filepath.Clean(moduleDir)
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
	loadModules := client.pendingWorkspaceLoad &&
		clientMD != nil &&
		clientMD.LoadWorkspaceModules &&
		!clientMD.SkipWorkspaceModules
	workspaceEnv, hasWorkspaceEnv := workspaceEnvFromClientMetadata(clientMD)

	// --- Detect workspace (pure — no dagger.json knowledge) ---
	ws, err := workspace.Detect(ctx, func(ctx context.Context, path string) (string, bool, error) {
		return core.StatFSExists(ctx, statFS, path)
	}, readFile, cwd)
	if err != nil {
		return err
	}

	var wsConfig *workspace.Config
	if ws.Initialized {
		wsConfig, err = loadWorkspaceConfig(ctx, readFile, ws)
		if err != nil {
			return err
		}
	}

	// --- Compat mode: build the ambient compat workspace from legacy dagger.json ---
	// Once an initialized workspace config exists, it owns ambient workspace
	// module loading. Legacy dagger.json compatibility remains only for
	// uninitialized workspaces.
	var compatWorkspace *workspace.CompatWorkspace
	moduleDir, hasModuleConfig, _ := core.Host{}.FindUp(ctx, statFS, cwd, workspace.ModuleConfigFileName)
	legacyCallerDir := legacyCallerModuleDir(isLocal, moduleDir)
	if wsConfig == nil && hasModuleConfig {
		cfgPath := filepath.Join(moduleDir, workspace.ModuleConfigFileName)
		if data, readErr := readFile(ctx, cfgPath); readErr == nil {
			compatWorkspace, _ = workspace.ParseCompatWorkspaceAt(data, cfgPath)
		}
		if compatWorkspace != nil {
			msg := legacyWorkspaceCompatMessage(cwd, cfgPath)
			console(ctx, msg)
			slog.Warn(msg,
				"config", cfgPath)
		}
	} else if wsConfig == nil {
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
	coreWS.SetCompatWorkspace(compatWorkspace)
	client.workspace = coreWS

	if !loadModules {
		return nil
	}

	if hasWorkspaceEnv {
		if wsConfig == nil {
			return fmt.Errorf("workspace env %q requires .dagger/config.toml", workspaceEnv)
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

	// (1) Ambient compat-workspace modules projected from legacy dagger.json.
	if compatWorkspace != nil {
		for _, legacyMod := range compatWorkspace.Modules {
			mod := pendingLegacyModule(
				ws,
				resolveLocalRef,
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
			wsDir := filepath.Join(ws.Root, ws.Path)
			rel, _ := filepath.Rel(wsDir, moduleDir)
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

	// (2) CWD module (nearest dagger.json by find-up from the caller)
	if hasModuleConfig && !hasPendingExtraModules(client) && !suppressCWDModuleForCompatWorkspace(compatWorkspace, moduleDir) {
		wsDir := filepath.Join(ws.Root, ws.Path)
		rel, _ := filepath.Rel(wsDir, moduleDir)
		pending = append(pending, pendingModule{
			Kind:       moduleLoadKindCWD,
			Ref:        resolveLocalRef(ws, rel),
			Name:       cwdModuleName(ctx, readFile, moduleDir),
			Entrypoint: true,
		})
	}

	// (3) Extra modules from -m flag are stored separately in
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
	return fmt.Sprintf("No workspace config found, inferring from %s.\nRun 'dagger migrate' when ready. More info: https://docs.dagger.io/reference/upgrade-to-workspaces", relPath)
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
		Address:     address,
		Path:        detected.Path,
		Initialized: detected.Initialized,
		HasConfig:   detected.Initialized,
		ClientID:    clientMetadata.ClientID,
	}
	if coreWS.Address == "" {
		coreWS.Address = localWorkspaceAddress(detected.Root, detected.Path)
	}
	if detected.Initialized {
		coreWS.ConfigPath = filepath.Join(detected.Path, workspace.LockDirName, workspace.ConfigFileName)
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

	loads, resolvedLoads = dedupeResolvedModuleLoads(loads, resolvedLoads)
	if err := arbitrateResolvedModuleLoads(loads, resolvedLoads); err != nil {
		client.modulesErr = err
		client.modulesLoaded = true
		return client.modulesErr
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
				Kind:       moduleLoadKindExtra,
				Ref:        extra.Ref,
				Name:       extra.Name,
				Entrypoint: extra.Entrypoint,
			},
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
	if load.mod.Kind == moduleLoadKindExtra {
		prefix = "load extra module: "
	}
	return prefix + moduleProgressName(load.mod)
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
	case moduleLoadKindCWD:
		return 2
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
		moduleLoadKindCWD:     nil,
		moduleLoadKindExtra:   nil,
	}
	for i := range loads {
		if !resolved[i].primaryEntrypoint {
			continue
		}
		candidatesByTier[loads[i].mod.Kind] = append(candidatesByTier[loads[i].mod.Kind], i)
	}

	for _, kind := range []moduleLoadKind{moduleLoadKindAmbient, moduleLoadKindCWD, moduleLoadKindExtra} {
		if len(candidatesByTier[kind]) > 1 {
			return entrypointConflictError(kind, candidatesByTier[kind], loads)
		}
	}

	winner := -1
	for _, kind := range []moduleLoadKind{moduleLoadKindExtra, moduleLoadKindCWD, moduleLoadKindAmbient} {
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
	case moduleLoadKindCWD:
		return fmt.Errorf("internal error: multiple distinct cwd entrypoint modules: %s", strings.Join(names, ", "))
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
	if src.Self().UsesLegacyWorkspaceFields() {
		switch mod.legacyFieldPolicy {
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
		mod.ConfigDefaults = legacyConfigDefaults(cfg.Customizations)
		mod.ArgCustomizations = cfg.Customizations
	}
	if entrypoint && defaultPathContextSrc.Self() != nil && defaultPathContextSrc.Self().Kind == core.ModuleSourceKindLocal {
		mod.LegacyCallerModuleDir = defaultPathContextSrc.Self().AsString()
	}
	return mod
}

func legacyConfigDefaults(customizations []*modules.ModuleConfigArgument) map[string]any {
	config := make(map[string]any)
	for _, cust := range customizations {
		if cust != nil && len(cust.Function) == 0 && cust.Default != "" {
			config[cust.Argument] = cust.Default
		}
	}
	if len(config) == 0 {
		return nil
	}
	return config
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
	if mod.DefaultPathContextSourceRef != "" {
		asModuleArgs = append(asModuleArgs, dagql.NamedInput{
			Name: "defaultPathContextSourceRef", Value: dagql.String(mod.DefaultPathContextSourceRef),
		})
		if mod.DefaultPathContextSourcePin != "" {
			asModuleArgs = append(asModuleArgs, dagql.NamedInput{
				Name: "defaultPathContextSourcePin", Value: dagql.String(mod.DefaultPathContextSourcePin),
			})
		}
	}
	if len(mod.ConfigDefaults) > 0 {
		wsJSON, err := json.Marshal(mod.ConfigDefaults)
		if err != nil {
			return nil, fmt.Errorf("encoding workspace config for %q: %w", mod.Ref, err)
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
			return nil, fmt.Errorf("encoding arg customizations for %q: %w", mod.Ref, err)
		}
		asModuleArgs = append(asModuleArgs, dagql.NamedInput{
			Name: "legacyArgCustomizationsJson", Value: dagql.String(string(custJSON)),
		})
	}
	return asModuleArgs, nil
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
