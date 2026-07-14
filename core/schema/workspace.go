package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(srv *dagql.Server) {
	currentWorkspaceField := dagql.Func("currentWorkspace", s.currentWorkspace).
		WithInput(dagql.PerCallInput).
		Doc("Detect and return the current workspace.").
		Experimental("Highly experimental API extracted from a more ambitious workspace implementation.").
		PassthroughTelemetry()

	migrateField := dagql.Func("migrate", s.migrate).
		View(AfterVersion("v1.0.0-0")).
		DoNotCache("Plans workspace migration against live host filesystem").
		Doc("Plan the explicit migration needed for the current workspace.",
			"The returned plan has an empty changeset and no steps when no migration is needed.").
		PassthroughTelemetry()

	dagql.Fields[*core.Query]{
		currentWorkspaceField,
	}.Install(srv)

	dagql.Fields[*core.Workspace]{
		dagql.Func("__workspaceModule", s.workspaceModule).
			View(AfterVersion("v1.0.0-0")),
		dagql.Func("__workspaceSDK", s.workspaceSDK).
			View(AfterVersion("v1.0.0-0")),
		dagql.Func("path", s.legacyPath).
			View(BeforeVersion("v1.0.0-0")).
			Doc("Workspace directory path relative to the workspace boundary."),
		dagql.Func("configPath", s.legacyConfigPath).
			View(BeforeVersion("v1.0.0-0")).
			Doc("Path to config.toml relative to the workspace boundary (empty if not initialized)."),
		dagql.Func("configFile", s.configFile).
			View(AfterVersion("v1.0.0-0")).
			Doc("Selected native workspace config file relative to the workspace root, if any."),
		dagql.Func("hasConfig", s.legacyHasConfig).
			View(BeforeVersion("v1.0.0-0")).
			Doc("Whether a config.toml file exists in the workspace."),
		dagql.Func("initialized", s.legacyInitialized).
			View(BeforeVersion("v1.0.0-0")).
			Doc("Whether .dagger/config.toml exists."),
		dagql.NodeFunc("directory", s.directory).
			WithInput(dagql.PerClientInput).
			Doc(`Returns a Directory from the workspace.`,
				`Relative paths resolve from the workspace cwd. Absolute paths resolve from the workspace root.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve. Relative paths (e.g., "src") resolve from the workspace cwd; absolute paths (e.g., "/src") resolve from the workspace root.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory.`),
			),
		dagql.NodeFunc("file", s.file).
			WithInput(dagql.PerClientInput).
			Doc(`Returns a File from the workspace.`,
				`Relative paths resolve from the workspace cwd. Absolute paths resolve from the workspace root.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve. Relative paths (e.g., "go.mod") resolve from the workspace cwd; absolute paths (e.g., "/go.mod") resolve from the workspace root.`),
			),
		dagql.NodeFunc("glob", s.glob).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc(`Returns a list of files and directories that match the given pattern.`,
				`Patterns match paths relative to the workspace root.`).
			Args(
				dagql.Arg("pattern").Doc(`Pattern to match (e.g., "*.md").`),
			),
		dagql.NodeFunc("search", s.search).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc(
				`Searches for content matching the given regular expression or literal string.`,
				`Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.`,
				`Runs ripgrep on the client host, falling back to grep if unavailable.`,
			).
			Args((func() []dagql.Argument {
				args := []dagql.Argument{
					dagql.Arg("paths").Doc("Directory or file paths to search"),
					dagql.Arg("globs").Doc("Glob patterns to match (e.g., \"*.md\")"),
				}
				args = append(args, (core.SearchOpts{}).Args()...)
				return args
			})()...),
		dagql.NodeFunc("findUp", s.findUp).
			WithInput(dagql.PerClientInput).
			Doc(`Search for a file or directory by walking up from the start path within the workspace.`,
				`Returns the absolute workspace path if found, or null if not found.`,
				`Relative start paths resolve from the workspace cwd.`,
				`The search stops at the workspace root and will not traverse above it.`).
			Args(
				dagql.Arg("name").Doc(`The name of the file or directory to search for.`),
				dagql.Arg("from").Doc(`Path to start the search from. Relative paths resolve from the workspace cwd; absolute paths resolve from the workspace root.`),
			),
		dagql.NodeFunc("git", s.git).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Git state for this workspace. Errors if the workspace is not in a git repository."),
		dagql.NodeFunc("withNewFile", s.withNewFile).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a new or replaced file, without mutating the source.").
			Args(
				dagql.Arg("path").Doc("Path of the new file. Relative paths resolve from the workspace cwd."),
				dagql.Arg("contents").Doc("Contents of the new file."),
				dagql.Arg("permissions").Doc("Permissions of the new file."),
			),
		dagql.NodeFunc("withNewDirectory", s.withNewDirectory).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a directory added, without mutating the source.").
			Args(
				dagql.Arg("path").Doc("Path of the added directory. Relative paths resolve from the workspace cwd."),
				dagql.Arg("source").Doc("Directory to add."),
			),
		dagql.NodeFunc("withChanges", s.withChanges).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a changeset applied, without mutating the source.").
			Args(
				dagql.Arg("changes").Doc("Changes to apply."),
			),
		dagql.NodeFunc("withModule", s.withModule).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a module installed in its config.").
			Args(
				dagql.Arg("ref").Doc("Module reference to install."),
				dagql.Arg("name").Doc("Override name for the installed module entry."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withoutModule", s.withoutModule).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a module removed from its config.").
			Args(
				dagql.Arg("name").Doc("Name of the installed module entry to remove."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withSDK", s.withSDK).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with an SDK installed in its config.").
			Args(
				dagql.Arg("ref").Doc("SDK module reference to install."),
				dagql.Arg("name").Doc("Override name for the installed SDK entry."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
				dagql.Arg("asSdkName").Doc("User-facing SDK name to persist under `[modules.<name>.as-sdk] name = ...`."),
			),
		dagql.NodeFunc("withoutSDK", s.withoutSDK).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with an SDK removed from its config.").
			Args(
				dagql.Arg("name").Doc("Name of the installed SDK entry to remove."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withInitModule", s.withInitModule).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a new module initialized.").
			Args(
				dagql.Arg("name").Doc("Name of the new module."),
				dagql.Arg("sdk").Doc("Workspace SDK name or module entry name to use."),
				dagql.Arg("path").Doc(`Workspace-relative path for the new module.`),
				dagql.Arg("source").Doc("Source subpath within the new module."),
				dagql.Arg("include").Doc("Additional include patterns for the module."),
				dagql.Arg("args").Doc("SDK-specific init arguments."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withInitClient", s.withInitClient).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a generated API client initialized.").
			Args(
				dagql.Arg("path").Doc("Workspace-relative output directory for the generated client."),
				dagql.Arg("sdk").Doc("Workspace SDK name or module entry name to use."),
				dagql.Arg("module").Doc("Workspace-relative path or canonical ref for the module the client binds to."),
				dagql.Arg("args").Doc("SDK-specific init arguments."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withConfigValue", s.withConfigValue).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a configuration value written.").
			Args(
				dagql.Arg("key").Doc("Dotted key path."),
				dagql.Arg("value").Doc("Value to set."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withConfigEnv", s.withConfigEnv).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a named config environment created.").
			Args(
				dagql.Arg("name").Doc("Environment name."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withoutConfigEnv", s.withoutConfigEnv).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a named config environment removed.").
			Args(
				dagql.Arg("name").Doc("Environment name."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withUpdatedLock", s.withUpdatedLock).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with refreshed lockfile state."),
		dagql.NodeFunc("sdks", s.sdks).
			View(AfterVersion("v1.0.0-0")).
			Doc("Installed SDKs."),
		dagql.NodeFunc("sdk", s.sdk).
			View(AfterVersion("v1.0.0-0")).
			Doc("An installed SDK, by name.").
			Args(
				dagql.Arg("name").Doc("SDK name to look up."),
			),
		dagql.NodeFunc("changes", s.changes).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace's pending overlay changes."),
		dagql.NodeFunc("export", s.export).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Writes pending workspace changes to the local Git workspace").
			Doc("Write this workspace's pending changes to its local Git workspace."),
		dagql.Func("configRead", s.configRead).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Reads live config from host").
			Doc("Read a configuration value from dagger.toml.",
				"If key is empty, returns the full config.",
				"If key points to a scalar, returns the value.",
				"If key points to a table, returns flattened dotted-key output.").
			Args(
				dagql.Arg("key").Doc("Dotted key path (e.g. modules.greeter.source). Empty for full config."),
			),
		dagql.Func("envList", s.envList).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Reads live config from host").
			Doc("List named environments defined in the workspace configuration."),
		dagql.NodeFunc("modules", s.modules).
			View(AfterVersion("v1.0.0-0")).
			Doc("List modules defined in the workspace configuration."),
		dagql.NodeFunc("module", s.module).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return a module defined in the workspace configuration.").
			Args(
				dagql.Arg("name").Doc("Module name to inspect."),
			),
		dagql.Func("cwd", s.cwd).
			View(AfterVersion("v1.0.0-0")).
			Doc("Current location within the workspace root.",
				`The workspace root is returned as "/".`,
				"Relative paths in workspace APIs resolve from here."),
		dagql.Func("checks", s.checks).
			Doc("Return all checks from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include checks matching the specified patterns"),
				dagql.Arg("skip").Doc("Skip checks matching the specified patterns").
					View(AfterVersion("v1.0.0-0")),
				dagql.Arg("noGenerate").Doc("When true, only return annotated check functions; exclude generate-as-checks").
					View(AfterVersion("v0.21.0")),
				dagql.Arg("onlyGenerate").Doc("When true, only return generate-as-checks; exclude annotated check functions").
					View(AfterVersion("v0.21.4")),
			),
		dagql.Func("generators", s.generators).
			Doc("Return all generators from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include generators matching the specified patterns"),
			),
		dagql.Func("services", s.services).
			Doc("Return all services from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include services matching the specified patterns"),
			),
		migrateField,
	}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.WorkspaceGit](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceModule](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceModuleSetting](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceSDK](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceMigration](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceMigrationStep](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.WorkspaceGit]{
		dagql.NodeFunc("__repository", s.workspaceGitRepository).
			Doc("(Internal-only) The git repository backing this workspace git state."),
		dagql.NodeFunc("head", s.workspaceGitHead).
			Doc("The checked-out HEAD of this workspace."),
		dagql.NodeFunc("uncommitted", s.workspaceGitUncommitted).
			Doc("Uncommitted changes in this workspace, using the same rules as GitRepository.uncommitted."),
	}.Install(srv)

	dagql.Fields[*core.WorkspaceModule]{
		dagql.NodeFunc("settings", s.moduleSettings).
			DoNotCache("Reads live config and module metadata from the workspace").
			Doc("List constructor-backed settings for this module."),
	}.Install(srv)
	dagql.Fields[*core.WorkspaceModuleSetting]{}.Install(srv)
	dagql.Fields[*core.WorkspaceSDK]{}.Install(srv)
	dagql.Fields[*core.WorkspaceMigration]{}.Install(srv)
	dagql.Fields[*core.WorkspaceMigrationStep]{}.Install(srv)
}

type workspaceArgs struct {
	Cwd string `default:"/"`
}

func syntheticWorkspaceFromRootfs(
	ctx context.Context,
	root dagql.ObjectResult[*core.Directory],
	cwdArg string,
	addressScheme string,
) (dagql.ObjectResult[*core.Workspace], error) {
	return syntheticWorkspaceFromDirectory(ctx, root, cwdArg, addressScheme)
}

func syntheticWorkspaceFromDirectory(
	ctx context.Context,
	root dagql.ObjectResult[*core.Directory],
	cwdArg string,
	addressScheme string,
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	cwd, err := resolveWorkspacePath(cwdArg, ".")
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	detected, err := detectWorkspaceFilesInDirectory(ctx, root, cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	rootDigest, err := root.ContentPreferredDigest(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	ws := &core.Workspace{
		Cwd:        detected.Cwd,
		ConfigFile: detected.ConfigFile,
		LockFile:   detected.LockFile,
		Address:    addressScheme + rootDigest.String(),
	}
	ws.SetRootfs(root)
	ws.SetSource(core.NewWorkspaceSourceDirectory(root))
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func syntheticWorkspaceFromGitRef(
	ctx context.Context,
	ref dagql.ObjectResult[*core.GitRef],
	cwdArg string,
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	cwd, err := resolveWorkspacePath(cwdArg, ".")
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	var rootResult dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, ref, &rootResult, dagql.Selector{
		Field: "tree",
		Args: []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	detected, err := detectWorkspaceFilesInDirectory(ctx, rootResult, cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	ws := &core.Workspace{
		Cwd:        detected.Cwd,
		ConfigFile: detected.ConfigFile,
		LockFile:   detected.LockFile,
		Address:    "git-ref://" + ref.Self().Ref.SHA,
	}
	ws.SetRootfs(rootResult)
	ws.SetSource(core.NewWorkspaceSourceGitRef(ref.Result))
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func detectWorkspaceFilesInDirectory(
	ctx context.Context,
	root dagql.ObjectResult[*core.Directory],
	cwd string,
) (*workspace.Workspace, error) {
	statFS := &core.DirectoryStatFS{Dir: root}
	detected, err := workspace.DetectInRoot(ctx, func(ctx context.Context, path string) (string, bool, error) {
		return core.StatFSExists(ctx, statFS, filepath.ToSlash(path))
	}, cwd, ".")
	if err != nil {
		return nil, fmt.Errorf("detect workspace files: %w", err)
	}
	return detected, nil
}

func (s *workspaceSchema) currentWorkspace(
	ctx context.Context,
	parent *core.Query,
	_ struct{},
) (*core.Workspace, error) {
	return parent.Server.CurrentWorkspace(ctx)
}

func (s *workspaceSchema) cwd(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.String, error) {
	_ = ctx
	return dagql.NewString(workspaceAPIPath(parent.Cwd)), nil
}

func (s *workspaceSchema) configFile(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.String, error) {
	_ = ctx
	return dagql.NewString(parent.ConfigFile), nil
}

func (s *workspaceSchema) legacyPath(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.String, error) {
	_ = ctx
	p := cleanWorkspaceRelPath(parent.Cwd)
	if p == "." {
		p = ""
	}
	return dagql.NewString(p), nil
}

func (s *workspaceSchema) legacyConfigPath(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.String, error) {
	_ = ctx
	return dagql.NewString(parent.ConfigFile), nil
}

func (s *workspaceSchema) legacyHasConfig(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.Boolean, error) {
	_ = ctx
	return dagql.NewBoolean(parent.ConfigFile != ""), nil
}

func (s *workspaceSchema) legacyInitialized(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.Boolean, error) {
	_ = ctx
	return dagql.NewBoolean(parent.ConfigFile != ""), nil
}

type workspaceDirectoryArgs struct {
	Path string

	core.CopyFilter

	Gitignore bool `default:"false"`
}

// resolveRootfs returns a lazy directory reference for a resolved workspace path.
// Local: per-call host.directory(absPath, include, exclude) via workspace client session.
// Local with overlay edits: sparse host base + changeset applied on top.
// Remote: navigates the pre-fetched rootfs.
func (s *workspaceSchema) resolveRootfs(
	ctx context.Context,
	ws *core.Workspace,
	resolvedPath string,
	filter core.CopyFilter,
	gitignore bool,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	if ws.HostPath() != "" && ws.ClientLocalBase() {
		if changes, ok := ws.OverlayChanges(); ok {
			return s.resolveHostOverlayRootfs(ctx, srv, ws, changes, resolvedPath, filter, gitignore)
		}
	}

	if root, ok := ws.SourceDirectory(); ok && root.Self() != nil {
		return s.resolveRootfsFromDirectory(ctx, srv, ws, root, resolvedPath, filter, gitignore)
	}
	if _, ok := ws.BaseSource().(*core.WorkspaceSourceRootlessLocal); ok {
		var empty dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, srv.Root(), &empty, dagql.Selector{Field: "directory"}); err != nil {
			return inst, fmt.Errorf("workspace directory %q: create rootless directory: %w", resolvedPath, err)
		}
		return s.resolveRootfsFromDirectory(ctx, srv, ws, empty, resolvedPath, filter, gitignore)
	}

	if ws.HostPath() != "" {
		ctx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return inst, err
		}
		absPath, err := pathutil.SandboxedRelativePath(resolvedPath, ws.HostPath())
		if err != nil {
			return inst, err
		}

		args := []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(absPath)},
		}
		if len(filter.Include) > 0 {
			includes := make(dagql.ArrayInput[dagql.String], len(filter.Include))
			for i, p := range filter.Include {
				includes[i] = dagql.String(p)
			}
			args = append(args, dagql.NamedInput{Name: "include", Value: includes})
		}
		if len(filter.Exclude) > 0 {
			excludes := make(dagql.ArrayInput[dagql.String], len(filter.Exclude))
			for i, p := range filter.Exclude {
				excludes[i] = dagql.String(p)
			}
			args = append(args, dagql.NamedInput{Name: "exclude", Value: excludes})
		}
		if gitignore {
			args = append(args,
				dagql.NamedInput{Name: "gitignore", Value: dagql.NewBoolean(true)},
				dagql.NamedInput{Name: "gitIgnoreRoot", Value: dagql.NewString(ws.HostPath())},
			)
		}
		err = srv.Select(ctx, srv.Root(), &inst,
			dagql.Selector{Field: "host"},
			dagql.Selector{Field: "directory", Args: args},
		)
		if err != nil {
			return inst, fmt.Errorf("workspace directory %q: %w", resolvedPath, err)
		}
		return inst, nil
	}

	root, err := workspaceRootfs(ws)
	if err != nil {
		return inst, fmt.Errorf("workspace directory %q: %w", resolvedPath, err)
	}
	return s.resolveRootfsFromDirectory(ctx, srv, ws, root, resolvedPath, filter, gitignore)
}

func (s *workspaceSchema) resolveRootfsFromDirectory(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	root dagql.ObjectResult[*core.Directory],
	resolvedPath string,
	filter core.CopyFilter,
	gitignore bool,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	_ = ws
	ctxDir := root
	if resolvedPath != "." && resolvedPath != "" {
		err := srv.Select(ctx, ctxDir, &ctxDir,
			dagql.Selector{
				Field: "directory",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(resolvedPath)}},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("workspace directory %q: %w", resolvedPath, err)
		}
	}

	if len(filter.Include) > 0 || len(filter.Exclude) > 0 || gitignore {
		ctxDirID, err := ctxDir.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace directory %q: get filtered source id: %w", resolvedPath, err)
		}
		withDirArgs := workspaceFilterWithDirectoryArgs(ctxDirID, filter, gitignore)
		err = srv.Select(ctx, srv.Root(), &ctxDir,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withDirectory", Args: withDirArgs},
		)
		if err != nil {
			return inst, fmt.Errorf("workspace directory %q (filtering): %w", resolvedPath, err)
		}
	}

	return ctxDir, nil
}

// resolveHostOverlayRootfs resolves a read against a host-backed overlay
// workspace: a sparse host.directory sync of just the requested paths, with the
// overlay's changeset applied on top. The overlay stores no full read root —
// materializing one would force the whole host tree to upload (see overlayEdit)
// — so reads stay as sparse as pristine host reads, at the cost of a cheap
// changeset apply per read. Reads reflect the host at read time plus the
// overlay's edits, matching pristine workspaces' per-call resolution.
func (s *workspaceSchema) resolveHostOverlayRootfs(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	changes dagql.ObjectResult[*core.Changeset],
	resolvedPath string,
	filter core.CopyFilter,
	gitignore bool,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	hostCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, err
	}
	absPath, err := pathutil.SandboxedRelativePath(".", ws.HostPath())
	if err != nil {
		return inst, err
	}

	// The host base is rooted at the workspace root (not resolvedPath) so the
	// changeset's root-relative paths line up; the filter is re-rooted to match.
	// The base only needs the requested paths: the changeset's diff layer carries
	// full content for touched paths, and whiteouts/modifications apply cleanly
	// onto a base that lacks them — exactly as building the delta root on an
	// empty base does. Keeping the base independent of the touched set also keeps
	// its cache identity stable across edits.
	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString(absPath)},
	}
	includes := rerootPatterns(resolvedPath, filter.Include)
	if len(includes) == 0 && resolvedPath != "." && resolvedPath != "" {
		// No include filter: request only the resolved subtree.
		subtree := strings.TrimSuffix(resolvedPath, "/")
		includes = []string{subtree, subtree + "/**"}
	}
	if len(includes) > 0 {
		arr := make(dagql.ArrayInput[dagql.String], len(includes))
		for i, p := range includes {
			arr[i] = dagql.String(p)
		}
		args = append(args, dagql.NamedInput{Name: "include", Value: arr})
	}
	if excludes := rerootPatterns(resolvedPath, filter.Exclude); len(excludes) > 0 {
		arr := make(dagql.ArrayInput[dagql.String], len(excludes))
		for i, p := range excludes {
			arr[i] = dagql.String(p)
		}
		args = append(args, dagql.NamedInput{Name: "exclude", Value: arr})
	}
	if gitignore {
		args = append(args,
			dagql.NamedInput{Name: "gitignore", Value: dagql.NewBoolean(true)},
			dagql.NamedInput{Name: "gitIgnoreRoot", Value: dagql.NewString(ws.HostPath())},
		)
	}
	var base dagql.ObjectResult[*core.Directory]
	if err := srv.Select(hostCtx, srv.Root(), &base,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "directory", Args: args},
	); err != nil {
		return inst, fmt.Errorf("workspace directory %q: %w", resolvedPath, err)
	}

	changesID, err := changes.ID()
	if err != nil {
		return inst, err
	}
	var merged dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, base, &merged, dagql.Selector{
		Field: "withChanges",
		Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
	}); err != nil {
		return inst, fmt.Errorf("workspace directory %q (overlay): %w", resolvedPath, err)
	}

	// Descend and re-apply the filter: the changeset applies at the workspace
	// root, so merged also contains touched paths outside the requested scope;
	// the descent plus filter trims them back out. Gitignore was already applied
	// host-side — the sparse tree lacks the .gitignore context to re-evaluate it,
	// and overlay edits win even for ignored paths.
	return s.resolveRootfsFromDirectory(ctx, srv, ws, merged, resolvedPath, filter, false)
}

