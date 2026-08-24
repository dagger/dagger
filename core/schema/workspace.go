package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
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
	"golang.org/x/mod/semver"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(srv *dagql.Server) {
	currentWorkspaceField := dagql.NodeFunc("currentWorkspace", s.currentWorkspace).
		WithInput(dagql.PerCallInput, dagql.PerSessionInput).
		NotReplayable("Resolves the calling client's workspace; the result carries that client's ID, which only resolves inside its own session.").
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
			Doc("Selected native workspace config file relative to the workspace cwd, if any."),
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
		dagql.NodeFunc("findRoots", s.findRoots).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc(`Find project roots marked by any of the given filenames, starting from a path relative to the workspace cwd.`,
				`Returns cwd-relative directory paths for every marked directory at or below start, plus the nearest marked ancestor when start itself is not marked.`,
				`Each returned path is usable as-is with other workspace APIs, e.g. directory(path).`).
			Args(
				dagql.Arg("start").Doc(`Directory to start from. Relative paths resolve from the workspace cwd.`),
				dagql.Arg("markers").Doc(`File basenames that mark a project root (e.g. ["go.mod"] or ["deno.json", "deno.jsonc"]).`),
				dagql.Arg("exclude").Doc(`Glob patterns pruning the walk below start (e.g. ["**/node_modules/**"]).`),
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
			Doc("Return this workspace with the given path replaced by a directory, without mutating the source.",
				"The source becomes the entire contents of the path: anything already there that the source does not carry is removed. Use withDirectory to keep it instead.").
			Args(
				dagql.Arg("path").Doc("Path to replace. Relative paths resolve from the workspace cwd."),
				dagql.Arg("source").Doc("Directory to write there."),
			),
		dagql.NodeFunc("withDirectory", s.withDirectory).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a directory merged into the given path, without mutating the source.",
				"Anything already at the path stays, and files the source carries win, as with Directory.withDirectory. Use withNewDirectory to replace the path instead.").
			Args(
				dagql.Arg("path").Doc("Path to merge into. Relative paths resolve from the workspace cwd."),
				dagql.Arg("source").Doc("Directory to merge there."),
			),
		dagql.NodeFunc("withoutFile", s.withoutFile).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a file removed, without mutating the source.").
			Args(
				dagql.Arg("path").Doc("Path of the file to remove. Relative paths resolve from the workspace cwd."),
			),
		dagql.NodeFunc("withoutDirectory", s.withoutDirectory).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a directory removed, without mutating the source.").
			Args(
				dagql.Arg("path").Doc("Path of the directory to remove. Relative paths resolve from the workspace cwd."),
			),
		dagql.NodeFunc("withChanges", s.withChanges).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a changeset applied, without mutating the source.").
			Args(
				dagql.Arg("changes").Doc("Changes to apply."),
			),
		dagql.NodeFunc("__withGeneratedLocalDependencies", s.withGeneratedLocalDependencies).
			Doc("(Internal-only) Return this workspace with a module's generated local dependency closure applied and recorded.",
				"Applies internally generated local-dependency changes for the module at the given path, and marks the workspace so nested generation for that module does not repeat the staging.").
			Args(
				dagql.Arg("module").Doc("Workspace-root-relative path of the module whose generated local dependencies these are."),
				dagql.Arg("changes").Doc("The staged dependency codegen to apply."),
			),
		dagql.NodeFunc("__sdkGenerators", s.sdkGenerators).
			Doc("(Internal-only) The generators exposed by an SDK installed in this workspace.",
				"Built straight from the SDK's own module, so it demands no workspace module loading: an init flow can generate for what it just created without pulling in the rest of the workspace.").
			Args(
				dagql.Arg("sdk").Doc("Workspace SDK name or module entry name whose generators to collect."),
			),
		dagql.NodeFunc("withWorkdir", s.withWorkdir).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with its working directory pointed at the given workspace-relative path.").
			Args(
				dagql.Arg("path").Doc("Workspace-relative path to use as the working directory."),
			),
		dagql.NodeFunc("withMountedDirectory", s.withMountedDirectory).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a directory mounted read-only at the given path, without mutating the source.",
				"Mounted content is readable through the normal workspace file tools but shadows the source at the mount path and stays out of the pending changeset: it never appears in changes, is never exported, and cannot be modified.").
			Args(
				dagql.Arg("path").Doc("Location of the mounted directory. Relative paths resolve from the workspace cwd."),
				dagql.Arg("source").Doc("Directory to mount."),
			),
		dagql.NodeFunc("withMountedFile", s.withMountedFile).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a file mounted read-only at the given path, without mutating the source.",
				"Mounted content is readable through the normal workspace file tools but shadows the source at the mount path and stays out of the pending changeset: it never appears in changes, is never exported, and cannot be modified.").
			Args(
				dagql.Arg("path").Doc("Location of the mounted file. Relative paths resolve from the workspace cwd."),
				dagql.Arg("source").Doc("File to mount."),
			),
		dagql.NodeFunc("withModule", s.withModule).
			View(AfterVersion("v1.0.0-0")).
			// Env-sensitive writes: what this records depends on the client's env
			// selection, which travels in client metadata rather than the
			// workspace ID, so a recipe recorded under one env must not replay
			// under another.
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with a module installed in its config.",
				"When the session selects an env, the module is recorded in that env's overlay and the env is created if missing.").
			Args(
				dagql.Arg("ref").Doc("Module reference to install."),
				dagql.Arg("name").Doc("Override name for the installed module entry."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withoutModule", s.withoutModule).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with a module removed from its config.",
				"When the session selects an env, only that env's overlay entry is removed.").
			Args(
				dagql.Arg("name").Doc("Name of the installed module entry to remove."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withSDK", s.withSDK).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with an SDK installed in its config.").
			Args(
				dagql.Arg("ref").Doc("SDK module reference to install."),
				dagql.Arg("name").Doc("Override name for the installed SDK entry."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
				dagql.Arg("asSdkName").Doc("User-facing SDK name to persist under `[modules.<name>.as-sdk] name = ...`."),
			),
		dagql.NodeFunc("withoutSDK", s.withoutSDK).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with an SDK removed from its config.").
			Args(
				dagql.Arg("name").Doc("Name of the installed SDK entry to remove."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withInitModule", s.withInitModule).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a new module initialized.",
				"The SDK's generators run for the new module, so the returned workspace carries the generated code it needs to be loadable.").
			Args(
				dagql.Arg("name").Doc("Name of the new module."),
				dagql.Arg("sdk").Doc("Workspace SDK name or module entry name to use."),
				dagql.Arg("path").Doc(`Path for the new module, relative to the workspace cwd; a leading "/" is relative to the workspace root. Defaults to .dagger/modules/<name> beside the workspace config.`),
				dagql.Arg("source").Doc("Source subpath within the new module."),
				dagql.Arg("include").Doc("Additional include patterns for the module."),
				dagql.Arg("args").Doc("SDK-specific init arguments."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
				dagql.Arg("noGenerate").Doc("Skip running the SDK's generators for the new module."),
			),
		dagql.NodeFunc("withInitClient", s.withInitClient).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a generated API client initialized.",
				"The SDK's generators run for the new client, so the returned workspace carries its generated bindings.").
			Args(
				dagql.Arg("path").Doc(`Output directory for the generated client, relative to the workspace cwd; a leading "/" is relative to the workspace root.`),
				dagql.Arg("sdk").Doc("Workspace SDK name or module entry name to use."),
				dagql.Arg("module").Doc("Workspace-relative path or canonical ref for the module the client binds to."),
				dagql.Arg("args").Doc("SDK-specific init arguments."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
				dagql.Arg("noGenerate").Doc("Skip running the SDK's generators for the new client."),
			),
		dagql.NodeFunc("withConfigValue", s.withConfigValue).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with a configuration value written.",
				"When the session selects an env, the key is scoped to that env's overlay and the env is created if missing.").
			Args(
				dagql.Arg("key").Doc("Dotted key path."),
				dagql.Arg("value").Doc("Value to set. Bools, integers, and comma-separated arrays are auto-detected."),
				dagql.Arg("values").Doc("List value to set. Elements are stored verbatim, with no auto-detection. Mutually exclusive with value."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withoutConfigValue", s.withoutConfigValue).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with a configuration value removed.",
				"Errors when the key is not currently set.",
				"When the session selects an env, the key is scoped to that env's overlay.").
			Args(
				dagql.Arg("key").Doc("Dotted key path (e.g. modules.greeter.settings.greeting)."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withConfigEnv", s.withConfigEnv).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Return this workspace with a named config environment created.").
			Args(
				dagql.Arg("name").Doc("Environment name."),
				dagql.Arg("here").Doc("Write to the workspace config directory at the workspace cwd."),
			),
		dagql.NodeFunc("withoutConfigEnv", s.withoutConfigEnv).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
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
			Doc("Return this workspace's changes, with paths relative to its working directory.",
				"Pass from to compare against an earlier workspace state. Omitting it preserves the cumulative behavior used by clients from before this argument was added.").
			Args(
				dagql.Arg("from").Doc("An earlier workspace state to compare against."),
			),
		dagql.NodeFunc("export", s.export).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Writes pending workspace changes to the local Git workspace").
			Doc("Write this workspace's pending changes to its local Git workspace."),
		dagql.NodeFunc("reloaded", s.reloaded).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerCallInput).
			Doc("Return this workspace with its cached host reads invalidated, so subsequent file and directory reads re-read the live host instead of a snapshot cached earlier in the session."),
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
			// The listing is the env-effective view, and the selected env comes
			// from client metadata rather than the workspace ID.
			DoNotCache("Reads live config from host").
			Doc("List modules defined in the workspace configuration.",
				"Reflects the selected env's effective view."),
		dagql.NodeFunc("module", s.module).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Reads live config from host").
			Doc("Return a module defined in the workspace configuration.",
				"Reflects the selected env's effective view.").
			Args(
				dagql.Arg("name").Doc("Module name to inspect."),
			),
		dagql.NodeFunc("moduleSource", s.moduleSource).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerClientInput).
			Doc("Load a module source from a path within the workspace.",
				`Relative paths (e.g., "foo") resolve from the workspace cwd; absolute paths (e.g., "/foo") resolve from the workspace root.`,
				"Fails if the path does not point to an initialized module.").
			Args(
				dagql.Arg("path").Doc(`Location of the module source to load, relative to the workspace cwd or absolute from the workspace root.`),
			),
		dagql.Func("cwd", s.cwd).
			View(AfterVersion("v1.0.0-0")).
			Doc("Current location within the workspace root.",
				`The workspace root is returned as "/".`,
				"Relative paths in workspace APIs resolve from here."),
		dagql.NodeFunc("checks", s.checks).
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
		dagql.NodeFunc("generators", s.generators).
			Doc("Return all generators from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include generators matching the specified patterns"),
			),
		dagql.NodeFunc("services", s.services).
			Doc("Return all services from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include services matching the specified patterns"),
			),
		dagql.NodeFunc("agents", s.agents).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return all agent middlewares from modules loaded in the workspace.").
			Args(
				dagql.Arg("include").Doc("Only include agents matching the specified patterns"),
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
	ws.SetSource(core.NewWorkspaceSourceGitRef(ref.Result, false))
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
	parent dagql.ObjectResult[*core.Query],
	_ struct{},
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	// Prefer a Workspace explicitly bound into the context (an LLM operating on
	// its own, possibly overlaid, Workspace; a generator/check group threading
	// the workspace it was rolled up from) over the session's frozen current
	// workspace, so module tools observe edits the agent has applied. This
	// mirrors loadWorkspaceArg's preference for the bound workspace.
	if boundWS, ok := core.WorkspaceFromContext(ctx); ok {
		return boundWS, nil
	}

	ws, err := parent.Self().Server.CurrentWorkspace(ctx)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
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
	configFile, err := workspacePathRelativeToCwd(parent.ConfigFile, parent.Cwd)
	if err != nil {
		return "", err
	}
	return dagql.NewString(configFile), nil
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

// resolveReadRootfs resolves a workspace read (Workspace.directory/file) with
// mounted content visible:
//   - Paths at or under a mount point resolve entirely against the read-only
//     mounts tree — mounted content shadows the source.
//   - Paths with mount points beneath them get the mounted content overlaid on
//     the source read, so listings include it. As with container mounts, the
//     mount point's parents don't need to exist in the source.
//
// Everything that materializes the workspace *source* (module loading,
// lockfiles, migration, git) resolves through resolveRootfs directly and never
// sees mounts, mirroring how changes and export exclude them.
func (s *workspaceSchema) resolveReadRootfs(
	ctx context.Context,
	ws *core.Workspace,
	resolvedPath string,
	filter core.CopyFilter,
	gitignore bool,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	mounts, ok := ws.MountsDir()
	if !ok {
		return s.resolveRootfs(ctx, ws, resolvedPath, filter, gitignore)
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if ws.MountedPath(resolvedPath) {
		return s.resolveRootfsFromDirectory(ctx, srv, ws, mounts, resolvedPath, filter, gitignore)
	}
	if !ws.HasMountsUnder(resolvedPath) {
		return s.resolveRootfs(ctx, ws, resolvedPath, filter, gitignore)
	}
	sub, err := s.resolveRootfsFromDirectory(ctx, srv, ws, mounts, resolvedPath, filter, gitignore)
	if err != nil {
		return inst, err
	}
	// The source path itself may not exist (mounts materialize their own
	// parents); serve the mounted content alone rather than erroring on the
	// source read.
	exists, err := s.workspaceReadPathExists(ctx, ws, resolvedPath)
	if err != nil {
		return inst, err
	}
	if !exists {
		return sub, nil
	}
	base, err := s.resolveRootfs(ctx, ws, resolvedPath, filter, gitignore)
	if err != nil {
		return inst, err
	}
	subID, err := sub.ID()
	if err != nil {
		return inst, err
	}
	var merged dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, base, &merged, dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
			{Name: "source", Value: dagql.NewID[*core.Directory](subID)},
		},
	}); err != nil {
		return inst, err
	}
	return merged, nil
}

// workspaceReadPathExists reports whether a resolved workspace path exists in
// the workspace source (host, overlay edits, or in-engine tree), without
// considering mounts.
func (s *workspaceSchema) workspaceReadPathExists(
	ctx context.Context,
	ws *core.Workspace,
	resolvedPath string,
) (bool, error) {
	rp := filepath.ToSlash(resolvedPath)
	// Overlay edits can create paths that exist in no base source; host-backed
	// overlays track them as touched paths (tree-backed overlays already fold
	// edits into the source directory checked below).
	if overlay, ok := ws.Source().(*core.WorkspaceSourceOverlay); ok {
		for _, touched := range overlay.TouchedPaths {
			tp := filepath.ToSlash(touched)
			if tp == rp || strings.HasPrefix(tp, rp+"/") || rp == "." {
				return true, nil
			}
		}
	}
	if ws.HostPath() != "" && ws.ClientLocalBase() {
		ctx, err := s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return false, err
		}
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return false, err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return false, fmt.Errorf("buildkit: %w", err)
		}
		statPath, err := pathutil.SandboxedRelativePath(resolvedPath, ws.HostPath())
		if err != nil {
			return false, err
		}
		_, exists, err := core.StatFSExists(ctx, core.NewCallerStatFS(bk), statPath)
		return exists, err
	}
	if root, ok := ws.SourceDirectory(); ok && root.Self() != nil {
		_, exists, err := core.StatFSExists(ctx, &core.DirectoryStatFS{Dir: root}, rp)
		return exists, err
	}
	// Rootless/synthetic workspaces have no base content.
	return false, nil
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
		ctx, err = s.withWorkspaceHostReadContext(ctx, ws)
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
	hostCtx, err := s.withWorkspaceHostReadContext(ctx, ws)
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
	return s.resolveReadRootfs(ctx, ws, resolvedPath, args.CopyFilter, args.Gitignore)
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

	dir, err := s.resolveReadRootfs(ctx, ws, parentDir, core.CopyFilter{
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
	if err := guardMountedPath(parent.Self(), resolvedPath); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
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

	var results []*core.SearchResult
	if ws.HostPath() == "" {
		// No host boundary: search the workspace's in-engine root filesystem.
		// Overlay edits are already visible here: value/git overlays surface
		// the changeset's after-tree as the source directory.
		rootfs, err := workspaceRootfs(ws)
		if err != nil {
			return nil, err
		}
		results, err = rootfs.Self().Search(ctx, rootfs, args.SearchOpts, false, args.Paths, args.Globs)
		if err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
	} else {
		var err error
		results, err = s.searchHost(ctx, ws, args)
		if err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}

		if _, ok := ws.OverlayChanges(); ok && ws.ClientLocalBase() {
			overlayResults, err := s.overlaySearchResults(ctx, ws, args)
			if err != nil {
				return nil, fmt.Errorf("search: %w", err)
			}
			results = mergeSearchResults(results, overlayResults, ws.OverlayPathTouched, args.Limit)
		}
	}

	// Mounted content is readable through the normal workspace file tools, so
	// it is searchable too: the mounts tree's results win at and under mount
	// points, mirroring resolveReadRootfs's shadowing.
	if mounts, ok := ws.MountsDir(); ok {
		mountResults, err := searchDirectoryTree(ctx, mounts, args)
		if err != nil {
			return nil, fmt.Errorf("search: mounts: %w", err)
		}
		results = mergeSearchResults(results, mountResults, ws.MountedPath, args.Limit)
	}

	emitSearchResults(ctx, results, args.FilesOnly)
	return dagql.Array[*core.SearchResult](results), nil
}

