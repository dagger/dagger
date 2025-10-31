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
	ctx context.Context,

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
	// +ignore=["**_test.go", "**/.git*", "**/.venv", "**/.dagger", ".*", "bin", "**/node_modules", "**/testdata/**", "**/.changes", ".changes", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack"]
	inputs *dagger.Directory,
) (*Version, error) {
	return &Version{
		Git:    git.AsGit(),
		GitDir: git,
		Inputs: inputs,
	}, nil
}

type Version struct {
	// +private
	Git    *dagger.GitRepository
	GitDir *dagger.Directory

	// +private
	Inputs *dagger.Directory
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
		next := "v0.0.0"
		rawDigest, err := v.Inputs.Digest(ctx)
		if err != nil {
			return "", err
		}
		_, digest, ok := strings.Cut(rawDigest, ":")
		if !ok {
			return "", fmt.Errorf("invalid digest: %s", rawDigest)
		}
		return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(time.Time{}), digest[:12]), nil
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
	head := v.Git.Head()
	commit, err := head.Commit(ctx)
	if err != nil {
		return "", err
	}
	commitDate, err := refTimestamp(ctx, head)
	if err != nil {
		return "", err
	}
	next := "v0.0.0"
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

	head := v.Git.Head()
	main := v.Git.Branch("main")
	mergeBase, err := head.CommonAncestor(main).Commit(ctx)
	if err != nil {
		return "", err
	}
	return mergeBase, nil
}

func (v Version) Dirty(ctx context.Context) (bool, error) {
	checkout := v.Git.Head().Tree()
	// XXX: doesn't handle removed files :(
	checkout = checkout.WithDirectory("", v.Inputs)
	status, err := dag.Container().
		From("alpine/git:latest").
		WithWorkdir("/src").
		WithMountedDirectory(".", checkout).
		WithExec([]string{"git", "status", "--porcelain"}).
		Stdout(ctx)
	if err != nil {
		return false, err
	}
	status = strings.TrimSpace(status)
	return status != "", nil
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
	// go time formatting is bizarre - this translates to "yymmddhhmmss"
	return t.Format("060102150405")
}