// rerootPatterns prefixes filter patterns (relative to a resolved workspace
// path) with that path, producing workspace-root-relative patterns.
func rerootPatterns(resolvedPath string, patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	if resolvedPath == "." || resolvedPath == "" {
		return patterns
	}
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = path.Join(resolvedPath, p)
	}
	return out
}

func workspaceRootfs(ws *core.Workspace) (dagql.ObjectResult[*core.Directory], error) {
	if ws == nil {
		return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("workspace is nil")
	}
	rootfs, ok := ws.SourceDirectory()
	if !ok || rootfs.Self() == nil {
		return rootfs, fmt.Errorf("workspace has no root filesystem")
	}
	return rootfs, nil
}

func (s *workspaceSchema) workspaceOverlayRootfs(ctx context.Context, ws *core.Workspace) (dagql.ObjectResult[*core.Directory], error) {
	if ws == nil {
		return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("workspace is required")
	}
	rootfs, ok := ws.SourceDirectory()
	if ok && rootfs.Self() != nil {
		return rootfs, nil
	}
	if ws.HostPath() == "" {
		return rootfs, fmt.Errorf("workspace has no root filesystem")
	}
	// Whole-tree materialization: legitimate for callers that need the full
	// workspace as a Directory (module-source loading, install flows). For a
	// host overlay this resolves as full host + changeset via the overlay
	// branch in resolveRootfs; edits and diffs never come through here (see
	// overlayEdit, which keeps the changeset delta-native).
	return s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
}

