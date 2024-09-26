package main

import (
	"context"
	"dagger/version/internal/dagger"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

func (v *Version) Git() *Git {
	ctr := dag.Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{"git"}}).
		WithWorkdir("/src")
	if v.Inputs != nil {
		ctr = ctr.WithDirectory(".", v.Inputs)
	}
	if v.GitDir != nil {
		ctr = ctr.
			WithDirectory(".", v.GitDir).
			// enter detached head state, then we can rewrite all our refs however we like later
			WithExec([]string{"sh", "-c", "git checkout $(git rev-parse HEAD)"})

		// do various unshallowing operations (only the bare minimum is
		// provided by the core git functions which are used by our remote git
		// module sources)
		remote := "https://github.com/dagger/dagger.git"
		maxDepth := "2147483647" // see https://git-scm.com/docs/shallow
		ctr = ctr.
			// we need the unshallowed history, so we can determine which tags are in it later
			WithExec([]string{"git", "fetch", "--no-tags", "--depth=" + maxDepth, remote, "HEAD"}).
			// we need main, so we can determine the merge base for it later
			WithExec([]string{"git", "fetch", "--no-tags", "--depth=" + maxDepth, remote, "refs/heads/main:refs/heads/main"}).
			// we need all the tags, so we can find all the release tags later
			WithExec([]string{"git", "fetch", "--tags", "--force", remote})
	}
	return &Git{ctr}
}

// Git is an opinionated helper for performing various commands on our dagger repo.
type Git struct {
	Container *dagger.Container
}

func (git *Git) valid(ctx context.Context) (bool, error) {
	entries, err := git.Container.Directory(".").Entries(ctx)
	if err != nil {
		return false, err
	}
	return slices.Contains(entries, ".git"), nil
}

type VersionTag struct {
	// The component this belongs to.
	Component string
	// The semver version for this component.
	Version string
	// The commit hash.
	Commit string

	// The tag creator date.
	// Distinct from *author* date, and not to be confused with the underlying commit date.
	Date string
}

// VersionTagLatests gets the latest version tag for a given component
func (git *Git) VersionTagLatest(
	ctx context.Context,

	component string, // +optional
) (*VersionTag, error) {
	versions, err := git.VersionTags(ctx, component)
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
func (git *Git) VersionTags(
	ctx context.Context,
	component string, // +optional
) ([]VersionTag, error) {
	if ok, err := git.valid(ctx); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}

	component = strings.TrimSuffix(component, "/")
	componentFilter := "v*"
	if component != "" {
		componentFilter = component + "/" + componentFilter
	}

	tagsRaw, err := git.Container.
		WithExec([]string{
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
		}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	var versionTags []VersionTag
	for _, line := range strings.Split(tagsRaw, "\n") {
		parts := strings.Split(line, " ")
		if len(parts) != 3 {
			continue
		}
		tag, commit, date := parts[0], parts[1], parts[2]
		version := strings.TrimPrefix(tag, component+"/")
		if !semver.IsValid(version) {
			continue
		}

		versionTag := VersionTag{
			Component: component,
			Version:   version,
			Date:      date,
			Commit:    commit,
		}
		versionTags = append(versionTags, versionTag)
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

func (git *Git) Head(ctx context.Context) (*Commit, error) {
	return git.Commit(ctx, "HEAD")
}

func (git *Git) Commit(ctx context.Context, ref string) (*Commit, error) {
	if ok, err := git.valid(ctx); err != nil {
		return nil, err
	} else if !ok {
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

func (git *Git) MergeBase(ctx context.Context, ref string, ref2 string) (*Commit, error) {
	if ok, err := git.valid(ctx); err != nil {
		return nil, err
	} else if !ok {
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
func (git *Git) Dirty(ctx context.Context) (bool, error) {
	status, err := git.status(ctx)
	if err != nil {
		return false, err
	}
	return len(status) > 0, nil
}

func (git *Git) status(ctx context.Context) (string, error) {
	if ok, err := git.valid(ctx); err != nil {
		return "", err
	} else if !ok {
		return "", nil
	}

	args := []string{"git", "status", "--porcelain", "--"}
	for _, ignore := range ignores {
		args = append(args, ":(exclude)"+ignore)
	}
	result, err := git.Container.WithExec(args).Stdout(ctx)
	if err != nil {
		return "", err
	}
	return strings.Trim(result, "\n"), nil
}
