package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	read := func() ([]string, error) {
		reader, err := moduleTreeReader(ctx, dag, src)
		if err != nil || reader == nil {
			return nil, err
		}
		return workspace.DeclaredGitClients(ctx, src.SourceRootSubpath, reader)
	}
	tree, ok := moduleTree(src)
	if !ok {
		return read()
	}
	memo := currentSessionSchemaBuilderMemo(ctx)
	id, err := tree.ID()
	if memo == nil || err != nil {
		return read()
	}
	// The tree's result is new whenever a file in it changes, dagger.toml
	// included, and result IDs are never reused.
	return memo.declaredGitClients(declaredGitClientsKey{treeID: id.EngineResultID(), moduleDir: src.SourceRootSubpath}, read)
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
		}
		return directoryFileReader(ctx, dag, tree), nil
	}
	if src.Kind == ModuleSourceKindLocal && src.Local != nil && !src.Local.Foreign {
		return hostFileReader(ctx, src)
	}
	return nil, nil
}

type declaredGitClientsKey struct {
	treeID    uint64
	moduleDir string
}

type declaredGitClientsEntry struct {
	mu       sync.Mutex
	declared []string
	loaded   bool
}

// declaredGitClients returns the declarations read for key, reading them at
// most once while reads succeed. A failed read is retried by the next call.
func (m *SchemaBuilderMemo) declaredGitClients(key declaredGitClientsKey, read func() ([]string, error)) ([]string, error) {
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
		return entry.declared, nil
	}
	declared, err := read()
	if err == nil {
		entry.declared, entry.loaded = declared, true
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
	hostMetadata, err := query.ModuleParentHostClientMetadata(ctx)
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