func requireLocalWorkspace(ws *core.Workspace, operation string) error {
	if ws == nil {
		return fmt.Errorf("workspace is required")
	}
	if ws.HostPath() == "" {
		return fmt.Errorf("%s is local-only", operation)
	}
	return nil
}

func isSyntheticWorkspace(ws *core.Workspace) bool {
	return ws != nil && ws.IsValueWorkspace()
}

func workspaceFilterWithDirectoryArgs(dirID *call.ID, filter core.CopyFilter, gitignore bool) []dagql.NamedInput {
	withDirArgs := []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString("/")},
		{Name: "source", Value: dagql.NewID[*core.Directory](dirID)},
	}
	if len(filter.Include) > 0 {
		includes := make(dagql.ArrayInput[dagql.String], len(filter.Include))
		for i, p := range filter.Include {
			includes[i] = dagql.String(p)
		}
		withDirArgs = append(withDirArgs, dagql.NamedInput{Name: "include", Value: includes})
	}
	if len(filter.Exclude) > 0 {
		excludes := make(dagql.ArrayInput[dagql.String], len(filter.Exclude))
		for i, p := range filter.Exclude {
			excludes[i] = dagql.String(p)
		}
		withDirArgs = append(withDirArgs, dagql.NamedInput{Name: "exclude", Value: excludes})
	}
	if gitignore {
		withDirArgs = append(withDirArgs, dagql.NamedInput{Name: "gitignore", Value: dagql.NewBoolean(true)})
	}
	return withDirArgs
}

