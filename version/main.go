// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/version/internal/dagger"
	"golang.org/x/mod/semver"
)

const daggerRepoURL = "https://github.com/dagger/dagger.git"

func New(
	ctx context.Context,

	// A directory containing the git metadata for the artifact to be versioned.
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.git"]
	gitParent *dagger.Directory,

	// A git repository containing the source code of the artifact to be versioned.
	// +optional
	git *dagger.GitRepository,

	// A directory containing all the inputs of the artifact to be versioned.
	// An input is any file that changes the artifact if it changes.
	// This directory is used to compute a digest. If any input changes, the digest changes.
	// - To avoid false positives, only include actual inputs
	// - To avoid false negatives, include *all* inputs
	// +optional
	// +defaultPath="/"
	// +ignore=["**_test.go", "**/.git*", "**/.venv", "**/.dagger", ".*", "bin", "**/node_modules", "**/testdata/**", "**/.changes", ".changes", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack", "!**/.gitignore"]
	inputs *dagger.Directory,
) *Version {
	v := &Version{
		Git:    git,
		Inputs: inputs.Filter(dagger.DirectoryFilterOpts{Gitignore: true}),
	}

	if v.Git == nil && gitParent != nil {
		if gitDir, err := gitParent.Directory(".git").Sync(ctx); err == nil {
			v.Git = gitDir.AsGit()
		}
	}

	return v
}

type Version struct {
	// +private
	Git *dagger.GitRepository

	// +private
	Inputs *dagger.Directory
}

// Generate a version string from the current context
func (v Version) Version(ctx context.Context) (string, error) {
	if v.Git == nil {
		return v.fallbackVersion(ctx)
	}

	dirty, err := v.Dirty(ctx)
	if err != nil {
		return "", err
	}

	if dirty {
		// this is a dirty version - git state is dirty
		// (v<major>.<minor>.<patch>-<timestamp>-dev-<inputdigest>)
		next, err := v.NextPatchVersion(ctx)
		if err != nil {
			return "", err
		}
		rawDigest, err := v.Inputs.Digest(ctx)
		if err != nil {
			return "", err
		}
		_, digest, ok := strings.Cut(rawDigest, ":")
		if !ok {
			return "", fmt.Errorf("invalid digest: %s", rawDigest)
		}
		return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(time.Now()), digest[:12]), nil
	}

	if tag, err := v.CurrentTag(ctx); err != nil {
		return "", err
	} else if tag != "" {
		// this is a tagged release
		// (v<major>.<minor>.<patch>)
		return tag, nil
	}

	// this is a clean, untagged version - git state is clean, but no tag
	// (v<major>.<minor>.<patch>-<timestamp>-dev-<commit>)
	next, err := v.NextPatchVersion(ctx)
	if err != nil {
		return "", err
	}
	head := v.Git.Head()
	commit, err := head.Commit(ctx)
	if err != nil {
		return "", err
	}
	commitDate, err := refTimestamp(ctx, head)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(commitDate), commit[:12]), nil
}

func (v Version) fallbackVersion(ctx context.Context) (string, error) {
	next, err := v.NextPatchVersion(ctx)
	if err != nil {
		return "", err
	}

	// Generate a version based on inputs digest
	rawDigest, err := v.Inputs.Digest(ctx)
	if err != nil {
		return "", err
	}
	_, digest, ok := strings.Cut(rawDigest, ":")
	if !ok {
		return "", fmt.Errorf("invalid digest: %s", rawDigest)
	}

	return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(time.Now()), digest[:12]), nil
}

// Return the tag to use when auto-downloading the engine image from the CLI
func (v Version) ImageTag(ctx context.Context) (string, error) {
	if v.Git == nil {
		return v.fallbackVersion(ctx)
	}

	if tag, err := v.CurrentTag(ctx); err != nil {
		return "", err
	} else if tag != "" {
		// this is a tagged release
		// (v<major>.<minor>.<patch>)
		return tag, nil
	}

	// For untagged builds, find merge-base with main
	// Try local main first, then origin/main for CI (detached HEAD)
	head := v.Git.Head()
	for _, ref := range []string{"main", "origin/main"} {
		if branch := v.Git.Branch(ref); branch != nil {
			if mergeBase, err := head.CommonAncestor(branch).Commit(ctx); err == nil {
				return mergeBase, nil
			}
		}
	}
	return head.Commit(ctx)
}

