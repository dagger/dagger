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
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/iancoleman/strcase"
	"golang.org/x/mod/semver"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(srv *dagql.Server) {
	// Let core derive workspace-served schemas (WorkspaceServedSchema) through
	// the overlay: re-resolving overlay-affected modules needs the overlay
	// rootfs machinery that lives in this package.
	core.SetWorkspaceOverlayModuleLoader(s.overlayModuleLoader)

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
		dagql.NodeFunc("withCommit", s.withCommit).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with its uncommitted changes staged as a git commit, without mutating the source.",
				"The commit is created engine-side, on top of the workspace's git HEAD plus any previously staged commit: the local checkout is left untouched. Afterwards Workspace.git.head resolves to the new commit, and Workspace.git.uncommitted holds whatever was left out of it, still pending on top.",
				"The operation is message-idempotent: when no committable change exists in scope and reachable history already contains the exact full message, the workspace is returned unchanged. A clean scope with no matching message is still an error.",
				"The commit is deterministic: the same workspace state and the same arguments always produce the same commit hash.").
			Args(
				dagql.Arg("message").Doc("Full commit message. Its exact value is also the logical identity used to recognize an already-applied retry across rebases and cherry-picks."),
				dagql.Arg("paths").Doc("Restrict the commit to these paths, like `git commit -- <paths>`. Relative paths resolve from the workspace cwd. Empty commits all uncommitted changes."),
				dagql.Arg("date").Doc("RFC3339 author and committer date. Required, so that the resulting commit hash does not depend on a hidden clock."),
				dagql.Arg("authorName").Doc("Author and committer name. Defaults to the git identity recorded when the workspace was loaded, else \"Dagger\"."),
				dagql.Arg("authorEmail").Doc("Author and committer email. Defaults to the git identity recorded when the workspace was loaded, else \"dagger@localhost\"."),
			),
		dagql.NodeFunc("__stagedCommit", s.stagedCommit).
			IsPersistable().
			Doc("(Internal-only) The repository tree resulting from staging the commit Workspace.withCommit describes.").
			Args(
				dagql.Arg("message").Doc("Commit message."),
				dagql.Arg("paths").Doc("Restrict the commit to these paths."),
				dagql.Arg("date").Doc("RFC3339 author and committer date."),
				dagql.Arg("authorName").Doc("Author and committer name."),
				dagql.Arg("authorEmail").Doc("Author and committer email."),
			),
		dagql.NodeFunc("commitsFrom", s.commitsFrom).
			View(AfterVersion("v1.0.0-0")).
			Doc("Plan which of another workspace's staged commits can be applied to this one.",
				"Both workspaces are expected to descend from the same checkout - typically this workspace and one an agent was spawned with. Each of the source's staged commits is classified against this one, oldest first, as if every pickable commit before it had already been applied: PICKED, REDUNDANT, CONFLICT (see reason and conflictPaths), or PICKABLE.",
				"Read-only: nothing is staged and neither workspace is modified. Pass the pickable hashes to withCommitsFrom to apply them.").
			Args(
				dagql.Arg("source").Doc("The workspace whose staged commits to consider."),
				dagql.Arg("commits").Doc("Restrict the plan to these commit hashes, full or an unambiguous prefix. They are always considered in the source's stack order. Empty considers every staged commit."),
			),
		dagql.NodeFunc("withCommitsFrom", s.withCommitsFrom).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with another workspace's staged commits replayed on top, without mutating either source.",
				"Each commit is applied to this workspace's current content as a patch - not as a whole-file overlay - so commits still land cleanly when this workspace has moved on since the source branched off. The replayed commit keeps the original message, date and author identity, and records the original commit as its origin, so pulling the same work again is recognised as already present.",
				"Commits this workspace already has, and commits whose content is already present, are skipped. A commit that cannot be applied is an error naming the commit and the conflicting paths: plan with commitsFrom first and pass the pickable hashes.").
			Args(
				dagql.Arg("source").Doc("The workspace whose staged commits to replay."),
				dagql.Arg("commits").Doc("Restrict the replay to these commit hashes, full or an unambiguous prefix. They are always applied in the source's stack order. Empty replays every staged commit."),
			),
		dagql.NodeFunc("__withReplayedCommit", s.withReplayedCommit).
			Doc("(Internal-only) Return this workspace with a commit staged, recording the commit it was replayed from.",
				"Backs one step of Workspace.withCommitsFrom: identical to Workspace.withCommit except that the resulting commit carries the origin, which is how a later pull recognises the work as already present.").
			Args(
				dagql.Arg("message").Doc("Commit message."),
				dagql.Arg("paths").Doc("Restrict the commit to these paths."),
				dagql.Arg("date").Doc("RFC3339 author and committer date."),
				dagql.Arg("authorName").Doc("Author and committer name."),
				dagql.Arg("authorEmail").Doc("Author and committer email."),
				dagql.Arg("origin").Doc("Hash of the commit this one is replayed from, collapsed to its own origin when it already had one."),
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
		dagql.NodeFunc("withConfigPaths", s.withConfigPaths).
			View(AfterVersion("v1.0.0-beta.10")).
			Doc("Return this workspace with its selected config and lockfile paths.").
			Args(
				dagql.Arg("configFile").Doc("Canonical path to the selected config file, relative to the workspace root. Empty clears the selection."),
				dagql.Arg("lockFile").Doc("Canonical path to the selected lockfile, relative to the workspace root. Empty clears the selection."),
			),
		dagql.NodeFunc("withConfigEnvironment", s.withConfigEnvironment).
			View(AfterVersion("v1.0.0-beta.10")).
			Doc("Return this workspace with the selected config environment.").
			Args(
				dagql.Arg("name").Doc("Config environment to select. Empty clears the selection."),
			),
		dagql.NodeFunc("withGitAuthor", s.withGitAuthor).
			View(AfterVersion("v1.0.0-beta.10")).
			Doc("Return this workspace with the default Git author identity used for commits.").
			Args(
				dagql.Arg("name").Doc("Default Git author and committer name."),
				dagql.Arg("email").Doc("Default Git author and committer email."),
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
		dagql.NodeFunc("withMountedCache", s.withMountedCache).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with a cache volume mounted at the given path, without mutating the source.",
				"Like a mounted directory, the cache shadows the source at the mount path and stays out of the pending changeset: it never appears in changes and is never exported to the workspace. Unlike a mounted directory it is writable, and export commits the edits made under it back into the cache volume.").
			Args(
				dagql.Arg("path").Doc("Location of the mounted cache. Relative paths resolve from the workspace cwd."),
				dagql.Arg("cache").Doc("Cache volume to mount."),
			),
		dagql.NodeFunc("withoutMount", s.withoutMount).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return this workspace with the content mounted at the given path unmounted.",
				"Removes whatever is mounted there — a cache volume, directory or file — along with anything mounted inside it. Pending edits to a mounted cache volume are discarded rather than committed.").
			Args(
				dagql.Arg("path").Doc("Location of the mount to remove. Relative paths resolve from the workspace cwd."),
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
			Doc("Write this workspace's pending changes to its local Git workspace.",
				"Edits made under a mounted cache volume are not pending changes; they are committed into that volume instead."),
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
		dagql.NodeFunc("checkpoint", s.checkpoint).
			View(AfterVersion("v1.0.0-beta.10")).
			DoNotCache("Captures live client Git state after local safety preflight").
			Doc("Return this workspace as a frozen, host-independent value whose recipe can be replayed without the originating client.",
				"Replayable sources pass through (with mutable Git refs pinned); an eligible client-local Git checkout is captured; unsupported or non-replayable sources fail with the offending recipe leaf named.").
			Args(
				dagql.Arg("include").Doc("Explicitly include and approve matching nonignored paths."),
				dagql.Arg("exclude").Doc("Exclude matching paths from capture."),
				dagql.Arg("maxUntrackedFileBytes").Doc("Maximum bytes allowed for one untracked file."),
				dagql.Arg("maxUntrackedTotalBytes").Doc("Maximum aggregate bytes allowed for untracked files."),
				dagql.Arg("maxUntrackedFiles").Doc("Maximum number of untracked files allowed."),
			),
		dagql.Func("addresses", s.addresses).
			View(AfterVersion("v1.0.0-0")).
			Doc("Addresses loadable from the workspace's installed modules: functions whose return type matches `type` and whose required args (beyond an auto-injected Workspace) are none, rendered as bare \"module:function\" references.").
			Args(
				dagql.Arg("type").Doc(`Name of the type the function must return to be listed, e.g. "Container".`),
			),
		migrateField,
	}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.WorkspaceGit](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceStagedCommit](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceCommitPick](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceModule](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceModuleSetting](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceSDK](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceMigration](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceMigrationStep](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceAddress](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.WorkspaceGit]{
		dagql.NodeFunc("__repository", s.workspaceGitRepository).
			Doc("(Internal-only) The git repository backing this workspace git state."),
		dagql.NodeFunc("head", s.workspaceGitHead).
			Doc("The checked-out HEAD of this workspace."),
		dagql.NodeFunc("uncommitted", s.workspaceGitUncommitted).
			Doc("Uncommitted changes in this workspace, using the same rules as GitRepository.uncommitted."),
		dagql.NodeFunc("unmanaged", s.workspaceGitUnmanaged).
			Doc("Pending workspace edits git cannot see - gitignored, or inside a nested repository.",
				"Workspace.export writes these to the local checkout, but they never appear in `uncommitted` and cannot be committed."),
		dagql.NodeFunc("stagedCommits", s.stagedCommits).
			Doc("Commits staged in this workspace but not yet saved to the local checkout.",
				"Ordered oldest to newest, matching the order they were staged in on top of the checkout's HEAD. Empty when nothing is staged."),
		dagql.NodeFunc("push", s.workspaceGitPush).
			DoNotCache("Updates a ref on a remote git repository").
			Doc("Push this workspace's git HEAD - including any staged commits - to a remote, and return the fully qualified remote ref that was updated.",
				"The push runs through the local checkout's own git, so the checkout's configured remotes, credential helpers and hooks apply, exactly as for `git push` run in the checkout. The checkout itself is never modified: commits staged in the workspace are transferred engine-side and pushed by hash, so they can land on a remote branch without first being saved to the local checkout.").
			Args(
				dagql.Arg("remote").Doc("Remote to push to: a remote name from the checkout's configuration, or a URL."),
				dagql.Arg("branch").Doc("Remote branch to update. Defaults to the checkout's currently checked-out branch, and is required when its HEAD is detached. A fully qualified ref (refs/...) is used as-is."),
				dagql.Arg("force").Doc("Allow a non-fast-forward update of the remote ref."),
			),
		dagql.NodeFunc("__stagedCommitEntry", s.stagedCommitEntry).
			IsPersistable().
			Doc("(Internal-only) One entry of WorkspaceGit.stagedCommits.").
			Args(
				dagql.Arg("index").Doc("Zero-based index into the staged commit stack, oldest first."),
			),
	}.Install(srv)

	dagql.Fields[*core.WorkspaceStagedCommit]{}.Install(srv)
	dagql.Fields[*core.WorkspaceCommitPick]{}.Install(srv)

	core.WorkspaceCommitPickStatuses.Install(srv, AfterVersion("v1.0.0-0"))
	core.WorkspaceCommitPickReasons.Install(srv, AfterVersion("v1.0.0-0"))

	dagql.Fields[*core.WorkspaceModule]{
		dagql.NodeFunc("settings", s.moduleSettings).
			DoNotCache("Reads live config and module metadata from the workspace").
			Doc("List constructor-backed settings for this module."),
	}.Install(srv)
	dagql.Fields[*core.WorkspaceModuleSetting]{}.Install(srv)
	dagql.Fields[*core.WorkspaceSDK]{}.Install(srv)
	dagql.Fields[*core.WorkspaceMigration]{}.Install(srv)
	dagql.Fields[*core.WorkspaceMigrationStep]{}.Install(srv)
	dagql.Fields[*core.WorkspaceAddress]{}.Install(srv)
}