func (s *workspaceSchema) directory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceDirectoryArgs,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	ws := parent.Self()
	return s.directoryAt(ctx, ws, ws.Cwd, args)
}

func (s *workspaceSchema) directoryAt(
	ctx context.Context,
	ws *core.Workspace,
	basePath string,
	args workspaceDirectoryArgs,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, basePath)
	if err != nil {
		return inst, err
	}
	return s.resolveRootfs(ctx, ws, resolvedPath, args.CopyFilter, args.Gitignore)
}

type workspaceFileArgs struct {
	Path string
}

func (s *workspaceSchema) file(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceFileArgs,
) (inst dagql.Result[*core.File], _ error) {
	ws := parent.Self()
	return s.fileAt(ctx, ws, ws.Cwd, args)
}

func (s *workspaceSchema) fileAt(
	ctx context.Context,
	ws *core.Workspace,
	basePath string,
	args workspaceFileArgs,
) (inst dagql.Result[*core.File], _ error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, basePath)
	if err != nil {
		return inst, err
	}
	parentDir := filepath.Dir(resolvedPath)
	basename := filepath.Base(resolvedPath)

	dir, err := s.resolveRootfs(ctx, ws, parentDir, core.CopyFilter{
		Include: []string{basename},
	}, false)
	if err != nil {
		return inst, fmt.Errorf("workspace file %q: %w", args.Path, err)
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, dir, &inst,
		dagql.Selector{
			Field: "file",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(basename)}},
		},
	); err != nil {
		return inst, fmt.Errorf("workspace file %q: %w", args.Path, err)
	}

	return inst, nil
}

func (s *workspaceSchema) withNewFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args WithNewFileArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(resolvedPath)},
				{Name: "contents", Value: dagql.NewString(args.Contents)},
				{Name: "permissions", Value: dagql.NewInt(args.Permissions)},
			},
		})
		return updated, err
	}, nil)
}

type workspaceSearchArgs struct {
	core.SearchOpts
	Paths []string `default:"[]"`
	Globs []string `default:"[]"`
}

func (s *workspaceSchema) search(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceSearchArgs,
) (dagql.Array[*core.SearchResult], error) {
	ws := parent.Self()

	if ws.HostPath() == "" {
		// No host boundary: search the workspace's in-engine root filesystem.
		rootfs, err := workspaceRootfs(ws)
		if err != nil {
			return nil, err
		}
		results, err := rootfs.Self().Search(ctx, rootfs, args.SearchOpts, true, args.Paths, args.Globs)
		if err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
		return dagql.Array[*core.SearchResult](results), nil
	}

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildkit: %w", err)
	}

	localResults, err := bk.SearchCallerHostPath(ctx, ws.HostPath(), &engine.LocalSearchOpts{
		Pattern:     args.Pattern,
		Literal:     args.Literal,
		Multiline:   args.Multiline,
		Dotall:      args.Dotall,
		Insensitive: args.Insensitive,
		SkipIgnored: args.SkipIgnored,
		SkipHidden:  args.SkipHidden,
		FilesOnly:   args.FilesOnly,
		Limit:       args.Limit,
		Paths:       args.Paths,
		Globs:       args.Globs,
	})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// Convert engine.LocalSearchResult to core.SearchResult and emit OTel logs
	stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
	defer stdio.Close()

	results := make([]*core.SearchResult, len(localResults))
	for i, lr := range localResults {
		result := &core.SearchResult{
			FilePath:       lr.FilePath,
			LineNumber:     lr.LineNumber,
			AbsoluteOffset: lr.AbsoluteOffset,
			MatchedLines:   lr.MatchedLines,
		}
		for _, sm := range lr.Submatches {
			result.Submatches = append(result.Submatches, &core.SearchSubmatch{
				Text:  sm.Text,
				Start: sm.Start,
				End:   sm.End,
			})
		}
		results[i] = result

		if args.FilesOnly {
			fmt.Fprintln(stdio.Stdout, result.FilePath)
		} else {
			ensureLn := result.MatchedLines
			if !strings.HasSuffix(ensureLn, "\n") {
				ensureLn += "\n"
			}
			fmt.Fprintf(stdio.Stdout, "%s:%d:%s", result.FilePath, result.LineNumber, ensureLn)
		}
	}

	return dagql.Array[*core.SearchResult](results), nil
}

type workspaceGlobArgs struct {
	Pattern string
}

func (s *workspaceSchema) glob(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceGlobArgs,
) (dagql.Array[dagql.String], error) {
	ws := parent.Self()

	if ws.HostPath() != "" {
		ctx, err := s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return nil, err
		}
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return nil, fmt.Errorf("buildkit: %w", err)
		}
		matches, err := bk.GlobCallerHostPath(ctx, ws.HostPath(), args.Pattern)
		if err != nil {
			return nil, fmt.Errorf("glob: %w", err)
		}
		return dagql.NewStringArray(matches...), nil
	}

	rootfs, err := workspaceRootfs(ws)
	if err != nil {
		return nil, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var matches dagql.Array[dagql.String]
	if err := srv.Select(ctx, rootfs, &matches, dagql.Selector{
		Field: "glob",
		Args: []dagql.NamedInput{
			{Name: "pattern", Value: dagql.NewString(args.Pattern)},
		},
	}); err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	return matches, nil
}

type workspaceWithNewDirectoryArgs struct {
	Path   string
	Source core.DirectoryID
}

func (s *workspaceSchema) withNewDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithNewDirectoryArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sourceID, err := args.Source.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(resolvedPath)},
				{Name: "source", Value: dagql.NewID[*core.Directory](sourceID)},
			},
		})
		return updated, err
	}, nil)
}

func (s *workspaceSchema) withChanges(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args withChangesArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	changesID, err := args.Changes.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	changesObj, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	touched, err := changesetTouchedPaths(ctx, changesObj.Self())
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, touched, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withChanges",
			Args: []dagql.NamedInput{
				{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)},
			},
		})
		return updated, err
	}, nil)
}

func (s *workspaceSchema) changes(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if changes, ok := parent.Self().OverlayChanges(); ok {
		return changes, nil
	}
	changes, err := core.NewEmptyChangeset(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, changes)
}

func (s *workspaceSchema) export(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (core.Void, error) {
	ws := parent.Self()
	hostPath, err := ws.ExportHostPath()
	if err != nil {
		return core.Void{}, err
	}

	changes, ok := ws.OverlayChanges()
	if !ok || changes.Self() == nil {
		return core.Void{}, nil
	}
	isEmpty, err := changes.Self().IsEmpty(ctx)
	if err != nil {
		return core.Void{}, err
	}
	if isEmpty {
		return core.Void{}, nil
	}

	exportCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return core.Void{}, err
	}
	if err := changes.Self().Export(exportCtx, hostPath); err != nil {
		return core.Void{}, err
	}
	if err := core.InvalidateCurrentWorkspace(exportCtx); err != nil {
		slog.Warn("could not invalidate workspace after export", "error", err)
	}
	return core.Void{}, nil
}

