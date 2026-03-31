package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	telemetry "github.com/dagger/otel-go"
)

type workspaceSchema struct{}

var _ SchemaResolvers = &workspaceSchema{}

func (s *workspaceSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.FuncWithCacheKey("currentWorkspace", s.currentWorkspace, dagql.CachePerCall).
			Doc("Detect and return the current workspace.").
			Experimental("Highly experimental API extracted from a more ambitious workspace implementation."),
	}.Install(srv)

	dagql.Fields[*core.Workspace]{
		dagql.NodeFuncWithCacheKey("withBranch", s.withBranch, dagql.CachePerClient).
			Doc(`Return a Workspace for the given branch. If the branch is different from`,
				`the currently checked-out branch, a git worktree is created on the host.`,
				`If the branch does not exist, it is created from the current branch tip.`).
			Args(
				dagql.Arg("branch").Doc(`The branch name (e.g. "agent/auth").`),
			),
		dagql.NodeFuncWithCacheKey("stage", DagOpWrapper(srv, s.stage), dagql.CachePerClient).
			DoNotCache("Writes to the local host.").
			Doc(`Apply a Changeset to the workspace and stage the affected paths in git.`,
				`Files are written (added/modified) and removed on disk, then precisely`,
				`the changed paths are staged via git add / git rm. Any pre-existing`,
				`unstaged user edits are preserved as unstaged changes.`,
				`Returns true if any changes were staged, false if the changeset was empty.`).
			Args(
				dagql.Arg("changes").Doc(`The changes to apply and stage.`),
				dagql.Arg("force").Doc(
					`Skip the 3-way merge for modified files and overwrite the working`,
					`tree directly. Use this to recover from merge conflicts or when`,
					`the changeset already contains the complete desired file content.`,
				).Default(dagql.Boolean(false)),
			),
		dagql.NodeFuncWithCacheKey("apply", DagOpWrapper(srv, s.apply), dagql.CachePerClient).
			DoNotCache("Writes to the local host.").
			Doc(`Export a Changeset to the workspace directory without staging in git.`,
				`Use this for files that should not be tracked by git (e.g. build`,
				`artifacts, binary files). Added and modified files are written to`,
				`disk; removed files are deleted.`,
				`Returns true if any files were changed, false if the changeset was empty.`).
			Args(
				dagql.Arg("changes").Doc(`The changes to apply.`),
			),
		dagql.NodeFuncWithCacheKey("commit", DagOpWrapper(srv, s.commit), dagql.CachePerClient).
			DoNotCache("Writes to the local host.").
			Doc(`Commit whatever is currently staged in the workspace's git index.`,
				`Returns the commit hash. Fails if there is nothing staged.`).
			Args(
				dagql.Arg("message").Doc(`The commit message.`),
			),
		dagql.NodeFuncWithCacheKey("directory",
			DagOpDirectoryWrapper(
				srv, s.directory,
				WithHashContentDir[*core.Workspace, workspaceDirectoryArgs](),
			), dagql.CachePerClient).
			Doc(`Returns a Directory from the workspace.`,
				`Relative paths resolve from the workspace directory. Absolute paths resolve from the workspace boundary.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve. Relative paths (e.g., "src") resolve from the workspace directory; absolute paths (e.g., "/src") resolve from the workspace boundary.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory.`),
			),
		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CachePerClient).
			Doc(`Returns a File from the workspace.`,
				`Relative paths resolve from the workspace directory. Absolute paths resolve from the workspace boundary.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve. Relative paths (e.g., "go.mod") resolve from the workspace directory; absolute paths (e.g., "/go.mod") resolve from the workspace boundary.`),
			),
		dagql.NodeFuncWithCacheKey("exists", s.exists, dagql.CachePerClient).
			Doc(`Check if a file or directory exists at the given path in the workspace.`).
			Args(
				dagql.Arg("path").Doc(`Path to check, relative to the workspace root (e.g., "src/main.go").`),
				dagql.Arg("expectedType").Doc(`If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").`),
				dagql.Arg("doNotFollowSymlinks").Doc(`If specified, do not follow symlinks.`),
			),
		dagql.NodeFuncWithCacheKey("glob", s.glob, dagql.CachePerClient).
			Doc(`Returns a list of files and directories that match the given pattern.`).
			Args(
				dagql.Arg("pattern").Doc(`Pattern to match (e.g., "*.md").`),
			),
		dagql.NodeFuncWithCacheKey("search", s.search, dagql.CachePerClient).
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
		dagql.NodeFuncWithCacheKey("findUp", s.findUp, dagql.CachePerClient).
			Doc(`Search for a file or directory by walking up from the start path within the workspace.`,
				`Returns the absolute workspace path if found, or null if not found.`,
				`Relative start paths resolve from the workspace directory.`,
				`The search stops at the workspace boundary and will not traverse above it.`).
			Args(
				dagql.Arg("name").Doc(`The name of the file or directory to search for.`),
				dagql.Arg("from").Doc(`Path to start the search from. Relative paths resolve from the workspace directory; absolute paths resolve from the workspace boundary.`),
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
	// TODO: push Git stuff to init time?
	// query, err := core.CurrentQuery(ctx)
	// if err != nil {
	// 	return nil, err
	// }
	// bk, err := query.Buildkit(ctx)
	// if err != nil {
	// 	return nil, fmt.Errorf("buildkit: %w", err)
	// }
	// cwd, err := bk.AbsPath(ctx, ".")
	// if err != nil {
	// 	return nil, fmt.Errorf("cwd: %w", err)
	// }

	// statFS := core.NewCallerStatFS(bk)
	// repoRoot, found, err := core.Host{}.FindUp(ctx, statFS, cwd, ".git")
	// if err != nil {
	// 	return nil, fmt.Errorf("workspace detection: %w", err)
	// }
	// if !found {
	// 	repoRoot = cwd
	// }

	// // Capture the current client ID so that when this workspace is passed to
	// // a module function, the directory/file resolvers can route host filesystem
	// // operations through the correct (original) client session.
	// clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	// if err != nil {
	// 	return nil, fmt.Errorf("client metadata: %w", err)
	// }

	// branch, err := bk.GitBranch(ctx, repoRoot)
	// if err != nil {
	// 	branch = "HEAD" // detached HEAD fallback
	// }

	// result := &core.Workspace{
	// 	Root:     repoRoot,
	// 	ClientID: clientMetadata.ClientID,
	// 	Branch:   branch,
	// 	RepoRoot: repoRoot,
	// }

	// return result, nil
	return parent.Server.CurrentWorkspace(ctx)
}

