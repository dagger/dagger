package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("currentWorkspace", s.currentWorkspace).
			Doc("Detect and return the current workspace.").
			Args(
				dagql.Arg("skipMigrationCheck").Doc("If true, skip legacy dagger.json migration checks."),
			),
	}.Install(srv)

	dagql.Fields[*core.Workspace]{
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
	}.Install(srv)
}

type workspaceArgs struct {
	SkipMigrationCheck bool `default:"false"`
}

func (s *workspaceSchema) currentWorkspace(
	ctx context.Context,
	parent *core.Query,
	args workspaceArgs,
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
		if args.SkipMigrationCheck && strings.Contains(err.Error(), "migration") {
			// Fall through — install/init can work in legacy projects
			ws = &workspace.Workspace{Root: cwd}
		} else {
			return nil, fmt.Errorf("workspace detection: %w", err)
		}
	}

	result := &core.Workspace{
		Root:      ws.Root,
		HasConfig: ws.Config != nil,
	}
	if ws.Config != nil {
		result.ConfigPath = filepath.Join(ws.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)
	}

	return result, nil
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
		daggerDir := filepath.Join(parent.Root, workspace.WorkspaceDirName)
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

	// Add module to config and write to host
	cfg.Modules[name] = workspace.ModuleEntry{Source: sourcePath}
	if err := writeWorkspaceConfig(ctx, bk, parent, cfg); err != nil {
		return "", err
	}

	configPath := filepath.Join(parent.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)
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

	// Module lives at .dagger/modules/<name>/ relative to workspace root
	modulePath := filepath.Join(parent.Root, workspace.WorkspaceDirName, "modules", args.Name)

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

	var genDir dagql.ObjectResult[*core.Directory]
	err = srv.Select(ctx, srv.Root(), &genDir, selectors...)
	if err != nil {
		return "", fmt.Errorf("generate module: %w", err)
	}

	// Export generated files to host
	if err := genDir.Self().Export(ctx, string(contextDirPath), false); err != nil {
		return "", fmt.Errorf("export module: %w", err)
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

	configPath := filepath.Join(parent.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)
	return dagql.String(fmt.Sprintf("Created module %q at %s\nInstalled in %s", args.Name, modulePath, configPath)), nil
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
	configHostPath := filepath.Join(parent.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)

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