func (s *workspaceSchema) overlayWorkspaceWithMutation(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	root dagql.ObjectResult[*core.Directory],
	mutate func(*core.Workspace),
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	var baseRoot dagql.ObjectResult[*core.Directory]
	if changes, ok := parent.Self().OverlayChanges(); ok {
		baseRoot = changes.Self().Before
	} else {
		baseRoot, err = s.workspaceOverlayRootfs(ctx, parent.Self())
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}

	baseRootID, err := baseRoot.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var changesResult dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, root, &changesResult, dagql.Selector{
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](baseRootID)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	ws := parent.Self().Clone()
	ws.SetRootfs(dagql.ObjectResult[*core.Directory]{})
	// Value/git/rootless workspaces diff full in-engine trees (no TouchedPaths);
	// the sparse delta-native path is host-only (see overlayEdit).
	ws.SetSource(core.NewWorkspaceSourceOverlay(parent.Self().Source(), nil, changesResult))
	if mutate != nil {
		mutate(ws)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

// overlayEdit applies an edit to a workspace, producing a new overlay workspace.
// `edit` applies the operation to a given base directory: for value/git/rootless
// workspaces the full read root (already in-engine, nothing to upload), for
// host-backed workspaces the delta root — the accumulated edits applied to an
// empty base, stored as the overlay changeset's After side, which never
// references the host tree. `touched` are the workspace-relative paths this
// edit affects.
//
// Host-backed overlays store no full read root at all: Directory.withChanges
// must materialize its base, so a host-tree root would force the whole
// workspace to upload on every edit. Instead the overlay's changes are computed
// as the delta root diffed against a sparse base — host.directory including
// only the cumulative touched paths — so forcing changes/export syncs just
// those files (new files sync nothing), and reads resolve sparsely against the
// host with the changeset applied on top (resolveHostOverlayRootfs).
//
// The sparse base preserves changeset semantics: rename detection pairs a
// removal with an addition, and any removal comes from an edit, which makes
// both paths touched and therefore present in the base.
func (s *workspaceSchema) overlayEdit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	touched []string,
	edit func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error),
	mutate func(*core.Workspace),
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	ws := parent.Self()

	// Value/git/rootless workspaces: the edit applies to an in-engine tree
	// (empty for rootless), so keep the full-root changeset accumulation.
	if ws.HostPath() == "" || !ws.ClientLocalBase() {
		fullBase, err := s.workspaceOverlayRootfs(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		fullRoot, err := edit(fullBase)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		return s.overlayWorkspaceWithMutation(ctx, parent, fullRoot, mutate)
	}

	// Host-backed: apply the edit to the accumulated empty-based delta root, so
	// neither the overlay build nor computing changes/export ever references the
	// full host tree.
	deltaBase, ok := ws.OverlayDeltaRoot()
	if !ok {
		if err := srv.Select(ctx, srv.Root(), &deltaBase, dagql.Selector{Field: "directory"}); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	deltaRoot, err := edit(deltaBase)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	touchedAll := unionPaths(ws.OverlayTouchedPaths(), touched)
	sparseBase, err := s.sparseHostBase(ctx, ws, touchedAll)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sparseBaseID, err := sparseBase.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var changesResult dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, deltaRoot, &changesResult, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](sparseBaseID)}},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	newWS := ws.Clone()
	newWS.SetRootfs(dagql.ObjectResult[*core.Directory]{})
	newWS.SetSource(core.NewWorkspaceSourceOverlay(ws.Source(), touchedAll, changesResult))
	if mutate != nil {
		mutate(newWS)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, newWS)
}

// unionPaths returns the ordered union of two path slices, preserving first-seen
// order and dropping duplicates.
func unionPaths(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, group := range [][]string{a, b} {
		for _, p := range group {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// sparseHostBase resolves the host workspace's base directory including only the
// given touched paths (and their subtrees), so diffing/exporting the overlay
// syncs just those files from the host rather than the whole tree. With no
// touched paths — or when none exist on the host — it is an empty directory.
func (s *workspaceSchema) sparseHostBase(
	ctx context.Context,
	ws *core.Workspace,
	touched []string,
) (dagql.ObjectResult[*core.Directory], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	var empty dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, srv.Root(), &empty, dagql.Selector{Field: "directory"}); err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	if len(touched) == 0 {
		return empty, nil
	}

	includes := make(dagql.ArrayInput[dagql.String], 0, len(touched)*2)
	for _, p := range touched {
		p = strings.TrimSuffix(p, "/")
		includes = append(includes, dagql.String(p), dagql.String(p+"/**"))
	}

	ctx, err = s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	absPath, err := pathutil.SandboxedRelativePath(".", ws.HostPath())
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	var out dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, srv.Root(), &out,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(absPath)},
			{Name: "include", Value: includes},
		}},
	); err != nil {
		return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("sparse host base: %w", err)
	}
	return out, nil
}

// changesetTouchedPaths returns the workspace-relative paths a changeset affects
// (added, modified, and removed), used to size the sparse diff base.
func changesetTouchedPaths(ctx context.Context, ch *core.Changeset) ([]string, error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths.Added)+len(paths.Modified)+len(paths.AllRemoved))
	out = append(out, paths.Added...)
	out = append(out, paths.Modified...)
	out = append(out, paths.AllRemoved...)
	return out, nil
}

func (s *workspaceSchema) git(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.WorkspaceGit], error) {
	var inst dagql.ObjectResult[*core.WorkspaceGit]
	if _, ok := parent.Self().SourceGitRef(); !ok {
		if err := s.ensureWorkspaceGitDirectory(ctx, parent.Self()); err != nil {
			return inst, err
		}
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, &core.WorkspaceGit{
		Workspace: parent,
	})
}

func (s *workspaceSchema) ensureWorkspaceGitDirectory(ctx context.Context, ws *core.Workspace) error {
	if _, ok := ws.SourceGitRef(); ok {
		return nil
	}
	var (
		statFS   core.StatFS
		statPath = ".git"
	)
	if ws.HostPath() != "" {
		var err error
		ctx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return err
		}

		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return fmt.Errorf("buildkit: %w", err)
		}

		statFS = core.NewCallerStatFS(bk)
		statPath, err = pathutil.SandboxedRelativePath(".git", ws.HostPath())
		if err != nil {
			return err
		}
	} else {
		statFS = &core.DirectoryStatFS{Dir: ws.Rootfs()}
	}

	_, st, err := statFS.Stat(ctx, statPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("workspace is not in a git repository")
	}
	if err != nil {
		return fmt.Errorf("workspace git metadata: %w", err)
	}
	// Git worktrees use a .git file that points to metadata outside the workspace.
	if st.FileType == core.FileTypeRegular {
		return fmt.Errorf("git worktrees are not supported by Workspace.git yet: .git is a file")
	}
	if !st.IsDir() {
		return fmt.Errorf("workspace git metadata .git has type %s, expected directory", st.FileType)
	}
	return nil
}

