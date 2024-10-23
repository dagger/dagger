// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/version/internal/dagger"
)

func New(
	ctx context.Context,

	// A directory containing all the inputs of the artifact to be versioned.
	// An input is any file that changes the artifact if it changes.
	// This directory is used to compute a digest. If any input changes, the digest changes.
	// - To avoid false positives, only include actual inputs
	// - To avoid false negatives, include *all* inputs
	// +optional
	// +defaultPath="/"
	// +ignore=["**_test.go", "**/.git*", "**/.venv", "**/.dagger", ".*", "bin", "**/node_modules", "**/testdata/**", "**/.changes", ".changes", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack"]
	inputs *dagger.Directory,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.git", "!**/.gitignore", ".git/config"]
	gitDir *dagger.Directory,
	// .changes file used to extract version information
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.changes/*"]
	changes *dagger.Directory,
) (*Version, error) {
	// NOTE: uploading the whole git dir is inefficient.
	// we can stop doing it once dagger/dagger#8520 ships

	// NOTE: .git/config is excluded, since *some* tools (GitHub actions)
	// produce weird configs with custom headers set

	git, err := git(ctx, gitDir, inputs)
	if err != nil {
		return nil, err
	}
	return &Version{
		Git:     git,
		Inputs:  inputs,
		Changes: changes,
	}, nil
}

// FIXME: this is copy-pasted from top-level +ignore. Find a way to DRY this up.
// FIXME: investigate behavior difference between dagger and git on: '**/testdata': dagger ignores, git requires extra /**
var ignores = []string{
	"**_test.go", "**/.git*", "**/.venv", "**/.dagger", ".*", "bin", "**/node_modules", "**/testdata/**", "**/.changes/**", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack",
}

type Version struct {
	Git *Git

	// +private
	Inputs *dagger.Directory

	// +private
	Changes *dagger.Directory
}

// Generate a version string from the current context
func (v Version) Version(ctx context.Context) (string, error) {
	dirty, err := v.Git.Dirty(ctx)
	if err != nil {
		return "", err
	}
	head, err := v.Git.Head(ctx)
	if err != nil {
		return "", err
	}
	if dirty || head == nil {
		// this is a dev version - git state is dirty, or we have no git state at all
		// (v<major>.<minor>.<patch>-<timestamp>-dev-<inputdigest>)
		next, err := v.NextReleaseVersion(ctx)
		if err != nil {
			return "", err
		}
		digest, err := v.Inputs.Digest(ctx)
		if err != nil {
			return "", err
		}
		if _, newDigest, ok := strings.Cut(digest, ":"); ok {
			digest = newDigest
		}
		// NOTE: the timestamp is empty here to prevent unnecessary rebuilds
		return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(time.Time{}), digest[:12]), nil
	}

	versionTag, err := v.Git.VersionTagLatest(ctx, "", head.Commit)
	if err != nil {
		return "", err
	}
	if versionTag != nil {
		// this is a tagged release - we got a release tag for this commit
		// (v<major>.<minor>.<patch>)
		return versionTag.Version, nil
	}

	// this is an untagged release - we didn't find a release tag
	// (v<major>.<minor>.<patch>-<timestamp>-<commit>)
	next, err := v.NextReleaseVersion(ctx)
	if err != nil {
		return "", err
	}
	commitDate, err := time.Parse(time.RFC3339, head.Date)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", next, pseudoversionTimestamp(commitDate), head.Commit[:12]), nil
}

func pseudoversionTimestamp(t time.Time) string {
	// go time formatting is bizarre - this translates to "yymmddhhmmss"
	return t.Format("060102150405")
}

// Return the tag to use when auto-downloading the engine image from the CLI
func (v Version) ImageTag(ctx context.Context) (string, error) {
	head, err := v.Git.Head(ctx)
	if err != nil {
		return "", err
	}
	if head == nil {
		// no git metadata
		// (empty)
		return "", nil
	}

	dirty, err := v.Git.Dirty(ctx)
	if err != nil {
		return "", err
	}
	if dirty {
		// this is a dev version - get the last commit from main on this branch
		// (<commit>)
		mergeBase, err := v.Git.MergeBase(ctx, "main", head.Commit)
		if err != nil {
			return "", err
		}
		return mergeBase.Commit, nil
	}

	versionTag, err := v.Git.VersionTagLatest(ctx, "", head.Commit)
	if err != nil {
		return "", err
	}
	if versionTag != nil {
		// this is a tagged release
		// (v<major>.<minor>.<patch>)
		return versionTag.Version, nil
	}

	// this is an untagged release - get the last commit from main on this branch
	// <commit>
	mergeBase, err := v.Git.MergeBase(ctx, "main", head.Commit)
	if err != nil {
		return "", err
	}
	return mergeBase.Commit, nil
}

// Determine the last released version.
func (v Version) LastReleaseVersion(ctx context.Context) (string, error) {
	tag, err := v.Git.VersionTagLatest(ctx, "", "")
	if err != nil {
		return "", err
	}
	if tag == nil {
		return "", fmt.Errorf("no releases found")
	}
	return tag.Version, nil
}

// Determine the "next" version to be released.
//
// It first attempts to use the version in .changes/.next, but if this fails,
// or that version seems to have already been released, then we automagically
// calculate the next patch release in the current series.
func (v Version) NextReleaseVersion(ctx context.Context) (string, error) {
	var nextVersion string

	// if there's a defined next version, try and use that
	content, err := v.Git.FileAt(ctx, ".changes/.next", "HEAD")
	if err != nil {
		return "", err
	}
	nextVersion = parseNextFile(content)

	// also try and determine what the last version from git was, so we can
	// auto-determine a next version from that
	lastVersion, err := v.Git.VersionTagLatest(ctx, "", "")
	if err != nil {
		return "", err
	}
	if lastVersion != nil {
		maybeNextVersion := bumpVersion(lastVersion.Version)
		if semver.Compare(maybeNextVersion, nextVersion) > 0 {
			// if the auto-bumped last version is greater than the defined
			// version, we've probably forgotten to update `.changes/.next`
			nextVersion = maybeNextVersion
		}
	}

	// HACK: fallback to the contents
	// we can remove this when remote modules have KeepGitDir by default
	if nextVersion == "" {
		entries, err := v.Changes.Directory(".changes").Entries(ctx)
		if err != nil {
			return "", err
		}
		if slices.Contains(entries, ".next") {
			content, err := v.Changes.File(".changes/.next").Contents(ctx)
			if err != nil {
				return "", err
			}
			nextVersion = parseNextFile(content)
		}
	}

	if nextVersion == "" {
		return "", fmt.Errorf("could not determine next version")
	}
	return nextVersion, nil
}

func bumpVersion(version string) string {
	if !semver.IsValid(version) {
		return version // cannot bump a non-semver version
	}
	version = baseVersion(version)
	majorMinor := semver.MajorMinor(version)
	patchStr, _ := strings.CutPrefix(version, majorMinor+".")
	patch, _ := strconv.Atoi(patchStr)
	return fmt.Sprintf("%s.%d", majorMinor, patch+1)
}

func baseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}

func parseNextFile(content string) (version string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			// empty
			continue
		}
		if strings.HasPrefix(line, "#") {
			// comment
			continue
		}

		return baseVersion(line)
	}
	return ""
}