type workspaceCheckpointArgs struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	Exclude dagql.Optional[dagql.ArrayInput[dagql.String]]

	MaxUntrackedFileBytes  dagql.Optional[dagql.Int]
	MaxUntrackedTotalBytes dagql.Optional[dagql.Int]
	MaxUntrackedFiles      dagql.Optional[dagql.Int]
}

type capturedCheckpointChunk struct {
	kind gitsession.CaptureGitChunk_Kind
	data []byte
}

func (s *workspaceSchema) checkpoint(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCheckpointArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	if ws == nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint requires a workspace")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	switch src := ws.Source().(type) {
	case *core.WorkspaceSourceClientLocal:
		return s.checkpointClientLocal(ctx, srv, parent, args)
	case *core.WorkspaceSourceRootlessLocal:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint cannot capture a rootless local workspace")
	case *core.WorkspaceSourceDirectory:
		if err := checkpointRecipeReplayable(ctx, srv, parent); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		return parent, nil
	case *core.WorkspaceSourceGitRef:
		if err := checkpointRecipeReplayable(ctx, srv, parent); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		return s.checkpointGitRef(ctx, srv, parent, src, nil)
	case *core.WorkspaceSourceOverlay:
		if err := checkpointRecipeReplayable(ctx, srv, parent); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		switch base := src.Base.(type) {
		case *core.WorkspaceSourceDirectory:
			return parent, nil
		case *core.WorkspaceSourceGitRef:
			return s.checkpointGitRef(ctx, srv, parent, base, src)
		case *core.WorkspaceSourceClientLocal:
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint cannot capture a client-local workspace after workspace edits; checkpoint it before applying overlays")
		case *core.WorkspaceSourceRootlessLocal:
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint cannot capture a rootless local workspace overlay")
		default:
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint cannot normalize overlay base %T", src.Base)
		}
	case nil:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint has no reconstructible source")
	default:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint does not support source %T", src)
	}
}

func checkpointRecipeReplayable(
	ctx context.Context,
	srv *dagql.Server,
	workspaceResult dagql.ObjectResult[*core.Workspace],
) error {
	recipe, err := workspaceResult.RecipeID(ctx)
	if err != nil {
		return fmt.Errorf("workspace checkpoint source recipe: %w", err)
	}
	if unsafe := srv.ClassifyRecipe(recipe).NotReplayable; unsafe != nil {
		return fmt.Errorf(
			"workspace checkpoint source is not replayable: field %s at call %s: %s",
			unsafe.Field,
			unsafe.Digest,
			unsafe.Reason,
		)
	}
	return nil
}

func (s *workspaceSchema) checkpointClientLocal(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCheckpointArgs,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	ws := parent.Self()
	if ws.HostPath() == "" {
		return inst, fmt.Errorf("workspace checkpoint requires a local Git workspace")
	}
	if len(ws.PendingCommits()) > 0 {
		return inst, fmt.Errorf("workspace checkpoint cannot capture a client-local workspace with pending commits")
	}
	if len(ws.MountPoints()) > 0 {
		return inst, fmt.Errorf("workspace checkpoint cannot capture a client-local workspace with mounts")
	}

	caller, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("workspace checkpoint caller metadata: %w", err)
	}
	if ws.ClientID == "" || caller.ClientID != ws.ClientID {
		return inst, fmt.Errorf("workspace checkpoint capture is only available to the workspace's owning client")
	}

	clientCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(clientCtx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Engine(clientCtx)
	if err != nil {
		return inst, fmt.Errorf("workspace checkpoint engine client: %w", err)
	}

	policy := &gitsession.CaptureGitPolicy{
		Include:                checkpointPatterns(args.Include),
		Exclude:                checkpointPatterns(args.Exclude),
		MaxUntrackedFileBytes:  checkpointOptionalInt(args.MaxUntrackedFileBytes),
		MaxUntrackedTotalBytes: checkpointOptionalInt(args.MaxUntrackedTotalBytes),
		MaxUntrackedFiles:      int32(checkpointOptionalInt(args.MaxUntrackedFiles)),
		MaxTotalBytes:          256 << 20,
	}

	var chunks []capturedCheckpointChunk
	capture := func() (*gitsession.CaptureGitMetadata, error) {
		chunks = nil
		return bk.CaptureGit(clientCtx, ws.HostPath(), policy, func(kind gitsession.CaptureGitChunk_Kind, data []byte) error {
			chunks = append(chunks, capturedCheckpointChunk{kind: kind, data: slices.Clone(data)})
			return nil
		})
	}
	var metadata *gitsession.CaptureGitMetadata
	for {
		metadata, err = capture()
		var approvalErr *engineutil.GitCaptureApprovalError
		if !errors.As(err, &approvalErr) {
			break
		}
		if len(approvalErr.Candidates) == 0 {
			return inst, fmt.Errorf("workspace checkpoint approval required without selected paths")
		}
		summary := checkpointApprovalSummary(approvalErr.Candidates)
		approved, promptErr := bk.PromptBool(clientCtx, "Include workspace changes?", summary)
		if promptErr != nil {
			return inst, fmt.Errorf("workspace checkpoint requires approval of every selected dirty path; pass include patterns in noninteractive use: %w", promptErr)
		}
		if !approved {
			return inst, fmt.Errorf("workspace checkpoint rejected selected dirty paths")
		}
		for _, candidate := range approvalErr.Candidates {
			if candidate.ApprovalToken == "" {
				return inst, fmt.Errorf("workspace checkpoint approval candidate %s has no state token", strconv.Quote(candidate.Path))
			}
			policy.ApprovalTokens = append(policy.ApprovalTokens, candidate.ApprovalToken)
		}
	}
	if err != nil {
		return inst, fmt.Errorf("capture workspace checkpoint: %w", err)
	}

	bundle := slices.Concat(checkpointBundleChunks(chunks)...)
	if int64(len(bundle)) != metadata.BundleBytes {
		return inst, fmt.Errorf("workspace checkpoint bundle is %d bytes, capture reported %d", len(bundle), metadata.BundleBytes)
	}
	workspaceEnv, _ := selectedWorkspaceEnv(clientCtx)
	inst, err = s.checkpointCapturedGitComposition(clientCtx, srv, ws, metadata, bundle, workspaceEnv)
	if err != nil {
		return inst, fmt.Errorf("construct portable workspace checkpoint: %w", err)
	}

	return inst, nil
}

func (s *workspaceSchema) checkpointGitRef(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
	source *core.WorkspaceSourceGitRef,
	overlay *core.WorkspaceSourceOverlay,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	ws := parent.Self()
	if len(ws.PendingCommits()) > 0 {
		return inst, fmt.Errorf("workspace checkpoint cannot normalize a Git workspace with pending commits")
	}
	if len(ws.MountPoints()) > 0 {
		return inst, fmt.Errorf("workspace checkpoint cannot normalize a Git workspace with mounts")
	}
	ref := source.Ref.Self()
	if ref == nil || ref.Ref == nil || ref.Ref.SHA == "" || ref.Repo.Self() == nil {
		return inst, fmt.Errorf("workspace checkpoint Git source has no resolved commit")
	}

	var pinned dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, ref.Repo, &pinned, dagql.Selector{
		Field: "ref",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(ref.Ref.SHA)}},
	}); err != nil {
		return inst, fmt.Errorf("pin workspace Git ref at %s: %w", ref.Ref.SHA, err)
	}
	if err := srv.Select(ctx, pinned, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(ws.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("normalize pinned Git workspace: %w", err)
	}

	if overlay != nil && overlay.Changes.Self() != nil {
		changesID, err := overlay.Changes.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint overlay identity: %w", err)
		}
		var withChanges dagql.ObjectResult[*core.Workspace]
		if err := srv.Select(ctx, inst, &withChanges, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		}); err != nil {
			return inst, fmt.Errorf("reapply workspace Git overlay: %w", err)
		}
		inst = withChanges
	}

	workspaceEnv, _ := selectedWorkspaceEnvFor(ctx, ws)
	return checkpointWorkspaceMetadataComposition(ctx, srv, inst, ws, workspaceEnv)
}

