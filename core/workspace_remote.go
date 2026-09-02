package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

// WorkspaceRemoteRef is a git ref that denotes a workspace: a clone ref, an
// optional version, and the subdirectory the workspace root sits in.
type WorkspaceRemoteRef struct {
	CloneRef        string
	Version         string
	WorkspaceSubdir string
}

// ParseWorkspaceRemoteRef parses a remote workspace ref, in either the
// fragment form (`host/repo#ref:subdir`) or the legacy `@ref` form.
func ParseWorkspaceRemoteRef(ctx context.Context, remoteRef string) (WorkspaceRemoteRef, error) {
	// Fragment refs are parsed via the same git URL parser used by Address.*.
	if strings.Contains(remoteRef, "#") {
		gitURL, err := gitutil.ParseURL(remoteRef)
		if err != nil {
			return WorkspaceRemoteRef{}, err
		}
		version := ""
		subdir := "."
		if gitURL.Fragment != nil {
			version = gitURL.Fragment.Ref
			subdir = gitURL.Fragment.Subdir
		}
		workspaceSubdir, err := NormalizeWorkspaceRemoteSubdir(subdir)
		if err != nil {
			return WorkspaceRemoteRef{}, fmt.Errorf("invalid git subdir in workspace ref %q: %w", remoteRef, err)
		}
		return WorkspaceRemoteRef{
			CloneRef:        gitURL.Remote(),
			Version:         version,
			WorkspaceSubdir: workspaceSubdir,
		}, nil
	}

	// Preserve legacy @ref parsing semantics for existing workspace refs.
	parsedRef, err := ParseGitRefString(ctx, remoteRef)
	if err != nil {
		return WorkspaceRemoteRef{}, err
	}
	workspaceSubdir := "."
	if parsedRef.RepoRootSubdir != "/" && parsedRef.RepoRootSubdir != "." {
		workspaceSubdir = parsedRef.RepoRootSubdir
	}
	return WorkspaceRemoteRef{
		CloneRef:        parsedRef.SourceCloneRef,
		Version:         parsedRef.ModVersion,
		WorkspaceSubdir: workspaceSubdir,
	}, nil
}

// NormalizeWorkspaceRemoteSubdir cleans a workspace subdirectory and refuses
// one that escapes the repository.
func NormalizeWorkspaceRemoteSubdir(subdir string) (string, error) {
	if subdir == "" {
		return ".", nil
	}
	subdir = filepath.Clean(subdir)
	subdir = strings.TrimPrefix(subdir, string(filepath.Separator))
	if subdir == "" || subdir == "." {
		return ".", nil
	}
	if !filepath.IsLocal(subdir) {
		return "", fmt.Errorf("path points outside repository: %q", subdir)
	}
	return subdir, nil
}

// CloneWorkspaceGitTree resolves a clone ref at an optional version and
// returns its tree. Going through the ordinary git(url).head / .ref(name)
// selectors is what records the resolved commit in the workspace lockfile.
func CloneWorkspaceGitTree(
	ctx context.Context,
	dag *dagql.Server,
	cloneRef string,
	version string,
) (dagql.ObjectResult[*Directory], dagql.ObjectResult[*GitRef], error) {
	// Build the ref selector — use "head" if no version specified.
	refSelector := dagql.Selector{Field: "head"}
	if version != "" {
		refSelector = dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(version)}},
		}
	}

	var gitRef dagql.ObjectResult[*GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(cloneRef)},
			},
		},
		refSelector,
	)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, gitRef, fmt.Errorf("resolving repo ref: %w", err)
	}

	var tree dagql.ObjectResult[*Directory]
	err = dag.Select(ctx, gitRef, &tree,
		dagql.Selector{
			Field: "tree",
			Args: []dagql.NamedInput{
				{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
			},
		},
	)
	if err != nil {
		return tree, gitRef, fmt.Errorf("cloning repo: %w", err)
	}
	return tree, gitRef, nil
}
