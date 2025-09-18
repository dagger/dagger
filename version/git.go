package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/version/internal/dagger"
)

// GitHelper is an opinionated helper for performing various commands on our dagger repo.
type GitHelper struct {
	Container *dagger.Container // +private
	Valid     bool              // +private
}

func git(ctx context.Context, gitDir *dagger.Directory, dir *dagger.Directory) (*dagger.GitRepository, *GitHelper, error) {
	ctr := dag.Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{"git"}}).
		WithWorkdir("/src")

	if dir != nil {
		ctr = ctr.WithDirectory(".", dir)
	}

	if gitDir != nil {
		ctr = ctr.WithDirectory(".", gitDir)
	}

	valid := false
	entries, err := ctr.Directory(".").Entries(ctx)
	if err != nil {
		return nil, nil, err
	}
	if slices.Contains(entries, ".git/") {
		valid = true
	}

	if valid {
		// enter detached head state, then we can rewrite all our refs however we like later
		// ctr = ctr.WithExec([]string{"sh", "-c", "git checkout -q $(git rev-parse HEAD)"})

		// manually add a remote (since .git/config was removed earlier)
		ctr = ctr.WithExec([]string{"git", "remote", "add", "origin", "https://github.com/dagger/dagger.git"})

		// do various unshallowing operations (only the bare minimum is
		// provided by the core git functions which are used by our remote git
		// module sources)
		maxDepth := "2147483647" // see https://git-scm.com/docs/shallow
		ctr = ctr.
			WithExec([]string{
				"git", "fetch",
				// force so that local tags get overridden if they were wrong
				"--force",
				// we need all the tags, so we can find all the release tags later
				"--tags",
				// we need the unshallowed history of our branches, so we
				// can determine which tags are in it later
				"--depth=" + maxDepth,
				"origin",
				// update HEAD
				"HEAD",
				// update main
				"refs/heads/main:refs/heads/main",
			})
	}

	return ctr.Directory(".").AsGit(), &GitHelper{
		Container: ctr,
		Valid:     valid,
	}, nil
}

type VersionTag struct {
	// The raw tag
	Tag string

	// The component this belongs to.
	Component string
	// The semver version for this component.
	Version string
	// The commit hash.
	Commit string

	// The creator date.
	// Distinct from *author* date, and not to be confused with the underlying commit date.
	Date string
}

// VersionTagLatest gets the latest version tag for a given component
func (git *GitHelper) VersionTagLatest(
	ctx context.Context,

	// Optional component tag prefix
	component string, // +optional
	// Optional commit sha to get tags for
	commit string, // +optional
) (*VersionTag, error) {
	versions, err := git.VersionTags(ctx, component, commit)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, nil
	}
	version := versions[len(versions)-1]
	return &version, nil
}

// VersionTags gets all version tags for a given component - the resulting
// versions are sorted in ascending order
func (git *GitHelper) VersionTags(
	ctx context.Context,

	// Optional component tag prefix
	component string, // +optional
	// Optional commit sha to get tags for
	commit string, // +optional
) ([]VersionTag, error) {
	if !git.Valid {
		return nil, nil
	}

	component = strings.TrimSuffix(component, "/")
	componentFilter := "v*"
	if component != "" {
		componentFilter = component + "/" + componentFilter
	}

	tagsArgs := []string{
		"git",
		"tag",
		// filter to only the desired component
		"-l", componentFilter,
		// filter to reachable commits from HEAD
		"--merged=HEAD",
		// format as "<tag> <commit> <date>"
		"--format", "%(refname:lstrip=2) %(objectname) %(creatordate:iso-strict)",
		// sort by ascending semver
		"--sort", "version:refname",
	}
	if commit != "" {
		// filter to specified commit
		tagsArgs = append(tagsArgs, "--points-at", commit)
	}
	tagsRaw, err := git.Container.WithExec(tagsArgs).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	var versionTags []VersionTag
	for _, line := range strings.Split(tagsRaw, "\n") {
		parts := strings.Split(line, " ")
		if len(parts) != 3 {
			continue
		}
		tag, tagCommit, date := parts[0], parts[1], parts[2]
		version := strings.TrimPrefix(tag, component+"/")
		if !semver.IsValid(version) {
			continue
		}

		versionTags = append(versionTags, VersionTag{
			Tag:       tag,
			Component: component,
			Version:   version,
			Date:      date,
			Commit:    tagCommit,
		})
	}

	return versionTags, nil
}

type Commit struct {
	// The commit hash.
	Commit string

	// The commit commit date.
	// Distinct from the *author* date.
	Date string
}

func (git *GitHelper) Head(ctx context.Context) (*Commit, error) {
	return git.Commit(ctx, "HEAD")
}

func (git *GitHelper) Commit(ctx context.Context, ref string) (*Commit, error) {
	if !git.Valid {
		return nil, nil
	}

	raw, err := git.Container.
		WithExec([]string{
			"git",
			"show",
			// skip the pretty output
			"-s",
			// format as "<commit> <date>"
			"--format=%H %cI",
			ref,
		}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)

	commit, date, _ := strings.Cut(raw, " ")
	return &Commit{
		Commit: commit,
		Date:   date,
	}, nil
}

func (git *GitHelper) MergeBase(ctx context.Context, ref string, ref2 string) (*Commit, error) {
	if !git.Valid {
		return nil, nil
	}

	raw, err := git.Container.
		WithExec([]string{
			"git",
			"merge-base",
			ref, ref2,
		}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)

	return git.Commit(ctx, raw)
}

// Return whether the current git state is dirty
func (git *GitHelper) Dirty(ctx context.Context) (bool, error) {
	status, err := git.status(ctx)
	if err != nil {
		return false, err
	}
	return len(status) > 0, nil
}

func (git *GitHelper) status(ctx context.Context) (string, error) {
	if !git.Valid {
		return "", nil
	}

	args := []string{"git", "status", "--porcelain", "--"}
	for _, ignore := range ignores {
		pathspec := ":(exclude)" + ignore
		args = append(args, pathspec, pathspec+"/**")
	}
	result, err := git.Container.WithExec(args).Stdout(ctx)
	if err != nil {
		return "", err
	}
	return strings.Trim(result, "\n"), nil
}

func (git *GitHelper) FileAt(ctx context.Context, filename string, ref string) (string, error) {
	if !git.Valid {
		return "", nil
	}

	data, err := git.Container.WithExec([]string{"git", "show", ref + ":" + filename}).Stdout(ctx)
	if err != nil {
		var execErr *dagger.ExecError
		if errors.As(err, &execErr) {
			if strings.Contains(execErr.Stderr, "exists on disk, but not in") {
				return "", nil
			} else if strings.Contains(execErr.Stderr, "does not exist in") {
				return "", nil
			} else {
				return "", err
			}
		}
	}
	return data, nil
}
