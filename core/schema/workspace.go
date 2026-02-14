package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.FuncWithCacheKey("currentWorkspace", s.currentWorkspace, dagql.CachePerCall).
			Doc("Detect and return the current workspace."),
	}.Install(srv)

	dagql.Fields[*core.Workspace]{
		dagql.NodeFuncWithCacheKey("directory",
			DagOpDirectoryWrapper(
				srv, s.directory,
				WithHashContentDir[*core.Workspace, workspaceDirectoryArgs](),
			), dagql.CacheAsRequested).
			Doc(`Returns a Directory from the workspace.`,
				`Relative paths resolve from the workspace root. Absolute paths resolve from the sandbox root.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve. Relative paths (e.g., "src") resolve from workspace root; absolute paths (e.g., "/src") resolve from sandbox root.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
			),
		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CacheAsRequested).
			Doc(`Returns a File from the workspace.`,
				`Relative paths resolve from the workspace root. Absolute paths resolve from the sandbox root.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve. Relative paths (e.g., "go.mod") resolve from workspace root; absolute paths (e.g., "/go.mod") resolve from sandbox root.`),
			),
		dagql.Func("init", s.workspaceInit).
			DoNotCache("Mutates workspace on host").
			Doc("Initialize a new workspace, creating .dagger/config.toml."),
		dagql.Func("install", s.install).
			DoNotCache("Mutates workspace config on host").
			Doc("Install a module into the workspace, writing config.toml to the host.").
			Args(
				dagql.Arg("ref").Doc("Module reference string (git URL or local path)."),
				dagql.Arg("name").Doc("Override name for the installed module entry."),
			),
		dagql.Func("moduleInit", s.moduleInit).
			DoNotCache("Mutates workspace and host filesystem").
			Doc("Create a new module in the workspace, scaffold its files, and auto-install it in config.toml.").
			Args(
				dagql.Arg("name").Doc("Name of the new module."),
				dagql.Arg("sdk").Doc("SDK to use (go, python, typescript)."),
				dagql.Arg("source").Doc("Source subpath within the module root."),
				dagql.Arg("include").Doc("Additional include patterns for the module."),
			),
		dagql.Func("configRead", s.configRead).
			DoNotCache("Reads live config from host").
			Doc("Read a configuration value from config.toml.",
				"If key is empty, returns the full config. "+
					"If key points to a scalar, returns the value. "+
					"If key points to a table, returns flattened dotted-key output.").
			Args(
				dagql.Arg("key").Doc("Dotted key path (e.g. modules.mymod.source). Empty for full config."),
			),
		dagql.Func("configWrite", s.configWrite).
			DoNotCache("Mutates workspace config on host").
			Doc("Write a configuration value to config.toml.",
				"Validates the key against the config schema and auto-detects value types.").
			Args(
				dagql.Arg("key").Doc("Dotted key path (e.g. modules.mymod.source)."),
				dagql.Arg("value").Doc("Value to set. Bools, integers, and comma-separated arrays are auto-detected."),
			),
		dagql.Func("checks", s.checks).
			Doc("Return all checks from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include checks matching the specified patterns"),
			),
		dagql.Func("generators", s.generators).
			Doc("Return all generators from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include generators matching the specified patterns"),
			),
	}.Install(srv)
}

func (s *workspaceSchema) currentWorkspace(
	ctx context.Context,
	parent *core.Query,
	_ struct{},
) (*core.Workspace, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildkit: %w", err)
	}
	cwd, err := bk.AbsPath(ctx, ".")
	if err != nil {
		return nil, fmt.Errorf("cwd: %w", err)
	}

	statFS := core.NewCallerStatFS(bk)
	ws, err := workspace.Detect(ctx, statFS, bk.ReadCallerHostFile, cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	// Capture the current client ID so that when this workspace is passed to
	// a module function, the directory/file resolvers can route host filesystem
	// operations through the correct (original) client session.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}

	result := &core.Workspace{
		SandboxRoot: ws.SandboxRoot,
		Path:        ws.Path,
		Initialized: ws.Initialized,
		HasConfig:   ws.Config != nil,
		ClientID:    clientMetadata.ClientID,
	}
	if ws.Config != nil {
		result.ConfigPath = filepath.Join(ws.SandboxRoot, ws.Path, workspace.WorkspaceDirName, workspace.ConfigFileName)
	}

	return result, nil
}

type workspaceDirectoryArgs struct {
	Path string

	core.CopyFilter

	DagOpInternalArgs
}

func (workspaceDirectoryArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerClient
}

func (s *workspaceSchema) directory(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceDirectoryArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	ws := parent.Self()

	// Override the client metadata in context to the workspace's owning client
	// so that host filesystem operations route through the correct session.
	// This is necessary when the workspace is passed to a module function —
	// the module's own session doesn't have access to the host filesystem.
	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	absPath, err := resolveWorkspacePath(args.Path, ws.SandboxRoot, ws.Path)
	if err != nil {
		return inst, err
	}

	dirArgs := []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString(absPath)},
	}
	if len(args.Include) > 0 {
		includes := make(dagql.ArrayInput[dagql.String], len(args.Include))
		for i, p := range args.Include {
			includes[i] = dagql.String(p)
		}
		dirArgs = append(dirArgs, dagql.NamedInput{Name: "include", Value: includes})
	}
	if len(args.Exclude) > 0 {
		excludes := make(dagql.ArrayInput[dagql.String], len(args.Exclude))
		for i, p := range args.Exclude {
			excludes[i] = dagql.String(p)
		}
		dirArgs = append(dirArgs, dagql.NamedInput{Name: "exclude", Value: excludes})
	}

	err = srv.Select(ctx, srv.Root(), &inst,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "directory", Args: dirArgs},
	)
	if err != nil {
		return inst, fmt.Errorf("workspace directory %q: %w", args.Path, err)
	}

	return inst, nil
}

type workspaceFileArgs struct {
	Path string
}

func (workspaceFileArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerClient
}

func (s *workspaceSchema) file(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceFileArgs) (inst dagql.Result[*core.File], _ error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	absPath, err := resolveWorkspacePath(args.Path, ws.SandboxRoot, ws.Path)
	if err != nil {
		return inst, err
	}
	fileDir, fileName := filepath.Split(absPath)

	if err := srv.Select(ctx, srv.Root(), &inst,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(fileDir)},
				{Name: "include", Value: dagql.ArrayInput[dagql.String]{dagql.NewString(fileName)}},
			},
		},
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(fileName)},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("workspace file %q: %w", args.Path, err)
	}

	return inst, nil
}

// resolveWorkspacePath resolves a path with two scopes:
//   - Relative paths resolve relative to the workspace directory (sandboxRoot/workspacePath).
//   - Absolute paths resolve relative to the sandbox root.
//
// All resolved paths are clamped to the sandbox root.
func resolveWorkspacePath(path, sandboxRoot, workspacePath string) (string, error) {
	clean := filepath.Clean(path)

	// Reject .. traversal
	if containsDotDot(clean) {
		return "", fmt.Errorf("path %q: '..' traversal not allowed", path)
	}

	var resolved string
	if filepath.IsAbs(clean) {
		// Absolute path: relative to sandbox root
		resolved = filepath.Join(sandboxRoot, clean[1:])
	} else {
		// Relative path: relative to workspace path within sandbox
		resolved = filepath.Join(sandboxRoot, workspacePath, clean)
	}

	// Clamp to sandbox root
	rootPrefix := filepath.Clean(sandboxRoot) + string(filepath.Separator)
	if resolved != filepath.Clean(sandboxRoot) && !strings.HasPrefix(resolved, rootPrefix) {
		return "", fmt.Errorf("path %q resolves outside sandbox root %q", path, sandboxRoot)
	}

	return resolved, nil
}

// containsDotDot reports whether the cleaned path contains a ".." component.
func containsDotDot(cleanPath string) bool {
	for _, part := range strings.Split(cleanPath, string(filepath.Separator)) {
		if part == ".." {
			return true
		}
	}
	return false
}

// withWorkspaceClientContext overrides the client metadata in context to the
// workspace's owning client ID. This ensures host filesystem operations route
// through the correct client session, even when called from a module context.
func (s *workspaceSchema) withWorkspaceClientContext(ctx context.Context, ws *core.Workspace) (context.Context, error) {
	if ws.ClientID == "" {
		return ctx, nil
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ctx, fmt.Errorf("get client metadata: %w", err)
	}
	if clientMetadata.ClientID == ws.ClientID {
		return ctx, nil // already in the right context
	}
	// Clone metadata and override the client ID to the workspace owner's
	override := *clientMetadata
	override.ClientID = ws.ClientID
	return engine.ContextWithClientMetadata(ctx, &override), nil
}

func (s *workspaceSchema) workspaceInit(
	ctx context.Context,
	parent *core.Workspace,
	args struct{},
) (dagql.String, error) {
	if parent.Initialized {
		daggerDir := filepath.Join(parent.SandboxRoot, parent.Path, workspace.WorkspaceDirName)
		return "", fmt.Errorf("workspace already initialized at %s", daggerDir)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	if err := ensureWorkspaceInitialized(ctx, bk, parent); err != nil {
		return "", err
	}

	daggerDir := filepath.Join(parent.SandboxRoot, parent.Path, workspace.WorkspaceDirName)
	return dagql.String(daggerDir), nil
}

// ensureWorkspaceInitialized creates .dagger/config.toml if the workspace is not yet initialized.
// This is the single code path for workspace initialization — install() and moduleInit() call it too.
func ensureWorkspaceInitialized(ctx context.Context, bk *buildkit.Client, ws *core.Workspace) error {
	if ws.Initialized {
		return nil // already initialized
	}

	sampleConfig := []byte(`# Dagger workspace configuration
# Install modules with: dagger install <module>
# Example:
#   dagger install github.com/dagger/dagger/modules/wolfi

[modules]
`)

	if err := exportConfigToHost(ctx, bk, ws, sampleConfig); err != nil {
		return fmt.Errorf("initializing workspace: %w", err)
	}
	ws.Initialized = true
	ws.HasConfig = true
	workspaceAbsPath := filepath.Join(ws.SandboxRoot, ws.Path)
	ws.ConfigPath = filepath.Join(workspaceAbsPath, workspace.WorkspaceDirName, workspace.ConfigFileName)
	return nil
}

type installArgs struct {
	Ref  string
	Name string `default:""`
}

func (s *workspaceSchema) install(
	ctx context.Context,
	parent *core.Workspace,
	args installArgs,
) (dagql.String, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	// Ensure workspace is initialized before installing
	if err := ensureWorkspaceInitialized(ctx, bk, parent); err != nil {
		return "", err
	}

	// Read current workspace config (re-read from host for fresh state)
	cfg, err := readWorkspaceConfig(ctx, bk, parent)
	if err != nil {
		return "", err
	}

	// Resolve module name and kind via dagql pipeline
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("dagql server: %w", err)
	}

	var moduleName dagql.String
	err = srv.Select(ctx, srv.Root(), &moduleName,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(args.Ref)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{Field: "moduleName"},
	)
	if err != nil {
		return "", fmt.Errorf("resolve module name: %w", err)
	}

	name := string(moduleName)
	if args.Name != "" {
		name = args.Name
	}

	// Determine source path
	sourcePath := args.Ref
	var kind core.ModuleSourceKind
	err = srv.Select(ctx, srv.Root(), &kind,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(args.Ref)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{Field: "kind"},
	)
	if err != nil {
		return "", fmt.Errorf("resolve module kind: %w", err)
	}

	if kind == core.ModuleSourceKindLocal {
		var contextDirPath dagql.String
		err = srv.Select(ctx, srv.Root(), &contextDirPath,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(args.Ref)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			},
			dagql.Selector{Field: "localContextDirectoryPath"},
		)
		if err != nil {
			return "", fmt.Errorf("local context dir: %w", err)
		}

		var depRootSubpath dagql.String
		err = srv.Select(ctx, srv.Root(), &depRootSubpath,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(args.Ref)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			},
			dagql.Selector{Field: "sourceRootSubpath"},
		)
		if err != nil {
			return "", fmt.Errorf("source root subpath: %w", err)
		}

		depAbsPath := filepath.Join(string(contextDirPath), string(depRootSubpath))
		workspaceAbsPath := filepath.Join(parent.SandboxRoot, parent.Path)
		daggerDir := filepath.Join(workspaceAbsPath, workspace.WorkspaceDirName)
		relPath, err := filepath.Rel(daggerDir, depAbsPath)
		if err != nil {
			return "", fmt.Errorf("compute relative path: %w", err)
		}
		sourcePath = relPath
	}

	// Check if already installed with same source
	if existing, ok := cfg.Modules[name]; ok && existing.Source == sourcePath {
		return dagql.String(fmt.Sprintf("Module %q is already installed", name)), nil
	}

	// Introspect constructor args for config hints (graceful degradation)
	var hints map[string][]workspace.ConstructorArgHint
	constructorArgs, err := IntrospectConstructorArgs(ctx, srv, args.Ref)
	if err != nil {
		slog.Warn("could not introspect constructor args for config hints", "module", name, "error", err)
	} else if len(constructorArgs) > 0 {
		hints = map[string][]workspace.ConstructorArgHint{
			name: constructorArgs,
		}
	}

	// Add module to config
	cfg.Modules[name] = workspace.ModuleEntry{Source: sourcePath}

	// Read existing raw TOML for comment preservation
	var existingTOML []byte
	if parent.HasConfig {
		existingTOML, _ = bk.ReadCallerHostFile(ctx, parent.ConfigPath)
	}

	// Write config with hints (preserving existing comments)
	if err := writeWorkspaceConfigWithHints(ctx, bk, parent, cfg, existingTOML, hints); err != nil {
		return "", err
	}

	workspaceAbsPath := filepath.Join(parent.SandboxRoot, parent.Path)
	configPath := filepath.Join(workspaceAbsPath, workspace.WorkspaceDirName, workspace.ConfigFileName)
	return dagql.String(fmt.Sprintf("Installed module %q in %s", name, configPath)), nil
}

type moduleInitArgs struct {
	Name    string
	SDK     string
	Source  string   `default:""`
	Include []string `default:"[]"`
}

func (s *workspaceSchema) moduleInit(
	ctx context.Context,
	parent *core.Workspace,
	args moduleInitArgs,
) (dagql.String, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	// Ensure workspace is initialized before creating a module
	if err := ensureWorkspaceInitialized(ctx, bk, parent); err != nil {
		return "", err
	}

	// Module lives at .dagger/modules/<name>/ relative to workspace root
	workspaceAbsPath := filepath.Join(parent.SandboxRoot, parent.Path)
	modulePath := filepath.Join(workspaceAbsPath, workspace.WorkspaceDirName, "modules", args.Name)

	// Make path relative to cwd for the moduleSource resolver
	cwd, err := bk.AbsPath(ctx, ".")
	if err != nil {
		return "", fmt.Errorf("cwd: %w", err)
	}
	relPath, err := filepath.Rel(cwd, modulePath)
	if err != nil {
		return "", fmt.Errorf("compute relative path: %w", err)
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("dagql server: %w", err)
	}

	// Check if module already exists
	var configExists dagql.Boolean
	err = srv.Select(ctx, srv.Root(), &configExists,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(relPath)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
				{Name: "allowNotExists", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{Field: "configExists"},
	)
	if err != nil {
		return "", fmt.Errorf("check module exists: %w", err)
	}
	if bool(configExists) {
		return "", fmt.Errorf("module %q already exists at %s", args.Name, modulePath)
	}

	// Get the context directory path for export
	var contextDirPath dagql.String
	err = srv.Select(ctx, srv.Root(), &contextDirPath,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(relPath)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
				{Name: "allowNotExists", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{Field: "localContextDirectoryPath"},
	)
	if err != nil {
		return "", fmt.Errorf("local context dir: %w", err)
	}

	// Build the moduleSource pipeline: withName → withSDK → withEngineVersion → generatedContextDirectory
	// Then export to host
	selectors := []dagql.Selector{
		{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(relPath)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
				{Name: "allowNotExists", Value: dagql.Boolean(true)},
			},
		},
		{
			Field: "withName",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(args.Name)}},
		},
		{
			Field: "withSDK",
			Args:  []dagql.NamedInput{{Name: "source", Value: dagql.String(args.SDK)}},
		},
	}

	if args.Source != "" {
		selectors = append(selectors, dagql.Selector{
			Field: "withSourceSubpath",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(args.Source)}},
		})
	}

	if len(args.Include) > 0 {
		patterns := make(dagql.ArrayInput[dagql.String], len(args.Include))
		for i, inc := range args.Include {
			patterns[i] = dagql.String(inc)
		}
		selectors = append(selectors, dagql.Selector{
			Field: "withIncludes",
			Args:  []dagql.NamedInput{{Name: "patterns", Value: patterns}},
		})
	}

	selectors = append(selectors, dagql.Selector{
		Field: "withEngineVersion",
		Args:  []dagql.NamedInput{{Name: "version", Value: dagql.String(modules.EngineVersionLatest)}},
	})
	selectors = append(selectors, dagql.Selector{Field: "generatedContextDirectory"})
	selectors = append(selectors, dagql.Selector{
		Field: "export",
		Args: []dagql.NamedInput{
			{Name: "path", Value: contextDirPath},
		},
	})

	var exported string
	err = srv.Select(ctx, srv.Root(), &exported, selectors...)
	if err != nil {
		return "", fmt.Errorf("generate module: %w", err)
	}

	// Auto-install in workspace config
	cfg, err := readWorkspaceConfig(ctx, bk, parent)
	if err != nil {
		return "", err
	}

	sourcePath := filepath.Join("modules", args.Name)
	cfg.Modules[args.Name] = workspace.ModuleEntry{Source: sourcePath}

	if err := writeWorkspaceConfig(ctx, bk, parent, cfg); err != nil {
		return "", err
	}

	configPath := filepath.Join(workspaceAbsPath, workspace.WorkspaceDirName, workspace.ConfigFileName)
	return dagql.String(fmt.Sprintf("Created module %q at %s\nInstalled in %s", args.Name, modulePath, configPath)), nil
}

type configReadArgs struct {
	Key string `default:""`
}

func (s *workspaceSchema) configRead(
	ctx context.Context,
	parent *core.Workspace,
	args configReadArgs,
) (dagql.String, error) {
	if !parent.HasConfig {
		return "", fmt.Errorf("no config.toml found in workspace")
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	data, err := bk.ReadCallerHostFile(ctx, parent.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("reading config: %w", err)
	}

	result, err := workspace.ReadConfigValue(data, args.Key)
	if err != nil {
		return "", err
	}

	return dagql.String(result), nil
}

type configWriteArgs struct {
	Key   string
	Value string
}

func (s *workspaceSchema) configWrite(
	ctx context.Context,
	parent *core.Workspace,
	args configWriteArgs,
) (dagql.String, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	var existingData []byte
	if parent.HasConfig {
		existingData, _ = bk.ReadCallerHostFile(ctx, parent.ConfigPath)
	}

	result, err := workspace.WriteConfigValue(existingData, args.Key, args.Value)
	if err != nil {
		return "", err
	}

	if err := exportConfigToHost(ctx, bk, parent, result); err != nil {
		return "", err
	}

	return dagql.String(args.Value), nil
}

func (s *workspaceSchema) checks(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.CheckGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	deps, err := query.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("current served deps: %w", err)
	}

	var allChecks []*core.Check
	for _, mod := range deps.Mods {
		userMod, ok := mod.(*core.Module)
		if !ok {
			continue
		}
		if mod.Name() == core.ModuleName {
			continue
		}
		checkGroup, err := userMod.Checks(ctx, include)
		if err != nil {
			return nil, fmt.Errorf("checks from module %q: %w", mod.Name(), err)
		}
		allChecks = append(allChecks, checkGroup.Checks...)
	}

	return &core.CheckGroup{
		Checks: allChecks,
	}, nil
}

func (s *workspaceSchema) generators(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.GeneratorGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	deps, err := query.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("current served deps: %w", err)
	}

	var allGenerators []*core.Generator
	for _, mod := range deps.Mods {
		userMod, ok := mod.(*core.Module)
		if !ok {
			continue
		}
		if mod.Name() == core.ModuleName {
			continue
		}
		generatorGroup, err := userMod.Generators(ctx, include)
		if err != nil {
			return nil, fmt.Errorf("generators from module %q: %w", mod.Name(), err)
		}
		allGenerators = append(allGenerators, generatorGroup.Generators...)
	}

	return &core.GeneratorGroup{
		Generators: allGenerators,
	}, nil
}

// readWorkspaceConfig reads the current workspace config from host, or returns a fresh empty config.
func readWorkspaceConfig(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, parent *core.Workspace) (*workspace.Config, error) {
	var cfg *workspace.Config
	if parent.HasConfig {
		data, err := bk.ReadCallerHostFile(ctx, parent.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		cfg, err = workspace.ParseConfig(data)
		if err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}
	if cfg == nil {
		cfg = &workspace.Config{}
	}
	if cfg.Modules == nil {
		cfg.Modules = make(map[string]workspace.ModuleEntry)
	}
	return cfg, nil
}

// writeWorkspaceConfig serializes and writes config.toml to the host.
// Uses a temp file + LocalFileExport to bypass the Directory/File abstraction
// which requires a buildkit session group not available in resolver context.
func writeWorkspaceConfig(ctx context.Context, bk *buildkit.Client, parent *core.Workspace, cfg *workspace.Config) error {
	configBytes := workspace.SerializeConfig(cfg)
	return exportConfigToHost(ctx, bk, parent, configBytes)
}

// writeWorkspaceConfigWithHints serializes config with comment-preserving TOML
// and constructor arg hints, then writes to the host.
func writeWorkspaceConfigWithHints(ctx context.Context, bk *buildkit.Client, parent *core.Workspace, cfg *workspace.Config, existingTOML []byte, hints map[string][]workspace.ConstructorArgHint) error {
	configBytes, err := workspace.SerializeConfigWithHints(cfg, existingTOML, hints)
	if err != nil {
		// Fallback to basic serialization without hints or comment preservation
		slog.Warn("falling back to basic config serialization", "error", err)
		configBytes = workspace.SerializeConfig(cfg)
	}
	return exportConfigToHost(ctx, bk, parent, configBytes)
}

// exportConfigToHost writes config bytes to config.toml on the host via temp file + LocalFileExport.
func exportConfigToHost(ctx context.Context, bk *buildkit.Client, parent *core.Workspace, configBytes []byte) error {
	workspaceAbsPath := filepath.Join(parent.SandboxRoot, parent.Path)
	configHostPath := filepath.Join(workspaceAbsPath, workspace.WorkspaceDirName, workspace.ConfigFileName)

	tmpFile, err := os.CreateTemp("", "workspace-config-*.toml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(configBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	if err := bk.LocalFileExport(ctx, tmpFile.Name(), workspace.ConfigFileName, configHostPath, true); err != nil {
		return fmt.Errorf("export config: %w", err)
	}
	return nil
}

// IntrospectConstructorArgs loads a module and extracts its constructor arguments
// as config hints. Returns nil (not error) if the module has no constructor.
func IntrospectConstructorArgs(ctx context.Context, srv *dagql.Server, ref string) ([]workspace.ConstructorArgHint, error) {
	var mod dagql.ObjectResult[*core.Module]
	err := srv.Select(ctx, srv.Root(), &mod,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(ref)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{Field: "asModule"},
	)
	if err != nil {
		return nil, fmt.Errorf("loading module: %w", err)
	}

	mainObj, ok := mod.Self().MainObject()
	if !ok {
		return nil, nil
	}

	if !mainObj.Constructor.Valid {
		return nil, nil
	}
	ctor := mainObj.Constructor.Value

	var hints []workspace.ConstructorArgHint
	for _, arg := range ctor.Args {
		hints = append(hints, buildHintFromArg(arg))
	}
	return hints, nil
}

// configurableObjectTypes maps core object type names to their address example values.
var configurableObjectTypes = map[string]string{
	"Container":     `"alpine:latest"`,
	"Directory":     `"./path"`,
	"File":          `"./file"`,
	"Secret":        `"env://MY_SECRET"`,
	"GitRepository": `"https://github.com/owner/repo"`,
	"GitRef":        `"https://github.com/owner/repo#main"`,
	"Service":       `"tcp://localhost:8080"`,
	"Socket":        `"unix:///var/run/docker.sock"`,
}

// buildHintFromArg converts a FunctionArg into a ConstructorArgHint.
func buildHintFromArg(arg *core.FunctionArg) workspace.ConstructorArgHint {
	typeLabel, exampleValue, configurable := typeInfoFromTypeDef(arg.TypeDef)

	// If arg has a non-null default value, format it as TOML instead of using the type example
	if arg.DefaultValue != nil {
		if formatted := formatDefaultAsToml(arg.DefaultValue); formatted != "" {
			exampleValue = formatted
		}
	}

	if !configurable {
		typeLabel += " (not configurable via config)"
	}

	return workspace.ConstructorArgHint{
		Name:         arg.Name,
		TypeLabel:    typeLabel,
		ExampleValue: exampleValue,
	}
}

// typeInfoFromTypeDef returns the type label, example value, and whether the type
// is configurable via config.toml string values.
func typeInfoFromTypeDef(td *core.TypeDef) (typeLabel, exampleValue string, configurable bool) {
	switch td.Kind {
	case core.TypeDefKindString:
		return "string", `""`, true
	case core.TypeDefKindInteger:
		return "int", "0", true
	case core.TypeDefKindFloat:
		return "float", "0.0", true
	case core.TypeDefKindBoolean:
		return "bool", "false", true
	case core.TypeDefKindEnum:
		if td.AsEnum.Valid {
			return td.AsEnum.Value.Name, `""`, true
		}
		return "enum", `""`, true
	case core.TypeDefKindScalar:
		if td.AsScalar.Valid {
			return td.AsScalar.Value.Name, `""`, true
		}
		return "scalar", `""`, true
	case core.TypeDefKindObject:
		if td.AsObject.Valid {
			objName := td.AsObject.Value.Name
			if example, ok := configurableObjectTypes[objName]; ok {
				return objName, example, true
			}
			return objName, `"..."`, false
		}
		return "object", `"..."`, false
	case core.TypeDefKindList:
		if td.AsList.Valid {
			elemLabel, _, _ := typeInfoFromTypeDef(td.AsList.Value.ElementTypeDef)
			return "[]" + elemLabel, `"..."`, false
		}
		return "list", `"..."`, false
	default:
		return string(td.Kind), `"..."`, false
	}
}

// formatDefaultAsToml converts a JSON-encoded default value to a TOML literal string.
// Returns empty string if the value can't be formatted or is null.
func formatDefaultAsToml(defaultValue core.JSON) string {
	raw := defaultValue.Bytes()
	if len(raw) == 0 {
		return ""
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return ""
	}

	switch v := val.(type) {
	case string:
		return fmt.Sprintf("%q", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	case nil:
		return "" // null means no default
	default:
		return ""
	}
}