func (s *workspaceSchema) workspaceGitRepository(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	_ struct{},
) (dagql.ObjectResult[*core.GitRepository], error) {
	var inst dagql.ObjectResult[*core.GitRepository]

	ws := parent.Self().Workspace.Self()
	if ref, ok := ws.SourceGitRef(); ok {
		return ref.Self().Repo, nil
	}
	if err := s.ensureWorkspaceGitDirectory(ctx, ws); err != nil {
		return inst, err
	}

	dir, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
	if err != nil {
		return inst, fmt.Errorf("workspace git directory: %w", err)
	}

	backend := &core.LocalGitRepository{
		Directory: dir,
	}
	repo, err := core.NewGitRepository(ctx, backend)
	if err != nil {
		return inst, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}

func (s *workspaceSchema) workspaceGitHead(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	_ struct{},
) (dagql.Result[*core.GitRef], error) {
	var inst dagql.Result[*core.GitRef]
	if ref, ok := parent.Self().Workspace.Self().SourceGitRef(); ok {
		return ref, nil
	}
	repo, err := s.selectWorkspaceGitRepository(ctx, parent)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, repo, &inst, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *workspaceSchema) workspaceGitUncommitted(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	_ struct{},
) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	ws := parent.Self().Workspace.Self()
	if changes, ok := ws.OverlayChanges(); ok {
		if ref, ok := ws.SourceGitRef(); ok {
			return gitRefWorkspaceChanges(ctx, ws, ref)
		}
		return changes, nil
	}
	if _, ok := ws.SourceGitRef(); ok {
		empty, err := core.NewEmptyChangeset(ctx)
		if err != nil {
			return inst, err
		}
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentCall(ctx, srv, empty)
	}
	repo, err := s.selectWorkspaceGitRepository(ctx, parent)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, repo, &inst, dagql.Selector{Field: "uncommitted"}); err != nil {
		return inst, err
	}
	return inst, nil
}

func gitRefWorkspaceChanges(
	ctx context.Context,
	ws *core.Workspace,
	ref dagql.Result[*core.GitRef],
) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	refID, err := ref.ID()
	if err != nil {
		return inst, err
	}
	refResult, err := dagql.NewID[*core.GitRef](refID).Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	var base dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, refResult, &base, dagql.Selector{
		Field: "tree",
		Args: []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
		},
	}); err != nil {
		return inst, err
	}
	baseID, err := base.ID()
	if err != nil {
		return inst, err
	}
	root, err := workspaceRootfs(ws)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, root, &inst, dagql.Selector{
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](baseID)},
		},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *workspaceSchema) selectWorkspaceGitRepository(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
) (dagql.ObjectResult[*core.GitRepository], error) {
	var repo dagql.ObjectResult[*core.GitRepository]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return repo, err
	}
	if err := srv.Select(ctx, parent, &repo, dagql.Selector{Field: "__repository"}); err != nil {
		return repo, err
	}
	return repo, nil
}

// resolveWorkspacePath resolves a workspace API path into a boundary-relative path:
//   - Relative paths resolve from the given boundary-relative base path.
//   - Absolute paths resolve from the workspace boundary (/).
//
// Returns a path relative to the workspace boundary.
func resolveWorkspacePath(pathArg, basePath string) (string, error) {
	if basePath == "" {
		basePath = "."
	}
	clean := filepath.Clean(pathArg)
	var resolved string
	if filepath.IsAbs(clean) {
		// Absolute path: relative to workspace boundary (strip leading /).
		resolved = strings.TrimPrefix(clean, string(filepath.Separator))
	} else {
		resolved = filepath.Join(basePath, clean)
	}
	resolved = filepath.Clean(resolved)
	if resolved == "" {
		resolved = "."
	}
	if filepath.IsAbs(resolved) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path %q escapes workspace root", pathArg)
	}
	return resolved, nil
}

func workspaceAPIPath(resolvedPath string) string {
	clean := path.Clean(filepath.ToSlash(resolvedPath))
	if clean == "." || clean == "" {
		return "/"
	}
	return "/" + strings.TrimPrefix(clean, "/")
}

type workspaceFindUpArgs struct {
	Name string
	From string `default:"."`
}

func (s *workspaceSchema) findUp(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceFindUpArgs,
) (dagql.Nullable[dagql.String], error) {
	none := dagql.Null[dagql.String]()
	ws := parent.Self()
	if args.Name == "." {
		// Existing SDK code uses "." to ask for the resolved start directory.
		// It is safe here because resolveWorkspacePath still enforces the
		// workspace boundary for args.From below.
	} else if !isWorkspaceBasename(args.Name) {
		return none, fmt.Errorf("workspace findUp name must be a basename")
	}

	resolvedFrom, err := resolveWorkspacePath(args.From, ws.Cwd)
	if err != nil {
		return none, err
	}
	curDir := path.Clean(filepath.ToSlash(resolvedFrom))
	if curDir == "" {
		curDir = "."
	}

	var statFS core.StatFS
	pathForStat := func(candidate string) (string, error) {
		return candidate, nil
	}
	if ws.HostPath() != "" {
		ctx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return none, err
		}
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return none, err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return none, fmt.Errorf("buildkit: %w", err)
		}
		statFS = core.NewCallerStatFS(bk)
		boundaryRoot := ws.HostPath()
		pathForStat = func(candidate string) (string, error) {
			return pathutil.SandboxedRelativePath(candidate, boundaryRoot)
		}
	} else {
		rootfs, err := workspaceRootfs(ws)
		if err != nil {
			return none, err
		}
		statFS = &core.DirectoryStatFS{Dir: rootfs}
	}

	// Walk up from the resolved start path, stopping at the workspace boundary.
	for {
		candidate := path.Join(curDir, args.Name)
		statPath, err := pathForStat(candidate)
		if err != nil {
			return none, err
		}
		_, exists, err := core.StatFSExists(ctx, statFS, statPath)
		if err != nil {
			return none, fmt.Errorf("stat %s: %w", candidate, err)
		}
		if exists {
			return dagql.NonNull(dagql.NewString(workspaceAPIPath(candidate))), nil
		}

		// Stop at workspace boundary.
		if path.Clean(curDir) == "." {
			break
		}

		nextDir := path.Dir(curDir)
		if nextDir == curDir {
			// hit filesystem root (shouldn't happen since we check workspace boundary first)
			break
		}
		curDir = nextDir
	}

	return none, nil
}

func isWorkspaceBasename(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if name != path.Base(name) {
		return false
	}
	return !strings.Contains(name, "\\")
}

func (s *workspaceSchema) checks(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include      dagql.Optional[dagql.ArrayInput[dagql.String]]
		Skip         dagql.Optional[dagql.ArrayInput[dagql.String]]
		NoGenerate   dagql.Optional[dagql.Boolean]
		OnlyGenerate dagql.Optional[dagql.Boolean]
	},
) (*core.CheckGroup, error) {
	if isSyntheticWorkspace(parent) {
		return &core.CheckGroup{}, nil
	}

	include := workspaceIncludePatterns(args.Include)
	skip := workspaceIncludePatterns(args.Skip)

	ctx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, err
	}

	noGenerate := args.NoGenerate.GetOr(false).Bool()
	onlyGenerate := args.OnlyGenerate.GetOr(false).Bool()

	cfg, err := workspaceConfigWithCompatFallback(ctx, parent)
	if err != nil {
		return nil, err
	}
	// Apply the workspace default only when no generate flag was passed.
	if !args.NoGenerate.Valid && !args.OnlyGenerate.Valid && cfg.CheckGenerated != nil && !*cfg.CheckGenerated {
		noGenerate = true
	}

	if err := ensureWorkspaceIncludeModulesLoaded(ctx, include); err != nil {
		return nil, err
	}
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}

	ignoreChecks := workspaceConfigSkipPatternsFromConfig(cfg, func(e workspace.ModuleEntry) []string {
		return e.Check.Skip
	})

	var allChecks []*core.Check
	for _, mod := range mods {
		checkGroup, err := core.NewCheckGroup(ctx, mod, nil, noGenerate, onlyGenerate)
		if err != nil {
			return nil, fmt.Errorf("checks from module %q: %w", mod.Self().Name(), err)
		}
		reparentWorkspaceTreeRoot(checkGroup.Node, mod.Self().Name())
		filtered, err := filterNodesByInclude(
			ctx,
			checkGroup.Checks,
			include,
			func(check *core.Check) *core.ModTreeNode { return check.Node },
			func(check *core.Check) string { return check.Name() },
			"check",
		)
		if err != nil {
			return nil, err
		}
		// Apply caller-requested skip patterns.
		if len(skip) > 0 {
			filtered, err = filterNodesByExclude(
				ctx,
				filtered,
				skip,
				func(check *core.Check) *core.ModTreeNode { return check.Node },
				func(check *core.Check) string { return check.Name() },
				"check",
			)
			if err != nil {
				return nil, err
			}
		}
		// Apply ignoreChecks exclusion for this toolchain's checks.
		if exclude := ignoreChecks[mod.Self().Name()]; len(exclude) > 0 {
			filtered, err = filterNodesByExclude(
				ctx,
				filtered,
				exclude,
				func(check *core.Check) *core.ModTreeNode { return check.Node },
				func(check *core.Check) string { return check.Name() },
				"check",
			)
			if err != nil {
				return nil, err
			}
		}
		allChecks = append(allChecks, filtered...)
	}

	return &core.CheckGroup{Checks: allChecks}, nil
}

