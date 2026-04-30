package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/version/internal/dagger"
	"golang.org/x/mod/semver"
)

// workspaceGit is the version module's local stand-in for the future
// Workspace.git API.
//
// It is intentionally narrow: every method reflects local workspace git state.
// Stopgap policy such as querying the hardcoded upstream repository belongs in
// Version methods, not here.
type workspaceGit struct {
	repo *dagger.GitRepository
}

// workspaceGit adapts the Version object's current private GitRepository field
// into the local facade used by the rest of the module.
//
// When core Workspace.git exists, this should become the only place that needs
// to change for the version module to consume it.
func (v Version) workspaceGit() workspaceGit {
	return workspaceGit{repo: v.Git}
}

// found reports whether local workspace git metadata is available.
//
// In linked worktrees this is currently false because the contextual .git input
// is a file, not a directory. That is a limitation of the current shim source,
// not the intended semantics of Workspace.git.
func (git workspaceGit) found() bool {
	return git.repo != nil
}

// head returns the checked-out workspace HEAD.
//
// Callers must check found first. Keeping this as a shim method makes the
// intended future replacement with Workspace.git().head mechanical.
func (git workspaceGit) head() *dagger.GitRef {
	return git.repo.Head()
}

// branch returns a branch ref from the local workspace repository.
//
// This only exists for the legacy ImageTag merge-base path. It should disappear
// when ImageTag is removed and build code uses Version everywhere.
func (git workspaceGit) branch(name string) *dagger.GitRef {
	return git.repo.Branch(name)
}

// uncommitted returns the local workspace changeset using GitRepository's rules.
//
// This is the replacement for the old manually filtered inputs directory. The
// changeset provides both dirty detection and the patch digest for dirty dev
// versions.
func (git workspaceGit) uncommitted() *dagger.Changeset {
	return git.repo.Uncommitted()
}

// currentSemverTag returns the highest semver tag pointing at workspace HEAD.
//
// includePrerelease controls whether pre-release tags such as v1.2.3-rc.1 are
// eligible. If local git is unavailable or no matching tag points at HEAD, the
// empty string is returned.
func (git workspaceGit) currentSemverTag(ctx context.Context, includePrerelease bool) (string, error) {
	if !git.found() {
		return "", nil
	}

	commit, err := git.head().Commit(ctx)
	if err != nil {
		return "", err
	}
	tags, err := git.tagsAtCommit(ctx, commit)
	if err != nil {
		return "", err
	}
	tag, ok := latestSemverTagName(tags, includePrerelease)
	if !ok {
		return "", nil
	}
	return tag, nil
}

// latestSemverTag returns the highest semver tag in the local workspace repo.
//
// Unlike the old helper this does not fall back to the upstream Dagger repo. If
// the workspace repo is unavailable, callers must choose their own fallback
// policy explicitly.
func (git workspaceGit) latestSemverTag(ctx context.Context, includePrerelease bool) (string, error) {
	if !git.found() {
		return "", fmt.Errorf("workspace git repository not available")
	}
	return latestSemverTag(ctx, git.repo, includePrerelease)
}

// tagsAtCommit returns all v-prefixed tags that resolve to the given commit.
//
// GitRepository does not currently expose "tags at HEAD", so this scans the tag
// list and resolves each candidate. The caller is responsible for semver
// filtering.
func (git workspaceGit) tagsAtCommit(ctx context.Context, commit string) ([]string, error) {
	tags, err := tagNames(ctx, git.repo)
	if err != nil {
		return nil, err
	}

	var matched []string
	for _, tag := range tags {
		tagCommit, err := git.repo.Tag(tag).Commit(ctx)
		if err != nil {
			return nil, err
		}
		if tagCommit == commit {
			matched = append(matched, tag)
		}
	}
	return matched, nil
}

// latestSemverTag returns the highest semver tag in repo.
//
// This helper is deliberately repo-oriented, not workspace-oriented. It is used
// both by workspaceGit and by the explicit no-local-git fallback, which passes
// the upstream Dagger repo itself.
func latestSemverTag(ctx context.Context, repo *dagger.GitRepository, includePrerelease bool) (string, error) {
	tags, err := tagNames(ctx, repo)
	if err != nil {
		return "", err
	}
	tag, ok := latestSemverTagName(tags, includePrerelease)
	if !ok {
		if !includePrerelease {
			return "", fmt.Errorf("no stable semver git tag found")
		}
		return "", fmt.Errorf("no semver git tag found")
	}
	return tag, nil
}

// tagNames returns v-prefixed git tag names without the refs/tags/ prefix.
//
// The GitRepository API returns refs, but the version module's semver logic
// expects normal tag names like v1.2.3.
func tagNames(ctx context.Context, repo *dagger.GitRepository) ([]string, error) {
	if repo == nil {
		return nil, fmt.Errorf("git repository not provided")
	}

	tags, err := repo.Tags(ctx, dagger.GitRepositoryTagsOpts{
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

// latestSemverTagName selects the highest semver tag from an already-loaded tag list.
//
// The boolean return is false when no tag matches the requested prerelease
// policy. Tags are trimmed before comparison because historical callers trimmed
// remote tag output defensively.
func latestSemverTagName(tags []string, includePrerelease bool) (string, bool) {
	latest := ""
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if !isSemverTag(tag, includePrerelease) {
			continue
		}
		if latest == "" || semver.Compare(tag, latest) > 0 {
			latest = tag
		}
	}
	return latest, latest != ""
}

// isSemverTag reports whether a tag is a valid semver tag for the requested policy.
//
// Stable-only callers use this to ignore prerelease tags when computing the next
// patch release baseline.
func isSemverTag(tag string, includePrerelease bool) bool {
	if !semver.IsValid(tag) {
		return false
	}
	return includePrerelease || semver.Prerelease(tag) == ""
}
