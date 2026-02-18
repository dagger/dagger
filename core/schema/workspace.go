package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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
				`Path is relative to workspace root. Use "." for the root directory.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to retrieve, relative to the workspace root (e.g., "src", ".").`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
			),
		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CachePerClient).
			Doc(`Returns a File from the workspace.`,
				`Path is relative to workspace root.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve, relative to the workspace root (e.g., "go.mod").`),
			),
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
	ws, err := workspace.Detect(ctx, statFS, bk.ReadCallerHostFile, cwd)
	if err != nil {
		if args.SkipMigrationCheck && strings.Contains(err.Error(), "migration") {
			// Fall through — install/init can work in legacy projects
			ws = &workspace.Workspace{Root: cwd}
		} else {
			return nil, fmt.Errorf("workspace detection: %w", err)
		}
	}

	// Capture the current client ID so that when this workspace is passed to
	// a module function, the directory/file resolvers can route host filesystem
	// operations through the correct (original) client session.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}

	result := &core.Workspace{
		Root:      ws.Root,
		HasConfig: ws.Config != nil,
		ClientID:  clientMetadata.ClientID,
	}
	if ws.Config != nil {
		result.ConfigPath = filepath.Join(ws.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)
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

	absPath, err := resolveWorkspacePath(args.Path, ws.Root)
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

	absPath, err := resolveWorkspacePath(args.Path, ws.Root)
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
	absStart, err := resolveWorkspacePath(args.From, ws.Root)
	if err != nil {
		return none, err
	}

	statFS := core.NewCallerStatFS(bk)
	cleanRoot := filepath.Clean(ws.Root)

	// Walk up from absStart, stopping at workspace root
	curDir := absStart
	for {
		candidate := filepath.Join(curDir, args.Name)
		_, _, err := statFS.Stat(ctx, candidate)
		if err == nil {
			// Found it — return path relative to workspace root
			relPath, err := filepath.Rel(cleanRoot, candidate)
			if err != nil {
				return none, fmt.Errorf("compute relative path: %w", err)
			}
			return dagql.NonNull(dagql.NewString(relPath)), nil
		}

		// Stop at workspace root
		if filepath.Clean(curDir) == cleanRoot {
			break
		}

		nextDir := filepath.Dir(curDir)
		if nextDir == curDir {
			// hit filesystem root (shouldn't happen since we check workspace root first)
			break
		}
		curDir = nextDir
	}

	return none, nil
}

// resolveWorkspacePath resolves a path relative to the workspace root.
// Absolute paths are treated as relative to the root (leading "/" is stripped).
// Returns an error if the resolved path escapes the workspace root via "..".
func resolveWorkspacePath(path, root string) (string, error) {
	// Treat absolute paths as relative to workspace root.
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		clean = clean[1:] // strip leading "/"
	}
	resolved := filepath.Join(root, clean)
	// Ensure the resolved path stays inside root.
	rootPrefix := filepath.Clean(root) + string(filepath.Separator)
	if resolved != filepath.Clean(root) && !strings.HasPrefix(resolved, rootPrefix) {
		return "", fmt.Errorf("path %q resolves outside workspace root %q", path, root)
	}
	return resolved, nil
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
