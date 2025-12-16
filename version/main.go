// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/version/internal/dagger"
	"golang.org/x/mod/semver"
)

func New(
	// A git repository containing the source code of the artifact to be versioned.
	// +optional
	// +defaultPath="/.git"
	// +ignore=["objects/pack/", "rr-cache"]
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

	// File containing the next release version (e.g. .changes/.next)
	// +optional
	// +defaultPath="/.changes/.next"
	nextVersionFile *dagger.File,
) *Version {
	return &Version{
		GitDir:          git,
		Inputs:          inputs,
		NextVersionFile: nextVersionFile,
	}
}

type Version struct {
	// +private
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
		// (v<major>.<minor>.<patch>-dev-<inputdigest>)
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
		return fmt.Sprintf("%s-dev-%s", next, digest[:12]), nil
	}

	if tag, err := v.CurrentTag(ctx); err != nil {
		return "", err
	} else if tag != "" {
		// this is a tagged release
		// (v<major>.<minor>.<patch>)
		return tag, nil
	}

	// this is a clean, untagged version - git state is clean, but no tag
	// (v<major>.<minor>.<patch>-dev-<commit>)
	next, err := v.NextReleaseVersion(ctx)
	if err != nil {
		return "", err
	}
	commit, err := v.headCommit(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-dev-%s", next, commit[:12]), nil
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

	// For untagged builds, return HEAD commit
	// Note: We can't compute merge-base without pack files, so we just use HEAD.
	// This means dev builds will use HEAD as the image tag.
	return v.headCommit(ctx)
}

func (v Version) Dirty(ctx context.Context) (bool, error) {
	status, err := dag.Container().
		From("alpine/git:latest").
		WithWorkdir("/src").
		WithMountedDirectory(".", v.Inputs).
		WithMountedDirectory(".git", v.GitDir).
		WithExec([]string{"git", "status", "--porcelain"}).
		Stdout(ctx)
	if err != nil {
		return false, err
	}
	status = strings.TrimSpace(status)
	return status != "", nil
}

func (v Version) CurrentTag(ctx context.Context) (string, error) {
	commit, err := v.headCommit(ctx)
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

// headCommit resolves HEAD to a commit SHA by reading git files directly.
// This avoids using the Dagger Git API which requires pack files.
func (v Version) headCommit(ctx context.Context) (string, error) {
	headContent, err := v.GitDir.File("HEAD").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD: %w", err)
	}

	// HEAD is either a direct SHA (detached) or a symbolic ref
	ref, isSymbolic := strings.CutPrefix(strings.TrimSpace(headContent), "ref: ")
	if !isSymbolic {
		return ref, nil
	}

	// Try loose ref first (e.g., .git/refs/heads/main)
	if content, err := v.GitDir.File(ref).Contents(ctx); err == nil {
		return strings.TrimSpace(content), nil
	}

	// Fall back to packed-refs
	packedRefs, err := v.GitDir.File("packed-refs").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve ref %s: %w", ref, err)
	}
	for line := range strings.SplitSeq(packedRefs, "\n") {
		sha, lineRef, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && lineRef == ref {
			return sha, nil
		}
	}
	return "", fmt.Errorf("ref %s not found in packed-refs", ref)
}

func (v Version) tagsAtCommit(ctx context.Context, commit string) ([]string, error) {
	content, err := v.GitDir.File("packed-refs").Contents(ctx)
	if err != nil {
		return nil, nil
	}
	var tags []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		sha, ref := parts[0], parts[1]
		if sha == commit && strings.HasPrefix(ref, "refs/tags/") {
			tag := strings.TrimPrefix(ref, "refs/tags/")
			tags = append(tags, tag)
		}
	}
	return tags, nil
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
