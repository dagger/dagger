package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// WorkspaceRemoteRef is a git ref that denotes a workspace: a clone ref, an
// optional version, the subdirectory the workspace root sits in, and which
// selector syntax named it.
type WorkspaceRemoteRef struct {
	CloneRef        string
	Version         string
	WorkspaceSubdir string
	Selector        gitref.SelectorType
}

// ParseWorkspaceRemoteRef parses a remote workspace ref, in either the
// explicit `#` selector form or the Go-like `@` form, following a dagger-get
// redirect when the host publishes one.
//
// Both the `-W <ref>` selection path and an [[include]] whose source is a git
// ref come through here, so a vanity ref means the same thing in a dagger.toml
// as it does on the command line.
func ParseWorkspaceRemoteRef(ctx context.Context, remoteRef string) (WorkspaceRemoteRef, error) {
	return ParseWorkspaceRemoteRefWithResolver(ctx, remoteRef, ResolveDaggerGetRedirect)
}

// ParseWorkspaceRemoteRefWithResolver is ParseWorkspaceRemoteRef with the
// redirect lookup injected, so tests can parse without reaching the network.
func ParseWorkspaceRemoteRefWithResolver(
	ctx context.Context,
	remoteRef string,
	resolve func(context.Context, string) (string, error),
) (WorkspaceRemoteRef, error) {
	// A # selector is the explicit Git URL form: protocol://repo#ref:subpath.
	if strings.Contains(remoteRef, "#") {
		parsedRef, err := ParseGitRefString(ctx, remoteRef)
		if err != nil {
			return WorkspaceRemoteRef{}, err
		}
		workspaceSubdir, err := NormalizeWorkspaceRemoteSubdir(parsedRef.RepoRootSubdir)
		if err != nil {
			return WorkspaceRemoteRef{}, fmt.Errorf("invalid git subdir in workspace ref %q: %w", remoteRef, err)
		}
		cloneRef := parsedRef.SourceCloneRef
		resolvedRef, err := resolve(ctx, cloneRef)
		if err != nil {
			return WorkspaceRemoteRef{}, err
		}
		if resolvedRef != cloneRef {
			// A redirect may point into a subdirectory of the repository it
			// names, so the fragment's own subdir hangs off that one.
			redirected, err := ParseGitRefString(ctx, resolvedRef)
			if err != nil {
				return WorkspaceRemoteRef{}, err
			}
			cloneRef = redirected.SourceCloneRef
			resolvedSubdir := redirected.RepoRootSubdir
			if resolvedSubdir == "/" {
				resolvedSubdir = "."
			}
			workspaceSubdir, err = NormalizeWorkspaceRemoteSubdir(filepath.Join(resolvedSubdir, workspaceSubdir))
			if err != nil {
				return WorkspaceRemoteRef{}, err
			}
		}
		return WorkspaceRemoteRef{
			CloneRef:        cloneRef,
			Version:         parsedRef.ModVersion,
			WorkspaceSubdir: workspaceSubdir,
			Selector:        parsedRef.Selector,
		}, nil
	}

	// The @ selector uses Go-like import path semantics, with any path after the
	// repository root identifying the workspace subdirectory.
	remoteRef, err := resolve(ctx, remoteRef)
	if err != nil {
		return WorkspaceRemoteRef{}, err
	}
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
		Selector:        parsedRef.Selector,
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

// workspaceGitRefSelector picks how a remote workspace ref resolves. HEAD
// without a selector and literal ref resolution, unless an @ selector carries a
// SemVer query this API version supports.
func workspaceGitRefSelector(remote WorkspaceRemoteRef, supportsVersionQueries bool) dagql.Selector {
	refSelector := dagql.Selector{Field: "head"}
	if remote.Version == "" {
		return refSelector
	}
	refSelector = dagql.Selector{
		Field: "ref",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(remote.Version)}},
	}
	if remote.Selector != gitref.ModuleVersionSelector ||
		!supportsVersionQueries ||
		!IsReleaseVersionQuery(remote.Version) {
		return refSelector
	}
	refSelector = dagql.Selector{
		Field: "latest",
		Args: []dagql.NamedInput{{
			Name:  "version",
			Value: dagql.String(remote.Version),
		}},
	}
	if remote.WorkspaceSubdir != "." {
		refSelector.Args = append(refSelector.Args, dagql.NamedInput{
			Name:  "tagPrefix",
			Value: dagql.String(remote.WorkspaceSubdir),
		})
	}
	return refSelector
}

// CloneWorkspaceGitTree resolves a remote workspace ref and returns its tree.
// Going through the ordinary git(url).head / .ref(name) / .latest(version)
// selectors is what records the resolved commit in the workspace lockfile.
func CloneWorkspaceGitTree(
	ctx context.Context,
	dag *dagql.Server,
	remote WorkspaceRemoteRef,
) (dagql.ObjectResult[*Directory], dagql.ObjectResult[*GitRef], error) {
	supportsVersionQueries := AfterVersion(workspace.VersionQueriesVersion).Contains(dag.View)

	var gitRef dagql.ObjectResult[*GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(remote.CloneRef)},
			},
		},
		workspaceGitRefSelector(remote, supportsVersionQueries),
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