// searchHost runs the search client-side against the workspace's host path
// and converts the results, without emitting them to span stdio.
func (s *workspaceSchema) searchHost(
	ctx context.Context,
	ws *core.Workspace,
	args workspaceSearchArgs,
) ([]*core.SearchResult, error) {
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
		return nil, err
	}

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
	}
	return results, nil
}

// overlaySearchResults searches the overlay's delta root — the accumulated
// edits applied to an empty base, which holds the full after-state of every
// touched path.
func (s *workspaceSchema) overlaySearchResults(
	ctx context.Context,
	ws *core.Workspace,
	args workspaceSearchArgs,
) ([]*core.SearchResult, error) {
	delta, ok := ws.OverlayDeltaRoot()
	if !ok || delta.Self() == nil {
		return nil, nil
	}
	results, err := searchDirectoryTree(ctx, delta, args)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	return results, nil
}

// searchDirectoryTree runs the search against a sparse in-engine tree (the
// overlay's delta root or the mounts tree). The tree lacks paths that only
// exist elsewhere in the workspace, so explicit search paths are applied as a
// post-filter instead of being passed to ripgrep (which errors on missing
// path operands).
func searchDirectoryTree(
	ctx context.Context,
	tree dagql.ObjectResult[*core.Directory],
	args workspaceSearchArgs,
) ([]*core.SearchResult, error) {
	opts := args.SearchOpts
	opts.Limit = nil // the limit caps the merged results, not each side
	results, err := tree.Self().Search(ctx, tree, opts, false, nil, args.Globs)
	if err != nil {
		return nil, err
	}
	if len(args.Paths) == 0 {
		return results, nil
	}
	scoped := results[:0]
	for _, r := range results {
		if searchPathInScopes(r.FilePath, args.Paths) {
			scoped = append(scoped, r)
		}
	}
	return scoped, nil
}