type workspaceDirectoryArgs struct {
	Path string

	core.CopyFilter

	Gitignore bool `default:"false"`

	DagOpInternalArgs
}

func (workspaceDirectoryArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerCall
}

// resolveRootfs returns a lazy directory reference for a resolved workspace path.
// Local: per-call host.directory(absPath, include, exclude) via workspace client session.
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

	ctxDir := ws.Rootfs()
	if resolvedPath != "." && resolvedPath != "" {
		err = srv.Select(ctx, ctxDir, &ctxDir,
			dagql.Selector{
				Field: "directory",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(resolvedPath)}},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("workspace directory %q: %w", resolvedPath, err)
		}
	}

	if len(filter.Include) > 0 || len(filter.Exclude) > 0 {
		withDirArgs := []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
			{Name: "directory", Value: dagql.NewID[*core.Directory](ctxDir.ID())},
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

func (s *workspaceSchema) directory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceDirectoryArgs,
) (inst dagql.ObjectResult[*core.Directory], _ error) {
	ws := parent.Self()
	resolvedPath := resolveWorkspacePath(args.Path, ws.Path)
	return s.resolveRootfs(ctx, ws, resolvedPath, args.CopyFilter, args.Gitignore)
}

type workspaceFileArgs struct {
	Path string
}

func (workspaceFileArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerCall
}

