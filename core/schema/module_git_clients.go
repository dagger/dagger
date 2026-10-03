package schema

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// treeFileReader reads a file at a path relative to the root of a module's
// own tree. found is false when there is no regular file there.
type treeFileReader func(rel string) (data []byte, found bool, err error)

// callerDeclaresGitClient reports whether the config governing the calling
// module declares a git client at the given address. The config is the
// nearest dagger.toml between the module's directory and the root of its own
// tree; with none, nothing is declared.
func callerDeclaresGitClient(
	ctx context.Context,
	dag *dagql.Server,
	caller dagql.ObjectResult[*core.Module],
	address string,
) (bool, error) {
	if !caller.Self().Source.Valid || caller.Self().Source.Value.Self() == nil {
		return false, nil
	}
	src := caller.Self().Source.Value.Self()
	read, err := moduleTreeReader(ctx, dag, src)
	if err != nil || read == nil {
		return false, err
	}
	declared, err := declaredGitClients(ctx, src.SourceRootSubpath, read)
	if err != nil || len(declared) == 0 {
		return false, err
	}
	want := gitClientKey(address)
	return slices.ContainsFunc(declared, func(ref string) bool {
		return gitClientKey(ref) == want
	}), nil
}

// moduleTreeReader reads files from the tree a module was loaded from, the
// same tree its local clients resolve in. It is nil for a module without one.
func moduleTreeReader(ctx context.Context, dag *dagql.Server, src *core.ModuleSource) (treeFileReader, error) {
	switch src.Kind {
	case core.ModuleSourceKindGit:
		return directoryFileReader(ctx, dag, src.Git.UnfilteredContextDir), nil
	case core.ModuleSourceKindDir:
		return directoryFileReader(ctx, dag, src.DirSrc.OriginalContextDir), nil
	case core.ModuleSourceKindLocal:
		return hostFileReader(ctx, src)
	default:
		return nil, nil
	}
}

func directoryFileReader(ctx context.Context, dag *dagql.Server, tree dagql.ObjectResult[*core.Directory]) treeFileReader {
	if tree.Self() == nil {
		return nil
	}
	return func(rel string) ([]byte, bool, error) {
		var exists dagql.Boolean
		if err := dag.Select(ctx, tree, &exists, dagql.Selector{
			Field: "exists",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(rel)},
				{Name: "expectedType", Value: dagql.Opt(core.ExistsTypeRegular)},
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

func hostFileReader(ctx context.Context, src *core.ModuleSource) (treeFileReader, error) {
	root, err := src.LocalContextDirectoryPath()
	if err != nil {
		return nil, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	hostMetadata, err := query.ModuleParentHostClientMetadata(ctx)
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

// declaredGitClients reads the git clients the governing config declares for
// the module at moduleDir, a path relative to the root of its tree.
func declaredGitClients(ctx context.Context, moduleDir string, read treeFileReader) ([]string, error) {
	moduleDir = path.Clean(moduleDir)
	for dir := moduleDir; ; dir = path.Dir(dir) {
		data, found, err := read(path.Join(dir, workspace.ConfigFileName))
		if err != nil {
			return nil, err
		}
		if found {
			scope := moduleDir
			switch {
			case dir == moduleDir:
				scope = "."
			case dir != ".":
				scope = strings.TrimPrefix(moduleDir, dir+"/")
			}
			cfg, err := workspace.ParseConfigAt(ctx, data, ".")
			if err != nil {
				return nil, nil //nolint:nilerr // deliberate: a config that does not parse declares nothing
			}
			return workspace.ModuleScopeGitClients(cfg, ".")[scope], nil
		}
		if dir == "." {
			return nil, nil
		}
	}
}

// gitClientKey names the repository path, and the directory in it, that a
// git module address points at. Scheme, user, version, a "#ref" selector and
// ".git" suffixes do not change it, nor does the host's case. It reads the
// address alone: parsing it the way a load does can look the host up over the
// network.
func gitClientKey(address string) string {
	rest := address
	scheme, after, hasScheme := strings.Cut(rest, "://")
	if hasScheme && !strings.ContainsAny(scheme, "/@:") {
		rest = after
	}
	if at := strings.IndexByte(rest, '@'); at >= 0 && at < strings.IndexAny(rest+"/", "/:") {
		rest = rest[at+1:]
	}
	if colon := strings.IndexByte(rest, ':'); colon >= 0 && colon < strings.IndexAny(rest+"/", "/") {
		if port, _, _ := strings.Cut(rest[colon+1:], "/"); !isNumber(port) {
			rest = rest[:colon] + "/" + rest[colon+1:]
		}
	}
	if base, selector, ok := strings.Cut(rest, "#"); ok {
		rest = base
		if _, subdir, ok := strings.Cut(selector, ":"); ok {
			rest += "/" + subdir
		}
	} else if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		rest = rest[:at]
	}
	host, repoPath, _ := strings.Cut(rest, "/")
	segments := strings.Split(path.Clean("/"+repoPath), "/")
	for i, segment := range segments {
		segments[i] = strings.TrimSuffix(segment, ".git")
	}
	return strings.ToLower(host) + strings.TrimSuffix(strings.Join(segments, "/"), "/")
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
