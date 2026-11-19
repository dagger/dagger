package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"weak"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// ModuleDeclaredGitClients reads the git clients that the config governing a
// module declares for it, from the tree the module was loaded from, the same
// tree its local clients resolve in. It is empty for a module without one.
//
// Every call of the module's functions asks, to key the call. A git or
// directory module's tree is an immutable result, so the session reads its
// declarations once per tree; a local module's are read from its host every
// time, since the file there can change.
func ModuleDeclaredGitClients(ctx context.Context, dag *dagql.Server, src *ModuleSource) ([]string, error) {
	ws := src.Workspace
	track := src.Kind == ModuleSourceKindGit && ws.Self() != nil
	read := func() (result declaredGitClientsRead, err error) {
		reader, err := moduleTreeReader(ctx, dag, src)
		if err != nil || reader == nil {
			return result, err
		}
		if track {
			result.paths = &declaredGitClientPaths{}
			load := reader
			reader = func(rel string) ([]byte, bool, error) {
				result.paths.visited = append(result.paths.visited, rel)
				return load(rel)
			}
		}
		result.declared, err = workspace.DeclaredGitClients(ctx, src.SourceRootSubpath, reader)
		return result, err
	}
	var origin dagql.AnyResult
	if tree, ok := moduleTree(src); ok && tree.Self() != nil {
		origin = tree
	}
	var key declaredGitClientsKey
	if track {
		origin = ws
		key.workspace = weak.Make(ws.Self())
	}
	lookup := read
	if origin != nil {
		memo := currentSessionSchemaBuilderMemo(ctx)
		if id, err := origin.ID(); memo != nil && err == nil && id != nil && id.EngineResultID() != 0 {
			key.treeID = id.EngineResultID()
			key.moduleDir = src.SourceRootSubpath
			lookup = func() (declaredGitClientsRead, error) { return memo.declaredGitClients(key, read) }
		}
	}
	result, err := lookup()
	if paths := result.paths; paths != nil {
		recordWorkspaceReads(ctx, ws, func(c *workspaceInputCollector) []provenance.Selector {
			return paths.forWorkspace(c, ws.Self())
		})
	}
	return result.declared, err
}

// moduleTree is the immutable tree a git or directory module was loaded
// from.
func moduleTree(src *ModuleSource) (dagql.ObjectResult[*Directory], bool) {
	switch {
	case src.Kind == ModuleSourceKindGit && src.Git != nil:
		return src.Git.UnfilteredContextDir, true
	case src.Kind == ModuleSourceKindDir && src.DirSrc != nil:
		return src.DirSrc.OriginalContextDir, true
	default:
		return dagql.ObjectResult[*Directory]{}, false
	}
}

// moduleTreeReader is nil for a module whose tree this engine cannot reach,
// such as a local module whose host belongs to another engine.
func moduleTreeReader(ctx context.Context, dag *dagql.Server, src *ModuleSource) (workspace.TreeFileReader, error) {
	if tree, ok := moduleTree(src); ok {
		// A module loaded through a Git workspace has that workspace's tree.
		if ws := src.Workspace.Self(); ws != nil {
			if fs, ok := ws.GitSparseTreeFS(); ok {
				return func(rel string) ([]byte, bool, error) {
					return directoryFileReader(ctx, dag, fs.dirFor(ctx, rel))(rel)
				}, nil
			}
			if src.Kind == ModuleSourceKindGit && tree.Self() == nil {
				if _, mounted := ws.MountsDir(); !mounted {
					tree, _ = ws.SourceDirectory()
				}
				if tree.Self() == nil {
					var err error
					tree, err = NewModuleSourceFS(nil, src).ContextDirectory(ctx, CopyFilter{})
					if err != nil {
						return nil, err
					}
				}
			}
		}
		return directoryFileReader(ctx, dag, tree), nil
	}
	if src.Kind == ModuleSourceKindLocal && src.Local != nil && !src.Local.Foreign {
		return hostFileReader(ctx, src)
	}
	return nil, nil
}

type declaredGitClientsKey struct {
	// Rows can be republished in place. Retain no workspace payload for the memo.
	workspace weak.Pointer[Workspace]
	treeID    uint64
	moduleDir string
}