// mergeSearchResults combines two sides of a workspace search with per-file
// replacement: the second side's view wins for every path the predicate
// claims (an overlay's touched paths, a mount's shadowed paths), so replaced
// files don't surface stale lines and removed files drop out entirely. The
// merged set is sorted for determinism and capped at limit when set.
func mergeSearchResults(
	base, winning []*core.SearchResult,
	claimed func(string) bool,
	limit *int,
) []*core.SearchResult {
	merged := make([]*core.SearchResult, 0, len(base)+len(winning))
	for _, r := range base {
		if !claimed(r.FilePath) {
			merged = append(merged, r)
		}
	}
	merged = append(merged, winning...)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].FilePath != merged[j].FilePath {
			return merged[i].FilePath < merged[j].FilePath
		}
		return merged[i].LineNumber < merged[j].LineNumber
	})
	if limit != nil && *limit >= 0 && len(merged) > *limit {
		merged = merged[:*limit]
	}
	return merged
}

// searchPathInScopes reports whether a result path falls under any of the
// requested search paths (matching a file itself or anything beneath a
// directory).
func searchPathInScopes(filePath string, scopes []string) bool {
	fp := path.Clean(filepath.ToSlash(filePath))
	for _, scope := range scopes {
		sc := path.Clean(strings.TrimPrefix(filepath.ToSlash(scope), "/"))
		if sc == "." || fp == sc || strings.HasPrefix(fp, sc+"/") {
			return true
		}
	}
	return false
}

// emitSearchResults writes search results to the span's stdio so they show up
// in progress output (and are visible to an LLM driving the search).
func emitSearchResults(ctx context.Context, results []*core.SearchResult, filesOnly bool) {
	stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
	defer stdio.Close()
	for _, result := range results {
		if filesOnly {
			fmt.Fprintln(stdio.Stdout, result.FilePath)
		} else {
			ensureLn := result.MatchedLines
			if !strings.HasSuffix(ensureLn, "\n") {
				ensureLn += "\n"
			}
			fmt.Fprintf(stdio.Stdout, "%s:%d:%s", result.FilePath, result.LineNumber, ensureLn)
		}
	}
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

	var matches []string
	if ws.HostPath() != "" {
		hostCtx, err := s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return nil, err
		}
		query, err := core.CurrentQuery(hostCtx)
		if err != nil {
			return nil, err
		}
		bk, err := query.Engine(hostCtx)
		if err != nil {
			return nil, fmt.Errorf("buildkit: %w", err)
		}
		matches, err = bk.GlobCallerHostPath(hostCtx, ws.HostPath(), args.Pattern)
		if err != nil {
			return nil, fmt.Errorf("glob: %w", err)
		}
		if _, ok := ws.OverlayChanges(); ok && ws.ClientLocalBase() {
			overlayMatches, err := s.overlayGlobMatches(ctx, ws, args.Pattern)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}
			matches = mergeGlobMatches(matches, overlayMatches, ws.OverlayPathTouched)
		}
	} else {
		rootfs, err := workspaceRootfs(ws)
		if err != nil {
			return nil, err
		}
		matches, err = globDirectoryTree(ctx, rootfs, args.Pattern)
		if err != nil {
			return nil, fmt.Errorf("glob: %w", err)
		}
	}

	// Mounted content is readable through the normal workspace file tools, so
	// it is globbable too: the mounts tree's matches win at and under mount
	// points, mirroring resolveReadRootfs's shadowing.
	if mounts, ok := ws.MountsDir(); ok {
		mountMatches, err := globDirectoryTree(ctx, mounts, args.Pattern)
		if err != nil {
			return nil, fmt.Errorf("glob: mounts: %w", err)
		}
		matches = mergeGlobMatches(matches, mountMatches, ws.MountedPath)
	}

	return dagql.NewStringArray(matches...), nil
}