func (s *workspaceSchema) checkpointCapturedGitComposition(
	ctx context.Context,
	srv *dagql.Server,
	captured *core.Workspace,
	metadata *gitsession.CaptureGitMetadata,
	bundleBytes []byte,
	workspaceEnv string,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	if metadata == nil {
		return inst, fmt.Errorf("workspace checkpoint capture metadata is missing")
	}

	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, srv.Root(), &repo, dagql.Selector{
		Field: "git",
		Args:  []dagql.NamedInput{{Name: "url", Value: dagql.NewString(metadata.RemoteUrl)}},
	}); err != nil {
		return inst, fmt.Errorf("load workspace checkpoint remote: %w", err)
	}

	if len(bundleBytes) > 0 {
		var file dagql.ObjectResult[*core.File]
		if err := srv.Select(ctx, srv.Root(), &file, dagql.Selector{
			Field: "blob",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString("workspace-checkpoint.bundle")},
				{Name: "contents", Value: dagql.Bytes(bundleBytes)},
				{Name: "permissions", Value: dagql.NewInt(0o600)},
			},
		}); err != nil {
			return inst, fmt.Errorf("embed workspace checkpoint bundle: %w", err)
		}
		var bundle dagql.ObjectResult[*core.GitBundle]
		if err := srv.Select(ctx, file, &bundle, dagql.Selector{Field: "asGitBundle"}); err != nil {
			return inst, fmt.Errorf("parse workspace checkpoint bundle: %w", err)
		}
		bundleID, err := bundle.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint bundle identity: %w", err)
		}
		var imported dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, repo, &imported, dagql.Selector{
			Field: "withBundle",
			Args: []dagql.NamedInput{
				{Name: "bundle", Value: dagql.NewID[*core.GitBundle](bundleID)},
				{Name: "prerequisiteRef", Value: dagql.NewString(metadata.RemoteRef)},
			},
		}); err != nil {
			return inst, fmt.Errorf("import workspace checkpoint bundle: %w", err)
		}
		repo = imported
	}

	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, repo, &head, dagql.Selector{
		Field: "ref",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(metadata.HeadSha)}},
	}); err != nil {
		return inst, fmt.Errorf("select workspace checkpoint HEAD %s: %w", metadata.HeadSha, err)
	}
	if err := srv.Select(ctx, head, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(captured.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("construct workspace checkpoint from HEAD: %w", err)
	}

	if metadata.WorktreeSha != "" {
		treeArgs := []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
			{Name: "depth", Value: dagql.NewInt(0)},
			{Name: "includeTags", Value: dagql.NewBoolean(false)},
		}
		var headTree dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, head, &headTree, dagql.Selector{Field: "tree", Args: treeArgs}); err != nil {
			return inst, fmt.Errorf("workspace checkpoint HEAD tree: %w", err)
		}
		var worktree dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, repo, &worktree, dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(metadata.WorktreeSha)}},
		}); err != nil {
			return inst, fmt.Errorf("select workspace checkpoint worktree %s: %w", metadata.WorktreeSha, err)
		}
		var worktreeTree dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, worktree, &worktreeTree, dagql.Selector{Field: "tree", Args: treeArgs}); err != nil {
			return inst, fmt.Errorf("workspace checkpoint worktree tree: %w", err)
		}
		headTreeID, err := headTree.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint HEAD tree identity: %w", err)
		}
		var changes dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, worktreeTree, &changes, dagql.Selector{
			Field: "changes",
			Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](headTreeID)}},
		}); err != nil {
			return inst, fmt.Errorf("workspace checkpoint worktree changes: %w", err)
		}
		changesID, err := changes.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint changes identity: %w", err)
		}
		var withChanges dagql.ObjectResult[*core.Workspace]
		if err := srv.Select(ctx, inst, &withChanges, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		}); err != nil {
			return inst, fmt.Errorf("apply workspace checkpoint worktree: %w", err)
		}
		inst = withChanges
	}

	return checkpointWorkspaceMetadataComposition(ctx, srv, inst, captured, workspaceEnv)
}

func checkpointWorkspaceMetadataComposition(
	ctx context.Context,
	srv *dagql.Server,
	workspaceResult dagql.ObjectResult[*core.Workspace],
	metadata *core.Workspace,
	workspaceEnv string,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	if err := srv.Select(ctx, workspaceResult, &inst, dagql.Selector{
		Field: "withConfigPaths",
		Args: []dagql.NamedInput{
			{Name: "configFile", Value: dagql.NewString(metadata.ConfigFile)},
			{Name: "lockFile", Value: dagql.NewString(metadata.LockFile)},
		},
	}); err != nil {
		return inst, err
	}
	var withEnv dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, inst, &withEnv, dagql.Selector{
		Field: "withConfigEnvironment",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(workspaceEnv)}},
	}); err != nil {
		return inst, err
	}
	var withAuthor dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, withEnv, &withAuthor, dagql.Selector{
		Field: "withGitAuthor",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(metadata.GitAuthorName)},
			{Name: "email", Value: dagql.NewString(metadata.GitAuthorEmail)},
		},
	}); err != nil {
		return inst, err
	}
	withAuthor.Self().SetPortableCheckpoint()
	return withAuthor, nil
}

func checkpointApprovalSummary(candidates []*gitsession.CaptureGitCandidate) string {
	var summary strings.Builder
	summary.WriteString("Include all selected workspace changes in the portable agent workspace?\n")
	for _, candidate := range candidates {
		kind := "untracked"
		if candidate.Tracked {
			kind = "tracked"
		}
		fmt.Fprintf(&summary, "\n- %s (%s, %d bytes", strconv.Quote(candidate.Path), kind, candidate.Bytes)
		if candidate.Classification != "" {
			fmt.Fprintf(&summary, "; warning: %s", candidate.Classification)
		}
		summary.WriteString(")")
	}
	return summary.String()
}

func checkpointPatterns(arg dagql.Optional[dagql.ArrayInput[dagql.String]]) []string {
	if !arg.Valid {
		return nil
	}
	patterns := make([]string, len(arg.Value))
	for i, pattern := range arg.Value {
		patterns[i] = pattern.String()
	}
	return patterns
}

func checkpointOptionalInt(arg dagql.Optional[dagql.Int]) int64 {
	if !arg.Valid {
		return 0
	}
	return int64(arg.Value)
}

func checkpointBundleChunks(chunks []capturedCheckpointChunk) (bundle [][]byte) {
	const traceChunkBytes = 256 << 10
	for _, chunk := range chunks {
		if chunk.kind != gitsession.CAPTURE_CHUNK_BUNDLE {
			continue
		}
		data := chunk.data
		for len(data) > 0 {
			n := min(len(data), traceChunkBytes)
			bundle = append(bundle, slices.Clone(data[:n]))
			data = data[n:]
		}
	}
	return bundle
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
	if repo := ref.Self().Repo.Self(); repo != nil && repo.URL.Valid {
		ws.SetGitOrigin(repo.URL.Value.String())
	}
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
//   - Paths at or under a mount point resolve entirely against the mounts tree
//     — mounted content shadows the source.
//   - Paths with mount points beneath them get the mounted content overlaid on
//     the source read, so listings include it. As with container mounts, the
//     mount point's parents don't need to exist in the source.
//
// Everything that materializes the workspace *source* (module loading,
// lockfiles, migration, git) resolves through resolveRootfs directly and never
// sees mounts, mirroring how changes excludes them and export writes them to
// their own destination (a cache volume) rather than to the workspace.
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
//
// A host-backed workspace's root carries the checkout's dangling .git pointer
// file (worktree/submodule); the engine never interprets it -- git-ness is
// provided canonically via materializeWorkspaceGit -- so it is dropped from the
// root snapshot here, covering every route resolveRootfsInner takes to a root
// read (host, overlay, references, mount) without disturbing their logic. Only
// the workspace root carries the pointer, and DropRootGitPointerFile is a no-op
// unless a `.git` *file* is actually present, so a plain .git directory and
// value-backed workspaces are untouched.
func (s *workspaceSchema) resolveRootfs(
	ctx context.Context,
	ws *core.Workspace,
	resolvedPath string,
	filter core.CopyFilter,
	gitignore bool,
) (dagql.ObjectResult[*core.Directory], error) {
	inst, err := s.resolveRootfsInner(ctx, ws, resolvedPath, filter, gitignore)
	if err != nil {
		return inst, err
	}
	isRoot := resolvedPath == "." || resolvedPath == "/" || resolvedPath == ""
	if ws.HostPath() != "" && isRoot && inst.Self() != nil {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return inst, err
		}
		inst, err = core.DropRootGitPointerFile(ctx, srv, inst)
		if err != nil {
			return inst, err
		}
	}
	return inst, nil
}

