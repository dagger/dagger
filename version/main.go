// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context" //nolint:gosec
	"crypto/sha1"
	"dagger/version/internal/dagger"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
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
	// +ignore=["**_test.go", "**/.git*", "**/.venv", "**/.dagger", ".*", "bin", "**/node_modules", "**/testdata", "**.changes", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack"]
	inputs *dagger.Directory,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.git"]
	gitDir *dagger.Directory,
	// .changes file used to extract version information
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.changes/*"]
	changes *dagger.Directory,
) (*Version, error) {

	return &Version{
		// FIXME: upload the whole git dir is inefficient.
		// We can stop doing it once this is shipped:  https://github.com/dagger/dagger/issues/8520
		GitDir:  gitDir,
		Inputs:  inputs,
		Changes: changes,
	}, nil
}

type Version struct {
	GitDir *dagger.Directory
	Inputs *dagger.Directory

	Changes *dagger.Directory
}

func (v Version) Version(ctx context.Context) (string, error) {
	tag, err := v.SemverTag(ctx)
	if err != nil {
		return "", err
	}
	if tag != "" {
		// FIXME: we don't handle dirty checkout of a semvar tag checkout
		return tag, nil
	}
	next, err := v.NextVersion(ctx)
	if err != nil {
		return "", err
	}
	commit, err := v.Commit(ctx)
	if err != nil {
		return "", err
	}
	digest, err := v.InputsDigest(ctx)
	if err != nil {
		return "", err
	}
	// FIXME we don't differentiate between clean and dirty checkout:
	// instead we always add the commit + digest
	return fmt.Sprintf("%s-%s-%s", next, commit, digest), nil
}

func (v Version) gitRepo() *dagger.GitRepo {
	return dag.Git().Load(v.GitDir, dagger.GitLoadOpts{
		Worktree: v.Inputs,
	})
}

// Return the semver-compatible tag pointing to the current tag, or an empty string
func (v Version) SemverTag(ctx context.Context) (string, error) {
	tagsRaw, err := v.gitRepo().
		Command([]string{"tag", "--points-at", "HEAD"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	tags := strings.Split(strings.Trim(tagsRaw, "\n"), "\n")
	for _, tag := range tags {
		if semver.IsValid(tag) {
			return tag, nil
		}
	}
	// No semver-compatible tag pointing to the current commit
	return "", nil
}

// Return the current git commit
func (v Version) Commit(ctx context.Context) (string, error) {
	commit, err := v.gitRepo().
		// FIXME: contribute GitRepo.head() uptsream
		Command([]string{"rev-parse", "--short", "HEAD"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	return strings.Trim(commit, "\n"), nil
}

// Compute a stable digest of the input files, to differentiate dev builds on the same commit
// FIXME: the digest doesn't seem very stable...
func (v Version) InputsDigest(ctx context.Context) (string, error) {
	id, err := v.Inputs.ID(ctx)
	if err != nil {
		return "", err
	}
	h := sha1.New() //nolint:gosec
	h.Write([]byte(id))
	dgst := hex.EncodeToString(h.Sum(nil))
	return dgst, nil
}

// Determine the "next" version to be released
// It first attempts to use the version in .changes/.next, but if this fails,
// or that version seems to have already been released, then we automagically
// calculate the next patch release in the current series.
func (v Version) NextVersion(ctx context.Context) (string, error) {
	// this is kinda meh, since it assumes changie releases match up with git
	// tags - thankfully this is true for us (otherwise, we'd have to look at
	// *all* the tags in the source, which would be slow)
	entries, err := v.Changes.Directory(".changes").Entries(ctx)
	if err != nil {
		return "", err
	}
	// if there's a defined next version, try and use that
	var definedNextVersion string
	if slices.Contains(entries, ".next") {
		content, err := v.Changes.File(".changes/.next").Contents(ctx)
		if err != nil {
			return "", err
		}
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

			definedNextVersion = baseVersion(line)
			break
		}
	}
	// also try and determine what the last version was, so we can
	// auto-determine a next version from that
	var lastVersion string
	for _, entry := range entries {
		entry, _ := strings.CutSuffix(entry, filepath.Ext(entry))
		if semver.Compare(entry, lastVersion) > 0 {
			lastVersion = entry
		}
	}
	if lastVersion == "" {
		return "", fmt.Errorf("could not find any valid versions")
	}
	lastVersion = baseVersion(lastVersion)

	majorMinor := semver.MajorMinor(lastVersion)
	patchStr, _ := strings.CutPrefix(lastVersion, majorMinor+".")
	patch, err := strconv.Atoi(patchStr)
	if err != nil {
		return "", err
	}
	nextVersion := fmt.Sprintf("%s.%d", majorMinor, patch+1)

	if semver.Compare(definedNextVersion, nextVersion) > 0 {
		// if the defined next version is larger than the auto-generated one,
		// override it - this'll be the case for when we plan to bump to a
		// minor version
		nextVersion = definedNextVersion
	}
	return nextVersion, nil
}

func baseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}

/*

	// If we get a commit, make sure it's valid (regardless of if we use it)
	if (commit != "") && (!commitRegexp.MatchString(commit)) {
		return nil, fmt.Errorf("invalid commit sha: %s", commit)
	}
	// If we have a semver tag, use just that
	// Example: "v0.1.0"
	if !semver.IsValid(tag) {
		return &Version{Tag: tag}, nil
	}
	// If we have a valid commit sha, use that + next version
	// Example: "v0.2.0-ad997972f96272f3e140e12b12e00ef4d6e9450b"
	if commit != "" {
		next, err := nextVersion(ctx, changes)
		if err != nil {
			return nil, err
		}
		return &Version{
			Commit:    commit,
			Next:      next,
			Timestamp: pseudoversionTimestamp(time.Now().UTC()),
		}, nil
	}
	// Fall back to input hash + next version
	// Example: "v0.2.0-deadbeefdeadbeefdeadbeef"
	dgst, err := dirhash(ctx, inputs)
	if err != nil {
		return nil, err
	}
	next, err := nextVersion(ctx, changes)
	if err != nil {
		return nil, err
	}
	return &Version{
		Dev:       dgst,
		Next:      next,
		Timestamp: pseudoversionTimestamp(time.Time{}),
	}, nil
}

var commitRegexp = regexp.MustCompile("^[0-9a-f]{40}$")

// Complete version string
func (info *Version) Version() string {
	if info.Tag != "" {
		return info.Tag
	}

	nextVersion := info.Next
	if nextVersion == "" {
		nextVersion = "v0.0.0"
	}
	timestamp := info.Timestamp
	if timestamp == "" {
		timestamp = pseudoversionTimestamp(time.Time{})
	}

	var rest string
	switch {
	case info.Commit != "":
		rest = info.Commit[:12]
	case info.Dev != "":
		rest = "dev-" + info.Dev[:12]
	default:
		rest = "dev-" + strings.Repeat("0", 12)
	}
	return fmt.Sprintf("%s-%s-%s", nextVersion, timestamp, rest)
}

// nextVersion determines the "next" version to be released.
//
// It first attempts to use the version in .changes/.next, but if this fails,
// or that version seems to have already been released, then we automagically
// calculate the next patch release in the current series.
func nextVersion(ctx context.Context, dir *dagger.Directory) (string, error) {
	// this is kinda meh, since it assumes changie releases match up with git
	// tags - thankfully this is true for us (otherwise, we'd have to look at
	// *all* the tags in the source, which would be slow)
	entries, err := dir.Directory(".changes").Entries(ctx)
	if err != nil {
		return "", err
	}

	// if there's a defined next version, try and use that
	var definedNextVersion string
	if slices.Contains(entries, ".next") {
		content, err := dir.File(".changes/.next").Contents(ctx)
		if err != nil {
			return "", err
		}
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

			definedNextVersion = baseVersion(line)
			break
		}
	}

	// also try and determine what the last version was, so we can
	// auto-determine a next version from that
	var lastVersion string
	for _, entry := range entries {
		entry, _ := strings.CutSuffix(entry, filepath.Ext(entry))
		if semver.Compare(entry, lastVersion) > 0 {
			lastVersion = entry
		}
	}
	if lastVersion == "" {
		return "", fmt.Errorf("could not find any valid versions")
	}
	lastVersion = baseVersion(lastVersion)

	majorMinor := semver.MajorMinor(lastVersion)
	patchStr, _ := strings.CutPrefix(lastVersion, majorMinor+".")
	patch, err := strconv.Atoi(patchStr)
	if err != nil {
		return "", err
	}
	nextVersion := fmt.Sprintf("%s.%d", majorMinor, patch+1)

	if semver.Compare(definedNextVersion, nextVersion) > 0 {
		// if the defined next version is larger than the auto-generated one,
		// override it - this'll be the case for when we plan to bump to a
		// minor version
		nextVersion = definedNextVersion
	}
	return nextVersion, nil
}

func pseudoversionTimestamp(t time.Time) string {
	// go time formatting is bizarre - this translates to "yymmddhhmmss"
	return t.Format("060102150405")
}

func baseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}
*/