func (v Version) Dirty(ctx context.Context) (bool, error) {
	if v.Git == nil {
		return true, nil
	}

	committed := v.Git.Head().Tree()

	// Overlay local inputs onto committed tree, then diff.
	// This detects additions and modifications but not deletions.
	// We use overlay instead of direct diff to avoid false positives from
	// gitignored files and empty directories that exist locally but not in git.
	local := committed.WithDirectory("", v.Inputs)
	changes := committed.Changes(local)

	isEmpty, err := changes.IsEmpty(ctx)
	if err != nil {
		return false, err
	}
	return !isEmpty, nil
}

func (v Version) CurrentTag(ctx context.Context) (string, error) {
	if v.Git == nil {
		return "", nil
	}

	commit, err := v.Git.Head().Commit(ctx)
	if err != nil {
		return "", err
	}
	tags, err := v.tagsAtCommit(ctx, commit)
	if err != nil {
		return "", err
	}
	for _, tag := range tags {
		if semver.IsValid(tag) {
			return tag, nil
		}
	}
	return "", nil
}

// NextPatchVersion returns the next patch version after the latest stable semver git tag.
func (v Version) NextPatchVersion(ctx context.Context) (string, error) {
	tag, err := v.latestReleaseTag(ctx)
	if err != nil {
		return "", err
	}
	return bumpPatchVersion(tag)
}

func (v Version) tagsAtCommit(ctx context.Context, commit string) ([]string, error) {
	tags, err := v.gitTags(ctx, v.Git)
	if err != nil {
		return nil, err
	}

	var matched []string
	for _, tag := range tags {
		tagCommit, err := v.Git.Tag(tag).Commit(ctx)
		if err != nil {
			return nil, err
		}
		if tagCommit == commit {
			matched = append(matched, tag)
		}
	}
	return matched, nil
}

func (v Version) latestReleaseTag(ctx context.Context) (string, error) {
	tags, err := v.gitTags(ctx, v.releaseTagsGit())
	if err != nil {
		return "", err
	}

	latest := ""
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if !semver.IsValid(tag) || semver.Prerelease(tag) != "" {
			continue
		}
		if latest == "" || semver.Compare(tag, latest) > 0 {
			latest = tag
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no stable semver git tag found")
	}
	return latest, nil
}

func (v Version) releaseTagsGit() *dagger.GitRepository {
	if v.Git != nil {
		return v.Git
	}
	return dag.Git(daggerRepoURL)
}

func (v Version) gitTags(ctx context.Context, git *dagger.GitRepository) ([]string, error) {
	if git == nil {
		return nil, fmt.Errorf("git repository not provided")
	}

	tags, err := git.Tags(ctx, dagger.GitRepositoryTagsOpts{
		Patterns: []string{"refs/tags/v*"},
	})
	if err != nil {
		return nil, err
	}
	for i, tag := range tags {
		tags[i] = strings.TrimPrefix(tag, "refs/tags/")
	}
	return tags, nil
}

func bumpPatchVersion(version string) (string, error) {
	original := version
	version = semver.Canonical(version)
	if version == "" {
		return "", fmt.Errorf("invalid semver: %q", original)
	}

	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid canonical semver: %q", version)
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid patch version %q: %w", version, err)
	}

	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1), nil
}

func refTimestamp(ctx context.Context, head *dagger.GitRef) (time.Time, error) {
	checkout := head.Tree()
	status, err := dag.Container().
		From("alpine/git:latest").
		WithWorkdir("/src").
		WithMountedDirectory(".", checkout).
		WithExec([]string{"git", "log", "-1", "--format=%cI"}).
		Stdout(ctx)
	if err != nil {
		return time.Time{}, err
	}
	status = strings.TrimSpace(status)
	t, err := time.Parse(time.RFC3339, status)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func pseudoversionTimestamp(t time.Time) string {
	// go time formatting is bizarre - this translates to "yyyymmddhhmmss"
	// inspired from: https://cs.opensource.google/go/x/mod/+/refs/tags/v0.22.0:module/pseudo.go
	return t.UTC().Format("20060102150405")
}
