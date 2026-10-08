package core

import (
	"context"
	"errors"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// ModuleDeclaredGitClients reads the git clients that the config governing a
// module declares for it, from the tree the module was loaded from, the same
// tree its local clients resolve in. It is empty for a module without one.
func ModuleDeclaredGitClients(ctx context.Context, dag *dagql.Server, src *ModuleSource) ([]string, error) {
	read, err := moduleTreeReader(ctx, dag, src)
	if err != nil || read == nil {
		return nil, err
	}
	return workspace.DeclaredGitClients(ctx, src.SourceRootSubpath, read)
}

// moduleTreeReader is nil for a module whose tree this engine cannot reach,
// such as a local module whose host belongs to another engine.
func moduleTreeReader(ctx context.Context, dag *dagql.Server, src *ModuleSource) (workspace.TreeFileReader, error) {
	switch {
	case src.Kind == ModuleSourceKindGit && src.Git != nil:
		return directoryFileReader(ctx, dag, src.Git.UnfilteredContextDir), nil
	case src.Kind == ModuleSourceKindDir && src.DirSrc != nil:
		return directoryFileReader(ctx, dag, src.DirSrc.OriginalContextDir), nil
	case src.Kind == ModuleSourceKindLocal && src.Local != nil && !src.Local.Foreign:
		return hostFileReader(ctx, src)
	default:
		return nil, nil
	}
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