func (s *workspaceSchema) resolveRootfsInner(
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
	return s.workspaceEdit(ctx, parent, resolvedPath, ownedPaths(resolvedPath), nil, func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(targetPath)},
				{Name: "contents", Value: dagql.NewString(args.Contents)},
				{Name: "permissions", Value: dagql.NewInt(args.Permissions)},
			},
		})
		return updated, err
	})
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

	// Mounted content lives in the mounts tree, never in the source rootfs or
	// on the host, and ripgrep hard-errors on path operands that don't exist.
	// So explicit paths that resolve to mounted content are withheld from the
	// source-side searches — the mounts-side search below covers them via
	// post-filter — and when every requested path is mount-covered the source
	// side is skipped entirely.
	sourcePaths, searchSource, err := s.searchSourcePaths(ctx, ws, args.Paths)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	sourceArgs := args
	sourceArgs.Paths = sourcePaths

	var results []*core.SearchResult
	switch {
	case !searchSource:
		// Every requested path is covered by mounts: results come solely from
		// the mounts tree below.
	case ws.HostPath() == "":
		// No host boundary: search the workspace's in-engine root filesystem.
		// Overlay edits are already visible here: value/git overlays surface
		// the changeset's after-tree as the source directory.
		rootfs, err := workspaceRootfs(ws)
		if err != nil {
			return nil, err
		}
		results, err = rootfs.Self().Search(ctx, rootfs, sourceArgs.SearchOpts, false, sourceArgs.Paths, sourceArgs.Globs)
		if err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
	default:
		results, err = s.searchHost(ctx, ws, sourceArgs)
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
	// points, mirroring resolveReadRootfs's shadowing, and explicit search
	// paths under mounts — withheld from the source side above — are honored
	// here via searchDirectoryTree's post-filter.
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

// searchSourcePaths filters explicit search paths down to the operands the
// source-side search (rootfs or host) can safely receive. Mounted content
// exists only in the mounts tree, and ripgrep hard-errors on missing path
// operands, so:
//
//   - a path at or under a mount point is always dropped: the mounts-side
//     search covers it via searchDirectoryTree's post-filter;
//   - a path that is a strict ancestor of a mount point is dropped when the
//     source doesn't have it — mounts materialize their own parents, so the
//     path can exist in the workspace view through the mount alone.
//
// Paths uninvolved with mounts pass through untouched, so a genuinely
// nonexistent path still errors like today. The boolean reports whether the
// source-side search should run at all: false when the caller scoped the
// search entirely to mounted content.
func (s *workspaceSchema) searchSourcePaths(
	ctx context.Context,
	ws *core.Workspace,
	paths []string,
) ([]string, bool, error) {
	if len(paths) == 0 {
		return nil, true, nil
	}
	sourcePaths := make([]string, 0, len(paths))
	for _, p := range paths {
		scope := cleanSearchScope(p)
		if ws.MountedPath(scope) {
			continue
		}
		if ws.HasMountsUnder(scope) {
			exists, err := s.workspaceReadPathExists(ctx, ws, scope)
			if err != nil {
				return nil, false, err
			}
			if !exists {
				continue
			}
		}
		sourcePaths = append(sourcePaths, p)
	}
	return sourcePaths, len(sourcePaths) > 0, nil
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

// cleanSearchScope normalizes a caller-supplied search path to the
// workspace-root-relative form used for scope comparisons.
func cleanSearchScope(scope string) string {
	return path.Clean(strings.TrimPrefix(filepath.ToSlash(scope), "/"))
}

// searchPathInScopes reports whether a result path falls under any of the
// requested search paths (matching a file itself or anything beneath a
// directory).
func searchPathInScopes(filePath string, scopes []string) bool {
	fp := path.Clean(filepath.ToSlash(filePath))
	for _, scope := range scopes {
		sc := cleanSearchScope(scope)
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
	return s.workspaceEdit(ctx, parent, resolvedPath, ownedPaths(resolvedPath), nil, func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error) {
		// Clearing first is what lets both overlay branches share this edit:
		// an edit that ignores whatever the base holds at the path reads the
		// same on a full read root as on a host overlay's delta root.
		cleared, err := clearedForWrite(ctx, srv, base, targetPath)
		if err != nil {
			return cleared, err
		}
		var updated dagql.ObjectResult[*core.Directory]
		err = srv.Select(ctx, cleared, &updated, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(targetPath)},
				{Name: "source", Value: dagql.NewID[*core.Directory](sourceID)},
			},
		})
		return updated, err
	})
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
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	// Directory.withDirectory MERGES: the delta root ends up owning the
	// source's files under resolvedPath, and nothing else there. Declaring the
	// directory itself would put the whole host subtree in the sparse base, and
	// every host file the source does not also carry would read as deleted — so
	// the edit owns exactly the paths it writes, while readsExisting seeds the
	// existing content it merges onto.
	sourceFiles, err := s.directoryFilePaths(ctx, source)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace withDirectory %q: %w", args.Path, err)
	}
	owned := make([]string, 0, len(sourceFiles))
	for _, p := range sourceFiles {
		owned = append(owned, path.Join(resolvedPath, p))
	}
	return s.workspaceEdit(ctx, parent, resolvedPath, ownedPaths(owned...), []string{resolvedPath}, func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(targetPath)},
				{Name: "source", Value: dagql.NewID[*core.Directory](sourceID)},
			},
		})
		return updated, err
	})
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

// directoryFilePaths returns every file the given directory holds, as paths
// relative to it. Directories are deliberately left out: a directory path in a
// sparse base include list drags in its whole host subtree (see overlayEdit).
//
// It diffs against an empty directory rather than globbing because
// ChangesetPaths always marks a directory entry with a trailing slash, whereas
// Directory.glob only does so for a new-enough client view — and this is a
// distinction the overlay cannot afford to get wrong.
func (s *workspaceSchema) directoryFilePaths(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
) ([]string, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var empty dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, srv.Root(), &empty, dagql.Selector{Field: "directory"}); err != nil {
		return nil, err
	}
	emptyID, err := empty.ID()
	if err != nil {
		return nil, err
	}
	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, dir, &changes, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](emptyID)}},
	}); err != nil {
		return nil, err
	}
	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return nil, err
	}
	return filePathsOnly(paths.Added), nil
}

// removeAndPruneSelectors builds the selector chain for a workspace removal:
// the removal itself, followed by a withoutDirectory of the topmost ancestor
// the removal empties (see emptiedAncestorDir). prune is an ancestor of
// targetPath in the same workspace-relative space, and is only ever set for
// base-overlay edits — emptiedAncestorDir declines to prune under a mount.
func removeAndPruneSelectors(targetPath, removeField, prune string) []dagql.Selector {
	sels := []dagql.Selector{{
		Field: removeField,
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(targetPath)},
		},
	}}
	if prune != "" {
		sels = append(sels, dagql.Selector{
			Field: "withoutDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(prune)},
			},
		})
	}
	return sels
}

// prunedRemoval is what a removal actually deletes: the path itself, plus the
// emptied ancestor chain the prune takes with it. Both must be declared, or the
// prune reads as a deletion nobody asked for and keepRemovalAncestors puts the
// chain straight back.
func prunedRemoval(targetPath, prune string) []string {
	if prune == "" {
		return []string{targetPath}
	}
	return []string{targetPath, prune}
}

// emptiedAncestorDir reports the topmost workspace-relative ancestor directory
// of removedPath that the removal will leave empty, or "" when the removal
// empties no ancestor. Callers remove that directory along with the path, which
// prunes the whole emptied chain in one selection.
//
// Why prune at all: git cannot represent an empty directory, so a workspace
// tree that keeps one after its last entry is deleted reports a phantom
// `ADDED dir/ +0 -0` entry in git-anchored status — and can even make
// Changeset.isEmpty (true) disagree with Changeset.diffStats (one bare dir
// entry). Deleting the last file in a directory is expected to make the
// directory go away, exactly as `git rm` leaves no empty parent behind.
//
// This is workspace-editor (`git rm`-like) behaviour only: the underlying
// Directory.withoutFile/withoutDirectory keep their literal semantics, and a
// directory that is genuinely empty on its own — never emptied by a removal —
// is left untouched.
//
// Emptiness is decided against the workspace's own directory listing (the
// pre-edit source tree), never the overlay delta root: for host-backed
// workspaces the delta only holds touched paths, so a directory that still has
// untouched host files would look empty there and get wrongly whiteouted.
func (s *workspaceSchema) emptiedAncestorDir(
	ctx context.Context,
	ws *core.Workspace,
	removedPath string,
) string {
	// Mounted content is not workspace source: it stays out of
	// Workspace.changes and is never git-anchored, so an empty directory under
	// a mount is harmless. Skipping mounts also keeps prune confined to the
	// base overlay, since a cache-mount edit writes to the mounts tree instead.
	if ws.MountedPath(removedPath) {
		return ""
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return ""
	}
	topmost := ""
	child := removedPath
	for {
		dir := path.Dir(child)
		// Never prune the workspace root itself.
		if dir == "." || dir == "/" || dir == "" {
			break
		}
		// Stop at anything mounted, in either direction: an ancestor that is
		// itself mounted is out of the base overlay's reach, and one that still
		// holds a mount beneath it is not empty — reads there keep serving the
		// mounted content, so whiteouting it in the source would be wrong.
		if ws.MountedPath(dir) || ws.HasMountsUnder(dir) {
			break
		}
		entries, err := s.workspaceDirEntries(ctx, srv, ws, dir)
		if err != nil {
			// Pruning is best-effort cleanup: an unreadable parent just means
			// there is nothing to prune here.
			break
		}
		// Stop at the first ancestor that still has entries of its own.
		if len(entries) != 1 || strings.TrimSuffix(entries[0], "/") != path.Base(child) {
			break
		}
		topmost = dir
		child = dir
	}
	return topmost
}