type declaredGitClientsRead struct {
	declared []string
	paths    *declaredGitClientPaths
}

// Keep the path proof with the immutable workspace lookup. A later caller
// still records the selectors against its own attached workspace producer.
type declaredGitClientPaths struct {
	visited   []string
	once      sync.Once
	selectors []provenance.Selector
}

func (paths *declaredGitClientPaths) forWorkspace(c *workspaceInputCollector, ws *Workspace) []provenance.Selector {
	paths.once.Do(func() {
		for _, rel := range paths.visited {
			selector := provenance.Selector{Path: "/" + rel, Kind: provenance.WorkspaceRead, Origin: "ModuleSource.declaredGitClients"}
			if _, mounted := ws.MountsDir(); mounted {
				selector.Path = "/"
				selector.Conservative = true
			} else if sparse, ok := ws.GitSparseTree(); ok {
				// The lookup used dirFor. Its manifest proves both regular
				// files and missing candidates without mounting the full tree.
				covered, err := sparse.coversPath(c.ctx, rel)
				if err != nil || !covered {
					selector.Path = "/"
					selector.Conservative = true
				}
			} else {
				selector = c.workspaceReadSelector(ws, selector)
			}
			paths.selectors = append(paths.selectors, selector)
		}
	})
	return paths.selectors
}

type declaredGitClientsEntry struct {
	mu     sync.Mutex
	result declaredGitClientsRead
	loaded bool
}

// declaredGitClients returns the declarations read for key, reading them at
// most once while reads succeed. A failed read is retried by the next call.
func (m *SchemaBuilderMemo) declaredGitClients(key declaredGitClientsKey, read func() (declaredGitClientsRead, error)) (declaredGitClientsRead, error) {
	m.mu.Lock()
	if m.gitClients == nil {
		m.gitClients = make(map[declaredGitClientsKey]*declaredGitClientsEntry)
	}
	entry := m.gitClients[key]
	if entry == nil {
		entry = &declaredGitClientsEntry{}
		m.gitClients[key] = entry
	}
	m.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.loaded {
		return entry.result, nil
	}
	declared, err := read()
	if err == nil {
		entry.result, entry.loaded = declared, true
	}
	return declared, err
}

func directoryFileReader(ctx context.Context, dag *dagql.Server, tree dagql.ObjectResult[*Directory]) workspace.TreeFileReader {
	if tree.Self() == nil {
		return nil
	}
	return func(rel string) ([]byte, bool, error) {
		var exists dagql.Boolean
		if err := dag.Select(ctx, tree, &exists, dagql.Selector{
			Field: "exists",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(rel)},
				{Name: "expectedType", Value: dagql.Opt(ExistsTypeRegular)},
			},
		}); err != nil || !exists {
			return nil, false, err
		}
		var contents dagql.String
		if err := dag.Select(ctx, tree, &contents,
			dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(rel)}}},
			dagql.Selector{Field: "contents"},
		); err != nil {
			return nil, false, err
		}
		return []byte(contents), true, nil
	}
}

func hostFileReader(ctx context.Context, src *ModuleSource) (workspace.TreeFileReader, error) {
	root, err := src.LocalContextDirectoryPath()
	if err != nil {
		return nil, err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	hostMetadata, err := LocalSourceHostClientMetadata(ctx, query, src)
	if errors.Is(err, ErrNoCurrentModule) {
		// A call's cache key is computed in its caller's client, which, with
		// no module above it, is itself the host the module was loaded from.
		hostMetadata, err = query.NonModuleParentClientMetadata(ctx)
	}
	if err != nil {
		return nil, err
	}
	hostCtx := engine.ContextWithClientMetadata(ctx, hostMetadata)
	bk, err := query.Engine(hostCtx)
	if err != nil {
		return nil, err
	}
	return func(rel string) ([]byte, bool, error) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		stat, err := bk.StatCallerHostPath(hostCtx, abs, false)
		if status.Code(err) == codes.NotFound {
			return nil, false, nil
		}
		if err != nil || stat.IsDir() {
			return nil, false, err
		}
		data, err := bk.ReadCallerHostFile(hostCtx, abs)
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	}, nil
}