// overlayGlobMatches runs the glob against the overlay's delta root — the
// accumulated edits applied to an empty base, which holds the full
// after-state of every touched path.
func (s *workspaceSchema) overlayGlobMatches(
	ctx context.Context,
	ws *core.Workspace,
	pattern string,
) ([]string, error) {
	delta, ok := ws.OverlayDeltaRoot()
	if !ok || delta.Self() == nil {
		return nil, nil
	}
	matches, err := globDirectoryTree(ctx, delta, pattern)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	return matches, nil
}

// globDirectoryTree runs a glob against an in-engine directory tree (the
// workspace rootfs, the overlay's delta root, or the mounts tree).
func globDirectoryTree(
	ctx context.Context,
	tree dagql.ObjectResult[*core.Directory],
	pattern string,
) ([]string, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var matches dagql.Array[dagql.String]
	if err := srv.Select(ctx, tree, &matches, dagql.Selector{
		Field: "glob",
		Args: []dagql.NamedInput{
			{Name: "pattern", Value: dagql.NewString(pattern)},
		},
	}); err != nil {
		return nil, err
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = string(m)
	}
	return out, nil
}

// mergeGlobMatches combines two sides of a workspace glob with per-path
// replacement: the second side's view wins for every path the predicate
// claims (an overlay's touched paths, a mount's shadowed paths), so removed
// paths drop out and added ones come from the winning tree. Parent
// directories that exist in both trees dedup to the base's entry. The merged
// set is sorted for determinism.
func mergeGlobMatches(base, winning []string, claimed func(string) bool) []string {
	seen := make(map[string]bool, len(base)+len(winning))
	merged := make([]string, 0, len(base)+len(winning))
	add := func(m string) {
		key := path.Clean(filepath.ToSlash(m))
		if !seen[key] {
			seen[key] = true
			merged = append(merged, m)
		}
	}
	for _, m := range base {
		if claimed(m) {
			continue
		}
		add(m)
	}
	for _, m := range winning {
		add(m)
	}
	sort.Strings(merged)
	return merged
}

type workspaceWriteDirectoryArgs struct {
	Path   string
	Source core.DirectoryID
}

func (s *workspaceSchema) withNewDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWriteDirectoryArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := guardMountedPath(parent.Self(), resolvedPath); err != nil {
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
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		// Clearing first is what lets both overlay branches share this edit:
		// an edit that ignores whatever the base holds at the path reads the
		// same on a full read root as on a host overlay's delta root.
		cleared, err := clearedForWrite(ctx, srv, base, resolvedPath)
		if err != nil {
			return cleared, err
		}
		var updated dagql.ObjectResult[*core.Directory]
		err = srv.Select(ctx, cleared, &updated, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(resolvedPath)},
				{Name: "source", Value: dagql.NewID[*core.Directory](sourceID)},
			},
		})
		return updated, err
	}, nil)
}

func (s *workspaceSchema) withDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWriteDirectoryArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := guardMountedPath(parent.Self(), resolvedPath); err != nil {
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
	// The edit layers onto what the path already holds, so the base has to
	// carry it — see overlayEdit's readsExisting.
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, []string{resolvedPath}, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
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

// clearedForWrite returns base holding nothing at path, ready for a write that
// replaces what was there. Clearing the workspace root starts from an empty
// directory instead: Directory.withoutDirectory removes the path itself, which
// at the root is the tree rather than its contents.
func clearedForWrite(
	ctx context.Context,
	srv *dagql.Server,
	base dagql.ObjectResult[*core.Directory],
	resolvedPath string,
) (dagql.ObjectResult[*core.Directory], error) {
	var cleared dagql.ObjectResult[*core.Directory]
	if resolvedPath == "." {
		err := srv.Select(ctx, srv.Root(), &cleared, dagql.Selector{Field: "directory"})
		return cleared, err
	}
	err := srv.Select(ctx, base, &cleared, dagql.Selector{
		Field: "withoutDirectory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(resolvedPath)}},
	})
	return cleared, err
}

type workspaceWithoutFileArgs struct {
	Path string
}

//nolint:dupl // symmetric with (*workspaceSchema).withoutDirectory; sharing hides the file vs directory semantic
func (s *workspaceSchema) withoutFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithoutFileArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := guardMountedPath(parent.Self(), resolvedPath); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withoutFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(resolvedPath)},
			},
		})
		return updated, err
	}, nil)
}

type workspaceWithoutDirectoryArgs struct {
	Path string
}

//nolint:dupl // symmetric with (*workspaceSchema).withoutFile; sharing hides the directory vs file semantic
func (s *workspaceSchema) withoutDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithoutDirectoryArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(args.Path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := guardMountedPath(parent.Self(), resolvedPath); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, []string{resolvedPath}, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withoutDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(resolvedPath)},
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
	return s.applyChangeset(ctx, parent, args.Changes, nil)
}

// __withGeneratedLocalDependencies (Dagger-internal) applies the changeset
// produced by internal local-dependency generation for the module at the
// given workspace-root-relative path, and records that module in the
// workspace's StagedGeneration set so nested local-dependency generation
// for it short-circuits instead of re-staging the closure this workspace
// already carries. Combining the apply and the mark in one field keeps the
// provenance structural: a workspace can only be marked for a module by
// applying that module's staged dependency codegen.
func (s *workspaceSchema) withGeneratedLocalDependencies(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		Module  string
		Changes dagql.ID[*core.Changeset]
	},
) (dagql.ObjectResult[*core.Workspace], error) {
	modPath := cleanWorkspaceRelPath(args.Module)
	return s.applyChangeset(ctx, parent, args.Changes, func(ws *core.Workspace) {
		if !slices.Contains(ws.StagedGeneration, modPath) {
			ws.StagedGeneration = append(slices.Clone(ws.StagedGeneration), modPath)
			slices.Sort(ws.StagedGeneration)
		}
	})
}

// applyChangeset overlays a changeset onto the workspace, optionally mutating
// the resulting workspace value. Shared by withChanges and
// __withGeneratedLocalDependencies.
func (s *workspaceSchema) applyChangeset(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	changes dagql.ID[*core.Changeset],
	mutate func(*core.Workspace),
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	changesID, err := changes.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	changesObj, err := changes.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	touched, err := changesetTouchedPaths(ctx, changesObj.Self())
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	for _, p := range touched {
		if err := guardMountedPath(parent.Self(), p); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	return s.overlayEdit(ctx, parent, touched, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withChanges",
			Args: []dagql.NamedInput{
				{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)},
			},
		})
		return updated, err
	}, mutate)
}