// workspaceDirEntries lists a workspace directory as the source sees it: host
// content with overlay edits applied, resolved exactly like every other read of
// the workspace source. Mounted content is deliberately not overlaid — it is
// not what a prune whiteouts, and callers stop before listing a directory that
// holds a mount anyway.
func (s *workspaceSchema) workspaceDirEntries(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	dir string,
) ([]string, error) {
	root, err := s.resolveRootfs(ctx, ws, dir, core.CopyFilter{}, false)
	if err != nil {
		return nil, err
	}
	var entries dagql.Array[dagql.String]
	if err := srv.Select(ctx, root, &entries, dagql.Selector{Field: "entries"}); err != nil {
		return nil, err
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = string(e)
	}
	return out, nil
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
	prune := s.emptiedAncestorDir(ctx, parent.Self(), resolvedPath)
	return s.workspaceEdit(ctx, parent, resolvedPath, removedPaths(prunedRemoval(resolvedPath, prune)...), nil, func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, removeAndPruneSelectors(targetPath, "withoutFile", prune)...)
		return updated, err
	})
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
	prune := s.emptiedAncestorDir(ctx, parent.Self(), resolvedPath)
	return s.workspaceEdit(ctx, parent, resolvedPath, removedPaths(prunedRemoval(resolvedPath, prune)...), nil, func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, removeAndPruneSelectors(targetPath, "withoutDirectory", prune)...)
		return updated, err
	})
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
	touched, err := changesetOverlayPaths(ctx, changesObj.Self())
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	for _, p := range touched.all() {
		if err := guardMountedPath(parent.Self(), p); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		// v1: a changeset that reaches into a cache mount is rejected. Its edits
		// would have to be split by mount and routed into the mounts tree to
		// reach the volume; applied to the overlay they would silently sit under
		// the mount, invisible to reads and never committed (a follow-up).
		if mount, ok := parent.Self().CacheMountForPath(p); ok {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("withChanges path %q falls under cache mount %q; applying a changeset across a cache mount boundary is not supported", p, mount.Target)
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

func (s *workspaceSchema) withConfigPaths(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		ConfigFile string
		LockFile   string
	},
) (dagql.ObjectResult[*core.Workspace], error) {
	ws, err := workspaceWithConfigPaths(parent.Self(), args.ConfigFile, args.LockFile)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func workspaceWithConfigPaths(parent *core.Workspace, configFile, lockFile string) (*core.Workspace, error) {
	if err := validateWorkspaceMetadataPath("config file", configFile); err != nil {
		return nil, err
	}
	if err := validateWorkspaceMetadataPath("lock file", lockFile); err != nil {
		return nil, err
	}
	ws := parent.Clone()
	ws.ConfigFile = configFile
	ws.LockFile = lockFile
	return ws, nil
}

func validateWorkspaceMetadataPath(label, value string) error {
	if value == "" {
		return nil
	}
	clean := path.Clean(value)
	if path.IsAbs(value) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("workspace %s path %q must be inside the workspace root", label, value)
	}
	if clean != value {
		return fmt.Errorf("workspace %s path %q must be canonical", label, value)
	}
	return nil
}

func (s *workspaceSchema) withConfigEnvironment(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct{ Name string },
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := workspaceWithConfigEnvironment(parent.Self(), args.Name)
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func workspaceWithConfigEnvironment(parent *core.Workspace, name string) *core.Workspace {
	ws := parent.Clone()
	ws.SetWorkspaceEnv(name)
	return ws
}

func (s *workspaceSchema) withGitAuthor(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		Name  string
		Email string
	},
) (dagql.ObjectResult[*core.Workspace], error) {
	ws, err := workspaceWithGitAuthor(parent.Self(), args.Name, args.Email)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func workspaceWithGitAuthor(parent *core.Workspace, name, email string) (*core.Workspace, error) {
	if (name == "") != (email == "") {
		return nil, fmt.Errorf("workspace Git author name and email must be set together")
	}
	ws := parent.Clone()
	ws.GitAuthorName = name
	ws.GitAuthorEmail = email
	return ws, nil
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
	resolvedPath, err := resolveMountPath(path, parent.Self().Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sourceID, err := source.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	mounts, err := workspaceMountsTree(ctx, srv, parent.Self())
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
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

// resolveMountPath resolves a mount path argument to a workspace-root-relative
// path, rejecting the workspace root: mounting over it would shadow the whole
// source, which no mount kind supports.
func resolveMountPath(p, cwd string) (string, error) {
	resolvedPath, err := resolveWorkspacePath(p, cwd)
	if err != nil {
		return "", err
	}
	if resolvedPath == "." || resolvedPath == "" {
		return "", fmt.Errorf("cannot mount over the workspace root")
	}
	return resolvedPath, nil
}

// workspaceMountsTree returns the workspace's mounts tree, or a fresh empty
// directory when nothing is mounted yet. Every mount kind shares this one tree,
// keyed by workspace-root-relative path, so reads, search, glob and findUp pick
// mounted content up without knowing what was mounted.
func workspaceMountsTree(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
) (dagql.ObjectResult[*core.Directory], error) {
	if mounts, ok := ws.MountsDir(); ok {
		return mounts, nil
	}
	var empty dagql.ObjectResult[*core.Directory]
	err := srv.Select(ctx, srv.Root(), &empty, dagql.Selector{Field: "directory"})
	return empty, err
}

// guardMountedPath rejects overlay edits that target a path at or under a
// read-only mount point. Cache mounts are writable, so edits under them are
// allowed and routed into the mounts tree instead (see workspaceEdit). It is a
// no-op when the workspace has no mounts.
func guardMountedPath(ws *core.Workspace, resolvedPath string) error {
	if _, writable := ws.CacheMountForPath(resolvedPath); writable {
		return nil
	}
	if ws.MountedPath(resolvedPath) {
		return fmt.Errorf("workspace path %q is a read-only mount and cannot be modified", resolvedPath)
	}
	return nil
}

type workspaceWithMountedCacheArgs struct {
	Path  string
	Cache core.CacheVolumeID
}

// withMountedCache mounts a cache volume like withMountedDirectory mounts a
// Directory: the volume's content goes into the mounts tree at the resolved
// workspace path, so reads, search, glob and findUp see it shadowing the source
// and it stays out of Workspace.changes. What differs is that the mount is
// writable — edits under it update the mounts tree (see workspaceEdit) and
// export commits them back into the volume, diffed against the baseline taken
// here.
//
// Mounting itself copies nothing: the baseline Directory is lazy, so the volume
// is read only once something actually reaches into the mount.
func (s *workspaceSchema) withMountedCache(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithMountedCacheArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	resolvedPath, err := resolveMountPath(args.Path, ws.Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	cache, err := args.Cache.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if cache.Self() == nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("cache volume is nil")
	}

	// A lazy immutable view of the volume's mutable content — no copy happens
	// until it is evaluated. The read is session-scoped and not replayable, like
	// host.directory; depending on it taints this call the same way, so the
	// field itself needs no marker.
	var baseline dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, cache, &baseline, dagql.Selector{
		Field: "__snapshotDirectory",
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("snapshot cache volume: %w", err)
	}
	baselineID, err := baseline.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	mounts, err := workspaceMountsTree(ctx, srv, ws)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var updated dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, mounts, &updated, dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(resolvedPath)},
			{Name: "source", Value: dagql.NewID[*core.Directory](baselineID)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	newWS := ws.WithCacheMounted(updated, core.WorkspaceCacheMount{
		Target:   resolvedPath,
		Volume:   cache,
		Baseline: baseline,
	})
	return dagql.NewObjectResultForCurrentCall(ctx, srv, newWS)
}

type workspaceWithoutMountArgs struct {
	Path string
}

// withoutMount drops whatever is mounted at the given path — a cache volume, a
// directory or a file — along with anything mounted inside it, and removes its
// content from the mounts tree so the workspace source shows through again.
// Pending cache mount edits are discarded with the mount: only export commits
// them.
func (s *workspaceSchema) withoutMount(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithoutMountArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	resolvedPath, err := resolveWorkspacePath(args.Path, ws.Cwd)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	mounts, ok := ws.MountsDir()
	if !ok {
		return dagql.NewObjectResultForCurrentCall(ctx, srv, ws.Clone())
	}
	// withoutDirectory removes the path whatever its type, so one call covers
	// mounted files too.
	var updated dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, mounts, &updated, dagql.Selector{
		Field: "withoutDirectory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(resolvedPath)}},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws.WithoutMountedAt(updated, resolvedPath))
}

// mountEdit applies an edit under a cache mount. Mounted content is keyed by
// workspace-root-relative path, so the edit lands in the mounts tree at exactly
// the path a base edit would have used — only the tree it writes to differs.
// Nothing touches the overlay, so cache mount edits never show up in
// Workspace.changes; export is what carries them into the volume.
func (s *workspaceSchema) mountEdit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	resolvedPath string,
	edit func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error),
) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	mounts, ok := parent.Self().MountsDir()
	if !ok {
		// Unreachable: a path is only writable-mounted if something is mounted.
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace path %q is under a cache mount but the workspace has no mounts", resolvedPath)
	}
	updated, err := edit(mounts, resolvedPath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, parent.Self().WithMountsDir(updated))
}

// workspaceEdit dispatches a single-path edit to either a cache mount (when the
// path lands under one) or the base overlay. `touched` classifies what the edit
// does to resolvedPath — see overlayTouched; getting it wrong is not a no-op.
func (s *workspaceSchema) workspaceEdit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	resolvedPath string,
	touched overlayTouched,
	readsExisting []string,
	edit func(base dagql.ObjectResult[*core.Directory], targetPath string) (dagql.ObjectResult[*core.Directory], error),
) (dagql.ObjectResult[*core.Workspace], error) {
	if _, ok := parent.Self().CacheMountForPath(resolvedPath); ok {
		return s.mountEdit(ctx, parent, resolvedPath, edit)
	}
	return s.overlayEdit(ctx, parent, touched, readsExisting, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		return edit(base, resolvedPath)
	}, nil)
}

// workspaceOverlayChanges returns an overlay workspace's pending changes as
// seen from the staged commits: the overlay's own changeset when nothing is
// staged, otherwise the diff between the staged tree — the overlay's base with
// every staged commit's content applied — and the overlay's current tree, which
// is exactly the uncommitted remainder.
//
// Staging a commit deliberately leaves the overlay itself alone, so the tree the
// workspace serves (Workspace.file / .directory, and every container mount built
// from them) is byte-identical before and after a commit. Only the diff views
// move: they re-anchor on the staged tree.
//
// The staged tree is built by applying the staged changeset to the overlay's
// (sparse, touched-paths-only) base, so this stays as sparse as the overlay
// itself — no full-tree host read is forced by asking for status.
//
// The second return value reports whether the workspace has an overlay at all;
// workspaces without one read their pending changes from git instead.
func (s *workspaceSchema) workspaceOverlayChanges(
	ctx context.Context,
	ws *core.Workspace,
) (dagql.ObjectResult[*core.Changeset], bool, error) {
	overlay, ok := ws.OverlayChanges()
	if !ok || overlay.Self() == nil {
		return overlay, false, nil
	}
	staged, ok := ws.StagedChanges()
	if !ok {
		return overlay, true, nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return overlay, true, err
	}
	stagedTree, err := stagedTreeOver(ctx, overlay.Self().Before, staged)
	if err != nil {
		return overlay, true, err
	}
	stagedTreeID, err := stagedTree.ID()
	if err != nil {
		return overlay, true, err
	}
	var remainder dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, overlay.Self().After, &remainder, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](stagedTreeID)}},
	}); err != nil {
		return overlay, true, fmt.Errorf("resolve uncommitted remainder: %w", err)
	}
	return remainder, true, nil
}

// keepRemovalAncestors re-creates, in the delta root, the ancestor directories
// of every removed path that the removal did not itself delete.
//
// A removal is the one edit that puts host content in the sparse base without
// putting it in the delta root — that is how the removal becomes visible. But
// the base cannot hold `a/b/c` without also holding `a` and `a/b`, and those
// ancestors are just as absent from the delta root, so the diff reports them
// as removed too. Removed paths collapse to their topmost entry, so `a/` is
// what export deletes: the whole directory, siblings and all, from a request to
// delete one thing inside it.
//
// Recreating the surviving ancestors makes the delta root say what is actually
// true — `a/` and `a/b/` still exist, `a/b/c` does not — and the removal
// narrows back to what was asked for. Ancestors that ARE in the removed set
// (the emptied chain a prune deliberately takes with it, see emptiedAncestorDir)
// are left alone.
func keepRemovalAncestors(
	ctx context.Context,
	srv *dagql.Server,
	deltaRoot dagql.ObjectResult[*core.Directory],
	removed []string,
) (dagql.ObjectResult[*core.Directory], error) {
	seen := map[string]struct{}{}
	var keep []string
	for _, p := range removed {
		p = normalizeCommitPath(p)
		if p == "" {
			continue
		}
		for dir := path.Dir(p); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			// Covered, not equal: an ancestor beneath a pruned chain is gone
			// with it, and recreating it would recreate the whole chain.
			if pathCoveredBy(dir, removed) {
				continue
			}
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			keep = append(keep, dir)
		}
	}
	if len(keep) == 0 {
		return deltaRoot, nil
	}
	slices.Sort(keep)
	for _, dir := range keep {
		var updated dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, deltaRoot, &updated, dagql.Selector{
			Field: "withNewDirectory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(dir)}},
		}); err != nil {
			return deltaRoot, fmt.Errorf("preserve ancestor %q of a removal: %w", dir, err)
		}
		deltaRoot = updated
	}
	return deltaRoot, nil
}

