package schema

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/core"
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
			Experimental("Highly experimental API extracted from a more ambitious workspace implementation.").
			Args(
				dagql.Arg("skipMigrationCheck").Doc("If true, skip legacy dagger.json migration checks."),
			),
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
				`Path is relative to workspace root. Use "." for the root directory.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve, relative to the workspace root (e.g., "src", ".").`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory.`),
			),
		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CachePerClient).
			Doc(`Returns a File from the workspace.`,
				`Path is relative to workspace root.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve, relative to the workspace root (e.g., "go.mod").`),
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
				`Returns the path relative to the workspace root if found, or null if not found.`,
				`The search stops at the workspace root and will not traverse above it.`).
			Args(
				dagql.Arg("name").Doc(`The name of the file or directory to search for.`),
				dagql.Arg("from").Doc(`Path to start the search from, relative to the workspace root.`),
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
	repoRoot, found, err := core.Host{}.FindUp(ctx, statFS, cwd, ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}
	if !found {
		repoRoot = cwd
	}

	// Capture the current client ID so that when this workspace is passed to
	// a module function, the directory/file resolvers can route host filesystem
	// operations through the correct (original) client session.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}

	branch, err := bk.GitBranch(ctx, repoRoot)
	if err != nil {
		branch = "HEAD" // detached HEAD fallback
	}

	result := &core.Workspace{
		Root:     repoRoot,
		ClientID: clientMetadata.ClientID,
		Branch:   branch,
		RepoRoot: repoRoot,
	}

	return result, nil
}

type workspaceDirectoryArgs struct {
	Path string

	core.CopyFilter

	Gitignore bool `default:"false"`

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

	absPath, err := pathutil.SandboxedRelativePath(args.Path, ws.Root)
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
	if args.Gitignore {
		dirArgs = append(dirArgs,
			dagql.NamedInput{Name: "gitignore", Value: dagql.NewBoolean(true)},
			// The workspace root is already the repo root, so pass it
			// directly to avoid a redundant .git search.
			dagql.NamedInput{Name: "gitIgnoreRoot", Value: dagql.NewString(ws.Root)},
		)
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

	absPath, err := pathutil.SandboxedRelativePath(args.Path, ws.Root)
	if err != nil {
		return inst, err
	}
	fileDir, fileName := path.Split(absPath)

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

type workspaceSearchArgs struct {
	core.SearchOpts
	Paths []string `default:"[]"`
	Globs []string `default:"[]"`
}

func (workspaceSearchArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerClient
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

	localResults, err := bk.SearchCallerHostPath(ctx, ws.Root, &engine.LocalSearchOpts{
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
	return dagql.CacheTypePerClient
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

	matches, err := bk.GlobCallerHostPath(ctx, ws.Root, args.Pattern)
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
	return dagql.CacheTypePerClient
}

func (s *workspaceSchema) exists(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceExistsArgs) (dagql.Boolean, error) {
	ws := parent.Self()

	ctx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return false, err
	}

	absPath, err := pathutil.SandboxedRelativePath(args.Path, ws.Root)
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

type workspaceFindUpArgs struct {
	Name string
	From string `default:"."`
}

func (workspaceFindUpArgs) CacheType() dagql.CacheControlType {
	return dagql.CacheTypePerClient
}

func (s *workspaceSchema) findUp(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceFindUpArgs) (dagql.Nullable[dagql.String], error) {
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

	// Resolve start path relative to workspace root
	absStart, err := pathutil.SandboxedRelativePath(args.From, ws.Root)
	if err != nil {
		return none, err
	}

	statFS := core.NewCallerStatFS(bk)
	cleanRoot := path.Clean(ws.Root)

	// Walk up from absStart, stopping at workspace root
	curDir := absStart
	for {
		candidate := path.Join(curDir, args.Name)
		_, _, err := statFS.Stat(ctx, candidate)
		if err == nil {
			// Found it — return path relative to workspace root
			relPath, err := pathutil.LexicalRelativePath(cleanRoot, candidate)
			if err != nil {
				return none, fmt.Errorf("compute relative path: %w", err)
			}
			return dagql.NonNull(dagql.NewString(relPath)), nil
		}

		// Stop at workspace root
		if path.Clean(curDir) == cleanRoot {
			break
		}

		nextDir := path.Dir(curDir)
		if nextDir == curDir {
			// hit filesystem root (shouldn't happen since we check workspace root first)
			break
		}
		curDir = nextDir
	}

	return none, nil
}

// sanitizeBranch replaces "/" with "-" for use in filesystem paths.
func sanitizeBranch(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

func (s *workspaceSchema) withBranch(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct{ Branch string },
) (*core.Workspace, error) {
	ws := parent.Self()
	if ws.Branch == args.Branch {
		return ws, nil
	}

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

	worktreePath := ws.RepoRoot + "-worktrees/" + sanitizeBranch(args.Branch)
	actualPath, err := bk.GitWorktreeAdd(ctx, ws.RepoRoot, args.Branch, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	return &core.Workspace{
		Root:     actualPath,
		ClientID: ws.ClientID,
		Branch:   args.Branch,
		RepoRoot: ws.RepoRoot,
	}, nil
}

type workspaceStageArgs struct {
	Changes dagql.ID[*core.Changeset]

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
	tempDir := ws.Root + "-dagger-stage-tmp"
	if err := changeset.Self().Export(ctx, tempDir); err != nil {
		return false, fmt.Errorf("export changeset: %w", err)
	}

	// Step 2: Stage and merge. For each file:
	//  - Added: copy to worktree, write blob to index
	//  - Modified: write blob to index (agent's version), then 3-way merge
	//    into the working tree so user edits on other lines are preserved
	//  - Removed: remove from index and disk
	// The temp dir is cleaned up by the handler.
	staged, err := bk.GitStage(ctx, ws.Root, tempDir, paths.Added, paths.Modified, paths.Removed)
	if err != nil {
		return false, fmt.Errorf("stage: %w", err)
	}

	return dagql.Boolean(staged), nil
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

	hash, err := bk.GitCommit(ctx, ws.Root, args.Message)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	return dagql.String(hash), nil
}

// withWorkspaceClientContext overrides the client metadata in context to the
// workspace's owning client ID. This ensures host filesystem operations route
// through the correct client session, even when called from a module context.
func (s *workspaceSchema) withWorkspaceClientContext(ctx context.Context, ws *core.Workspace) (context.Context, error) {
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