func (s *workspaceSchema) withWorkdir(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		Path string
	},
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	// Public schema surface: keep the working directory inside the workspace root.
	// cleanWorkspaceRelPath is only filepath.Clean, so reject absolute paths and
	// anything escaping via "..".
	cwd := cleanWorkspaceRelPath(args.Path)
	if filepath.IsAbs(args.Path) || cwd == ".." || strings.HasPrefix(cwd, ".."+string(filepath.Separator)) {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace working directory %q must be a relative path within the workspace root", args.Path)
	}
	ws := parent.Self().Clone()
	ws.Cwd = cwd
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

type workspaceWithMountedDirectoryArgs struct {
	Path   string
	Source core.DirectoryID
}

func (s *workspaceSchema) withMountedDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithMountedDirectoryArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return withMountedSource(ctx, parent, args.Path, args.Source, "withDirectory")
}

type workspaceWithMountedFileArgs struct {
	Path   string
	Source core.FileID
}

func (s *workspaceSchema) withMountedFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithMountedFileArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return withMountedSource(ctx, parent, args.Path, args.Source, "withFile")
}

// withMountedSource is the shared implementation of withMountedDirectory and
// withMountedFile: it attaches the given source (a Directory or File) into the
// workspace's read-only mounts tree at the resolved workspace path via the
// named Directory field ("withDirectory" or "withFile"), and records the mount
// point.
func withMountedSource[T dagql.Typed](
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	path string,
	source dagql.ID[T],
	field string,
) (dagql.ObjectResult[*core.Workspace], error) {
	resolvedPath, err := resolveWorkspacePath(path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if resolvedPath == "." {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("cannot mount over the workspace root")
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sourceID, err := source.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	mounts, ok := parent.Self().MountsDir()
	if !ok {
		if err := srv.Select(ctx, srv.Root(), &mounts, dagql.Selector{Field: "directory"}); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	var updated dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, mounts, &updated, dagql.Selector{
		Field: field,
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(resolvedPath)},
			{Name: "source", Value: dagql.NewID[T](sourceID)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	ws := parent.Self().WithMounted(updated, resolvedPath)
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

// guardMountedPath rejects overlay edits that target a path at or under a
// mount point, keeping mounted content read-only. It is a no-op when the
// workspace has no mounts.
func guardMountedPath(ws *core.Workspace, resolvedPath string) error {
	if ws.MountedPath(resolvedPath) {
		return fmt.Errorf("workspace path %q is a read-only mount and cannot be modified", resolvedPath)
	}
	return nil
}

func (s *workspaceSchema) changes(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		From dagql.Optional[dagql.ID[*core.Workspace]]
	},
) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if !args.From.Valid {
		var changes dagql.ObjectResult[*core.Changeset]
		if overlay, ok := parent.Self().OverlayChanges(); ok {
			changes = overlay
		} else {
			empty, err := core.NewEmptyChangeset(ctx)
			if err != nil {
				return inst, err
			}
			changes, err = dagql.NewObjectResultForCurrentCall(ctx, srv, empty)
			if err != nil {
				return inst, err
			}
		}
		if callerPastChangesetCwdCutover(ctx) {
			return reRootChangesetToCwd(ctx, changes, parent.Self().Cwd)
		}
		return changes, nil
	}

	from, err := args.From.Value.Load(ctx, srv)
	if err != nil {
		return inst, fmt.Errorf("load comparison workspace: %w", err)
	}
	changes, err := s.workspaceChangesBetween(ctx, from, parent)
	if err != nil {
		return inst, err
	}

	if callerPastChangesetCwdCutover(ctx) {
		return reRootChangesetToCwd(ctx, changes, parent.Self().Cwd)
	}
	return changes, nil
}

// workspaceChangesBetween compares two workspace values without materializing
// an entire client-local workspace. Host-backed workspaces are reconstructed
// over a sparse host view containing only paths touched by either side; all
// other workspace kinds already have full in-engine roots.
func (s *workspaceSchema) workspaceChangesBetween(
	ctx context.Context,
	from dagql.ObjectResult[*core.Workspace],
	after dagql.ObjectResult[*core.Workspace],
) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	var beforeRoot, afterRoot dagql.ObjectResult[*core.Directory]
	if from.Self().ClientLocalBase() || after.Self().ClientLocalBase() {
		if !from.Self().ClientLocalBase() || !after.Self().ClientLocalBase() ||
			from.Self().HostPath() != after.Self().HostPath() ||
			from.Self().ClientID != after.Self().ClientID {
			return inst, fmt.Errorf("cannot compare workspaces with different host roots")
		}

		touched := unionPaths(from.Self().OverlayTouchedPaths(), after.Self().OverlayTouchedPaths())
		base, err := s.sparseHostBase(ctx, after.Self(), touched)
		if err != nil {
			return inst, err
		}
		apply := func(ws *core.Workspace) (dagql.ObjectResult[*core.Directory], error) {
			changes, ok := ws.OverlayChanges()
			if !ok {
				return base, nil
			}
			changesID, err := changes.ID()
			if err != nil {
				return dagql.ObjectResult[*core.Directory]{}, err
			}
			var root dagql.ObjectResult[*core.Directory]
			err = srv.Select(ctx, base, &root, dagql.Selector{
				Field: "withChanges",
				Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
			})
			return root, err
		}
		beforeRoot, err = apply(from.Self())
		if err != nil {
			return inst, err
		}
		afterRoot, err = apply(after.Self())
		if err != nil {
			return inst, err
		}
	} else {
		beforeRoot, err = s.workspaceOverlayRootfs(ctx, from.Self())
		if err != nil {
			return inst, err
		}
		afterRoot, err = s.workspaceOverlayRootfs(ctx, after.Self())
		if err != nil {
			return inst, err
		}
	}

	beforeID, err := beforeRoot.ID()
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, afterRoot, &inst, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

// changesetCwdCutover is the first module engine version whose changesets
// come back from the engine measured from the workspace cwd instead of the
// workspace root. Clients apply a returned changeset at their own cwd, so
// cwd-measured is what lands files in the right place; modules built before
// the cutover keep the root-measured form and do the translation themselves
// (via the polyfill, #13769).
//
// NOTE: this must be the release that ships the polyfill-removal surface —
// adjust here if that release ends up with a different number.
const changesetCwdCutover = "v1.0.0-beta.10"

// callerPastChangesetCwdCutover reports whether the calling module declares
// an engine version at or past the cwd cutover. The schema view cannot carry
// this decision — views are base-version granular, so v1.0.0-beta.7 and the
// cutover both collapse to the v1.0.0 view — hence the exact declared
// version decides. Callers that are not module code (the CLI, SDK clients,
// tests) always get the new behavior.
//
// Call this at resolver entry: some resolvers swap in another client's
// metadata mid-flight, which would change the answer.
func callerPastChangesetCwdCutover(ctx context.Context) bool {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return true
	}
	mod, err := query.CurrentModule(ctx)
	if err != nil {
		return errors.Is(err, core.ErrNoCurrentModule)
	}
	return semver.Compare(mod.Self().Source.Value.Self().EngineVersion, changesetCwdCutover) >= 0
}

// reRootChangesetToCwd re-measures a workspace-root-measured changeset from
// the workspace cwd, so a client applying it at its own cwd writes the right
// files. A change outside the cwd cannot be expressed cwd-relative, so it
// fails loudly instead of silently losing an edit.
func reRootChangesetToCwd(
	ctx context.Context,
	changeset dagql.ObjectResult[*core.Changeset],
	cwd string,
) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	cwd = cleanWorkspaceRelPath(cwd)
	if cwd == "" || cwd == "." {
		return changeset, nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	paths, err := changeset.Self().ComputePaths(ctx)
	if err != nil {
		return inst, err
	}
	var outside []string
	for _, group := range [][]string{paths.Added, paths.Modified, paths.Removed} {
		for _, p := range group {
			// Sparse snapshots carry structural changes for the directories
			// leading to cwd. Selecting the cwd subtree drops those ancestors;
			// only a sibling or other unrelated path is truly outside scope.
			if !workspacePathInOrLeadingToCwd(p, cwd) {
				outside = append(outside, p)
			}
		}
	}
	if len(outside) > 0 {
		return inst, fmt.Errorf("changes fall outside the current directory %q: %s", cwd, strings.Join(outside, ", "))
	}
	if len(paths.Added)+len(paths.Modified)+len(paths.Removed) == 0 {
		return changeset, nil
	}

	// Selecting a subdirectory is metadata only, so this costs nothing. A
	// side that holds nothing under the cwd (e.g. the before side of a pure
	// addition in a sparse comparison) becomes an empty directory.
	subdirOrEmpty := func(dir dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var exists bool
		if err := srv.Select(ctx, dir, &exists, dagql.Selector{
			Field: "exists",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(cwd)}},
		}); err != nil {
			return dagql.ObjectResult[*core.Directory]{}, err
		}
		var sub dagql.ObjectResult[*core.Directory]
		if !exists {
			err := srv.Select(ctx, srv.Root(), &sub, dagql.Selector{Field: "directory"})
			return sub, err
		}
		err := srv.Select(ctx, dir, &sub, dagql.Selector{
			Field: "directory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(cwd)}},
		})
		return sub, err
	}
	before, err := subdirOrEmpty(changeset.Self().Before)
	if err != nil {
		return inst, err
	}
	after, err := subdirOrEmpty(changeset.Self().After)
	if err != nil {
		return inst, err
	}
	beforeID, err := before.ID()
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, after, &inst, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

// workspacePathInOrLeadingToCwd accepts a path inside cwd and the structural
// parent directories a sparse changeset needs in order to contain that path.
func workspacePathInOrLeadingToCwd(p, cwd string) bool {
	p = cleanWorkspaceRelPath(strings.TrimSuffix(p, "/"))
	cwd = cleanWorkspaceRelPath(cwd)
	return p == cwd || strings.HasPrefix(p, cwd+"/") || strings.HasPrefix(cwd, p+"/")
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
	// The export just changed the workspace's on-disk content, so host reads
	// (Workspace.file / .directory) cached earlier in this session are stale —
	// they are cached per client for the client's whole lifetime
	// (dagql.PerClientInput). Bump the client's read epoch so subsequent reads
	// land in a fresh per-client cache namespace and re-read the live host.
	// Best-effort, like the invalidation above: a bookkeeping failure must not
	// fail an export that already succeeded.
	if err := core.BumpWorkspaceReadEpoch(exportCtx); err != nil {
		slog.Warn("could not bump workspace read epoch after export", "error", err)
	}
	return core.Void{}, nil
}

// reloaded returns the workspace unchanged, having invalidated the workspace
// owner's cached host reads.
//
// Workspace.file / Workspace.directory resolve through host.directory, which is
// cached per client for the client's whole lifetime (dagql.PerClientInput). In
// a long-lived session — a `dagger agent` conversation — a file read early on
// keeps returning that original snapshot even after the files change on disk
// underneath it. Export bumps the read epoch itself, since it is the operation
// that changed them; this field covers the other direction, where an agent
// discards its pending overlay to re-sync with whatever the host now holds
// (the CLI's ctrl+u), and any other caller that knows its cached reads are
// stale.
func (s *workspaceSchema) reloaded(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	// Bump under the workspace's owning client, the same context export bumps
	// in and the one withWorkspaceHostReadContext reads the epoch from — a
	// bump under the caller's own client would be a silent no-op whenever the
	// caller is not the owner (e.g. a module handed the workspace). A value
	// workspace has no owning client and no host reads to invalidate, so the
	// bump is skipped rather than failed.
	if parent.Self().ClientID != "" {
		bumpCtx, err := withWorkspaceClientContext(ctx, parent.Self())
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		// Best-effort, like export's invalidation: failing to bump only falls
		// back to the prior (stale) read behavior, which is not worth failing
		// over.
		if err := core.BumpWorkspaceReadEpoch(bumpCtx); err != nil {
			slog.Warn("could not bump workspace read epoch", "error", err)
		}
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, parent.Self().Clone())
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
	ws.SetSource(core.NewWorkspaceSourceOverlay(parent.Self().Source(), nil, nil, changesResult))
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
// `readsExisting` names the touched paths whose current content the edit builds
// on, rather than overwriting outright. Those two bases hold different things
// at a path — the full read root has the workspace's content, the delta root
// only what earlier edits wrote — so an edit that reads the base means two
// different things across workspace kinds unless the delta root is first seeded
// with what the workspace holds there (dagger/dagger#13955). Leave it empty
// whenever the edit's result at `touched` is independent of what was there.
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
	readsExisting []string,
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
	touchedAll := unionPaths(ws.OverlayTouchedPaths(), touched)
	seededAll := unionPaths(ws.OverlaySeededPaths(), readsExisting)
	// Resolved before the edit so the seed below comes out of the very base the
	// changeset is diffed against: one host read on both sides means seeded
	// content can never read as a change.
	sparseBase, err := s.sparseHostBase(ctx, ws, touchedAll)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	deltaBase, ok := ws.OverlayDeltaRoot()
	if !ok {
		if err := srv.Select(ctx, srv.Root(), &deltaBase, dagql.Selector{Field: "directory"}); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	if len(seededAll) > 0 {
		deltaBase, err = s.seedDeltaRoot(ctx, srv, ws, deltaBase, sparseBase, seededAll)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	deltaRoot, err := edit(deltaBase)
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
	newWS.SetSource(core.NewWorkspaceSourceOverlay(ws.Source(), touchedAll, seededAll, changesResult))
	if mutate != nil {
		mutate(newWS)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, newWS)
}

// seedDeltaRoot layers the workspace's current content at the given paths onto
// the delta root, for edits that build on what a path already holds. The delta
// root carries only what overlay edits wrote, so such an edit would otherwise
// find the path empty and — once the delta root is diffed against the sparse
// host base, which does carry the host's content there — read as having
// replaced it.
//
// The seed comes out of `base`, the very sparse host base the resulting
// changeset is diffed against, with the overlay's own changeset applied so that
// earlier edits still win and earlier removals stay removed. Taking it from
// there rather than from a host read of its own is what keeps seeded content
// from ever reading as a change: both sides of the diff see one host snapshot,
// and because every later edit re-seeds these paths from its own base, content
// the caller never wrote is never pinned to a stale one. Layering onto the
// delta root rather than replacing it keeps edits outside these paths intact.
//
// Sizing the sparse base to the paths the source writes, instead of seeding,
// would also stop the removals — but a path the host does not have yet matches
// nothing, so its parent directories are absent from the base and the changeset
// reports directories the workspace already has as added.
func (s *workspaceSchema) seedDeltaRoot(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	delta dagql.ObjectResult[*core.Directory],
	base dagql.ObjectResult[*core.Directory],
	paths []string,
) (dagql.ObjectResult[*core.Directory], error) {
	existing := base
	if changes, ok := ws.OverlayChanges(); ok {
		changesID, err := changes.ID()
		if err != nil {
			return delta, err
		}
		if err := srv.Select(ctx, base, &existing, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		}); err != nil {
			return delta, fmt.Errorf("seed overlay delta root: %w", err)
		}
	}
	existingID, err := existing.ID()
	if err != nil {
		return delta, err
	}
	// The base spans every touched path, not just the seeded ones; trim to the
	// seeded paths so the rest of the delta root stays the caller's own writes.
	var scoped dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, srv.Root(), &scoped,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
			{Name: "source", Value: dagql.NewID[*core.Directory](existingID)},
			{Name: "include", Value: sparseIncludePatterns(paths)},
		}},
	); err != nil {
		return delta, fmt.Errorf("seed overlay delta root: %w", err)
	}
	scopedID, err := scoped.ID()
	if err != nil {
		return delta, err
	}
	var seeded dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, delta, &seeded, dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
			{Name: "source", Value: dagql.NewID[*core.Directory](scopedID)},
		},
	}); err != nil {
		return delta, fmt.Errorf("seed overlay delta root: %w", err)
	}
	return seeded, nil
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

	includes := sparseIncludePatterns(touched)

	ctx, err = s.withWorkspaceHostReadContext(ctx, ws)
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