func (s *workspaceSchema) file(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceFileArgs,
) (inst dagql.Result[*core.File], _ error) {
	ws := parent.Self()

	resolvedPath := resolveWorkspacePath(args.Path, ws.Path)
	parentDir := filepath.Dir(resolvedPath)
	basename := filepath.Base(resolvedPath)

	dir, err := s.resolveRootfs(ctx, ws, parentDir, core.CopyFilter{}, false)
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

type workspaceSearchArgs struct {
	core.SearchOpts
	Paths []string `default:"[]"`
	Globs []string `default:"[]"`
}

func (workspaceSearchArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerCall
}

func (s *workspaceSchema) search(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceSearchArgs) (dagql.Array[*core.SearchResult], error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildkit: %w", err)
	}

	localResults, err := bk.SearchCallerHostPath(ctx, ws.Path, &engine.LocalSearchOpts{
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

func (workspaceGlobArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerCall
}

func (s *workspaceSchema) glob(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceGlobArgs) (dagql.Array[dagql.String], error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildkit: %w", err)
	}

	matches, err := bk.GlobCallerHostPath(ctx, ws.Path, args.Pattern)
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}

	return dagql.NewStringArray(matches...), nil
}

type workspaceExistsArgs struct {
	Path                string
	ExpectedType        dagql.Optional[core.ExistsType]
	DoNotFollowSymlinks bool `default:"false"`
}

func (workspaceExistsArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerCall
}

func (s *workspaceSchema) exists(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceExistsArgs) (dagql.Boolean, error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return false, err
	}

	absPath, err := pathutil.SandboxedRelativePath(args.Path, ws.Path)
	if err != nil {
		return false, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return false, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return false, fmt.Errorf("buildkit: %w", err)
	}

	statFS := core.NewCallerStatFS(bk)

	// Use Lstat (Stat) or follow-symlinks stat depending on the flag.
	// When checking for symlink type, always use Lstat.
	followSymlinks := !args.DoNotFollowSymlinks && args.ExpectedType.Value != core.ExistsTypeSymlink
	var stat *core.Stat
	if followSymlinks {
		_, stat, err = statFS.StatFollow(ctx, absPath)
	} else {
		_, stat, err = statFS.Stat(ctx, absPath)
	}
	if err != nil {
		// Path does not exist
		return false, nil
	}

	if args.ExpectedType.Valid {
		switch args.ExpectedType.Value {
		case core.ExistsTypeDirectory:
			return dagql.NewBoolean(stat.FileType == core.FileTypeDirectory), nil
		case core.ExistsTypeRegular:
			return dagql.NewBoolean(stat.FileType == core.FileTypeRegular), nil
		case core.ExistsTypeSymlink:
			return dagql.NewBoolean(stat.FileType == core.FileTypeSymlink), nil
		}
	}

	return true, nil
}