// assertOverlayRemovalsIntended refuses a host-backed overlay whose changeset
// reports a removal no edit asked for.
//
// A host overlay's removals are *inferred*: they are whatever the sparse base
// holds and the delta root does not. That makes them only as trustworthy as the
// bookkeeping that decides what goes into the base — and a mistake there does
// not fail, it silently deletes files, at whatever scale the mistaken path
// covers. (A touched `internal/` once cost 660 files.)
//
// So the inference is checked against the ledger of what edits actually removed,
// at the two boundaries where a wrong answer stops being recoverable: staging a
// commit, and writing the user's checkout. Both already force the changeset, so
// the check costs one memoized walk of paths already computed.
//
// What trips it: a touched directory whose contents the delta root only partly
// owns, and a write that replaces an existing directory with a file. Both are
// cases where inferring deletion from absence is wrong, and where the honest
// answer is to stop.
func (s *workspaceSchema) assertOverlayRemovalsIntended(
	ctx context.Context,
	ws *core.Workspace,
) error {
	// Value/git/rootless overlays diff full in-engine trees on both sides:
	// their removals are observed, not inferred, and there is no ledger.
	if ws.HostPath() == "" || !ws.ClientLocalBase() {
		return nil
	}
	changes, ok := ws.OverlayChanges()
	if !ok || changes.Self() == nil {
		return nil
	}
	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("verify overlay removals: %w", err)
	}
	intended := ws.OverlayRemovedPaths()
	var unintended []string
	for _, p := range paths.AllRemoved {
		if !pathCoveredBy(p, intended) {
			unintended = append(unintended, p)
		}
	}
	if len(unintended) == 0 {
		return nil
	}
	slices.Sort(unintended)
	return fmt.Errorf(
		"workspace would delete %d path(s) that no edit removed: %s; "+
			"refusing rather than destroying content nothing asked to change",
		len(unintended), strings.Join(samplePaths(unintended, 5), ", "))
}

// pathCoveredBy reports whether p is one of, or nested under, the given paths.
func pathCoveredBy(p string, paths []string) bool {
	p = normalizeCommitPath(p)
	if p == "" {
		return false
	}
	for _, q := range paths {
		q = normalizeCommitPath(q)
		if q == "" {
			continue
		}
		if p == q || strings.HasPrefix(p, q+"/") {
			return true
		}
	}
	return false
}

