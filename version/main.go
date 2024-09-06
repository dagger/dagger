// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context"
	"crypto/sha1" //nolint:gosec
	"dagger/version/internal/dagger"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

func New(ctx context.Context,
	// A directory containing all the inputs of the artifact to be versioned: the Dagger engine + CLI
	// An input is any file that changes the artifact if it changes.
	// This directory is used to compute a digest. If any input changes, the digest changes.
	// - To avoid false positives, only include actual inputs
	// - To avoid false negatives, include *all* inputs
	// +optional
	// +defaultPath="/"
	// +ignore=[".git", "bin", "**/node_modules", "**/testdata", "**.changes", "docs", "helm", "release", "version", "modules", "*.md", "LICENSE", "NOTICE", "hack"]
	inputs *dagger.Directory,
	// .changes file used to extract version information
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.changes/*"]
	changes *dagger.Directory,
	// A git tag to use in the version, in short format. Example: "v0.1.0"
	// Tags not in semver format are silently ignored
	// +optional
	tag string,
	// The git commit sha to use in the version
	// +optional
	commit string,
) (*Version, error) {
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

type Version struct {
	// Git tag component
	Tag string

	// The next version
	Next string
	// Timestamp component
	Timestamp string
	// Git commit component
	Commit string
	// Dev component
	Dev string
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

func dirhash(ctx context.Context, dir *dagger.Directory) (string, error) {
	id, err := dir.ID(ctx)
	if err != nil {
		return "", err
	}
	h := sha1.New() //nolint:gosec
	h.Write([]byte(id))
	dgst := hex.EncodeToString(h.Sum(nil))
	return dgst, nil
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
