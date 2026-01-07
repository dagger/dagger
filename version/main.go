// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/version/internal/dagger"
	"golang.org/x/mod/semver"
)

func New(
	// A git repository containing the source code of the artifact to be versioned.
	// +optional
	// +defaultPath="/.git"
	git *dagger.Directory,

	// A directory containing all the inputs of the artifact to be versioned.
	// An input is any file that changes the artifact if it changes.
	// This directory is used to compute a digest. If any input changes, the digest changes.
	// - To avoid false positives, only include actual inputs
	// - To avoid false negatives, include *all* inputs
	// +optional
	// +defaultPath="/"
	// +ignore=["**_test.go", "**/.git*", "**/.venv", "**/.dagger", ".*", "bin", "**/node_modules", "**/testdata/**", "**/.changes", ".changes", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack", "!**/.gitignore"]
	inputs *dagger.Directory,

	// File containing the next release version (e.g. .changes/.next)
	// +optional
	// +defaultPath="/.changes/.next"
	nextVersionFile *dagger.File,
) *Version {
	return &Version{
		Git:             git.AsGit(),
		GitDir:          git,
		Inputs:          inputs.Filter(dagger.DirectoryFilterOpts{Gitignore: true}),
		NextVersionFile: nextVersionFile,
	}
}

type Version struct {
	// +private
	Git    *dagger.GitRepository
	GitDir *dagger.Directory

	// +private
	Inputs *dagger.Directory

	// +private
	NextVersionFile *dagger.File
}

// Generate a version string from the current context
func (v Version) Version(ctx context.Context) (string, error) {
	dirty, err := v.Dirty(ctx)
	if err != nil {
		return "", err
	}

	if dirty {
		// this is a dirty version - git state is dirty
		// (v<major>.<minor>.<patch>-<timestamp>-dev-<inputdigest>)
		next, err := v.NextReleaseVersion(ctx)
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
	next, err := v.NextReleaseVersion(ctx)
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

// Return the tag to use when auto-downloading the engine image from the CLI
func (v Version) ImageTag(ctx context.Context) (string, error) {
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

func (v Version) tagsAtCommit(ctx context.Context, commit string) ([]string, error) {
	// NOTE: this uses the git dir directly rather than the git repo
	// since there's no dagger API to do this operation
	out, err := dag.Container().
		From("alpine/git:latest").
		WithWorkdir("/src").
		WithMountedDirectory(".git", v.GitDir).
		WithExec([]string{"git", "tag", "-l", "--points-at=" + commit}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
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

// NextReleaseVersion returns the next release version from .changes/.next
func (v Version) NextReleaseVersion(ctx context.Context) (string, error) {
	if v.NextVersionFile == nil {
		return "", fmt.Errorf("next version file not provided")
	}
	content, err := v.NextVersionFile.Contents(ctx)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "v") && semver.IsValid(line) {
			return line, nil
		}
	}
	return "", fmt.Errorf("no valid version found in next version file")
}