// sparseIncludePatterns turns workspace-relative paths into include patterns
// covering each path and everything under it.
func sparseIncludePatterns(paths []string) dagql.ArrayInput[dagql.String] {
	includes := make(dagql.ArrayInput[dagql.String], 0, len(paths)*2)
	for _, p := range paths {
		p = strings.TrimSuffix(p, "/")
		includes = append(includes, dagql.String(p), dagql.String(p+"/**"))
	}
	return includes
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
	// Git worktree and submodule checkouts have a .git *file* pointing at
	// metadata outside the workspace; that pointer is followed and flattened
	// when the repository is resolved (see flattenWorkspaceGitPointer), so a
	// regular .git file is acceptable here.
	if st.FileType != core.FileTypeRegular && !st.IsDir() {
		return fmt.Errorf("workspace git metadata .git has type %s, expected directory or pointer file", st.FileType)
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

	// Git worktree and submodule checkouts have a .git *pointer file* at their
	// root, whose target lives outside the workspace boundary. Follow the
	// pointer against the host and flatten the real git dir into a standalone
	// .git directory so the LocalGitRepository sees a plain repository.
	dir, err = s.flattenWorkspaceGitPointer(ctx, ws, dir)
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

// flattenWorkspaceGitPointer resolves a .git pointer file (worktree/submodule
// checkout) at the workspace root into a standalone .git directory, following
// the pointer against the workspace's host. A plain .git directory is returned
// unchanged. A workspace with no host (in-memory rootfs) has nowhere to follow
// the pointer to, so a pointer file there stays unresolved and downstream git
// operations report the plain failure.
func (s *workspaceSchema) flattenWorkspaceGitPointer(
	ctx context.Context,
	ws *core.Workspace,
	dir dagql.ObjectResult[*core.Directory],
) (dagql.ObjectResult[*core.Directory], error) {
	if ws.HostPath() == "" {
		return dir, nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dir, err
	}
	flattened, err := core.FlattenGitPointer(ctx, srv, dir, ws.HostPath(),
		func(ctx context.Context, dag *dagql.Server, hostPath string) (dagql.ObjectResult[*core.Directory], error) {
			return s.loadWorkspaceHostDir(ctx, dag, ws, hostPath)
		})
	if errors.Is(err, core.ErrNoGitContext) {
		// No .git at all: leave the original directory so downstream
		// callers surface the plain "not a git repository" failure, matching
		// pre-worktree behavior.
		return dir, nil
	}
	return flattened, err
}

// loadWorkspaceHostDir loads an absolute host path as a Directory, routed
// through the workspace's owning client session. Unlike workspace reads this
// is not bounded to the workspace root: gitfile targets legitimately live
// outside it (e.g. the main checkout's .git). FlattenGitPointer validates what
// it loads.
func (s *workspaceSchema) loadWorkspaceHostDir(
	ctx context.Context,
	dag *dagql.Server,
	ws *core.Workspace,
	hostPath string,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	ctx, err = s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, err
	}
	err = dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(hostPath)},
				{Name: "noCache", Value: dagql.Boolean(true)},
			},
		},
	)
	return inst, err
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
//
// A path typed on Windows arrives spelled with backslashes, which mean nothing
// to the Linux engine resolving it — filepath would keep "a\b" as a single
// element named "a\b" — so separators are normalized first, as they are for
// host paths (see engine/client/pathutil). The cost is that a file whose name
// really does contain a backslash cannot be addressed through these APIs.
func resolveWorkspacePath(pathArg, basePath string) (string, error) {
	if basePath == "" {
		basePath = "."
	}
	clean := filepath.Clean(strings.ReplaceAll(pathArg, `\`, "/"))
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

func workspacePathRelativeToCwd(rootRelPath, cwd string) (string, error) {
	if rootRelPath == "" {
		return "", nil
	}
	rel, err := filepath.Rel(cleanWorkspaceRelPath(cwd), cleanWorkspaceRelPath(rootRelPath))
	if err != nil {
		return "", fmt.Errorf("workspace path relative to cwd: %w", err)
	}
	if rel == "" {
		return ".", nil
	}
	return filepath.ToSlash(rel), nil
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

	// Mounted content is visible to findUp like any other workspace read: the
	// mounts tree (which also materializes mount points' parents) is consulted
	// first, and at or under a mount point it fully shadows the source.
	var mountsFS core.StatFS
	if mounts, ok := ws.MountsDir(); ok {
		mountsFS = &core.DirectoryStatFS{Dir: mounts}
	}
	candidateExists := func(candidate string) (bool, error) {
		if mountsFS != nil {
			_, exists, err := core.StatFSExists(ctx, mountsFS, candidate)
			if err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
			if ws.MountedPath(candidate) {
				// The mount shadows whatever the source holds here.
				return false, nil
			}
		}
		statPath, err := pathForStat(candidate)
		if err != nil {
			return false, err
		}
		_, exists, err := core.StatFSExists(ctx, statFS, statPath)
		return exists, err
	}

	// Walk up from the resolved start path, stopping at the workspace boundary.
	for {
		candidate := path.Join(curDir, args.Name)
		exists, err := candidateExists(candidate)
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

type workspaceFindRootsArgs struct {
	Start   string `default:"."`
	Markers []string
	Exclude []string `default:"[]"`
}

func (s *workspaceSchema) findRoots(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceFindRootsArgs,
) (dagql.Array[dagql.String], error) {
	if len(args.Markers) == 0 {
		return nil, fmt.Errorf("workspace findRoots requires at least one marker")
	}
	for _, name := range args.Markers {
		if !isWorkspaceBasename(name) {
			return nil, fmt.Errorf("workspace findRoots marker must be a basename: %q", name)
		}
	}
	start, err := resolveWorkspacePath(args.Start, parent.Self().Cwd)
	if err != nil {
		return nil, err
	}

	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	ancestor, err := s.nearestAncestorRoot(ctx, parent, start, args.Markers)
	if err != nil {
		return nil, err
	}
	if ancestor != "" {
		add(ancestor)
	}

	descendants, err := s.descendantRoots(ctx, parent, start, args.Markers, args.Exclude)
	if err != nil {
		return nil, err
	}
	for _, dir := range descendants {
		add(dir)
	}

	return dagql.NewStringArray(dirs...), nil
}

// nearestAncestorRoot returns the closest strict ancestor of start holding a
// marker, as a cwd-relative path, or "" when there is none.
func (s *workspaceSchema) nearestAncestorRoot(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	start string,
	markers []string,
) (string, error) {
	bestDir := ""
	bestDepth := -1
	for _, name := range markers {
		hit, err := s.findUp(ctx, parent, workspaceFindUpArgs{Name: name, From: workspaceAPIPath(start)})
		if err != nil {
			return "", err
		}
		if !hit.Valid {
			continue
		}
		// findUp returns a workspace-absolute path like "/sub/deno.json".
		dir := strings.TrimPrefix(path.Dir(hit.Value.String()), "/")
		if dir == "" {
			dir = "."
		}
		if depth := workspaceDirDepth(dir); depth > bestDepth {
			bestDepth = depth
			bestDir = dir
		}
	}
	if bestDepth < 0 {
		return "", nil
	}
	if bestDir == cleanWorkspaceRelPath(start) {
		return "", nil
	}
	return workspacePathRelativeToCwd(bestDir, parent.Self().Cwd)
}

// workspaceDirDepth returns the depth of a workspace-root-relative directory,
// 0 at the root.
func workspaceDirDepth(dir string) int {
	if dir == "." {
		return 0
	}
	return strings.Count(dir, "/") + 1
}

// descendantRoots returns every directory at or below start holding one of the
// markers, as cwd-relative paths.
func (s *workspaceSchema) descendantRoots(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	start string,
	markers []string,
	exclude []string,
) ([]string, error) {
	ws := parent.Self()
	include := make([]string, len(markers))
	for i, name := range markers {
		include[i] = "**/" + name
	}
	dir, err := s.directoryAt(ctx, ws, start, workspaceDirectoryArgs{
		Path: ".",
		CopyFilter: core.CopyFilter{
			Include: include,
			Exclude: exclude,
		},
	})
	if err != nil {
		return nil, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, name := range markers {
		var matches dagql.Array[dagql.String]
		if err := srv.Select(ctx, dir, &matches, dagql.Selector{
			Field: "glob",
			Args: []dagql.NamedInput{
				{Name: "pattern", Value: dagql.NewString("**/" + name)},
			},
		}); err != nil {
			return nil, fmt.Errorf("glob %s: %w", name, err)
		}
		for _, match := range matches {
			descendant := path.Dir(match.String())
			if descendant == "/" || descendant == "" {
				descendant = "."
			}
			root := cleanWorkspaceRelPath(filepath.Join(start, descendant))
			rel, err := workspacePathRelativeToCwd(root, ws.Cwd)
			if err != nil {
				return nil, err
			}
			dirs = append(dirs, rel)
		}
	}
	return dirs, nil
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
	parentResult dagql.ObjectResult[*core.Workspace],
	args struct {
		Include      dagql.Optional[dagql.ArrayInput[dagql.String]]
		Skip         dagql.Optional[dagql.ArrayInput[dagql.String]]
		NoGenerate   dagql.Optional[dagql.Boolean]
		OnlyGenerate dagql.Optional[dagql.Boolean]
	},
) (*core.CheckGroup, error) {
	parent := parentResult.Self()
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

	// check is strict: a module that can't load is a failure, by design.
	if _, err := ensureWorkspaceModulesLoaded(ctx, include, false); err != nil {
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

	return &core.CheckGroup{Checks: allChecks, BoundWorkspace: parentResult}, nil
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
	parentResult dagql.ObjectResult[*core.Workspace],
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.GeneratorGroup, error) {
	parent := parentResult.Self()
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
	// exist yet gets them from its SDK's generator. A module that fails to load
	// is skipped with a warning instead of failing the whole run, and its
	// failure message is carried on loadFailures so the CLI can honor
	// --require-load.
	loadFailures, err := ensureWorkspaceModulesLoaded(ctx, include, true)
	if err != nil {
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

	return &core.GeneratorGroup{
		Generators:     allGenerators,
		LoadFailures:   loadFailures,
		BoundWorkspace: parentResult,
	}, nil
}

func (s *workspaceSchema) services(
	ctx context.Context,
	parentResult dagql.ObjectResult[*core.Workspace],
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.UpGroup, error) {
	parent := parentResult.Self()
	if isSyntheticWorkspace(parent) {
		return &core.UpGroup{}, nil
	}

	include := workspaceIncludePatterns(args.Include)

	ctx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, err
	}

	// up is strict: a module that can't load is a failure, by design.
	if _, err := ensureWorkspaceModulesLoaded(ctx, include, false); err != nil {
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

	return &core.UpGroup{Ups: allUps, BoundWorkspace: parentResult}, nil
}

func (s *workspaceSchema) agents(
	ctx context.Context,
	parentResult dagql.ObjectResult[*core.Workspace],
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.AgentGroup, error) {
	parent := parentResult.Self()
	if isSyntheticWorkspace(parent) {
		return &core.AgentGroup{}, nil
	}

	include := workspaceIncludePatterns(args.Include)

	ctx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, err
	}

	// agent composition is strict: a module that can't load is a failure.
	if _, err := ensureWorkspaceModulesLoaded(ctx, include, false); err != nil {
		return nil, err
	}
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}

	var allAgents []*core.Agent
	for _, mod := range mods {
		agentGroup, err := core.NewAgentGroup(ctx, mod, nil)
		if err != nil {
			return nil, fmt.Errorf("agents from module %q: %w", mod.Self().Name(), err)
		}
		reparentWorkspaceTreeRoot(agentGroup.Node, mod.Self().Name())
		filtered, err := filterNodesByInclude(
			ctx,
			agentGroup.Agents,
			include,
			func(agent *core.Agent) *core.ModTreeNode { return agent.Node },
			func(agent *core.Agent) string { return agent.Name() },
			"agent",
		)
		if err != nil {
			return nil, err
		}
		allAgents = append(allAgents, filtered...)
	}

	return &core.AgentGroup{Agents: allAgents, BoundWorkspace: parentResult}, nil
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

// ensureWorkspaceModulesLoaded loads the workspace modules the include patterns
// demand (all when they don't narrow). Selector fields validate against the
// core schema, so loading can wait until resolution. With bestEffort, per-module
// load failures are collected and returned instead of aborting (used by unscoped
// 'dagger generate'); the check/up resolvers pass false to stay strict.
func ensureWorkspaceModulesLoaded(ctx context.Context, include []string, bestEffort bool) ([]string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
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

// withWorkspaceHostReadContext is withWorkspaceClientContext plus the client's
// current workspace read epoch folded into the per-client cache namespace, so
// cached host.directory reads are scoped per epoch. When the epoch is bumped
// (Workspace.export, after the agent's changes are written to disk, or
// Workspace.reloaded when its overlay is discarded instead), reads issued
// afterwards land in a fresh namespace and
// re-read the live host instead of returning a per-client snapshot cached
// earlier in the same session. Use it for host reads that must reflect on-disk
// content (Workspace.file / Workspace.directory and the diff base of edits).
func (s *workspaceSchema) withWorkspaceHostReadContext(ctx context.Context, ws *core.Workspace) (context.Context, error) {
	ctx, err := withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, err
	}
	epoch, err := core.WorkspaceReadEpoch(ctx)
	if err != nil {
		return nil, err
	}
	return dagql.WithNamedPerClientCacheScope(ctx, epoch), nil
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
