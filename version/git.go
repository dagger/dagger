package main

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/version/internal/dagger"
)

// Git is an opinionated helper for performing various commands on our dagger repo.
type Git struct {
	Repo *dagger.GitRepository

	Directory *dagger.Directory

	Container *dagger.Container // +private
}

func git(repo *dagger.GitRepository, dir *dagger.Directory) (*Git, error) {
	checkout := repo.Head().Tree(dagger.GitRefTreeOpts{Depth: -1})

	ctr := dag.Container().From("alpine/git:latest")
	// ctr := dag.Wolfi().Container(dagger.WolfiContainerOpts{Packages: []string{"git"}})
	ctr = ctr.WithWorkdir("/src")
	if repo != nil {
		ctr = ctr.WithDirectory(".", checkout)
	}
	if dir != nil {
		ctr = ctr.WithDirectory(".", dir)
	}

	return &Git{
		Repo:      repo,
		Directory: dag.Directory().WithDirectory(".git", checkout.Directory(".git")),
		Container: ctr,
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
func (git *Git) VersionTagLatest(
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
func (git *Git) VersionTags(
	ctx context.Context,

	// Optional component tag prefix
	component string, // +optional
	// Optional commit sha to get tags for
	commit string, // +optional
) ([]VersionTag, error) {
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

type Branch struct {
	// The raw branch
	Branch string
	// The commit hash.
	Commit string
}

func (git *Git) Branches(
	ctx context.Context,

	// Optional commit sha to get branches for
	commit string, // +optional
) ([]Branch, error) {
	branchArgs := []string{
		"git",
		"branch",
		// filter to reachable commits from HEAD
		"--merged=HEAD",
		// format as "<tag> <commit>"
		"--format", "%(refname:lstrip=2) %(objectname)",
		// sort by ascending semver
		"--sort", "version:refname",
	}
	if commit != "" {
		// filter to specified commit
		branchArgs = append(branchArgs, "--points-at", commit)
	}
	branchesRaw, err := git.Container.WithExec(branchArgs).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	var branches []Branch
	for _, line := range strings.Split(branchesRaw, "\n") {
		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			continue
		}
		branch, branchCommit := parts[0], parts[1]

		branches = append(branches, Branch{
			Branch: branch,
			Commit: branchCommit,
		})
	}

	return branches, nil
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

func (git *Git) MergeBase(ctx context.Context, ref string, ref2 string) (string, error) {
	return git.Repo.Ref(ref).CommonAncestor(git.Repo.Ref(ref2)).Commit(ctx)
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

func (git *Git) FileAt(ctx context.Context, filename string, ref string) (string, error) {
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