// samplePaths returns at most n paths, with a count of what it left out, for
// error messages that must stay readable when the list is hundreds long.
func samplePaths(paths []string, n int) []string {
	if len(paths) <= n {
		return paths
	}
	return append(slices.Clone(paths[:n]), fmt.Sprintf("and %d more", len(paths)-n))
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
		changes, ok, err := s.workspaceOverlayChanges(ctx, parent.Self())
		if err != nil {
			return inst, err
		}
		if !ok {
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

	// Verified before the fast-forward below, so a workspace carrying phantom
	// deletions is refused with the checkout still untouched.
	if err := s.assertOverlayRemovalsIntended(ctx, ws); err != nil {
		return core.Void{}, fmt.Errorf("save: %w", err)
	}

	// Staged commits land first. They fast-forward unchanged when the checkout
	// has not moved, or are replayed onto its new tip when another save advanced
	// the branch, so the remaining overlay changes below are written on top of
	// the resulting HEAD (and `git status` on the host then shows exactly them).
	// A conflict is prepared away from the checkout and leaves it untouched.
	if len(ws.PendingCommits()) > 0 {
		if err := s.exportPendingCommits(ctx, ws); err != nil {
			return core.Void{}, err
		}
	}

	// Base export: write the primary overlay changeset to the local Git
	// workspace. Only a non-empty base changeset requires a valid host path;
	// cache mount write-through (below) runs regardless of the base source.
	//
	// The changeset is the staged-relative one: the fast-forward above already
	// wrote every committed path into the work tree, so what is left to write is
	// exactly the uncommitted remainder — each path lands once.
	changes, hasChanges, err := s.workspaceOverlayChanges(ctx, ws)
	if err != nil {
		return core.Void{}, err
	}
	if hasChanges && changes.Self() != nil {
		isEmpty, err := changes.Self().IsEmpty(ctx)
		if err != nil {
			return core.Void{}, err
		}
		if !isEmpty {
			exportCtx, hostPath, err := workspaceExportContext(ctx, ws)
			if err != nil {
				return core.Void{}, err
			}
			if err := changes.Self().Export(exportCtx, hostPath); err != nil {
				return core.Void{}, err
			}
			if err := core.InvalidateCurrentWorkspace(exportCtx); err != nil {
				slog.Warn("could not invalidate workspace after export", "error", err)
			}
			// The export just changed the workspace's on-disk content, so host
			// reads (Workspace.file / .directory) cached earlier in this
			// session are stale — they are cached per client for the client's
			// whole lifetime (dagql.PerClientInput). Bump the client's read
			// epoch so subsequent reads land in a fresh per-client cache
			// namespace and re-read the live host. Best-effort, like the
			// invalidation above: a bookkeeping failure must not fail an
			// export that already succeeded.
			if err := core.BumpWorkspaceReadEpoch(exportCtx); err != nil {
				slog.Warn("could not bump workspace read epoch after export", "error", err)
			}
		}
	}

	// Cache mount write-through: commit each mount's edits into its volume so
	// containers and modules mounting the same volume observe them. Mounted
	// content lives in the mounts tree, deliberately outside the overlay
	// changeset, so this is the only path that persists it — and it runs
	// regardless of the base source, since a value or remote workspace can still
	// carry a writable cache mount.
	if mounts, ok := ws.MountsDir(); ok {
		for _, mount := range ws.CacheMounts() {
			if mount.Volume.Self() == nil || mount.Baseline.Self() == nil {
				continue
			}
			changes, err := s.cacheMountChanges(ctx, mounts, mount)
			if err != nil {
				return core.Void{}, fmt.Errorf("diff cache mount %q: %w", mount.Target, err)
			}
			if err := mount.Volume.Self().CommitChanges(ctx, changes.Self()); err != nil {
				return core.Void{}, fmt.Errorf("commit cache mount %q: %w", mount.Target, err)
			}
		}
	}

	return core.Void{}, nil
}

// cacheMountChanges diffs a cache mount's current content in the mounts tree
// against the baseline captured when it was mounted. Diffing against the
// baseline rather than the volume's present content keeps write-through to the
// edits made through this workspace, leaving anything another writer added to
// the volume meanwhile in place.
func (s *workspaceSchema) cacheMountChanges(
	ctx context.Context,
	mounts dagql.ObjectResult[*core.Directory],
	mount core.WorkspaceCacheMount,
) (inst dagql.ObjectResult[*core.Changeset], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var current dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, mounts, &current, dagql.Selector{
		Field: "directory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(mount.Target)}},
	}); err != nil {
		return inst, err
	}
	baselineID, err := mount.Baseline.ID()
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, current, &inst, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](baselineID)}},
	}); err != nil {
		return inst, err
	}
	return inst, nil
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
//
// The invalidation is entirely a side effect on the owning client's read epoch,
// so the result is the PARENT's own object result rather than a new one minted
// for this call. That is load-bearing: the field carries dagql.PerCallInput (it
// must re-run, and re-bump, on every call), so a result minted for the current
// call would carry a random nonce in its ID — and every workspace the caller
// derives afterwards would chain off that ID for the rest of the session,
// permanently novel and unmatchable in the cache. Module loads pay that price
// worst: an agent that reloads once would re-declare its own modules on every
// subsequent edit. Returning parent keeps the epoch bump and leaves the
// caller's call chain exactly where it was.
func (s *workspaceSchema) reloaded(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Workspace], error) {
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
	return parent, nil
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
	ws.SetSource(core.NewWorkspaceSourceOverlay(parent.Self().Source(), nil, nil, nil, changesResult))
	if mutate != nil {
		mutate(ws)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

// overlayTouched is the paths one edit affects, classified by what the sparse
// base is allowed to hold for them.
//
// The classification is the whole safety story of the sparse base, so it is a
// type rather than a bare []string: an entry in the wrong bucket does not fail,
// it silently deletes files. See overlayEdit.
type overlayTouched struct {
	// Owned are paths whose content the delta root now holds in full. The
	// sparse base carries them so a modified file has a before-image to diff
	// against; a path here that the delta root does NOT fully own turns every
	// host file beneath it into a phantom deletion.
	//
	// Only ever leaf files, or directories the edit copied in wholesale. Never
	// a directory the edit merely wrote *into*, and never a synthesized parent
	// of an added path — a new file deep in an existing tree owns its own path,
	// not `internal/`.
	Owned []string
	// Removed are paths the edit deliberately deleted. These are the only
	// paths that may be in the sparse base and absent from the delta root, and
	// therefore the only removals the resulting changeset may report.
	Removed []string
}

func ownedPaths(paths ...string) overlayTouched {
	return overlayTouched{Owned: paths}
}

func removedPaths(paths ...string) overlayTouched {
	return overlayTouched{Removed: paths}
}

// all returns every path the edit affects, in either bucket.
func (t overlayTouched) all() []string {
	return slices.Concat(t.Owned, t.Removed)
}

// overlayEdit applies an edit to a workspace, producing a new overlay workspace.
// `edit` applies the operation to a given base directory: for value/git/rootless
// workspaces the full read root (already in-engine, nothing to upload), for
// host-backed workspaces the delta root — the accumulated edits applied to an
// empty base, stored as the overlay changeset's After side, which never
// references the host tree. `touched` are the workspace-relative paths this
// edit affects, classified (see overlayTouched).
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
// The sparse base preserves changeset semantics in BOTH directions, and only
// the first direction is self-evident:
//
//   - Every removal must be visible. A removal comes from an edit, which makes
//     the path touched and therefore present in the base to be diffed away.
//
//   - Nothing else may look like a removal. An include pattern that matches a
//     directory matches everything under it, so a touched *directory* pulls its
//     entire host subtree into the base. Whatever the delta root does not also
//     hold there then reads as deleted — silently, and for content no edit ever
//     mentioned. That is why `touched` is classified: Owned paths must be fully
//     owned by the delta root, and anything else must be a declared Removed.
//
// The second direction is bookkeeping, so it is also checked rather than
// trusted: assertOverlayRemovalsIntended re-derives the removals the changeset
// actually reports and refuses any the ledger cannot account for.
func (s *workspaceSchema) overlayEdit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	touched overlayTouched,
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
	touchedAll := unionPaths(ws.OverlayTouchedPaths(), touched.all())
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

	removedAll := unionPaths(ws.OverlayRemovedPaths(), touched.Removed)
	deltaRoot, err = keepRemovalAncestors(ctx, srv, deltaRoot, removedAll)
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
	newWS.SetSource(core.NewWorkspaceSourceOverlay(ws.Source(), touchedAll, removedAll, seededAll, changesResult))
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

// filePathsOnly drops directory entries from a changeset path list. Every
// producer of those lists marks a directory with a trailing slash
// (listSubdirectories, changesetDelta.addedDirs, appendRemovedTree), so the
// slash is the discriminator.
func filePathsOnly(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasSuffix(p, "/") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// changesetOverlayPaths classifies a changeset's paths for the overlay: the
// content the changed tree owns, and what it deliberately removed.
//
// Added is deliberately stripped of its directory entries. A changeset reports
// one for every directory it creates, including the parents that merely had to
// exist for a nested file to land — adding `a/b/c.txt` to an empty tree reports
// `a/`, `a/b/` and `a/b/c.txt`. Those parents name real directories in the
// workspace whose other contents the change knows nothing about, so carrying
// them into the sparse base drags in whole host subtrees and turns every
// sibling into a phantom deletion (see overlayEdit).
//
// AllRemoved keeps its directory entries: a removal is precisely the case where
// the base must carry a subtree the changed tree does not.
func changesetOverlayPaths(ctx context.Context, ch *core.Changeset) (overlayTouched, error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return overlayTouched{}, err
	}
	return overlayTouched{
		Owned:   slices.Concat(filePathsOnly(paths.Added), filePathsOnly(paths.Modified)),
		Removed: paths.AllRemoved,
	}, nil
}

// changesetTouchedPaths returns the workspace-relative paths a changeset affects
// (added, modified, and removed), used to size the sparse diff base and to scope
// commits and conflict checks. Synthesized parent directories are excluded — see
// changesetOverlayPaths.
func changesetTouchedPaths(ctx context.Context, ch *core.Changeset) ([]string, error) {
	touched, err := changesetOverlayPaths(ctx, ch)
	if err != nil {
		return nil, err
	}
	return touched.all(), nil
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
	// metadata outside the workspace; that layout is never interpreted by the
	// engine -- the repository is reconstructed canonically from the client's
	// own git pack when it is resolved (see materializeWorkspaceGit) -- so a
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

	var dir dagql.ObjectResult[*core.Directory]
	if latest, ok := ws.LatestPendingCommit(); ok && latest.Repo.Self() != nil {
		// Commits staged engine-side live in this tree's .git; reading the
		// workspace's own repository would still report the pre-commit HEAD.
		dir = latest.Repo
		// The staged tree's work tree is frozen at commit time, so overlay
		// edits made *after* the commit are missing from it. Applying the
		// staged-anchored remainder — the diff between the staged tree and
		// the overlay's current tree — brings the work tree back up to date,
		// which is what makes GitRepository.uncommitted report the true
		// remainder.
		//
		// It must be the remainder rather than the overlay's own changeset:
		// the overlay is anchored on the pre-commit checkout, an anchor that
		// cannot express deleting a path whose only existence is owed to a
		// staged commit — there "add then delete" cancels out to an empty
		// delta, so the deletion would silently vanish from the work tree
		// (and from status, and from any commit naming the path).
		changes, ok, err := s.workspaceOverlayChanges(ctx, ws)
		if err != nil {
			return inst, err
		}
		if ok && changes.Self() != nil {
			changesID, err := changes.ID()
			if err != nil {
				return inst, err
			}
			srv, err := core.CurrentDagqlServer(ctx)
			if err != nil {
				return inst, err
			}
			if err := srv.Select(ctx, dir, &dir, dagql.Selector{
				Field: "withChanges",
				Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
			}); err != nil {
				return inst, fmt.Errorf("workspace git directory (overlay): %w", err)
			}
		}
	} else {
		if err := s.ensureWorkspaceGitDirectory(ctx, ws); err != nil {
			return inst, err
		}

		var (
			err  error
			fast bool
		)
		if ws.HostPath() != "" && ws.ClientLocalBase() {
			dir, fast, err = s.materializeWorkspaceGitWorktree(ctx, ws)
			if err != nil {
				return inst, fmt.Errorf("workspace git worktree: %w", err)
			}
		}
		if !fast {
			dir, err = s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
			if err != nil {
				return inst, fmt.Errorf("workspace git directory: %w", err)
			}

			// Git worktree and submodule checkouts have a .git *pointer file* at
			// their root, whose target lives outside the workspace boundary.
			// Replace whatever .git the synced tree carries with a canonical
			// reconstruction of the checkout's repository (packed by the client's
			// own git), so the LocalGitRepository sees a plain repository.
			dir, err = s.materializeWorkspaceGit(ctx, ws, dir)
			if err != nil {
				return inst, fmt.Errorf("workspace git directory: %w", err)
			}
		} else if changes, ok := ws.OverlayChanges(); ok && changes.Self() != nil {
			changesID, err := changes.ID()
			if err != nil {
				return inst, err
			}
			srv, err := core.CurrentDagqlServer(ctx)
			if err != nil {
				return inst, err
			}
			if err := srv.Select(ctx, dir, &dir, dagql.Selector{
				Field: "withChanges",
				Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
			}); err != nil {
				return inst, fmt.Errorf("workspace git directory (overlay): %w", err)
			}
		}
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

// materializeWorkspaceGitWorktree builds a host-backed workspace repository
// without resolving its whole root directory. It checks out the canonical pack
// for HEAD, asks the client for only the git-visible worktree delta, and applies
// that delta directly to an engine snapshot. The bool is false when the client
// predates the delta RPC (or the checkout has no canonical HEAD), in which case
// callers retain the full-directory compatibility path.
func (s *workspaceSchema) materializeWorkspaceGitWorktree(
	ctx context.Context,
	ws *core.Workspace,
) (dagql.ObjectResult[*core.Directory], bool, error) {
	var inst dagql.ObjectResult[*core.Directory]
	clientCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, false, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, false, err
	}
	epoch, err := core.WorkspaceReadEpoch(clientCtx)
	if err != nil {
		return inst, false, err
	}

	var scratch dagql.ObjectResult[*core.Directory]
	if err := srv.Select(clientCtx, srv.Root(), &scratch, dagql.Selector{Field: "directory"}); err != nil {
		return inst, false, err
	}
	canonical, err := core.MaterializeHostGitCheckout(clientCtx, srv, scratch, ws.HostPath(), "epoch:"+epoch)
	if err != nil {
		return inst, false, err
	}
	if _, err := canonical.Self().Stat(clientCtx, canonical, srv, ".git", true); err != nil {
		// MaterializeHostGitCheckout leaves scratch unchanged for clients that
		// do not implement checkout packing.
		return inst, false, nil
	}

	var repoResult dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(clientCtx, canonical, &repoResult, dagql.Selector{Field: "asGit"}); err != nil {
		return inst, false, err
	}
	var head dagql.Result[*core.GitRef]
	if err := srv.Select(clientCtx, repoResult, &head, dagql.Selector{Field: "head"}); err != nil {
		// An unborn checkout has no HEAD tree. The old directory route retains
		// its existing behavior for this uncommon state.
		return inst, false, nil
	}
	if head.Self() == nil || head.Self().Ref == nil || head.Self().Ref.SHA == "" {
		return inst, false, nil
	}
	headID, err := head.ID()
	if err != nil {
		return inst, false, err
	}
	headObject, err := dagql.NewID[*core.GitRef](headID).Load(clientCtx, srv)
	if err != nil {
		return inst, false, err
	}

	var clean dagql.ObjectResult[*core.Directory]
	if err := srv.Select(clientCtx, headObject, &clean, dagql.Selector{
		Field: "tree",
		Args: []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(false)},
			{Name: "depth", Value: dagql.NewInt(0)},
			{Name: "includeTags", Value: dagql.NewBoolean(true)},
		},
	}); err != nil {
		return inst, false, err
	}

	if err := srv.Select(clientCtx, clean, &inst, dagql.Selector{
		Field: "__withGitWorktree",
		Args: []dagql.NamedInput{
			{Name: "checkoutPath", Value: dagql.NewString(ws.HostPath())},
			{Name: "expectedHeadSHA", Value: dagql.NewString(head.Self().Ref.SHA)},
		},
	}); errors.Is(err, engineutil.ErrGitWorktreeUnsupported) {
		return dagql.ObjectResult[*core.Directory]{}, false, nil
	} else if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, false, err
	}
	return inst, true, nil
}