// resolveWorkspacePath resolves a workspace API path into a boundary-relative path:
//   - Relative paths resolve from the workspace directory (workspacePath/).
//   - Absolute paths resolve from the workspace boundary (/).
//
// Returns a path relative to the workspace boundary.
func resolveWorkspacePath(pathArg, workspacePath string) string {
	clean := filepath.Clean(pathArg)
	if filepath.IsAbs(clean) {
		// Absolute path: relative to workspace boundary (strip leading /).
		return clean[1:]
	}
	// Relative path: relative to workspace directory within boundary.
	return filepath.Join(workspacePath, clean)
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

func (workspaceFindUpArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerCall
}

func (s *workspaceSchema) findUp(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceFindUpArgs,
) (dagql.Nullable[dagql.String], error) {
	none := dagql.Null[dagql.String]()
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return none, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return none, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return none, fmt.Errorf("buildkit: %w", err)
	}

	resolvedFrom := resolveWorkspacePath(args.From, ws.Path)
	curDir := path.Clean(filepath.ToSlash(resolvedFrom))
	if curDir == "" {
		curDir = "."
	}

	var statFS core.StatFS
	pathForStat := func(candidate string) (string, error) {
		return candidate, nil
	}
	if ws.HostPath() != "" {
		statFS = core.NewCallerStatFS(bk)
		boundaryRoot := ws.HostPath()
		pathForStat = func(candidate string) (string, error) {
			return pathutil.SandboxedRelativePath(candidate, boundaryRoot)
		}
	} else {
		statFS = &core.DirectoryStatFS{Dir: ws.Rootfs()}
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

func (s *workspaceSchema) checks(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.CheckGroup, error) {
	include := workspaceIncludePatterns(args.Include)
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}
	// Build a map of toolchain module name → ignoreChecks patterns from
	// each module's toolchain config.
	ignoreChecks := toolchainIgnorePatterns(mods, func(cfg *modules.ModuleConfigDependency) []string {
		return cfg.IgnoreChecks
	})

	var allChecks []*core.Check
	for _, mod := range mods {
		checkGroup, err := mod.Checks(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("checks from module %q: %w", mod.Name(), err)
		}
		reparentWorkspaceTreeRoot(checkGroup.Node, mod.Name())
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
		// Apply ignoreChecks exclusion for this toolchain's checks.
		if exclude := ignoreChecks[mod.Name()]; len(exclude) > 0 {
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

func (s *workspaceSchema) generators(
	ctx context.Context,
	parent *core.Workspace,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.GeneratorGroup, error) {
	include := workspaceIncludePatterns(args.Include)
	mods, err := currentWorkspacePrimaryModules(ctx)
	if err != nil {
		return nil, err
	}

	moduleGenerators := make([]struct {
		mod   *core.Module
		group *core.GeneratorGroup
	}, 0, len(mods))
	generatorModuleCount := 0
	for _, mod := range mods {
		generatorGroup, err := mod.Generators(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("generators from module %q: %w", mod.Name(), err)
		}
		if len(generatorGroup.Generators) > 0 {
			generatorModuleCount++
		}
		moduleGenerators = append(moduleGenerators, struct {
			mod   *core.Module
			group *core.GeneratorGroup
		}{
			mod:   mod,
			group: generatorGroup,
		})
	}

	var allGenerators []*core.Generator
	allowSingleModuleCompat := generatorModuleCount == 1
	for _, entry := range moduleGenerators {
		reparentWorkspaceTreeRoot(entry.group.Node, entry.mod.Name())
		filtered, err := filterGeneratorsByInclude(
			ctx,
			entry.group.Generators,
			include,
			allowSingleModuleCompat,
		)
		if err != nil {
			return nil, err
		}
		allGenerators = append(allGenerators, filtered...)
	}

	return &core.GeneratorGroup{Generators: allGenerators}, nil
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

func currentWorkspacePrimaryModules(ctx context.Context) ([]*core.Module, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	served, err := query.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("current served deps: %w", err)
	}

	mods := make([]*core.Module, 0, len(served.PrimaryMods()))
	for _, mod := range served.PrimaryMods() {
		userMod, ok := mod.(*core.Module)
		if !ok {
			continue
		}
		if userMod.Name() == core.ModuleName {
			continue
		}
		mods = append(mods, userMod)
	}
	return mods, nil
}

// toolchainIgnorePatterns builds a map of toolchain module name → ignore
// patterns by scanning each module's source config for toolchain entries.
func toolchainIgnorePatterns(
	mods []*core.Module,
	getPatterns func(*modules.ModuleConfigDependency) []string,
) map[string][]string {
	result := make(map[string][]string)
	for _, mod := range mods {
		if !mod.Source.Valid {
			continue
		}
		src := mod.Source.Value.Self()
		if src == nil {
			continue
		}
		for _, cfg := range src.ConfigToolchains {
			if patterns := getPatterns(cfg); len(patterns) > 0 {
				result[cfg.Name] = patterns
			}
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

// sanitizeBranch replaces "/" with "-" for use in filesystem paths.
func sanitizeBranch(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

func (s *workspaceSchema) withBranch(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		Branch string
		Base   dagql.Optional[dagql.String]
	},
) (*core.Workspace, error) {
	ws := parent.Self()
	if ws.Branch == args.Branch {
		return ws, nil
	}

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("buildkit: %w", err)
	}

	var base string
	if args.Base.Valid {
		base = args.Base.Value.String()
	}

	worktreePath := ws.RepoRoot + "-worktrees/" + sanitizeBranch(args.Branch)
	actualPath, err := bk.GitWorktreeAdd(ctx, ws.RepoRoot, args.Branch, worktreePath, base)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	return &core.Workspace{
		Path:     actualPath,
		ClientID: ws.ClientID,
		Branch:   args.Branch,
		RepoRoot: ws.RepoRoot,
	}, nil
}

type workspaceStageArgs struct {
	Changes dagql.ID[*core.Changeset]
	Force   dagql.Boolean `default:"false"`

	RawDagOpInternalArgs
}

func (s *workspaceSchema) stage(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceStageArgs,
) (dagql.Boolean, error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return false, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}

	changeset, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return false, fmt.Errorf("load changeset: %w", err)
	}

	// Compute the changeset paths (added/modified/removed)
	paths, err := changeset.Self().ComputePaths(ctx)
	if err != nil {
		return false, fmt.Errorf("compute paths: %w", err)
	}

	// Check if empty
	if len(paths.Added) == 0 && len(paths.Modified) == 0 && len(paths.Removed) == 0 {
		return false, nil
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return false, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return false, fmt.Errorf("buildkit: %w", err)
	}

	// Step 1: Export changeset to a temp dir (not the worktree!) so we
	// never clobber user's in-progress edits.
	tempDir := ws.Path + "-dagger-stage-tmp"
	if err := changeset.Self().Export(ctx, tempDir); err != nil {
		return false, fmt.Errorf("export changeset: %w", err)
	}

	// Filter out directory entries from Added — git only tracks files,
	// and os.ReadFile on a directory path fails. Directories are created
	// implicitly by MkdirAll when writing their child files.
	var addedFiles []string
	for _, p := range paths.Added {
		if !strings.HasSuffix(p, "/") {
			addedFiles = append(addedFiles, p)
		}
	}

	// Step 2: Stage and merge. For each file:
	//  - Added: copy to worktree, write blob to index
	//  - Modified: write blob to index (agent's version), then 3-way merge
	//    into the working tree so user edits on other lines are preserved
	//  - Removed: remove from index and disk
	// The temp dir is cleaned up by the handler.
	staged, err := bk.GitStage(ctx, ws.Path, tempDir, addedFiles, paths.Modified, paths.Removed, bool(args.Force))
	if err != nil {
		return false, fmt.Errorf("stage: %w", err)
	}

	return dagql.Boolean(staged), nil
}

type workspaceApplyArgs struct {
	Changes dagql.ID[*core.Changeset]

	RawDagOpInternalArgs
}

func (s *workspaceSchema) apply(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceApplyArgs,
) (dagql.Boolean, error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return false, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}

	changeset, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return false, fmt.Errorf("load changeset: %w", err)
	}

	// Compute the changeset paths to check if empty.
	paths, err := changeset.Self().ComputePaths(ctx)
	if err != nil {
		return false, fmt.Errorf("compute paths: %w", err)
	}

	if len(paths.Added) == 0 && len(paths.Modified) == 0 && len(paths.Removed) == 0 {
		return false, nil
	}

	// Export directly to the workspace root — no git staging.
	if err := changeset.Self().Export(ctx, ws.Path); err != nil {
		return false, fmt.Errorf("apply: %w", err)
	}

	return true, nil
}

type workspaceCommitArgs struct {
	Message string

	RawDagOpInternalArgs
}

func (s *workspaceSchema) commit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCommitArgs,
) (dagql.String, error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return "", err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	hash, err := bk.GitCommit(ctx, ws.Path, args.Message)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	return dagql.String(hash), nil
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
