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
		dagql.NodeFuncWithCacheKey("directory",
			DagOpDirectoryWrapper(
				srv, s.directory,
				WithHashContentDir[*core.Workspace, workspaceDirectoryArgs](),
			), dagql.CachePerClient).
			Doc(`Returns a Directory from the workspace.`,
				`Path must be absolute in host/repo context.`,
				`By default, paths outside the workspace access boundary are rejected.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve. Must be an absolute path.`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory.`),
			),
		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CachePerClient).
			Doc(`Returns a File from the workspace.`,
				`Path must be absolute in host/repo context.`,
				`By default, paths outside the workspace access boundary are rejected.`).
			Args(
				dagql.Arg("path").Doc(`Absolute location of the file to retrieve in host/repo context.`),
			),
		dagql.NodeFuncWithCacheKey("findUp", s.findUp, dagql.CachePerClient).
			Doc(`Search for a file or directory by walking up from the start path within the workspace.`,
				`Returns the absolute path if found, or null if not found.`,
				`The search stops at the workspace access boundary and will not traverse above it.`).
			Args(
				dagql.Arg("name").Doc(`The name of the file or directory to search for.`),
				dagql.Arg("from").Doc(`Absolute path to start the search from in host/repo context.`),
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
	cwd = normalizeWorkspacePath(cwd)

	statFS := core.NewCallerStatFS(bk)
	boundaryRoot, found, err := core.Host{}.FindUp(ctx, statFS, cwd, ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}
	if !found {
		boundaryRoot = cwd
	}
	boundaryRoot = normalizeWorkspacePath(boundaryRoot)

	// Capture the current client ID so that when this workspace is passed to
	// a module function, the directory/file resolvers can route host filesystem
	// operations through the correct (original) client session.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}

	result := &core.Workspace{
		Root:     boundaryRoot,
		ClientID: clientMetadata.ClientID,
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

func normalizeWorkspacePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "" {
		return "."
	}
	return p
}

func isAbsoluteWorkspacePath(p string) bool {
	p = normalizeWorkspacePath(p)
	return pathutil.GetDrive(p) != "" || strings.HasPrefix(p, "/")
}

func requireAbsoluteWorkspacePath(name, userPath string) (string, error) {
	clean := normalizeWorkspacePath(userPath)
	if !isAbsoluteWorkspacePath(clean) {
		return "", fmt.Errorf("%s %q must be absolute", name, userPath)
	}
	return clean, nil
}

func isPathWithinPrefix(targetPath, prefix string) bool {
	targetPath = normalizeWorkspacePath(targetPath)
	prefix = normalizeWorkspacePath(prefix)

	targetDrive := pathutil.GetDrive(targetPath)
	prefixDrive := pathutil.GetDrive(prefix)
	if targetDrive != "" && prefixDrive != "" && targetDrive != prefixDrive {
		return false
	}

	if targetPath == prefix {
		return true
	}
	if prefix == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	return strings.HasPrefix(targetPath, prefix+"/")
}

func workspaceAccessBoundary(ws *core.Workspace) string {
	return normalizeWorkspacePath(ws.Root)
}

func (s *workspaceSchema) enforceAccessBoundary(ws *core.Workspace, absPath string) error {
	accessBoundary := workspaceAccessBoundary(ws)
	if accessBoundary == "" || accessBoundary == "." {
		return nil
	}

	if isPathWithinPrefix(absPath, accessBoundary) {
		return nil
	}

	return fmt.Errorf("path %q is outside workspace access boundary %q", absPath, accessBoundary)
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

	absPath, err := requireAbsoluteWorkspacePath("path", args.Path)
	if err != nil {
		return inst, err
	}
	if err := s.enforceAccessBoundary(ws, absPath); err != nil {
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
		gitIgnoreRoot := workspaceAccessBoundary(ws)
		dirArgs = append(dirArgs,
			dagql.NamedInput{Name: "gitignore", Value: dagql.NewBoolean(true)},
			dagql.NamedInput{Name: "gitIgnoreRoot", Value: dagql.NewString(gitIgnoreRoot)},
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

	absPath, err := requireAbsoluteWorkspacePath("path", args.Path)
	if err != nil {
		return inst, err
	}
	if err := s.enforceAccessBoundary(ws, absPath); err != nil {
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

type workspaceFindUpArgs struct {
	Name string
	From string
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

	absStart, err := requireAbsoluteWorkspacePath("from", args.From)
	if err != nil {
		return none, err
	}
	if err := s.enforceAccessBoundary(ws, absStart); err != nil {
		return none, err
	}

	statFS := core.NewCallerStatFS(bk)
	cleanAccessBoundary := path.Clean(workspaceAccessBoundary(ws))

	// Walk up from absStart, stopping at workspace access boundary.
	curDir := absStart
	for {
		candidate := path.Join(curDir, args.Name)
		_, _, err := statFS.Stat(ctx, candidate)
		if err == nil {
			return dagql.NonNull(dagql.NewString(normalizeWorkspacePath(candidate))), nil
		}

		// Stop at workspace access boundary.
		if path.Clean(curDir) == cleanAccessBoundary {
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