type workspaceGeneratorModule struct {
	mod          dagql.ObjectResult[*core.Module]
	name         string
	group        *core.GeneratorGroup
	sourceDigest string
	isWrapper    bool
}

func selectVisibleGeneratorModules(entries []workspaceGeneratorModule) []workspaceGeneratorModule {
	// If a wrapper module exposes generators from a blueprint/toolchain, hide the
	// raw source module's generator namespace and keep the user-facing wrapper.
	hasWrapperBySource := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.isWrapper {
			hasWrapperBySource[entry.sourceDigest] = true
		} else if _, ok := hasWrapperBySource[entry.sourceDigest]; !ok {
			hasWrapperBySource[entry.sourceDigest] = false
		}
	}

	visible := make([]workspaceGeneratorModule, 0, len(entries))
	for _, entry := range entries {
		if hasWrapperBySource[entry.sourceDigest] && !entry.isWrapper {
			continue
		}
		visible = append(visible, entry)
	}
	return visible
}

func (s *workspaceSchema) generators(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.GeneratorGroup, error) {
	if isSyntheticWorkspace(parent) {
		return &core.GeneratorGroup{}, nil
	}

	include := workspaceIncludePatterns(args.Include)

	ctx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, err
	}

	// Best-effort: generate is often what repairs a module that can't load —
	// e.g. a dagger-module.toml module whose committed generated files don't
	// exist yet gets them from its SDK's generator. A module that fails to
	// load is skipped with a warning instead of failing the whole run.
	if err := ensureWorkspaceModulesLoaded(ctx, include, true); err != nil {
		return nil, err
	}
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}

	ignoreGenerators, err := workspaceConfigSkipPatterns(ctx, parent, func(e workspace.ModuleEntry) []string {
		return e.Generate.Skip
	})
	if err != nil {
		return nil, err
	}

	moduleGenerators := make([]workspaceGeneratorModule, 0, len(mods))
	for _, mod := range mods {
		generatorGroup, err := core.NewGeneratorGroup(ctx, mod, nil)
		if err != nil {
			return nil, fmt.Errorf("generators from module %q: %w", mod.Self().Name(), err)
		}
		if len(generatorGroup.Generators) == 0 {
			continue
		}

		source := mod.Self().GetSource()
		if source == nil {
			return nil, fmt.Errorf("generators from module %q: no module source available", mod.Self().Name())
		}
		sourceDigest, err := source.SourceImplementationDigest(ctx)
		if err != nil {
			return nil, fmt.Errorf("generators from module %q: source implementation digest: %w", mod.Self().Name(), err)
		}

		isWrapper := false
		contextSource := mod.Self().GetContextSource()
		if contextSource != nil {
			contextDigest, err := contextSource.SourceImplementationDigest(ctx)
			if err != nil {
				return nil, fmt.Errorf("generators from module %q: context source implementation digest: %w", mod.Self().Name(), err)
			}
			isWrapper = sourceDigest != contextDigest
		}

		moduleGenerators = append(moduleGenerators, workspaceGeneratorModule{
			mod:          mod,
			name:         mod.Self().Name(),
			group:        generatorGroup,
			sourceDigest: sourceDigest.String(),
			isWrapper:    isWrapper,
		})
	}

	rawIgnoreGeneratorsBySource := make(map[string][]string, len(moduleGenerators))
	for _, entry := range moduleGenerators {
		if entry.isWrapper {
			continue
		}
		if exclude := ignoreGenerators[entry.name]; len(exclude) > 0 {
			rawIgnoreGeneratorsBySource[entry.sourceDigest] = append(rawIgnoreGeneratorsBySource[entry.sourceDigest], exclude...)
		}
	}

	moduleGenerators = selectVisibleGeneratorModules(moduleGenerators)

	var allGenerators []*core.Generator
	allowSingleModuleCompat := len(moduleGenerators) == 1
	for _, entry := range moduleGenerators {
		reparentWorkspaceTreeRoot(entry.group.Node, entry.name)
		filtered, err := filterGeneratorsByInclude(
			ctx,
			entry.group.Generators,
			include,
			allowSingleModuleCompat,
		)
		if err != nil {
			return nil, err
		}
		exclude := ignoreGenerators[entry.name]
		if entry.isWrapper {
			// Keep ignore behavior attached to the raw toolchain alias even when the
			// workspace view hides that alias behind a wrapper module.
			exclude = append(exclude, rawIgnoreGeneratorsBySource[entry.sourceDigest]...)
		}
		if len(exclude) > 0 {
			filtered, err = filterNodesByExclude(
				ctx,
				filtered,
				exclude,
				func(generator *core.Generator) *core.ModTreeNode { return generator.Node },
				func(generator *core.Generator) string { return generator.Name() },
				"generator",
			)
			if err != nil {
				return nil, err
			}
		}
		allGenerators = append(allGenerators, filtered...)
	}

	return &core.GeneratorGroup{Generators: allGenerators}, nil
}

func (s *workspaceSchema) services(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.UpGroup, error) {
	if isSyntheticWorkspace(parent) {
		return &core.UpGroup{}, nil
	}

	include := workspaceIncludePatterns(args.Include)

	ctx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, err
	}

	if err := ensureWorkspaceIncludeModulesLoaded(ctx, include); err != nil {
		return nil, err
	}
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}

	ignoreServices, err := workspaceConfigSkipPatterns(ctx, parent, func(e workspace.ModuleEntry) []string {
		return e.Up.Skip
	})
	if err != nil {
		return nil, err
	}

	var allUps []*core.Up
	for _, mod := range mods {
		upGroup, err := core.NewUpGroup(ctx, mod, nil)
		if err != nil {
			return nil, fmt.Errorf("services from module %q: %w", mod.Self().Name(), err)
		}
		reparentWorkspaceTreeRoot(upGroup.Node, mod.Self().Name())
		filtered, err := filterNodesByInclude(
			ctx,
			upGroup.Ups,
			include,
			func(up *core.Up) *core.ModTreeNode { return up.Node },
			func(up *core.Up) string { return up.Name() },
			"service",
		)
		if err != nil {
			return nil, err
		}
		if exclude := ignoreServices[mod.Self().Name()]; len(exclude) > 0 {
			filtered, err = filterNodesByExclude(
				ctx,
				filtered,
				exclude,
				func(up *core.Up) *core.ModTreeNode { return up.Node },
				func(up *core.Up) string { return up.Name() },
				"service",
			)
			if err != nil {
				return nil, err
			}
		}
		allUps = append(allUps, filtered...)
	}

	// Resolve port mappings from the workspace config's top-level [ports.<host>]
	// declarations.
	wsCfg, err := workspaceConfigWithCompatFallback(ctx, parent)
	if err != nil {
		return nil, err
	}
	for hostStr, pm := range wsCfg.Ports {
		host, err := strconv.Atoi(hostStr)
		if err != nil {
			return nil, fmt.Errorf("workspace port key %q: %w", hostStr, err)
		}
		for _, up := range allUps {
			if up.Name() != pm.BackendService {
				continue
			}
			up.PortMappings = append(up.PortMappings, core.PortForward{
				Frontend: &host,
				Backend:  pm.BackendPort,
				Protocol: core.NetworkProtocolTCP,
			})
		}
	}

	return &core.UpGroup{Ups: allUps}, nil
}