// materializeWorkspaceGit replaces a host-backed workspace's synced .git with
// a canonical reconstruction of the checkout's repository. The client's own
// git packs the repository (HEAD plus all branches and tags) and the engine
// rebuilds a standalone .git from that pack. The engine never interprets the
// host checkout's raw git layout -- worktree/submodule pointer files,
// commondirs, separate git dirs are all the client git's business -- so the
// result is identical for a given ref state regardless of how the checkout is
// laid out on the host.
//
// A workspace with no host (in-memory rootfs) keeps its embedded .git: there
// is no client checkout to pack. A checkout that is not a git repository is
// returned unchanged so downstream git operations surface the plain "not a git
// repository" failure, matching pre-worktree behavior.
func (s *workspaceSchema) materializeWorkspaceGit(
	ctx context.Context,
	ws *core.Workspace,
	dir dagql.ObjectResult[*core.Directory],
) (dagql.ObjectResult[*core.Directory], error) {
	if ws.HostPath() == "" {
		return dir, nil
	}
	clientCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return dir, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dir, err
	}
	// Pin the reconstruction to the session's cached view of the checkout by
	// keying it on the workspace read epoch rather than the checkout's live
	// ref state: a checkout that advances mid-session must NOT be silently
	// re-read, or a staged-commit export could fast-forward over the user's
	// own commit instead of detecting that the local branch moved. The epoch
	// bumps on export/reload, exactly when the pinned view should be refreshed
	// -- the same scoping Workspace.file/.directory host reads use.
	epoch, err := core.WorkspaceReadEpoch(clientCtx)
	if err != nil {
		return dir, err
	}
	out, err := core.MaterializeHostGitCheckout(clientCtx, srv, dir, ws.HostPath(), "epoch:"+epoch)
	if errors.Is(err, core.ErrNoGitContext) {
		// No .git at all: leave the original directory so downstream callers
		// surface the plain "not a git repository" failure, matching
		// pre-worktree behavior.
		return dir, nil
	}
	return out, err
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
	if _, ok := ws.OverlayChanges(); ok {
		if ref, ok := ws.SourceGitRef(); ok {
			return gitRefWorkspaceChanges(ctx, ws, ref)
		}
		if !ws.ClientLocalBase() {
			// Value/rootless overlays have no local checkout to diff against:
			// their pending set *is* the overlay. Staged commits re-anchor
			// this diff on the staged tree, so committed content stops showing
			// as pending without ever leaving the workspace tree.
			changes, _, err := s.workspaceOverlayChanges(ctx, ws)
			return changes, err
		}
		// Host-backed overlays fall through to the repository route below.
		//
		// "Uncommitted" means the same thing whether or not the agent has
		// edited anything: everything the work tree holds that the (staged)
		// HEAD does not. Answering from the overlay changeset instead would
		// re-anchor the diff on the host's *dirty* state at the moment the
		// overlay was created, silently dropping every change the checkout
		// already carried — so a single edit would make files the user had
		// been working on disappear from status/diff, and from a commit made
		// with no paths. The repository route diffs the effective tree
		// (host + overlay, plus the staged commits' .git — see
		// workspaceGitRepository) against the cleaned HEAD work tree, which
		// covers both layers at once.
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

func (s *workspaceSchema) workspaceGitUnmanaged(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	_ struct{},
) (dagql.ObjectResult[*core.Changeset], error) {
	ws := parent.Self().Workspace.Self()

	// Only host-backed overlays have two different views to compare. For
	// value, rootless and git-ref workspaces `uncommitted` *is* the overlay
	// (see workspaceGitUncommitted), so the difference is empty by
	// construction and reporting anything here would double-report.
	if ws.HostPath() == "" || !ws.ClientLocalBase() {
		return emptyChangesetResult(ctx)
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	var uncommitted dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, parent, &uncommitted, dagql.Selector{Field: "uncommitted"}); err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}

	overlay, remaining, err := s.workspaceUnmanagedRemainder(ctx, ws, uncommitted)
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	if len(remaining) == 0 {
		return emptyChangesetResult(ctx)
	}
	scoped, _, err := s.scopeChangesetToPaths(ctx, overlay, remaining)
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	return scoped, nil
}

// workspaceUnmanagedRemainder computes the set difference between the paths the
// overlay changeset touches (what Workspace.export writes to the checkout) and
// the paths git's uncommitted view reports (what status/diff/commit operate on).
//
// The leftovers are the pending edits git cannot see at all: gitignored paths,
// paths inside a nested repository, and anything else the cleaned-HEAD baseline
// in LocalGitRepository.Cleaned leaves byte-identical on both sides of the diff.
// It is deliberately cause-agnostic, so .git/info/exclude, core.excludesFile and
// any future mechanism are covered without enumerating them.
//
// Returns the overlay changeset alongside the remaining paths, so callers can
// project the paths back onto it with scopeChangesetToPaths.
func (s *workspaceSchema) workspaceUnmanagedRemainder(
	ctx context.Context,
	ws *core.Workspace,
	uncommitted dagql.ObjectResult[*core.Changeset],
) (overlay dagql.ObjectResult[*core.Changeset], remaining []string, err error) {
	overlay, ok, err := s.workspaceOverlayChanges(ctx, ws)
	if err != nil || !ok || overlay.Self() == nil {
		return overlay, nil, err
	}
	overlayPaths, err := changesetTouchedPaths(ctx, overlay.Self())
	if err != nil {
		return overlay, nil, fmt.Errorf("compute overlay paths: %w", err)
	}
	if len(overlayPaths) == 0 {
		return overlay, nil, nil
	}
	if uncommitted.Self() == nil {
		return overlay, overlayPaths, nil
	}
	trackedPaths, err := changesetTouchedPaths(ctx, uncommitted.Self())
	if err != nil {
		return overlay, nil, fmt.Errorf("compute uncommitted paths: %w", err)
	}
	tracked := make(map[string]struct{}, len(trackedPaths))
	for _, p := range trackedPaths {
		tracked[p] = struct{}{}
	}
	for _, p := range overlayPaths {
		if _, ok := tracked[p]; !ok {
			remaining = append(remaining, p)
		}
	}
	return overlay, remaining, nil
}

// unmanagedPathsInScope returns the subset of the given resolved commit scope
// that git cannot track: paths with pending overlay edits that never show up in
// the workspace's uncommitted set.
func (s *workspaceSchema) unmanagedPathsInScope(
	ctx context.Context,
	ws *core.Workspace,
	uncommitted dagql.ObjectResult[*core.Changeset],
	resolved []string,
) ([]string, error) {
	if ws.HostPath() == "" || !ws.ClientLocalBase() {
		return nil, nil
	}
	_, remaining, err := s.workspaceUnmanagedRemainder(ctx, ws, uncommitted)
	if err != nil {
		return nil, err
	}
	return commitPathsInScope(remaining, resolved), nil
}

func emptyChangesetResult(ctx context.Context) (dagql.ObjectResult[*core.Changeset], error) {
	var inst dagql.ObjectResult[*core.Changeset]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	empty, err := core.NewEmptyChangeset(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, empty)
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
	if isSyntheticWorkspace(parent) && !parent.IsModuleBearingValue() {
		return &core.AgentGroup{}, nil
	}

	include := workspaceIncludePatterns(args.Include)

	var mods []dagql.ObjectResult[*core.Module]
	if parent.IsModuleBearingValue() {
		// A module-bearing value has no owning client or served-module snapshot.
		// Resolve every configured module from its own value-backed tree;
		// workspaceOverlayModules treats this as a config-wide invalidation and
		// therefore includes untouched local modules too.
		overlayMods, err := s.workspaceOverlayModules(ctx, parentResult, include)
		if err != nil {
			return nil, err
		}
		mods = mergeOverlayModules(nil, overlayMods)
	} else {
		var err error
		ctx, err = s.withWorkspaceClientContext(ctx, parent)
		if err != nil {
			return nil, err
		}

		// agent composition is strict: a module that can't load is a failure.
		if _, err := ensureWorkspaceModulesLoaded(ctx, include, false); err != nil {
			return nil, err
		}
		mods, err = currentWorkspacePrimaryModules(ctx)
		if err != nil {
			return nil, err
		}

		// The served modules above are the workspace as it was on disk when the
		// session started. Re-resolve whatever the workspace's pending overlay
		// touches, so an agent recomposing itself (install/reload) sees its own
		// staged edits to module source and to dagger.toml.
		overlayMods, err := s.workspaceOverlayModules(ctx, parentResult, include)
		if err != nil {
			return nil, err
		}
		mods = mergeOverlayModules(mods, overlayMods)
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

// addresses lists module functions loadable as bare "module:function" address
// references (hack/designs/sandboxes.md §5): top-level functions on each
// installed module's main object — the only shape resolveModuleRef can load —
// whose return type name matches the requested type and whose required args
// (beyond an auto-injected Workspace) are none.
func (s *workspaceSchema) addresses(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Type string
	},
) ([]*core.WorkspaceAddress, error) {
	if isSyntheticWorkspace(parent) {
		return []*core.WorkspaceAddress{}, nil
	}

	ctx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, err
	}

	// Best-effort, like generators: discovery lists what is loadable, and a
	// module that fails to load contributes no loadable addresses anyway
	// (resolveModuleRef hard-errors on it). A broken module must not hide the
	// rest of the workspace's addresses; its load failure surfaces as a
	// warning from EnsureWorkspaceModules.
	if _, err := ensureWorkspaceModulesLoaded(ctx, nil, true); err != nil {
		return nil, err
	}
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}

	// An entrypoint module's functions are hoisted onto the Query root and no
	// module field is served for it, so a "module:function" reference can never
	// resolve (see demandLoadInstalledModule); exclude those modules here.
	cfg, err := workspaceConfigWithCompatFallback(ctx, parent)
	if err != nil {
		return nil, err
	}
	entrypoints := make(map[string]bool, len(cfg.Modules))
	for name, entry := range cfg.Modules {
		if entry.Entrypoint {
			entrypoints[strcase.ToKebab(name)] = true
		}
	}

	var addresses core.WorkspaceAddresses
	for _, mod := range mods {
		modName := mod.Self().Name()
		if entrypoints[strcase.ToKebab(modName)] {
			continue
		}
		mainObj, ok := mod.Self().MainObject()
		if !ok {
			continue
		}
		for _, fnRes := range mainObj.Functions {
			fn := fnRes.Self()
			retType := fn.ReturnType.Self()
			// A list return can't be lifted into a single object; ast.Type.Name
			// would still report the element type's name, so rule lists out first.
			if retType.Kind == core.TypeDefKindList {
				continue
			}
			if retType.ToType().Name() != args.Type {
				continue
			}
			if core.FunctionRequiresArgsExceptWorkspace(fn) {
				continue
			}
			// Kebab-case both segments for consistency with CLI-facing names;
			// resolveModuleRef normalizes with ToLowerCamel, so this round-trips.
			addresses = append(addresses, &core.WorkspaceAddress{
				Value:       strcase.ToKebab(modName) + ":" + strcase.ToKebab(fn.Name),
				Description: fn.Description,
			})
		}
	}
	addresses.Sort()
	return addresses, nil
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
	return withWorkspaceClientIDContext(ctx, ws.ClientID)
}

// workspaceExportContext resolves where an explicit save of ws must write and
// returns both the checkout path and a context routed to the client that owns
// it. An ordinary local workspace saves to itself; remote, synthetic, and
// portable workspaces have no implicit destination and fail rather than guess.
func workspaceExportContext(ctx context.Context, ws *core.Workspace) (context.Context, string, error) {
	clientID, hostPath, err := ws.ExportTarget(ctx)
	if err != nil {
		return nil, "", err
	}
	exportCtx, err := withWorkspaceClientIDContext(ctx, clientID)
	if err != nil {
		return nil, "", err
	}
	return exportCtx, hostPath, nil
}

func withWorkspaceClientIDContext(ctx context.Context, clientID string) (context.Context, error) {
	if clientID == "" {
		return nil, fmt.Errorf("workspace has no client ID")
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}
	clientMetadata, err := query.SpecificClientMetadata(ctx, clientID)
	if err != nil {
		return ctx, fmt.Errorf("get client metadata: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}