func workspaceIncludePatterns(includeArg dagql.Optional[dagql.ArrayInput[dagql.String]]) []string {
	if !includeArg.Valid {
		return nil
	}
	patterns := make([]string, 0, len(includeArg.Value))
	for _, pattern := range includeArg.Value {
		patterns = append(patterns, pattern.String())
	}
	return patterns
}

func filterGeneratorsByInclude(
	ctx context.Context,
	generators []*core.Generator,
	include []string,
	allowSingleModuleCompat bool,
) ([]*core.Generator, error) {
	if len(include) == 0 {
		return generators, nil
	}

	filtered := make([]*core.Generator, 0, len(generators))
	for _, generator := range generators {
		match, err := matchWorkspaceInclude(ctx, generator.Node, include)
		if err != nil {
			return nil, fmt.Errorf("generator %q include match: %w", generator.Name(), err)
		}
		if !match && allowSingleModuleCompat {
			match, err = matchSingleModuleInclude(ctx, generator.Node, include)
			if err != nil {
				return nil, fmt.Errorf("generator %q compat include match: %w", generator.Name(), err)
			}
		}
		if match {
			filtered = append(filtered, generator)
		}
	}
	return filtered, nil
}

// matchSingleModuleInclude tries a match without the first element in the path,
// so that "foo" can match "my-module:foo"
func matchSingleModuleInclude(
	ctx context.Context,
	node *core.ModTreeNode,
	include []string,
) (bool, error) {
	if node == nil {
		return false, nil
	}
	path := node.Path()
	if len(path) < 2 {
		return false, nil
	}
	return matchWorkspaceIncludePath(ctx, path[1:], include)
}

func matchWorkspaceIncludePath(
	ctx context.Context,
	path core.ModTreePath,
	include []string,
) (bool, error) {
	if len(include) == 0 {
		return true, nil
	}
	if len(path) == 0 {
		return false, nil
	}
	for _, pattern := range include {
		if match, err := path.Glob(ctx, pattern); err != nil {
			return false, err
		} else if match {
			return true, nil
		}
		patternAsPath := core.NewModTreePath(pattern)
		if patternAsPath.Contains(ctx, path) {
			return true, nil
		}
	}
	return false, nil
}

// ensureWorkspaceIncludeModulesLoaded loads the workspace modules the include
// patterns demand (all when they don't narrow). Selector fields validate
// against the core schema, so loading can wait until resolution.
func ensureWorkspaceIncludeModulesLoaded(ctx context.Context, include []string) error {
	return ensureWorkspaceModulesLoaded(ctx, include, false)
}

func ensureWorkspaceModulesLoaded(ctx context.Context, include []string, bestEffort bool) error {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return err
	}
	return query.Server.EnsureWorkspaceModules(ctx, include, bestEffort)
}

func currentWorkspacePrimaryModules(ctx context.Context) ([]dagql.ObjectResult[*core.Module], error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	served, err := query.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("current served deps: %w", err)
	}

	mods := make([]dagql.ObjectResult[*core.Module], 0, len(served.PrimaryMods()))
	for _, mod := range served.PrimaryMods() {
		modResult := mod.ModuleResult()
		if modResult.Self() == nil {
			continue
		}
		if modResult.Self().Name() == core.ModuleName {
			continue
		}
		mods = append(mods, modResult)
	}
	return mods, nil
}

// workspaceConfigWithCompatFallback returns the real workspace config when it
// exists, the shared legacy compat projection when it does not, or an empty
// config for workspaces with neither.
func workspaceConfigWithCompatFallback(
	ctx context.Context,
	ws *core.Workspace,
) (*workspace.Config, error) {
	if ws.ConfigFile != "" {
		cfg, err := readWorkspaceConfig(ctx, ws)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	}

	if compat := ws.CompatWorkspace(); compat != nil {
		return compat.WorkspaceConfig(), nil
	}

	return &workspace.Config{}, nil
}

// workspaceConfigSkipPatterns reads per-module skip patterns from the served
// workspace config shape, keyed by module name. In legacy compat workspaces,
// there is no dagger.toml yet, so use the shared compat projection that
// migration also persists.
func workspaceConfigSkipPatterns(
	ctx context.Context,
	ws *core.Workspace,
	getter func(workspace.ModuleEntry) []string,
) (map[string][]string, error) {
	cfg, err := workspaceConfigWithCompatFallback(ctx, ws)
	if err != nil {
		return nil, err
	}
	return workspaceConfigSkipPatternsFromConfig(cfg, getter), nil
}

// workspaceConfigSkipPatternsFromConfig derives per-module skip patterns from an
// already-loaded workspace config.
func workspaceConfigSkipPatternsFromConfig(
	cfg *workspace.Config,
	getter func(workspace.ModuleEntry) []string,
) map[string][]string {
	result := make(map[string][]string)
	for name, entry := range cfg.Modules {
		if patterns := getter(entry); len(patterns) > 0 {
			result[name] = patterns
		}
	}
	return result
}

// filterNodesByExclude removes items whose nodes match any of the exclude
// patterns. Matching uses the same single-module compat fallback as include
// filtering (stripping the leading module name segment).
func filterNodesByExclude[T any](
	ctx context.Context,
	items []T,
	exclude []string,
	nodeOf func(T) *core.ModTreeNode,
	nameOf func(T) string,
	itemKind string,
) ([]T, error) {
	if len(exclude) == 0 {
		return items, nil
	}

	filtered := make([]T, 0, len(items))
	for _, item := range items {
		match, err := matchWorkspaceInclude(ctx, nodeOf(item), exclude)
		if err != nil {
			return nil, fmt.Errorf("%s %q exclude match: %w", itemKind, nameOf(item), err)
		}
		if !match {
			// Also try without module prefix for single-module compat.
			match, err = matchSingleModuleInclude(ctx, nodeOf(item), exclude)
			if err != nil {
				return nil, fmt.Errorf("%s %q exclude compat match: %w", itemKind, nameOf(item), err)
			}
		}
		if !match {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func reparentWorkspaceTreeRoot(root *core.ModTreeNode, modName string) {
	if root == nil {
		return
	}
	root.Parent = &core.ModTreeNode{}
	root.Name = modName
}

func matchWorkspaceInclude(ctx context.Context, node *core.ModTreeNode, include []string) (bool, error) {
	if len(include) == 0 {
		return true, nil
	}
	if node == nil {
		return false, nil
	}
	return node.Match(ctx, include)
}

func filterNodesByInclude[T any](
	ctx context.Context,
	items []T,
	include []string,
	nodeOf func(T) *core.ModTreeNode,
	nameOf func(T) string,
	itemKind string,
) ([]T, error) {
	if len(include) == 0 {
		return items, nil
	}

	filtered := make([]T, 0, len(items))
	for _, item := range items {
		match, err := matchWorkspaceInclude(ctx, nodeOf(item), include)
		if err != nil {
			return nil, fmt.Errorf("%s %q include match: %w", itemKind, nameOf(item), err)
		}
		// Preserve old single-module semantics: if the pattern doesn't match
		// the full workspace path (module:check), retry against just the
		// check path without the leading module name segment.
		if !match {
			match, err = matchSingleModuleInclude(ctx, nodeOf(item), include)
			if err != nil {
				return nil, fmt.Errorf("%s %q compat include match: %w", itemKind, nameOf(item), err)
			}
		}
		if match {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// withWorkspaceClientContext overrides the client metadata in context to the
// workspace's owning client ID. This ensures host filesystem operations route
// through the correct client session, even when called from a module context.
func (s *workspaceSchema) withWorkspaceClientContext(ctx context.Context, ws *core.Workspace) (context.Context, error) {
	return withWorkspaceClientContext(ctx, ws)
}

// withWorkspaceClientContext overrides the client metadata in context to the
// workspace's owning client ID. This ensures host filesystem operations route
// through the correct client session, even when called from a module context.
func withWorkspaceClientContext(ctx context.Context, ws *core.Workspace) (context.Context, error) {
	if ws.ClientID == "" {
		return nil, fmt.Errorf("workspace has no client ID")
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}
	clientMetadata, err := query.SpecificClientMetadata(ctx, ws.ClientID)
	if err != nil {
		return ctx, fmt.Errorf("get client metadata: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}
